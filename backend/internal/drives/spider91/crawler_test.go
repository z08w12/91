package spider91

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/video-site/backend/internal/catalog"
)

// TestCrawlerRunOnceFullFlow 用一个伪 python 脚本 + httptest 服务器
// 把 Crawler.RunOnce 的完整流程跑一遍：脚本生成 JSON、下载视频和封面、入库、
// 重复运行跳过已存在的 viewkey。
func TestCrawlerRunOnceFullFlow(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-based fake script only on unix")
	}

	tmp := t.TempDir()

	// 1. 假 HTTP 服务器：根据路径返回视频数据或封面数据
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "video1.mp4"):
			w.Header().Set("Content-Type", "video/mp4")
			_, _ = w.Write([]byte("FAKEVIDEO1"))
		case strings.Contains(r.URL.Path, "video2.mp4"):
			w.Header().Set("Content-Type", "video/mp4")
			_, _ = w.Write([]byte("FAKEVIDEO2BYTES"))
		case strings.Contains(r.URL.Path, "thumb1.jpg"):
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write([]byte("\xff\xd8\xff\xe0fakejpg1"))
		case strings.Contains(r.URL.Path, "thumb2.jpg"):
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write([]byte("\xff\xd8\xff\xe0fakejpg2"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	// 2. 假 python 脚本：解析 --output / --stream-output 参数，
	//    在 stream 模式下逐行 echo 每条视频的 JSON 到 stdout（模拟 Python 端 stream），
	//    同时仍写 --output 文件作归档。
	videoEntries := []map[string]string{
		{
			"title":      "Video One",
			"thumb_url":  srv.URL + "/thumbs/thumb1.jpg",
			"video_url":  srv.URL + "/videos/video1.mp4",
			"viewkey":    "vk-001",
			"detail_url": srv.URL + "/v.php?viewkey=vk-001",
		},
		{
			"title":      "Video Two",
			"thumb_url":  srv.URL + "/thumbs/thumb2.jpg",
			"video_url":  srv.URL + "/videos/video2.mp4",
			"viewkey":    "vk-002",
			"detail_url": srv.URL + "/v.php?viewkey=vk-002",
		},
	}
	scriptPath := filepath.Join(tmp, "fake_spider.sh")
	scriptBody := buildFakeSpiderScript(videoEntries)
	if err := os.WriteFile(scriptPath, []byte(scriptBody), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	// 3. 准备 catalog + driver + crawler
	dbPath := filepath.Join(tmp, "test.db")
	cat, err := catalog.Open(dbPath)
	if err != nil {
		t.Fatalf("open catalog: %v", err)
	}
	defer cat.Close()

	driveID := "spider91-test"
	rootDir := filepath.Join(tmp, "spider91", driveID)
	commonThumbs := filepath.Join(tmp, "previews", "thumbs")
	drv := New(Config{ID: driveID, RootDir: rootDir})

	// 把 drive 也写入 catalog（Crawler 不直接读，但 main 真实流程会写）
	if err := cat.UpsertDrive(context.Background(), &catalog.Drive{
		ID:   driveID,
		Kind: Kind,
		Name: "test crawler",
	}); err != nil {
		t.Fatalf("upsert drive: %v", err)
	}

	var newVideos []*catalog.Video
	c := NewCrawler(CrawlerConfig{
		Driver:         drv,
		Catalog:        cat,
		PythonPath:     "sh",
		ScriptPath:     scriptPath,
		CommonThumbDir: commonThumbs,
		SpiderTimeout:  10 * time.Second,
		DownloadTimeout: 10 * time.Second,
		OnNewVideo: func(v *catalog.Video) {
			newVideos = append(newVideos, v)
		},
	})

	// 4. 第一次 RunOnce：应该新入库 2 条
	res, err := c.RunOnce(context.Background(), 15)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if res.NewVideos != 2 || res.Skipped != 0 || res.Failed != 0 {
		t.Fatalf("first run result: new=%d skipped=%d failed=%d, want 2/0/0",
			res.NewVideos, res.Skipped, res.Failed)
	}
	if res.TargetNew != 15 {
		t.Fatalf("first run TargetNew = %d, want 15", res.TargetNew)
	}
	if res.SeenSnapshot != 0 {
		t.Fatalf("first run SeenSnapshot = %d, want 0 (catalog empty before first run)", res.SeenSnapshot)
	}
	if len(newVideos) != 2 {
		t.Fatalf("OnNewVideo called %d times, want 2", len(newVideos))
	}

	// 5. 检查文件落盘
	for _, item := range []struct {
		viewkey  string
		size     int64
		thumbLen int
	}{
		{"vk-001", 10, 11},
		{"vk-002", 15, 11},
	} {
		videoPath := filepath.Join(rootDir, "videos", item.viewkey+".mp4")
		info, err := os.Stat(videoPath)
		if err != nil {
			t.Fatalf("video %s missing: %v", item.viewkey, err)
		}
		if info.Size() != item.size {
			t.Fatalf("video %s size = %d, want %d", item.viewkey, info.Size(), item.size)
		}

		thumbPath := filepath.Join(rootDir, "thumbs", item.viewkey+".jpg")
		if _, err := os.Stat(thumbPath); err != nil {
			t.Fatalf("thumb %s missing: %v", item.viewkey, err)
		}

		// 复制到 common thumbs 目录的副本，名字按 videoID 来
		videoID := BuildVideoID(driveID, item.viewkey)
		commonThumb := filepath.Join(commonThumbs, videoID+".jpg")
		if _, err := os.Stat(commonThumb); err != nil {
			t.Fatalf("common thumb %s missing: %v", commonThumb, err)
		}
	}

	// 6. 检查 catalog 入库
	for _, viewkey := range []string{"vk-001", "vk-002"} {
		videoID := BuildVideoID(driveID, viewkey)
		v, err := cat.GetVideo(context.Background(), videoID)
		if err != nil {
			t.Fatalf("GetVideo %s: %v", videoID, err)
		}
		if v.DriveID != driveID {
			t.Fatalf("video %s drive_id = %q want %q", videoID, v.DriveID, driveID)
		}
		if v.FileID != viewkey+".mp4" {
			t.Fatalf("video %s file_id = %q want %q", videoID, v.FileID, viewkey+".mp4")
		}
		if v.ThumbnailURL == "" {
			t.Fatalf("video %s ThumbnailURL empty (cover should be ready)", videoID)
		}
		if v.Author != DefaultAuthor {
			t.Fatalf("video %s author = %q want %q", videoID, v.Author, DefaultAuthor)
		}
		// 每条视频都应该带 "91porn" 标签（UpsertVideo 路径自动同步 tags 表）
		hasDefaultTag := false
		for _, tag := range v.Tags {
			if tag == DefaultTag {
				hasDefaultTag = true
				break
			}
		}
		if !hasDefaultTag {
			t.Fatalf("video %s tags = %v, want contain %q", videoID, v.Tags, DefaultTag)
		}
	}

	// 7. 第二次 RunOnce：viewkey 已存在 → 全部 skipped，无新文件下载
	newVideos = nil
	res2, err := c.RunOnce(context.Background(), 15)
	if err != nil {
		t.Fatalf("second RunOnce: %v", err)
	}
	if res2.NewVideos != 0 {
		t.Fatalf("second run NewVideos = %d, want 0", res2.NewVideos)
	}
	if res2.Skipped != 2 {
		t.Fatalf("second run Skipped = %d, want 2", res2.Skipped)
	}
	// 第二次运行时 catalog 里已经有 2 条，seen snapshot 应该写出 2 个 viewkey
	if res2.SeenSnapshot != 2 {
		t.Fatalf("second run SeenSnapshot = %d, want 2", res2.SeenSnapshot)
	}
	if len(newVideos) != 0 {
		t.Fatalf("second run OnNewVideo fired %d times, want 0", len(newVideos))
	}
}

// TestCrawlerRunOnceMissingScript 报错而不是 panic。
func TestCrawlerRunOnceMissingScript(t *testing.T) {
	tmp := t.TempDir()
	cat, err := catalog.Open(filepath.Join(tmp, "x.db"))
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	defer cat.Close()
	drv := New(Config{ID: "x", RootDir: filepath.Join(tmp, "x")})

	c := NewCrawler(CrawlerConfig{
		Driver:     drv,
		Catalog:    cat,
		PythonPath: "python3",
		ScriptPath: filepath.Join(tmp, "does-not-exist.py"),
	})

	if _, err := c.RunOnce(context.Background(), 1); err == nil {
		t.Fatalf("expected error for missing script")
	}
}


// TestCrawlerThumbDownloadFailureMarksStatusFailed 验证：网站封面下载失败时
// crawler 把 thumbnail_status 显式标 'failed'，避免 enqueueDriveGeneration 的
// waitForThumbnailsBeforePreview 因为 count > 0 把 teaser 卡死等待。
//
// 历史 bug：之前 thumb 下载失败仅打 log，url='', status 走 schema DEFAULT 'pending'。
// CountVideosNeedingThumbnail 条件是 url='' AND status != 'failed' → count=1。
// spider91 drive 的 thumb worker 按设计不处理 spider91 视频 → 没人会改 status。
// 结果 teaser 永远卡在 [preview] waiting for 1 thumbnails before teaser generation。
func TestCrawlerThumbDownloadFailureMarksStatusFailed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-based fake script only on unix")
	}
	tmp := t.TempDir()

	// 假 HTTP 服务器：thumb 路径返回 500，video 正常返回字节。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "video.mp4"):
			w.Header().Set("Content-Type", "video/mp4")
			_, _ = w.Write([]byte("FAKEVIDEO"))
		case strings.Contains(r.URL.Path, "thumb.jpg"):
			http.Error(w, "broken", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	videoEntries := []map[string]string{
		{
			"title":      "Thumb Failure Video",
			"thumb_url":  srv.URL + "/thumbs/thumb.jpg",
			"video_url":  srv.URL + "/videos/video.mp4",
			"viewkey":    "vk-thumb-fail",
			"detail_url": srv.URL + "/v.php?viewkey=vk-thumb-fail",
		},
	}
	scriptPath := filepath.Join(tmp, "fake.sh")
	if err := os.WriteFile(scriptPath, []byte(buildFakeSpiderScript(videoEntries)), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	cat, err := catalog.Open(filepath.Join(tmp, "test.db"))
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	defer cat.Close()

	driveID := "thumbfail-drive"
	drv := New(Config{ID: driveID, RootDir: filepath.Join(tmp, "spider91", driveID)})
	if err := cat.UpsertDrive(context.Background(), &catalog.Drive{
		ID: driveID, Kind: Kind, Name: "thumbfail",
	}); err != nil {
		t.Fatalf("upsert drive: %v", err)
	}

	c := NewCrawler(CrawlerConfig{
		Driver:          drv,
		Catalog:         cat,
		PythonPath:      "sh",
		ScriptPath:      scriptPath,
		CommonThumbDir:  filepath.Join(tmp, "previews", "thumbs"),
		SpiderTimeout:   10 * time.Second,
		DownloadTimeout: 10 * time.Second,
	})

	res, err := c.RunOnce(context.Background(), 5)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if res.NewVideos != 1 {
		t.Fatalf("expected 1 new video, got %d (failed=%d)", res.NewVideos, res.Failed)
	}

	got, err := cat.GetVideo(context.Background(), "spider91-"+driveID+"-vk-thumb-fail")
	if err != nil {
		t.Fatalf("get video: %v", err)
	}
	if got.ThumbnailURL != "" {
		t.Errorf("ThumbnailURL = %q, want empty (download failed)", got.ThumbnailURL)
	}

	// 关键断言：CountVideosNeedingThumbnail 应该返回 0。
	// 该函数的 SQL 条件是 `url = '' AND status != 'failed'`；如果 crawler 没把
	// status 标 'failed'（schema DEFAULT 'pending'），count 就会是 1，外层
	// waitForThumbnailsBeforePreview 会因为 count > 0 把 teaser 卡死等待。
	count, err := cat.CountVideosNeedingThumbnail(context.Background(), driveID)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Fatalf("CountVideosNeedingThumbnail = %d, want 0 (status should be 'failed' to unblock teaser worker)", count)
	}
}


// buildFakeSpiderScript 生成一个伪 python 脚本（其实是 sh）。
//
// 行为：
//   - 解析 --output FILE / --stream-output 两个 flag
//   - --stream-output 时：逐行输出每个 entry 的 JSON 到 stdout 并 flush
//   - --output 时：把完整 JSON 数据写到 FILE（向后兼容，且作归档）
//
// 用 sh 来写是为了避免 Python 依赖。每条 entry 的 JSON 用 Go marshal 出来后嵌入。
func buildFakeSpiderScript(entries []map[string]string) string {
	var sb strings.Builder
	sb.WriteString("#!/bin/sh\n")
	sb.WriteString("out=\"\"; stream=0\n")
	sb.WriteString("while [ $# -gt 0 ]; do case \"$1\" in --output) out=\"$2\"; shift 2;; --stream-output) stream=1; shift;; *) shift;; esac; done\n")

	// stream 模式：逐行 echo
	sb.WriteString("if [ \"$stream\" = \"1\" ]; then\n")
	for _, e := range entries {
		raw, _ := json.Marshal(e)
		// 用单引号 here-string 形式确保 JSON 中的双引号原样出来
		sb.WriteString("  cat <<'STREAM_EOF'\n")
		sb.Write(raw)
		sb.WriteString("\nSTREAM_EOF\n")
	}
	sb.WriteString("fi\n")

	// 写 --output 文件（带完整 wrapper）
	sb.WriteString("if [ -n \"$out\" ]; then\n")
	sb.WriteString("  mkdir -p \"$(dirname \"$out\")\" 2>/dev/null\n")
	sb.WriteString("  cat > \"$out\" <<'OUT_EOF'\n")
	wrapper := map[string]any{
		"crawl_time":  "2026-01-01T00:00:00",
		"total_videos": len(entries),
		"videos":      entries,
	}
	wrapped, _ := json.MarshalIndent(wrapper, "", "  ")
	sb.Write(wrapped)
	sb.WriteString("\nOUT_EOF\n")
	sb.WriteString("fi\n")
	return sb.String()
}
