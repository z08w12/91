package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/video-site/backend/internal/api"
	"github.com/video-site/backend/internal/auth"
	"github.com/video-site/backend/internal/catalog"
	"github.com/video-site/backend/internal/config"
	"github.com/video-site/backend/internal/drives"
	"github.com/video-site/backend/internal/drives/localupload"
	"github.com/video-site/backend/internal/drives/onedrive"
	"github.com/video-site/backend/internal/drives/p115"
	"github.com/video-site/backend/internal/drives/pikpak"
	"github.com/video-site/backend/internal/drives/quark"
	"github.com/video-site/backend/internal/drives/spider91"
	"github.com/video-site/backend/internal/drives/wopan"
	"github.com/video-site/backend/internal/preview"
	"github.com/video-site/backend/internal/proxy"
	"github.com/video-site/backend/internal/scanner"
	"github.com/video-site/backend/internal/spider91migrate"
)

func main() {
	cfgPath := "./config.yaml"
	if v := os.Getenv("VIDEO_CONFIG"); v != "" {
		cfgPath = v
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	if err := os.MkdirAll(filepath.Dir(cfg.Storage.DBPath), 0o755); err != nil {
		log.Fatalf("mkdir db dir: %v", err)
	}
	if err := os.MkdirAll(cfg.Storage.LocalPreviewDir, 0o755); err != nil {
		log.Fatalf("mkdir preview dir: %v", err)
	}

	cat, err := catalog.Open(cfg.Storage.DBPath)
	if err != nil {
		log.Fatalf("open catalog: %v", err)
	}
	defer cat.Close()

	app := &App{
		cfg:              cfg,
		cat:              cat,
		registry:         proxy.NewRegistry(),
		workers:          make(map[string]*preview.Worker),
		thumbWorkers:     make(map[string]*preview.ThumbWorker),
		spider91Crawlers: make(map[string]*spider91.Crawler),
	}
	app.proxy = proxy.New(app.registry)
	app.spider91Migrator = spider91migrate.New(spider91migrate.Config{
		Catalog:          cat,
		Registry:         app.registry,
		GetTargetDriveID: func() string { return app.Spider91UploadDriveID() },
	})

	// 初始化现有 drives
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	app.loadPreviewEnabled(ctx)
	app.loadTheme(ctx)
	app.loadSpider91UploadDriveID(ctx)
	if err := app.attachLocalUpload(ctx); err != nil {
		log.Printf("[local-upload] attach failed: %v", err)
	}

	existing, err := cat.ListDrives(ctx)
	if err != nil {
		log.Fatalf("list drives: %v", err)
	}
	for _, d := range existing {
		if err := app.attachDrive(ctx, d); err != nil {
			log.Printf("[drive %s] attach failed: %v", d.ID, err)
		}
	}

	authr := &auth.Authenticator{
		Username: cfg.Server.Admin.Username,
		Password: cfg.Server.Admin.Password,
		Catalog:  cat,
	}

	apiServer := &api.Server{
		Catalog:    cat,
		Proxy:      app.proxy,
		LocalDir:   cfg.Storage.LocalPreviewDir,
		UploadDir:  app.localUploadDir(),
		OnVideoUploaded: func(v *catalog.Video) {
			app.enqueueUploadedVideo(ctx, v)
		},
		GetTheme: func() string { return app.Theme() },
	}

	adminServer := &api.AdminServer{
		Catalog:         cat,
		Auth:            authr,
		LocalPreviewDir: cfg.Storage.LocalPreviewDir,
		OnDriveSaved: func(driveID string) error {
			d, err := cat.GetDrive(ctx, driveID)
			if err != nil {
				return err
			}
			return app.attachDrive(ctx, d)
		},
		OnDriveRemoved: func(driveID string) {
			app.detachDrive(driveID)
		},
		OnScanRequested: func(driveID string) {
			// spider91 的"重扫"等同于手动触发一次爬取；其它 drive 走标准 scan
			app.mu.Lock()
			_, isSpider91 := app.spider91Crawlers[driveID]
			app.mu.Unlock()
			if isSpider91 {
				go app.runSpider91Crawl(ctx, driveID)
				return
			}
			go app.runScan(ctx, driveID)
		},
		OnRegenPreview: func(videoID string) {
			go app.regenPreview(ctx, videoID)
		},
		OnRegenAllPreviews: func() {
			go app.regenAllPreviews(ctx)
		},
		OnRegenFailedPreviews: func(driveID string) {
			go app.regenFailedPreviews(ctx, driveID)
		},
		GetDriveGenerationStatuses: func() map[string]api.DriveGenerationStatuses {
			return app.driveGenerationStatuses()
		},
		GetPreviewEnabled: func() bool { return app.PreviewEnabled() },
		SetPreviewEnabled: func(enabled bool) error {
			return app.SetPreviewEnabled(ctx, enabled)
		},
		GetTheme: func() string { return app.Theme() },
		SetTheme: func(theme string) error {
			return app.SetTheme(ctx, theme)
		},
		GetSpider91UploadDriveID: func() string { return app.Spider91UploadDriveID() },
		SetSpider91UploadDriveID: func(id string) error {
			return app.SetSpider91UploadDriveID(ctx, id)
		},
	}

	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(corsMiddleware(cfg.Server.AllowedOrigins))

	apiServer.RegisterRoutes(r, authr)
	adminServer.Register(r)

	// 启动定时扫描
	go app.scanLoop(ctx)
	// spider91 爬虫专用的凌晨任务（默认 00:30 触发，避开网盘扫描窗口）
	go app.crawlerLoop(ctx)
	// spider91 → PikPak 上传 worker
	go app.spider91Migrator.Run(ctx)

	srv := &http.Server{
		Addr:    cfg.Server.Listen,
		Handler: r,
	}
	go func() {
		log.Printf("video-site backend listening on %s", cfg.Server.Listen)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	// 等待退出信号
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	<-sigs
	log.Println("shutting down...")
	shutCtx, shutCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutCancel()
	_ = srv.Shutdown(shutCtx)
}

// ---------- App ----------

type App struct {
	cfg      *config.Config
	cat      *catalog.Catalog
	registry *proxy.Registry
	proxy    *proxy.Proxy

	mu           sync.Mutex
	workers      map[string]*preview.Worker
	thumbWorkers map[string]*preview.ThumbWorker
	cancels      map[string]context.CancelFunc
	// spider91Crawlers 按 driveID 索引，每个 spider91 drive 独立一个 Crawler
	spider91Crawlers map[string]*spider91.Crawler

	// 运行时 preview 开关（从 DB 读）
	previewEnabled bool
	// 全站主题（"dark" | "pink"），从 DB 读
	theme string
	// 显式指定的 spider91 → PikPak 上传目标 drive ID；
	// 未设置时由 Spider91UploadDriveID() 自动挑唯一的 PikPak drive。
	spider91UploadDriveID string

	// spider91Migrator 周期把 spider91 视频上传到 PikPak。
	spider91Migrator *spider91migrate.Migrator
}

// PreviewEnabled 线程安全读
func (a *App) PreviewEnabled() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.previewEnabled
}

// SetPreviewEnabled 切换开关，写库 + 若开启则立刻补扫 pending
func (a *App) SetPreviewEnabled(ctx context.Context, enabled bool) error {
	a.mu.Lock()
	a.previewEnabled = enabled
	a.mu.Unlock()

	val := "0"
	if enabled {
		val = "1"
	}
	if err := a.cat.SetSetting(ctx, "preview.enabled", val); err != nil {
		return err
	}

	if enabled {
		// 异步补扫所有盘
		go func() {
			for _, d := range a.registry.All() {
				a.mu.Lock()
				w := a.workers[d.ID()]
				tw := a.thumbWorkers[d.ID()]
				a.mu.Unlock()
				a.enqueueDriveGeneration(ctx, d.ID(), w, tw)
			}
		}()
	}
	return nil
}

// loadPreviewEnabled 从 DB 读运行时开关，首次启动取 config 默认值
func (a *App) loadPreviewEnabled(ctx context.Context) {
	def := "0"
	if a.cfg.Preview.Enabled {
		def = "1"
	}
	v, err := a.cat.GetSetting(ctx, "preview.enabled", def)
	if err != nil {
		log.Printf("[preview] load setting: %v (fallback to config)", err)
		a.mu.Lock()
		a.previewEnabled = a.cfg.Preview.Enabled
		a.mu.Unlock()
		return
	}
	a.mu.Lock()
	a.previewEnabled = v == "1"
	a.mu.Unlock()
}

// Theme 线程安全读当前主题。
func (a *App) Theme() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.theme == "" {
		return "dark"
	}
	return a.theme
}

// SetTheme 切换并持久化主题；未知值会返回错误。
func (a *App) SetTheme(ctx context.Context, theme string) error {
	if theme != "dark" && theme != "pink" {
		return fmt.Errorf("unsupported theme %q", theme)
	}
	a.mu.Lock()
	a.theme = theme
	a.mu.Unlock()
	return a.cat.SetSetting(ctx, "ui.theme", theme)
}

// loadTheme 从 DB 读全站主题；找不到时回退到 "dark"。
func (a *App) loadTheme(ctx context.Context) {
	v, err := a.cat.GetSetting(ctx, "ui.theme", "dark")
	if err != nil {
		log.Printf("[theme] load setting: %v (fallback to dark)", err)
		a.mu.Lock()
		a.theme = "dark"
		a.mu.Unlock()
		return
	}
	if v != "pink" && v != "dark" {
		v = "dark"
	}
	a.mu.Lock()
	a.theme = v
	a.mu.Unlock()
}

// Spider91UploadDriveID 返回当前生效的 PikPak 上传目标 drive ID。
// 优先返回管理员显式设置的值；未设置时，如果系统中只有一个 pikpak drive，
// 返回它；多个或没有则返回空串（迁移 worker 跳过）。
func (a *App) Spider91UploadDriveID() string {
	a.mu.Lock()
	explicit := a.spider91UploadDriveID
	a.mu.Unlock()
	if explicit != "" {
		// 验证显式设置的 drive 仍然存在且是 pikpak 类型；不在则降级到自动选取
		if d, ok := a.registry.Get(explicit); ok && d.Kind() == "pikpak" {
			return explicit
		}
	}
	var found string
	for _, d := range a.registry.All() {
		if d.Kind() != "pikpak" {
			continue
		}
		if found != "" {
			// 多个 PikPak drive 时不自动选；管理员必须显式指定
			return ""
		}
		found = d.ID()
	}
	return found
}

// SetSpider91UploadDriveID 设置 PikPak 上传目标 drive ID 并持久化。
// 接受空字符串（清除显式设置，回退到自动模式）。
// 设置一个不存在或不是 pikpak 类型的 drive 会返回错误。
func (a *App) SetSpider91UploadDriveID(ctx context.Context, driveID string) error {
	driveID = strings.TrimSpace(driveID)
	if driveID != "" {
		d, ok := a.registry.Get(driveID)
		if !ok {
			return fmt.Errorf("drive %q not found", driveID)
		}
		if d.Kind() != "pikpak" {
			return fmt.Errorf("drive %q kind=%s, only pikpak can be spider91 upload target", driveID, d.Kind())
		}
	}
	a.mu.Lock()
	a.spider91UploadDriveID = driveID
	a.mu.Unlock()
	return a.cat.SetSetting(ctx, "spider91.upload_drive_id", driveID)
}

// loadSpider91UploadDriveID 从 DB 读上传目标 drive ID 设置；不存在时使用空串。
func (a *App) loadSpider91UploadDriveID(ctx context.Context) {
	v, err := a.cat.GetSetting(ctx, "spider91.upload_drive_id", "")
	if err != nil {
		log.Printf("[spider91] load upload drive setting: %v", err)
		return
	}
	a.mu.Lock()
	a.spider91UploadDriveID = strings.TrimSpace(v)
	a.mu.Unlock()
}

func (a *App) driveGenerationStatuses() map[string]api.DriveGenerationStatuses {
	a.mu.Lock()
	previewWorkers := make(map[string]*preview.Worker, len(a.workers))
	for id, worker := range a.workers {
		previewWorkers[id] = worker
	}
	thumbWorkers := make(map[string]*preview.ThumbWorker, len(a.thumbWorkers))
	for id, worker := range a.thumbWorkers {
		thumbWorkers[id] = worker
	}
	a.mu.Unlock()

	out := make(map[string]api.DriveGenerationStatuses, len(previewWorkers)+len(thumbWorkers))
	for id, worker := range previewWorkers {
		status := out[id]
		status.Preview = generationStatusFromPreview(worker.Status())
		out[id] = status
	}
	for id, worker := range thumbWorkers {
		status := out[id]
		status.Thumbnail = generationStatusFromPreview(worker.Status())
		missing, err := a.cat.CountVideosNeedingThumbnail(context.Background(), id)
		if err != nil {
			log.Printf("[thumb] count missing thumbnails %s: %v", id, err)
		} else {
			status.Thumbnail.QueueLength = missing
			if missing > 0 && status.Thumbnail.State == "idle" {
				status.Thumbnail.State = "queued"
			}
		}
		out[id] = status
	}
	return out
}

func generationStatusFromPreview(status preview.TaskStatus) api.GenerationStatus {
	state := status.State
	if state == "" {
		state = "idle"
	}
	out := api.GenerationStatus{
		State:        state,
		CurrentTitle: status.CurrentTitle,
		QueueLength:  status.QueueLength,
	}
	if !status.CooldownUntil.IsZero() {
		out.CooldownUntil = status.CooldownUntil.Format(time.RFC3339)
	}
	return out
}

func (a *App) attachDrive(ctx context.Context, d *catalog.Drive) error {
	var drv drives.Drive
	switch d.Kind {
	case "quark":
		drv = quark.New(quark.Config{
			ID:     d.ID,
			Cookie: d.Credentials["cookie"],
			RootID: d.RootID,
			OnCookieUpdate: func(cookie string) {
				d.Credentials["cookie"] = cookie
				_ = a.cat.UpsertDrive(ctx, d)
			},
		})
	case "p115":
		drv = p115.New(p115.Config{
			ID:     d.ID,
			Cookie: d.Credentials["cookie"],
			RootID: d.RootID,
		})
	case "pikpak":
		drv = pikpak.New(pikpak.Config{
			ID:               d.ID,
			Username:         d.Credentials["username"],
			Password:         d.Credentials["password"],
			Platform:         d.Credentials["platform"],
			RefreshToken:     d.Credentials["refresh_token"],
			AccessToken:      d.Credentials["access_token"],
			CaptchaToken:     d.Credentials["captcha_token"],
			DeviceID:         d.Credentials["device_id"],
			RootID:           d.RootID,
			DisableMediaLink: pikpak.ParseBoolDefault(d.Credentials["disable_media_link"], true),
			OnTokenUpdate: func(access, refresh, captcha, deviceID string) {
				d.Credentials["access_token"] = access
				d.Credentials["refresh_token"] = refresh
				d.Credentials["captcha_token"] = captcha
				d.Credentials["device_id"] = deviceID
				_ = a.cat.UpsertDrive(ctx, d)
			},
		})
	case "wopan":
		drv = wopan.New(wopan.Config{
			ID:           d.ID,
			AccessToken:  d.Credentials["access_token"],
			RefreshToken: d.Credentials["refresh_token"],
			FamilyID:     d.Credentials["family_id"],
			RootID:       d.RootID,
			OnTokenUpdate: func(access, refresh string) {
				d.Credentials["access_token"] = access
				d.Credentials["refresh_token"] = refresh
				_ = a.cat.UpsertDrive(ctx, d)
			},
		})
	case "onedrive":
		drv = onedrive.New(onedrive.Config{
			ID:           d.ID,
			RootID:       d.RootID,
			Region:       d.Credentials["region"],
			AccessToken:  d.Credentials["access_token"],
			RefreshToken: d.Credentials["refresh_token"],
			IsSharePoint: parseBoolDefault(d.Credentials["is_sharepoint"], false),
			SiteID:       d.Credentials["site_id"],
			RenewAPIURL:  d.Credentials["api_url_address"],
			OnTokenUpdate: func(access, refresh string) {
				if d.Credentials == nil {
					d.Credentials = make(map[string]string)
				}
				d.Credentials["access_token"] = access
				d.Credentials["refresh_token"] = refresh
				_ = a.cat.UpsertDrive(ctx, d)
			},
		})
	case spider91.Kind:
		drv = spider91.New(spider91.Config{
			ID:      d.ID,
			RootDir: a.spider91DriveDir(d.ID),
		})
	default:
		return fmt.Errorf("unknown drive kind: %s", d.Kind)
	}

	if err := drv.Init(ctx); err != nil {
		d.Status = "error"
		d.LastError = err.Error()
		_ = a.cat.UpsertDrive(ctx, d)
		return err
	}

	d.Status = "ok"
	d.LastError = ""
	_ = a.cat.UpsertDrive(ctx, d)

	a.registry.Set(d.ID, drv)

	// preview worker
	gen := preview.New(preview.Config{
		FFmpegPath:      a.cfg.Preview.FFmpegPath,
		FFprobePath:     a.cfg.Preview.FFprobePath,
		DurationSeconds: a.cfg.Preview.DurationSeconds,
		Width:           a.cfg.Preview.Width,
		Segments:        a.cfg.Preview.Segments,
		LocalDir:        a.cfg.Storage.LocalPreviewDir,
	})
	worker := preview.NewWorker(gen, a.cat, drv)
	thumbWorker := preview.NewThumbWorker(gen, a.cat, drv)

	workerCtx, cancel := context.WithCancel(ctx)
	go worker.Run(workerCtx)
	go thumbWorker.Run(workerCtx)

	a.registerPreviewWorkers(ctx, d.ID, worker, thumbWorker, cancel)

	// spider91 driver 还需要一个 crawler，挂在专用 map 里供 crawlerLoop 调用
	if sd, ok := drv.(*spider91.Driver); ok {
		a.attachSpider91Crawler(d, sd)
	}

	return nil
}

func (a *App) attachLocalUpload(ctx context.Context) error {
	drv := localupload.New(a.localUploadDir())
	if err := drv.Init(ctx); err != nil {
		return err
	}
	a.registry.Set(drv.ID(), drv)

	gen := preview.New(preview.Config{
		FFmpegPath:      a.cfg.Preview.FFmpegPath,
		FFprobePath:     a.cfg.Preview.FFprobePath,
		DurationSeconds: a.cfg.Preview.DurationSeconds,
		Width:           a.cfg.Preview.Width,
		Segments:        a.cfg.Preview.Segments,
		LocalDir:        a.cfg.Storage.LocalPreviewDir,
	})
	worker := preview.NewWorker(gen, a.cat, drv)
	thumbWorker := preview.NewThumbWorker(gen, a.cat, drv)

	workerCtx, cancel := context.WithCancel(ctx)
	go worker.Run(workerCtx)
	go thumbWorker.Run(workerCtx)

	a.registerPreviewWorkers(ctx, drv.ID(), worker, thumbWorker, cancel)
	return nil
}

func (a *App) localUploadDir() string {
	return filepath.Join(filepath.Dir(a.cfg.Storage.LocalPreviewDir), "uploads")
}

// spider91RootDir 是所有 spider91 drive 共享的根目录。
func (a *App) spider91RootDir() string {
	return filepath.Join(filepath.Dir(a.cfg.Storage.LocalPreviewDir), "spider91")
}

// spider91DriveDir 是单个 spider91 drive 的存储目录：<root>/<driveID>。
func (a *App) spider91DriveDir(driveID string) string {
	return filepath.Join(a.spider91RootDir(), driveID)
}

// commonThumbsDir 是所有 drive 共享的封面目录，/p/thumb/{videoID} 路由命中这里。
func (a *App) commonThumbsDir() string {
	return filepath.Join(a.cfg.Storage.LocalPreviewDir, "thumbs")
}

// defaultSpider91ScriptPath 推断仓库里爬虫脚本的默认路径。
// 当前进程从 backend/ 启动时，脚本位于 ../91VideoSpider/spider_91porn.py。
// 找不到时返回空字符串，上层会在 RunOnce 时报错提示用户手动填 script_path。
func (a *App) defaultSpider91ScriptPath() string {
	candidates := []string{
		// 优先从配置目录的父目录定位
		filepath.Join(filepath.Dir(filepath.Dir(a.cfg.Storage.LocalPreviewDir)), "91VideoSpider", "spider_91porn.py"),
		// 仓库 root（cwd 在 backend/ 时）
		filepath.Join("..", "91VideoSpider", "spider_91porn.py"),
		// cwd 已经是仓库 root 时
		filepath.Join("91VideoSpider", "spider_91porn.py"),
	}
	for _, p := range candidates {
		abs, err := filepath.Abs(p)
		if err != nil {
			continue
		}
		if _, err := os.Stat(abs); err == nil {
			return abs
		}
	}
	return ""
}

// attachSpider91Crawler 创建该 drive 对应的 Crawler 并注册到 a.spider91Crawlers。
func (a *App) attachSpider91Crawler(d *catalog.Drive, drv *spider91.Driver) {
	pythonPath := strings.TrimSpace(d.Credentials["python_path"])
	if pythonPath == "" {
		pythonPath = "python3"
	}
	scriptPath := strings.TrimSpace(d.Credentials["script_path"])
	if scriptPath == "" {
		scriptPath = a.defaultSpider91ScriptPath()
	}
	// 91porn CDN 在海外；空缺时回退到 HTTPS_PROXY / HTTP_PROXY 环境变量。
	proxyURL := strings.TrimSpace(d.Credentials["proxy"])

	driveID := d.ID
	c := spider91.NewCrawler(spider91.CrawlerConfig{
		Driver:         drv,
		Catalog:        a.cat,
		PythonPath:     pythonPath,
		ScriptPath:     scriptPath,
		WorkDir:        filepath.Dir(scriptPath),
		CommonThumbDir: a.commonThumbsDir(),
		ProxyURL:       proxyURL,
		OnNewVideo: func(v *catalog.Video) {
			// 新视频入库后，触发 teaser worker（不需要 thumb worker，封面已就绪）
			a.mu.Lock()
			worker := a.workers[driveID]
			previewEnabled := a.previewEnabled
			a.mu.Unlock()
			if previewEnabled && worker != nil {
				worker.Enqueue(v)
			}
		},
	})

	a.mu.Lock()
	a.spider91Crawlers[driveID] = c
	a.mu.Unlock()

	// 确保 "91porn" 系统标签存在，并把已入库的 spider91 视频按 author 字段
	// 匹配补打这个标签（CreateTagAndClassify 内部对所有视频走一遍 classify）。
	// 重复调用是幂等的：tags 用 INSERT OR IGNORE，video_tags 也是 INSERT OR IGNORE。
	bgCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	go func() {
		defer cancel()
		if _, err := a.cat.CreateTagAndClassify(bgCtx, spider91.DefaultTag, nil, "system"); err != nil {
			log.Printf("[spider91] ensure %q tag: %v", spider91.DefaultTag, err)
		}
	}()
}

func (a *App) registerPreviewWorkers(ctx context.Context, driveID string, worker *preview.Worker, thumbWorker *preview.ThumbWorker, cancel context.CancelFunc) {
	a.mu.Lock()
	if a.cancels == nil {
		a.cancels = make(map[string]context.CancelFunc)
	}
	if a.workers == nil {
		a.workers = make(map[string]*preview.Worker)
	}
	if a.thumbWorkers == nil {
		a.thumbWorkers = make(map[string]*preview.ThumbWorker)
	}
	if old, ok := a.cancels[driveID]; ok && old != nil {
		old()
	}
	if worker != nil {
		a.workers[driveID] = worker
	} else {
		delete(a.workers, driveID)
	}
	if thumbWorker != nil {
		a.thumbWorkers[driveID] = thumbWorker
	} else {
		delete(a.thumbWorkers, driveID)
	}
	if cancel != nil {
		a.cancels[driveID] = cancel
	} else {
		delete(a.cancels, driveID)
	}
	a.mu.Unlock()

	if worker != nil {
		if thumbWorker != nil {
			worker.BeforeTask = func(taskCtx context.Context) bool {
				return a.waitForThumbnailsBeforePreview(taskCtx, driveID)
			}
		} else {
			worker.BeforeTask = nil
		}
	}

	go a.enqueueDriveGeneration(ctx, driveID, worker, thumbWorker)
}

func (a *App) enqueuePending(ctx context.Context, driveID string, w *preview.Worker) {
	pending, err := a.cat.ListVideosByPreviewStatus(ctx, driveID, "pending", 0)
	if err != nil {
		log.Printf("[preview] list pending %s: %v", driveID, err)
		return
	}
	if len(pending) == 0 {
		return
	}
	log.Printf("[preview] enqueue %d pending videos for drive=%s", len(pending), driveID)
	for _, v := range pending {
		if !w.EnqueueBlocking(ctx, v) {
			log.Printf("[preview] enqueue pending canceled for drive=%s", driveID)
			return
		}
	}
}

func (a *App) enqueueDriveGeneration(ctx context.Context, driveID string, worker *preview.Worker, thumbWorker *preview.ThumbWorker) {
	if thumbWorker != nil {
		a.enqueueThumbnails(ctx, driveID, thumbWorker)
	}
	if !a.PreviewEnabled() || worker == nil {
		return
	}
	if thumbWorker != nil && !a.waitForThumbnailsBeforePreview(ctx, driveID) {
		return
	}
	a.enqueuePending(ctx, driveID, worker)
}

func (a *App) waitForThumbnailsBeforePreview(ctx context.Context, driveID string) bool {
	const pollInterval = time.Second
	var lastLog time.Time
	for {
		missing, err := a.cat.CountVideosNeedingThumbnail(ctx, driveID)
		if err != nil {
			log.Printf("[preview] count missing thumbnails drive=%s: %v", driveID, err)
			return false
		}
		if missing == 0 {
			return true
		}
		now := time.Now()
		if lastLog.IsZero() || now.Sub(lastLog) >= time.Minute {
			log.Printf("[preview] drive=%s waiting for %d thumbnails before teaser generation", driveID, missing)
			lastLog = now
		}
		timer := time.NewTimer(pollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return false
		case <-timer.C:
		}
	}
}

func (a *App) enqueueThumbnails(ctx context.Context, driveID string, w *preview.ThumbWorker) {
	pending, err := a.cat.ListVideosNeedingThumbnail(ctx, driveID, 0)
	if err != nil {
		log.Printf("[thumb] list pending %s: %v", driveID, err)
		return
	}
	if len(pending) == 0 {
		return
	}
	log.Printf("[thumb] enqueue %d missing thumbnails for drive=%s", len(pending), driveID)
	for _, v := range pending {
		if !w.EnqueueBlocking(ctx, v) {
			log.Printf("[thumb] enqueue missing thumbnails canceled for drive=%s", driveID)
			return
		}
	}
}

func (a *App) detachDrive(id string) {
	a.registry.Remove(id)
	a.mu.Lock()
	if cancel, ok := a.cancels[id]; ok {
		cancel()
		delete(a.cancels, id)
	}
	delete(a.workers, id)
	delete(a.thumbWorkers, id)
	delete(a.spider91Crawlers, id)
	a.mu.Unlock()
}

func (a *App) runScan(ctx context.Context, driveID string) {
	drv, ok := a.registry.Get(driveID)
	if !ok {
		log.Printf("[scan] drive %s not attached", driveID)
		return
	}

	a.mu.Lock()
	worker := a.workers[driveID]
	thumbWorker := a.thumbWorkers[driveID]
	a.mu.Unlock()

	var onNew func(v *catalog.Video)
	if thumbWorker != nil {
		onNew = func(v *catalog.Video) {
			if thumbWorker != nil && v.ThumbnailURL == "" {
				thumbWorker.Enqueue(v)
			}
		}
	}

	sc := scanner.New(a.cat, drv, a.cfg.Scanner.VideoExtensions, a.cfg.Scanner.MaxDepth, onNew)

	// 使用 drive 的 scan_root_id，否则 root_id
	d, err := a.cat.GetDrive(ctx, driveID)
	if err != nil {
		log.Printf("[scan] get drive %s: %v", driveID, err)
		return
	}
	startID := d.ScanRootID
	if startID == "" {
		startID = d.RootID
	}

	log.Printf("[scan] drive=%s start=%s", driveID, startID)
	stats, err := sc.Run(ctx, startID)
	if err != nil {
		log.Printf("[scan] drive=%s error: %v", driveID, err)
		return
	}
	log.Printf("[scan] drive=%s done scanned=%d added=%d errors=%d", driveID, stats.Scanned, stats.Added, stats.Errors)
	if drv.Kind() == "p115" && len(stats.ExcludedFileIDs) > 0 {
		removed, err := a.cleanupExcludedDriveVideos(ctx, driveID, stats.ExcludedFileIDs)
		if err != nil {
			log.Printf("[cleanup] excluded 115 videos drive=%s error: %v", driveID, err)
		} else if removed > 0 {
			log.Printf("[cleanup] removed %d excluded 115 videos for drive=%s", removed, driveID)
		}
	}
	if drv.Kind() == "pikpak" {
		if stats.Errors > 0 {
			log.Printf("[cleanup] skip stale PikPak cleanup for drive=%s: scan had %d directory errors", driveID, stats.Errors)
		} else {
			removed, err := a.cleanupMissingDriveVideos(ctx, driveID, stats.SeenFileIDs, stats.VisitedDirIDs, startID == drv.RootID())
			if err != nil {
				log.Printf("[cleanup] stale PikPak cleanup drive=%s error: %v", driveID, err)
			} else if removed > 0 {
				log.Printf("[cleanup] removed %d stale PikPak videos for drive=%s", removed, driveID)
			}
		}
	}
	a.enqueueDriveGeneration(ctx, driveID, worker, thumbWorker)
}

func (a *App) cleanupExcludedDriveVideos(ctx context.Context, driveID string, excludedFileIDs map[string]struct{}) (int, error) {
	if len(excludedFileIDs) == 0 {
		return 0, nil
	}
	items, err := a.cat.ListVideosByDrive(ctx, driveID)
	if err != nil {
		return 0, err
	}

	localDir := ""
	if a.cfg != nil {
		localDir = a.cfg.Storage.LocalPreviewDir
	}
	removed := 0
	for _, v := range items {
		if _, ok := excludedFileIDs[v.FileID]; !ok {
			continue
		}
		if err := removeLocalVideoAssets(localDir, v); err != nil {
			return removed, fmt.Errorf("remove local assets for %s: %w", v.ID, err)
		}
		if err := a.cat.DeleteVideo(ctx, v.ID); err != nil {
			return removed, fmt.Errorf("delete catalog video %s: %w", v.ID, err)
		}
		removed++
	}
	return removed, nil
}

func (a *App) cleanupMissingDriveVideos(ctx context.Context, driveID string, liveFileIDs map[string]struct{}, visitedDirIDs map[string]struct{}, fullDriveScan bool) (int, error) {
	items, err := a.cat.ListVideosByDrive(ctx, driveID)
	if err != nil {
		return 0, err
	}

	localDir := ""
	if a.cfg != nil {
		localDir = a.cfg.Storage.LocalPreviewDir
	}
	removed := 0
	for _, v := range items {
		if _, ok := liveFileIDs[v.FileID]; ok {
			continue
		}
		if !fullDriveScan {
			if _, ok := visitedDirIDs[v.ParentID]; !ok {
				continue
			}
		}
		if err := removeLocalVideoAssets(localDir, v); err != nil {
			return removed, fmt.Errorf("remove local assets for %s: %w", v.ID, err)
		}
		if err := a.cat.DeleteVideo(ctx, v.ID); err != nil {
			return removed, fmt.Errorf("delete catalog video %s: %w", v.ID, err)
		}
		removed++
	}
	return removed, nil
}

func removeLocalVideoAssets(localDir string, v *catalog.Video) error {
	if localDir == "" || v == nil || v.ID == "" {
		return nil
	}
	candidates := []string{
		v.PreviewLocal,
		filepath.Join(localDir, v.ID+".mp4"),
		filepath.Join(localDir, "thumbs", v.ID+".jpg"),
	}
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		clean, ok := localPathWithin(localDir, candidate)
		if !ok {
			continue
		}
		if _, ok := seen[clean]; ok {
			continue
		}
		seen[clean] = struct{}{}
		info, err := os.Stat(clean)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		if !info.Mode().IsRegular() {
			continue
		}
		if err := os.Remove(clean); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func localPathWithin(root, path string) (string, bool) {
	if strings.TrimSpace(root) == "" || strings.TrimSpace(path) == "" {
		return "", false
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", false
	}
	pathAbs, err := filepath.Abs(path)
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(rootAbs, pathAbs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", false
	}
	return pathAbs, true
}

func (a *App) enqueueUploadedVideo(ctx context.Context, v *catalog.Video) {
	if v == nil {
		return
	}
	a.mu.Lock()
	worker := a.workers[v.DriveID]
	thumbWorker := a.thumbWorkers[v.DriveID]
	previewEnabled := a.previewEnabled
	a.mu.Unlock()

	if thumbWorker != nil && v.ThumbnailURL == "" {
		thumbWorker.Enqueue(v)
	}
	if previewEnabled && worker != nil {
		worker.Enqueue(v)
	}
}

func (a *App) regenPreview(ctx context.Context, videoID string) {
	v, err := a.cat.GetVideo(ctx, videoID)
	if err != nil {
		return
	}
	a.mu.Lock()
	worker := a.workers[v.DriveID]
	a.mu.Unlock()
	if worker != nil {
		worker.EnqueueBlocking(ctx, v)
	}
}

func (a *App) regenAllPreviews(ctx context.Context) {
	items, total, err := a.cat.ListVideos(ctx, catalog.ListParams{Page: 1, PageSize: 1000000})
	if err != nil {
		log.Printf("[preview] list all videos for regen: %v", err)
		return
	}
	log.Printf("[preview] enqueue all visible videos for regen count=%d total=%d", len(items), total)
	queued := 0
	for _, v := range items {
		if err := ctx.Err(); err != nil {
			log.Printf("[preview] enqueue all canceled after %d videos: %v", queued, err)
			return
		}
		a.mu.Lock()
		worker := a.workers[v.DriveID]
		a.mu.Unlock()
		if worker == nil {
			continue
		}
		if !worker.EnqueueBlocking(ctx, v) {
			log.Printf("[preview] enqueue all canceled after %d videos", queued)
			return
		}
		queued++
	}
	log.Printf("[preview] enqueued all visible videos for regen queued=%d", queued)
}

func (a *App) regenFailedPreviews(ctx context.Context, driveID string) {
	items, err := a.cat.ListVideosByPreviewStatus(ctx, driveID, "failed", 0)
	if err != nil {
		log.Printf("[preview] list failed videos for regen drive=%s: %v", driveID, err)
		return
	}
	a.mu.Lock()
	worker := a.workers[driveID]
	a.mu.Unlock()
	if worker == nil {
		log.Printf("[preview] regen failed drive=%s skipped: worker not found", driveID)
		return
	}
	log.Printf("[preview] enqueue failed videos for regen drive=%s count=%d", driveID, len(items))
	queued := 0
	for _, v := range items {
		if err := ctx.Err(); err != nil {
			log.Printf("[preview] enqueue failed canceled drive=%s queued=%d: %v", driveID, queued, err)
			return
		}
		if err := a.cat.UpdatePreview(ctx, v.ID, "", "pending"); err != nil {
			log.Printf("[preview] reset failed video %s drive=%s: %v", v.ID, driveID, err)
			continue
		}
		v.PreviewFileID = ""
		v.PreviewLocal = ""
		v.PreviewStatus = "pending"
		if !worker.EnqueueBlocking(ctx, v) {
			log.Printf("[preview] enqueue failed canceled drive=%s queued=%d", driveID, queued)
			return
		}
		queued++
	}
	log.Printf("[preview] enqueued failed videos for regen drive=%s queued=%d", driveID, queued)
}

func (a *App) scanLoop(ctx context.Context) {
	if a.cfg.Scanner.IntervalSeconds <= 0 {
		return
	}
	interval := time.Duration(a.cfg.Scanner.IntervalSeconds) * time.Second
	var lastScheduledScan time.Time
	if a.scanAllOnceIfDue(ctx, time.Now(), lastScheduledScan, interval) {
		lastScheduledScan = time.Now()
	}

	checkEvery := interval
	if checkEvery > time.Minute {
		checkEvery = time.Minute
	}
	ticker := time.NewTicker(checkEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			if a.scanAllOnceIfDue(ctx, now, lastScheduledScan, interval) {
				lastScheduledScan = now
			}
		}
	}
}

func (a *App) scanAllOnceIfDue(ctx context.Context, now, lastScheduledScan time.Time, interval time.Duration) bool {
	if !scheduledScanDue(now, lastScheduledScan, interval) {
		return false
	}
	a.scanAllOnce(ctx)
	return true
}

func scheduledScanDue(now, lastScheduledScan time.Time, interval time.Duration) bool {
	if interval <= 0 || !scheduledScanAllowed(now) {
		return false
	}
	return lastScheduledScan.IsZero() || now.Sub(lastScheduledScan) >= interval
}

func scheduledScanAllowed(now time.Time) bool {
	hour := now.Hour()
	return hour >= 2 && hour < 7
}

func (a *App) scanAllOnce(ctx context.Context) {
	for _, d := range a.registry.All() {
		if !shouldScanDrive(d) {
			continue
		}
		a.runScan(ctx, d.ID())
	}
}

func shouldScanDrive(d drives.Drive) bool {
	if d == nil || d.ID() == localupload.DriveID {
		return false
	}
	// spider91 由专用的 crawlerLoop 触发，不参与 scanLoop
	if d.Kind() == spider91.Kind {
		return false
	}
	return true
}

// ---------- spider91 crawler loop ----------

const (
	// spider91DefaultCrawlHour 默认在凌晨 00 点的窗口内触发；可被每个 drive 的
	// credentials.crawl_hour 覆盖。
	spider91DefaultCrawlHour = 0
	// spider91MinIntervalHours 两次自动爬取之间的最小间隔，避免一天触发多次。
	spider91MinIntervalHours = 12
)

// crawlerLoop 每分钟轮询一次，命中触发窗口的 spider91 drive 就启动一次爬取。
// 每个 drive 单独跟踪 last_crawl_at（写在 credentials 里）。
func (a *App) crawlerLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	// 启动后立刻检查一次（可能错过了今天的窗口）
	a.tickSpider91(ctx, time.Now())
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			a.tickSpider91(ctx, now)
		}
	}
}

// tickSpider91 检查所有 spider91 drive，决定哪些该触发。
func (a *App) tickSpider91(ctx context.Context, now time.Time) {
	a.mu.Lock()
	crawlerIDs := make([]string, 0, len(a.spider91Crawlers))
	for id := range a.spider91Crawlers {
		crawlerIDs = append(crawlerIDs, id)
	}
	a.mu.Unlock()

	for _, id := range crawlerIDs {
		d, err := a.cat.GetDrive(ctx, id)
		if err != nil || d == nil {
			continue
		}
		if !spider91DueAt(now, d) {
			continue
		}
		go a.runSpider91Crawl(ctx, id)
	}
}

// spider91DueAt 判断 drive 是否应该在 now 触发自动爬取。
//   - 当前小时必须等于 drive.crawl_hour（默认 0）
//   - 距离上次 last_crawl_at 至少 spider91MinIntervalHours 小时
func spider91DueAt(now time.Time, d *catalog.Drive) bool {
	if d == nil {
		return false
	}
	hour := spider91IntCred(d, "crawl_hour", spider91DefaultCrawlHour)
	if hour < 0 || hour > 23 {
		hour = spider91DefaultCrawlHour
	}
	if now.Hour() != hour {
		return false
	}
	last := spider91IntCred(d, "last_crawl_at", 0)
	if last <= 0 {
		return true
	}
	delta := now.Sub(time.Unix(int64(last), 0))
	return delta >= time.Duration(spider91MinIntervalHours)*time.Hour
}

// runSpider91Crawl 运行一次完整爬取流程并把 last_crawl_at 写回 drive.credentials。
// 即使爬取失败也会更新 last_crawl_at，避免一直在错误循环里反复触发；下次窗口仍会重试。
func (a *App) runSpider91Crawl(ctx context.Context, driveID string) {
	a.mu.Lock()
	c := a.spider91Crawlers[driveID]
	a.mu.Unlock()
	if c == nil {
		return
	}

	d, err := a.cat.GetDrive(ctx, driveID)
	if err != nil || d == nil {
		log.Printf("[spider91] drive=%s lookup failed: %v", driveID, err)
		return
	}
	targetNew := spider91IntCred(d, "target_new", spider91.DefaultTargetNew)
	if targetNew <= 0 {
		targetNew = spider91.DefaultTargetNew
	}

	log.Printf("[spider91] drive=%s start crawl target_new=%d", driveID, targetNew)
	res, runErr := c.RunOnce(ctx, targetNew)
	if runErr != nil {
		log.Printf("[spider91] drive=%s crawl failed: %v", driveID, runErr)
	} else if res != nil {
		log.Printf("[spider91] drive=%s crawl done target=%d total=%d new=%d skipped=%d failed=%d seen_snapshot=%d",
			driveID, res.TargetNew, res.TotalEntries, res.NewVideos, res.Skipped, res.Failed, res.SeenSnapshot)
	}

	// 标记最后一次爬取时间
	if d.Credentials == nil {
		d.Credentials = make(map[string]string)
	}
	d.Credentials["last_crawl_at"] = strconv.FormatInt(time.Now().Unix(), 10)
	if runErr != nil {
		d.Status = "error"
		d.LastError = runErr.Error()
	} else {
		d.Status = "ok"
		d.LastError = ""
	}
	if err := a.cat.UpsertDrive(ctx, d); err != nil {
		log.Printf("[spider91] drive=%s update last_crawl_at: %v", driveID, err)
	}

	// 爬完立刻 ping 一次迁移 worker，不等下一个周期。
	if a.spider91Migrator != nil {
		a.spider91Migrator.Trigger()
	}
}

// spider91IntCred 解析 credentials 中的整数字段，缺省时返回 def。
func spider91IntCred(d *catalog.Drive, key string, def int) int {
	if d == nil || d.Credentials == nil {
		return def
	}
	raw := strings.TrimSpace(d.Credentials[key])
	if raw == "" {
		return def
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return def
	}
	return v
}

// ---------- middleware ----------

// corsMiddleware 返回一个 chi 中间件，按白名单匹配 Origin 决定是否回写
// CORS 响应头。
//
// 设计要点：
//   - 不再反射任意 Origin。Origin 必须出现在 allowedOrigins 中才会得到
//     Access-Control-Allow-Origin / Allow-Credentials 的"放行"响应头；
//     不在白名单的跨源请求拿不到这些头，浏览器会拒绝读响应内容。
//   - 同源请求（浏览器不发 Origin 头，或 Origin 等于自己）不需要 CORS 头，
//     直接放行。
//   - 始终带 Vary: Origin，避免反代缓存把 A Origin 的允许头喂给 B Origin。
//   - 对不在白名单的 OPTIONS 预检直接 403，避免被当成"放行"信号。
//
// allowedOrigins 由 config.Server.AllowedOrigins 注入；默认为空 = 完全
// 不允许跨源（最安全的默认值，同源部署不受影响）。
func corsMiddleware(allowedOrigins []string) func(http.Handler) http.Handler {
	allow := make(map[string]struct{}, len(allowedOrigins))
	for _, o := range allowedOrigins {
		o = strings.TrimSpace(o)
		if o == "" || o == "*" {
			// 通配符在带 cookie 的 CORS 下没意义且危险，直接忽略
			continue
		}
		allow[o] = struct{}{}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")

			// 任何走过 CORS 检查的响应都要带 Vary: Origin，避免缓存污染。
			w.Header().Add("Vary", "Origin")

			isAllowedOrigin := false
			if origin != "" {
				_, isAllowedOrigin = allow[origin]
			}

			if isAllowedOrigin {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Credentials", "true")
				w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,DELETE,OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
				w.Header().Set("Access-Control-Max-Age", "600")
			}

			if r.Method == http.MethodOptions {
				// 预检请求：只对白名单 Origin 返回 204；否则 403 让浏览器把请求拦下来。
				// 同源场景一般不会触发预检（浏览器只在跨源 + 复杂请求时才发 OPTIONS）。
				if isAllowedOrigin {
					w.WriteHeader(http.StatusNoContent)
					return
				}
				if origin != "" {
					http.Error(w, "cors: origin not allowed", http.StatusForbidden)
					return
				}
				// 没带 Origin 的 OPTIONS 不是 CORS 预检（可能是健康检查工具），
				// 直接交给下游处理。
			}

			next.ServeHTTP(w, r)
		})
	}
}

func parseBoolDefault(raw string, def bool) bool {
	if raw == "" {
		return def
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return def
	}
	return v
}
