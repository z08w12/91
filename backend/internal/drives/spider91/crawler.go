package spider91

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/video-site/backend/internal/catalog"
)

// 默认 author 标签，便于在前端筛选 spider91 来源的视频。
const DefaultAuthor = "91porn"

// DefaultTag 是所有 spider91 来源视频自动打的固定标签。
// backend 启动时会调用 CreateTagAndClassify 确保 tags 表里有 source="system"
// 的对应行，避免被孤儿标签清理掉。
const DefaultTag = "91porn"

// DefaultTargetNew 是凌晨任务默认的"凑够这么多新视频"目标数。
const DefaultTargetNew = 15

// 视频下载、列表页请求的 UA 沿用爬虫脚本里那一套，避免触发 Cloudflare 风控。
const downloadUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36"

// CrawlerConfig 是 Crawler 的依赖注入。
type CrawlerConfig struct {
	// Driver 是已挂载的 spider91 driver；crawler 用它的 VideoPath / ThumbPath 写入文件。
	Driver *Driver
	// Catalog 用于查重和入库。
	Catalog *catalog.Catalog
	// PythonPath 是用来跑爬虫脚本的解释器，通常是 "python3"。
	PythonPath string
	// ScriptPath 是 spider_91porn.py 的绝对路径。
	ScriptPath string
	// WorkDir 是跑 Python 时的 cwd；为空表示沿用当前进程工作目录。
	WorkDir string
	// CommonThumbDir 是 backend 的 data/previews/thumbs 目录；
	// crawler 会把封面再复制一份到 <CommonThumbDir>/<videoID>.jpg，
	// 让 /p/thumb/{videoID} 路由命中本地文件。
	CommonThumbDir string
	// HTTPClient 用于下载视频和封面；为空时使用内置默认 client。
	HTTPClient *http.Client
	// ProxyURL 可选的下载代理 URL（如 "http://127.0.0.1:7890"）。
	// 不为空则用它作为 HTTP/HTTPS 代理；为空则走 http.ProxyFromEnvironment（读 HTTPS_PROXY / HTTP_PROXY / NO_PROXY）。
	// 91porn CDN 节点位于海外，国内服务器直连通常很慢，需要走代理。
	ProxyURL string
	// SpiderTimeout 限制单次爬虫脚本运行时间。
	SpiderTimeout time.Duration
	// DownloadTimeout 限制单条视频/封面下载的耗时。
	DownloadTimeout time.Duration

	// OnNewVideo 是新视频成功入库后的回调，用于触发 teaser worker。
	OnNewVideo func(v *catalog.Video)
}

// Crawler 把 Python 爬虫产出包装成 catalog 入库流程。
type Crawler struct {
	cfg CrawlerConfig
	// runMu 保证同一个 Crawler 实例不会并发跑两次。
	runMu sync.Mutex
}

// NewCrawler 构造 Crawler。
func NewCrawler(cfg CrawlerConfig) *Crawler {
	if cfg.SpiderTimeout <= 0 {
		cfg.SpiderTimeout = 15 * time.Minute
	}
	if cfg.DownloadTimeout <= 0 {
		cfg.DownloadTimeout = 30 * time.Minute
	}
	if cfg.HTTPClient == nil {
		// 选 proxy 函数：显式 ProxyURL > 环境变量 > 直连
		proxyFn := http.ProxyFromEnvironment
		if strings.TrimSpace(cfg.ProxyURL) != "" {
			if u, err := url.Parse(cfg.ProxyURL); err == nil {
				proxyFn = http.ProxyURL(u)
			} else {
				log.Printf("[spider91] invalid proxy URL %q, falling back to env: %v", cfg.ProxyURL, err)
			}
		}
		cfg.HTTPClient = &http.Client{
			// 不限制总下载时长，靠 ctx 控制；只挡 dial / handshake / header
			Timeout: 0,
			Transport: &http.Transport{
				Proxy:                 proxyFn,
				ResponseHeaderTimeout: 60 * time.Second,
				MaxIdleConns:          10,
				IdleConnTimeout:       90 * time.Second,
			},
		}
	}
	return &Crawler{cfg: cfg}
}

// CrawlResult 汇总一次 RunOnce 的结果。
type CrawlResult struct {
	// TargetNew 是本次 RunOnce 的目标新增数（来自 drive.Credentials.target_new）。
	TargetNew    int
	// TotalEntries 是 Python 输出 JSON 里的视频条数（已被 spider 端去重过的新视频）。
	TotalEntries int
	// NewVideos 是真正下载完并入库的新视频数。
	NewVideos    int
	// Skipped 是 Go 侧二次校验时发现已存在的（理论上 Python 侧已经过滤过，正常情况下应为 0）。
	Skipped      int
	// Failed 是下载或入库失败的条数。
	Failed       int
	// SeenSnapshot 调用 Python 时实际写出的已知 viewkey 数量。
	SeenSnapshot int
	StartedAt    time.Time
	FinishedAt   time.Time
	OutputJSON   string
	SeenFile     string
}

// spiderVideoEntry 对应 spider_91porn.py 输出 JSON 中的单条视频。
type spiderVideoEntry struct {
	Title    string `json:"title"`
	ThumbURL string `json:"thumb_url"`
	VideoURL string `json:"video_url"`
	Viewkey  string `json:"viewkey"`
	DetailURL string `json:"detail_url"`
}

// RunOnce 执行一次"跑爬虫 → 下载 → 入库"流程：
//  1. 从 catalog 拉取本 drive 已存在的 viewkey 列表，写到临时文件
//  2. 启动 Python 爬虫（--target-new + --seen-viewkeys-file + --stream-output），
//     Python 每解析出一个 video 直链就把 entry 当作一行 JSON 写到 stdout。
//  3. Go 端 bufio.Scanner 按行读：每行立即下载视频和封面、入库。
//     这样 "Python 翻页找下一个" 与 "Go 下载当前一个" 在时间上重叠，缩短整轮耗时；
//     更重要的是不会让前几个下载耽误后面签名链接 e= 过期。
//  4. 全部消费完 + 子进程退出 → 返回 CrawlResult。teaser 不在此处入队，
//     由调用方 (App.runSpider91Crawl) 在 RunOnce 后统一调 enqueueDriveGeneration。
//
// targetNew <= 0 会被规范化成 spider91DefaultTargetNew（15）。
func (c *Crawler) RunOnce(ctx context.Context, targetNew int) (*CrawlResult, error) {
	c.runMu.Lock()
	defer c.runMu.Unlock()

	if c.cfg.Driver == nil {
		return nil, errors.New("spider91 crawler: driver not set")
	}
	if c.cfg.Catalog == nil {
		return nil, errors.New("spider91 crawler: catalog not set")
	}
	if strings.TrimSpace(c.cfg.PythonPath) == "" || strings.TrimSpace(c.cfg.ScriptPath) == "" {
		return nil, errors.New("spider91 crawler: python_path / script_path required")
	}
	if _, err := os.Stat(c.cfg.ScriptPath); err != nil {
		return nil, fmt.Errorf("spider91 crawler: script not found: %w", err)
	}
	if targetNew <= 0 {
		targetNew = DefaultTargetNew
	}

	if err := c.cfg.Driver.Init(ctx); err != nil {
		return nil, fmt.Errorf("spider91 crawler: driver init: %w", err)
	}

	result := &CrawlResult{TargetNew: targetNew, StartedAt: time.Now()}
	defer func() { result.FinishedAt = time.Now() }()

	// 1. 准备 .crawl/ 目录 + 已知 viewkey 列表
	//
	// 关键：路径必须用绝对路径，因为 Python 子进程的 cwd 我们设成了脚本所在目录
	// （为了让 Python 用 site-packages 里的 requests 等），传相对路径会被 Python
	// 当作相对它自己的 cwd 来解释，落在错的目录下，Go 这边再回头找又找不到。
	rootDir, err := filepath.Abs(c.cfg.Driver.RootDir())
	if err != nil {
		return result, fmt.Errorf("spider91 crawler: abs root dir: %w", err)
	}
	crawlDir := filepath.Join(rootDir, ".crawl")
	if err := os.MkdirAll(crawlDir, 0o755); err != nil {
		return result, fmt.Errorf("spider91 crawler: mkdir crawl: %w", err)
	}
	timestamp := time.Now().UTC().Format("20060102T150405Z")
	outputPath := filepath.Join(crawlDir, fmt.Sprintf("target-%d-%s.json", targetNew, timestamp))
	seenPath := filepath.Join(crawlDir, fmt.Sprintf("seen-%s.txt", timestamp))
	result.OutputJSON = outputPath
	result.SeenFile = seenPath

	seenCount, err := c.writeSeenViewkeys(ctx, seenPath)
	if err != nil {
		return result, fmt.Errorf("spider91 crawler: build seen list: %w", err)
	}
	result.SeenSnapshot = seenCount

	// 2-3. 启动 Python 爬虫（流式 stdout 协议），并边读边处理。
	//
	// 协议：Python 每解析出一个 video 的直链就把 entry JSON 写到 stdout 一行，
	// 立即 flush；本端 bufio.Scanner 收到一行就立即 processOne 下载视频和封面。
	// 这样把 "Python 等所有视频解析完 + Go 顺序下载 N 个" 重叠成 "Python 翻页找下一个的同时
	// Go 在下载当前一个"，缩短总耗时；更重要的是把每条直链 e= 过期时间窗用满 ——
	// 不会因为 Go 在下前面 7 个时让后面 8 个的签名超时。
	cmd, stdout, err := c.startSpiderTargetNew(ctx, targetNew, seenPath, outputPath)
	if err != nil {
		return result, fmt.Errorf("spider91 crawler: spider start: %w", err)
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024) // 单条 entry 远小于 4 MB；保险加大上限
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			_ = cmd.Process.Kill()
			break
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var item spiderVideoEntry
		if jerr := json.Unmarshal([]byte(line), &item); jerr != nil {
			log.Printf("[spider91] drive=%s stdout parse: %v line=%q", c.cfg.Driver.ID(), jerr, line)
			continue
		}
		result.TotalEntries++
		viewkey := strings.TrimSpace(item.Viewkey)
		if viewkey == "" || strings.TrimSpace(item.VideoURL) == "" {
			result.Failed++
			continue
		}
		if result.NewVideos >= targetNew {
			// Python 侧已用 target_new 控制；这里再兜底防止脚本异常多输出
			break
		}
		videoID := buildVideoID(c.cfg.Driver.ID(), viewkey)
		if existing, _ := c.cfg.Catalog.GetVideo(ctx, videoID); existing != nil {
			result.Skipped++
			continue
		}
		if perr := c.processOne(ctx, videoID, item); perr != nil {
			log.Printf("[spider91] drive=%s viewkey=%s failed: %v", c.cfg.Driver.ID(), viewkey, perr)
			result.Failed++
			continue
		}
		result.NewVideos++
	}
	if scerr := scanner.Err(); scerr != nil {
		log.Printf("[spider91] drive=%s stdout scan: %v", c.cfg.Driver.ID(), scerr)
	}
	if werr := cmd.Wait(); werr != nil {
		// 子进程被我们 Kill 是预期；其它错误（exit code != 0）记录日志但不当致命错误，
		// 因为流式模式下 stdout 已读完，能拿到的视频已经处理。
		if ctx.Err() == nil {
			log.Printf("[spider91] drive=%s spider exit: %v", c.cfg.Driver.ID(), werr)
		}
	}
	return result, nil
}

// writeSeenViewkeys 把当前 drive 下已入库的 viewkey 写到 path，供 Python 脚本读取。
//
// 注意：不能用 ListVideoFileIDsByDrive（按 drive_id 查），因为 spider91
// 视频被 spider91migrate 迁移到 PikPak 后 drive_id 已经不再是这个 drive。
// 改用 ListSpider91Viewkeys：它按 video.id 前缀（"spider91-<driveID>-"）查，
// 不受迁移影响——id 永远是 spider91-<driveID>-<viewkey>。
func (c *Crawler) writeSeenViewkeys(ctx context.Context, path string) (int, error) {
	viewkeys, err := c.cfg.Catalog.ListSpider91Viewkeys(ctx, c.cfg.Driver.ID())
	if err != nil {
		return 0, err
	}
	seen := make(map[string]struct{}, len(viewkeys))
	for _, vk := range viewkeys {
		vk = strings.TrimSpace(vk)
		if vk == "" {
			continue
		}
		seen[vk] = struct{}{}
	}

	tmp := path + ".part"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return 0, err
	}
	for viewkey := range seen {
		if _, err := f.WriteString(viewkey + "\n"); err != nil {
			_ = f.Close()
			_ = os.Remove(tmp)
			return 0, err
		}
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return 0, err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return 0, err
	}
	return len(seen), nil
}

// runSpiderTargetNew 启动 Python 子进程（--target-new + --seen-viewkeys-file
// + --stream-output）。返回 cmd 和 stdout 的 reader；调用方按行 JSON 消费 stdout，
// 每读到一行就立即 processOne，下完再读下一行。Python 的日志被引到 stderr，
// 由本函数转发到 backend log，不影响 stdout 的 JSONL 协议。
//
// 使用方负责调 cmd.Wait()，并 close stdout reader。
func (c *Crawler) startSpiderTargetNew(ctx context.Context, targetNew int, seenPath, outputPath string) (*exec.Cmd, io.ReadCloser, error) {
	args := []string{
		c.cfg.ScriptPath,
		"--target-new", fmt.Sprintf("%d", targetNew),
		"--seen-viewkeys-file", seenPath,
		"--output", outputPath,
		"--no-resume",
		"--quiet",
		"--stream-output",
	}
	// 子进程的 ctx 走外层 ctx 即可，不再额外加 SpiderTimeout —— 流式模式下
	// 单个视频的下载在 Go 端做超时控制（DownloadTimeout）；爬虫脚本主要时间在
	// 列表/详情页 + 网络等待，整轮上限通过外层 ctx 控制更准确。
	cmd := exec.CommandContext(ctx, c.cfg.PythonPath, args...)
	if c.cfg.WorkDir != "" {
		cmd.Dir = c.cfg.WorkDir
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = stdout.Close()
		return nil, nil, fmt.Errorf("stderr pipe: %w", err)
	}
	log.Printf("[spider91] drive=%s exec %s --target-new=%d --seen=%s --output=%s",
		c.cfg.Driver.ID(), c.cfg.ScriptPath, targetNew, seenPath, outputPath)
	if err := cmd.Start(); err != nil {
		_ = stdout.Close()
		_ = stderr.Close()
		return nil, nil, fmt.Errorf("start: %w", err)
	}
	// stderr 转发到 backend log。子进程退出时 reader 自动 EOF，goroutine 自然结束。
	go forwardSpiderLog(c.cfg.Driver.ID(), stderr)
	return cmd, stdout, nil
}

// forwardSpiderLog 把 Python stderr 逐行转发到 backend log，便于调试。
func forwardSpiderLog(driveID string, r io.Reader) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		log.Printf("[spider91:py] drive=%s %s", driveID, line)
	}
}

// processOne 处理单个 viewkey：下载视频 + 封面 + 复制封面 + 入库。
// 任一步失败会清理已写入的临时文件，不留半成品。
func (c *Crawler) processOne(ctx context.Context, videoID string, item spiderVideoEntry) error {
	viewkey := item.Viewkey
	// 视频文件后缀按直链 URL 真实后缀来定，避免直链返回的不是 mp4 时存错容器。
	videoExt := detectVideoExt(item.VideoURL)
	videoFile := viewkey + videoExt
	// 封面后缀同理，但 91porn 的封面绝大多数是 jpg；URL 提示其它格式时尊重之。
	thumbFile := viewkey + detectThumbExt(item.ThumbURL)

	videoPath, err := c.cfg.Driver.VideoPath(videoFile)
	if err != nil {
		return err
	}
	thumbPath, err := c.cfg.Driver.ThumbPath(thumbFile)
	if err != nil {
		return err
	}

	// 视频先下载（必须）；失败直接退出。
	dlCtx, cancel := context.WithTimeout(ctx, c.cfg.DownloadTimeout)
	defer cancel()

	videoSize, err := c.downloadAtomic(dlCtx, item.VideoURL, videoPath, item.DetailURL)
	if err != nil {
		return fmt.Errorf("download video: %w", err)
	}

	// 封面下载失败不致命，视频本身仍入库；下方在 UpsertVideo 后会把
	// thumbnail_status 显式标 'failed'（spider91 drive 的 thumb worker 按设计
	// 不处理 spider91 视频，没人能"兜底"）。
	thumbReady := false
	if strings.TrimSpace(item.ThumbURL) != "" {
		if _, err := c.downloadAtomic(dlCtx, item.ThumbURL, thumbPath, item.DetailURL); err != nil {
			log.Printf("[spider91] drive=%s viewkey=%s thumb download failed: %v", c.cfg.Driver.ID(), viewkey, err)
		} else {
			thumbReady = true
		}
	}

	// 把封面复制到 backend 的标准 thumbs 目录，让 /p/thumb/{videoID} 直接命中。
	if thumbReady && c.cfg.CommonThumbDir != "" {
		if err := os.MkdirAll(c.cfg.CommonThumbDir, 0o755); err != nil {
			log.Printf("[spider91] drive=%s mkdir common thumbs: %v", c.cfg.Driver.ID(), err)
			thumbReady = false
		} else {
			dst := filepath.Join(c.cfg.CommonThumbDir, videoID+".jpg")
			if err := copyFileAtomic(thumbPath, dst); err != nil {
				log.Printf("[spider91] drive=%s viewkey=%s copy thumb to common dir: %v", c.cfg.Driver.ID(), viewkey, err)
				thumbReady = false
			}
		}
	}

	// 入库
	now := time.Now()
	v := &catalog.Video{
		ID:            videoID,
		DriveID:       c.cfg.Driver.ID(),
		FileID:        videoFile,
		FileName:      videoFile,
		Title:         strings.TrimSpace(item.Title),
		Author:        DefaultAuthor,
		// 所有 spider91 视频统一打 "91porn" 标签，便于前台筛选；
		// UpsertVideo 会在新视频插入时自动 replaceVideoTags(... source="auto", createMissing=true)。
		Tags:          []string{DefaultTag},
		Ext:           strings.TrimPrefix(videoExt, "."),
		Quality:       "HD",
		Size:          videoSize,
		PreviewStatus: "pending",
		PublishedAt:   now,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if v.Title == "" {
		v.Title = viewkey
	}
	if thumbReady {
		// 设了 ThumbnailURL 后 thumb worker 会跳过这条视频，
		// 不再尝试用 ffmpeg 抽帧（封面已经是网站原图）。
		v.ThumbnailURL = "/p/thumb/" + v.ID
	}
	if err := c.cfg.Catalog.UpsertVideo(ctx, v); err != nil {
		// 入库失败 → 把刚下载的文件清理掉，避免占盘且下次还要清
		_ = os.Remove(videoPath)
		_ = os.Remove(thumbPath)
		return fmt.Errorf("upsert video: %w", err)
	}
	if !thumbReady {
		// 网站封面下载失败的视频：spider91 drive 的 thumb worker 按设计不
		// 处理 spider91 视频（封面应是网站原图直接保存），所以没人接手。
		// 显式标 'failed' 让 CountVideosNeedingThumbnail 排除（条件 status
		// != 'failed'），否则 enqueueDriveGeneration → waitForThumbnailsBeforePreview
		// 会因为 count > 0 把 teaser 入队永远卡在等待循环里。
		_ = c.cfg.Catalog.UpdateVideoMeta(ctx, v.ID, catalog.VideoMetaPatch{
			ThumbnailStatus: "failed",
		})
	}
	if c.cfg.OnNewVideo != nil {
		c.cfg.OnNewVideo(v)
	}
	log.Printf("[spider91] drive=%s viewkey=%s ok title=%q size=%d", c.cfg.Driver.ID(), viewkey, v.Title, v.Size)
	return nil
}

// downloadAtomic 下载 url 到 dst，先写到 dst.part 再 rename，避免半截文件。
// 返回最终文件大小。
func (c *Crawler) downloadAtomic(ctx context.Context, src, dst, referer string) (int64, error) {
	if strings.TrimSpace(src) == "" {
		return 0, errors.New("empty url")
	}
	if _, err := url.Parse(src); err != nil {
		return 0, fmt.Errorf("parse url: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return 0, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", downloadUA)
	if referer != "" {
		req.Header.Set("Referer", referer)
	}

	resp, err := c.cfg.HTTPClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, fmt.Errorf("http %d", resp.StatusCode)
	}

	tmp := dst + ".part"
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return 0, err
	}
	written, copyErr := io.Copy(out, resp.Body)
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(tmp)
		return 0, copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return 0, closeErr
	}
	if written <= 0 {
		_ = os.Remove(tmp)
		return 0, errors.New("empty body")
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return 0, err
	}
	return written, nil
}

// copyFileAtomic 把 src 复制到 dst，先写 .part 再 rename。
func copyFileAtomic(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp := dst + ".part"
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

// BuildVideoID 给定 driveID + viewkey，按统一规则生成 catalog 中 videos.id。
// 与 scanner 用法一致：<kind>-<driveID>-<fileID>。
func BuildVideoID(driveID, viewkey string) string {
	return buildVideoID(driveID, viewkey)
}

func buildVideoID(driveID, viewkey string) string {
	return Kind + "-" + driveID + "-" + viewkey
}

// detectVideoExt 从直链 URL 推断视频文件后缀。
//
// 91porn 直链路径形如 https://.../mp43/xxxx.mp4?st=...，path.Ext("xxxx.mp4") = ".mp4"。
// 但任何爬虫都可能拿到 .flv / .m3u8 / 没扩展名等情况；这里维护一个白名单：
//   - .mp4 / .webm / .mkv / .mov / .m4v / .flv / .avi → 直接用
//   - .m3u8 / .ts → 是流媒体清单，不能直接当单文件视频保存，回退到 .mp4，让上层察觉到下载结果异常
//   - 其它 → .mp4 兜底
func detectVideoExt(rawURL string) string {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u == nil {
		return ".mp4"
	}
	base := path.Base(u.Path)
	ext := strings.ToLower(path.Ext(base))
	switch ext {
	case ".mp4", ".webm", ".mkv", ".mov", ".m4v", ".flv", ".avi":
		return ext
	}
	return ".mp4"
}

// detectThumbExt 从封面 URL 推断后缀。默认 .jpg。
func detectThumbExt(rawURL string) string {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u == nil {
		return ".jpg"
	}
	base := path.Base(u.Path)
	ext := strings.ToLower(path.Ext(base))
	switch ext {
	case ".jpg", ".jpeg", ".png", ".webp", ".gif":
		return ext
	}
	return ".jpg"
}
