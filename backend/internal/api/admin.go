package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/video-site/backend/internal/auth"
	"github.com/video-site/backend/internal/catalog"
)

type AdminServer struct {
	Catalog *catalog.Catalog
	Auth    *auth.Authenticator
	// LocalPreviewDir is the local directory that stores generated teasers and thumbs.
	LocalPreviewDir string
	// Hooks：外层注入实际执行者
	OnDriveSaved               func(driveID string) error
	OnDriveRemoved             func(driveID string)
	OnScanRequested            func(driveID string)
	OnRegenPreview             func(videoID string)
	OnRegenAllPreviews         func()
	OnRegenFailedPreviews      func(driveID string)
	GetDriveGenerationStatuses func() map[string]DriveGenerationStatuses
	// OnTeaserEnabledChanged 在 per-drive teaser 开关被切换后调用。
	// enabled=true 时上层应该重新把 pending teaser 入队（类似旧的全局开关从关到开）；
	// enabled=false 时通常不用做事 —— worker 入队前会再次查 catalog，自然停止。
	OnTeaserEnabledChanged func(driveID string, enabled bool)
	// Theme 读写（"dark" | "pink"）
	GetTheme func() string
	SetTheme func(theme string) error
	// Spider91 → PikPak 上传目标 drive ID 读写
	GetSpider91UploadDriveID func() string
	SetSpider91UploadDriveID func(driveID string) error
}

type GenerationStatus struct {
	State         string `json:"state"`
	CurrentTitle  string `json:"currentTitle,omitempty"`
	QueueLength   int    `json:"queueLength"`
	CooldownUntil string `json:"cooldownUntil,omitempty"`
}

type DriveGenerationStatuses struct {
	Thumbnail GenerationStatus `json:"thumbnail"`
	Preview   GenerationStatus `json:"preview"`
}

func (a *AdminServer) Register(r chi.Router) {
	r.Route("/admin/api", func(r chi.Router) {
		// 登录、登出不需要鉴权
		r.Post("/login", a.handleLogin)
		r.Post("/logout", a.handleLogout)
		r.Get("/me", a.handleMe)

		// 其余路由需鉴权
		r.Group(func(r chi.Router) {
			r.Use(a.Auth.Required)

			// 网盘
			r.Get("/drives", a.handleListDrives)
			r.Get("/drives/storage", a.handleDriveStorage)
			r.Post("/drives", a.handleUpsertDrive)
			r.Delete("/drives/{id}", a.handleDeleteDrive)
			r.Post("/drives/{id}/rescan", a.handleRescan)
			r.Post("/drives/{id}/teaser-enabled", a.handleSetDriveTeaserEnabled)
			r.Post("/drives/{id}/previews/failed/regenerate", a.handleRegenFailedPreviews)

			// 视频
			r.Get("/videos", a.handleAdminListVideos)
			r.Put("/videos/{id}", a.handleUpdateVideo)
			r.Post("/videos/regen-preview", a.handleRegenAllPreviews)
			r.Post("/videos/{id}/regen-preview", a.handleRegenPreview)

			// 标签
			r.Get("/tags", a.handleListTags)
			r.Post("/tags", a.handleCreateTag)

			// 运行时设置
			r.Get("/settings", a.handleGetSettings)
			r.Put("/settings", a.handlePutSettings)
		})
	})
}

type loginReq struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (a *AdminServer) handleLogin(w http.ResponseWriter, r *http.Request) {
	var body loginReq
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	ok, err := a.Auth.Login(w, r, body.Username, body.Password)
	if err != nil {
		if errors.Is(err, auth.ErrLoginIPBanned) {
			http.Error(w, "ip banned", http.StatusForbidden)
			return
		}
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if !ok {
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *AdminServer) handleLogout(w http.ResponseWriter, r *http.Request) {
	a.Auth.Logout(w, r)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *AdminServer) handleMe(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie("vs_admin")
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"authenticated": false})
		return
	}
	ok, _ := a.Catalog.ValidateSession(r.Context(), c.Value)
	writeJSON(w, http.StatusOK, map[string]any{"authenticated": ok})
}

func (a *AdminServer) handleListDrives(w http.ResponseWriter, r *http.Request) {
	drives, err := a.Catalog.ListDrives(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	teaserCounts, err := a.Catalog.CountTeasersByDrive(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	thumbnailCounts, err := a.Catalog.CountThumbnailsByDrive(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	generationStatuses := map[string]DriveGenerationStatuses{}
	if a.GetDriveGenerationStatuses != nil {
		generationStatuses = a.GetDriveGenerationStatuses()
	}
	// 出参不返回凭证明文，只告诉前端是否已配置
	type out struct {
		ID                        string           `json:"id"`
		Kind                      string           `json:"kind"`
		Name                      string           `json:"name"`
		RootID                    string           `json:"rootId"`
		ScanRootID                string           `json:"scanRootId"`
		Status                    string           `json:"status"`
		LastError                 string           `json:"lastError,omitempty"`
		HasCredential             bool             `json:"hasCredential"`
		// TeaserEnabled 控制是否给本盘生成 teaser/封面。前端用它在网盘列表/编辑表单展示开关状态。
		TeaserEnabled bool `json:"teaserEnabled"`
		// LastCrawlAt 是 spider91 上次成功爬取的 unix 秒（来自 credentials.last_crawl_at）。
		// 其它 kind 留 0；前端用它显示"上次抓取: N 小时前"。
		LastCrawlAt               int64            `json:"lastCrawlAt,omitempty"`
		ThumbnailGenerationStatus GenerationStatus `json:"thumbnailGenerationStatus"`
		PreviewGenerationStatus   GenerationStatus `json:"previewGenerationStatus"`
		ThumbnailReadyCount       int              `json:"thumbnailReadyCount"`
		ThumbnailPendingCount     int              `json:"thumbnailPendingCount"`
		ThumbnailFailedCount      int              `json:"thumbnailFailedCount"`
		TeaserReadyCount          int              `json:"teaserReadyCount"`
		TeaserPendingCount        int              `json:"teaserPendingCount"`
		TeaserFailedCount         int              `json:"teaserFailedCount"`
	}
	list := make([]out, 0, len(drives))
	for _, d := range drives {
		counts := teaserCounts[d.ID]
		thumbCounts := thumbnailCounts[d.ID]
		generation := generationStatuses[d.ID]
		if generation.Thumbnail.State == "" {
			generation.Thumbnail.State = "idle"
		}
		if generation.Preview.State == "" {
			generation.Preview.State = "idle"
		}
		// spider91 没有用户凭证概念；只要存在 drive 行就视为"已配置"。
		// last_crawl_at 是后端自动写入的运行状态字段，不计入 hasCredential 判定。
		hasCred := false
		userCredKeys := 0
		for k := range d.Credentials {
			if k == "last_crawl_at" {
				continue
			}
			userCredKeys++
		}
		hasCred = userCredKeys > 0 || d.Kind == "spider91"

		var lastCrawlAt int64
		if d.Credentials != nil {
			if raw, ok := d.Credentials["last_crawl_at"]; ok && raw != "" {
				if v, err := strconv.ParseInt(raw, 10, 64); err == nil {
					lastCrawlAt = v
				}
			}
		}

		list = append(list, out{
			ID: d.ID, Kind: d.Kind, Name: d.Name,
			RootID: d.RootID, ScanRootID: d.ScanRootID,
			Status: d.Status, LastError: d.LastError,
			HasCredential:             hasCred,
			TeaserEnabled:             d.TeaserEnabled,
			LastCrawlAt:               lastCrawlAt,
			ThumbnailGenerationStatus: generation.Thumbnail,
			PreviewGenerationStatus:   generation.Preview,
			ThumbnailReadyCount:       thumbCounts.Ready,
			ThumbnailPendingCount:     thumbCounts.Pending,
			ThumbnailFailedCount:      thumbCounts.Failed,
			TeaserReadyCount:          counts.Ready,
			TeaserPendingCount:        counts.Pending,
			TeaserFailedCount:         counts.Failed,
		})
	}
	writeJSON(w, http.StatusOK, list)
}

type upsertDriveReq struct {
	ID          string            `json:"id"`
	Kind        string            `json:"kind"`
	Name        string            `json:"name"`
	RootID      string            `json:"rootId"`
	ScanRootID  string            `json:"scanRootId"`
	Credentials map[string]string `json:"credentials"`
	// TeaserEnabled 是 per-drive teaser/封面生成开关。
	// 用 *bool 区分 "未传" / "传了 false"：未传时表示客户端不打算改这个字段，
	// 沿用 catalog 现有值；新建时未传一律默认开启（true）。
	TeaserEnabled *bool `json:"teaserEnabled,omitempty"`
}

func (a *AdminServer) handleUpsertDrive(w http.ResponseWriter, r *http.Request) {
	var body upsertDriveReq
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if body.ID == "" || body.Kind == "" {
		http.Error(w, "id and kind are required", http.StatusBadRequest)
		return
	}
	// 凭证 / TeaserEnabled 都支持 "未传 = 沿用旧值"：先把现存 drive 拉出来一次。
	var existing *catalog.Drive
	if existingDrive, err := a.Catalog.GetDrive(r.Context(), body.ID); err == nil {
		existing = existingDrive
	}
	if len(body.Credentials) == 0 && existing != nil && len(existing.Credentials) > 0 {
		body.Credentials = existing.Credentials
	}

	// teaserEnabled 解析顺序：
	//   1. 请求显式带了 → 用请求值
	//   2. 请求没带 + 编辑现有 drive → 沿用旧值
	//   3. 请求没带 + 新建 drive → 默认 true（用户没特别说就生成）
	teaserEnabled := true
	switch {
	case body.TeaserEnabled != nil:
		teaserEnabled = *body.TeaserEnabled
	case existing != nil:
		teaserEnabled = existing.TeaserEnabled
	}

	d := &catalog.Drive{
		ID: body.ID, Kind: body.Kind, Name: body.Name,
		RootID: body.RootID, ScanRootID: body.ScanRootID,
		Credentials:   body.Credentials,
		Status:        "disconnected",
		TeaserEnabled: teaserEnabled,
	}
	if err := a.Catalog.UpsertDrive(r.Context(), d); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if a.OnDriveSaved != nil {
		if err := a.OnDriveSaved(body.ID); err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "warning": err.Error()})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *AdminServer) handleDeleteDrive(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := a.Catalog.DeleteDrive(r.Context(), id); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if a.OnDriveRemoved != nil {
		a.OnDriveRemoved(id)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *AdminServer) handleRescan(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if a.OnScanRequested != nil {
		a.OnScanRequested(id)
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true})
}

// teaserEnabledReq 是 POST /admin/api/drives/{id}/teaser-enabled 的入参。
type teaserEnabledReq struct {
	Enabled bool `json:"enabled"`
}

// handleSetDriveTeaserEnabled 切换某盘的 teaser 生成开关。
//
// 行为：
//   - 写 catalog.drives.teaser_enabled
//   - 调 OnTeaserEnabledChanged（main 注入；从关到开时会重新入队 pending teaser）
//   - 返回切换后的新值，方便前端乐观更新但又能以服务端为准
//
// 与 upsertDrive 的区别：那条接口要重传 kind / name / rootId 等，开关切换不该
// 牵连这些字段（顺手覆盖凭证或 rootID 容易出 bug）。所以单独走一条。
func (a *AdminServer) handleSetDriveTeaserEnabled(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		http.Error(w, "id is required", http.StatusBadRequest)
		return
	}
	var body teaserEnabledReq
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := a.Catalog.SetDriveTeaserEnabled(r.Context(), id, body.Enabled); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "drive not found", http.StatusNotFound)
			return
		}
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if a.OnTeaserEnabledChanged != nil {
		a.OnTeaserEnabledChanged(id, body.Enabled)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "teaserEnabled": body.Enabled})
}

func (a *AdminServer) handleAdminListVideos(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	size, _ := strconv.Atoi(q.Get("size"))
	if page <= 0 {
		page = 1
	}
	if size <= 0 || size > 100 {
		size = 100
	}
	items, total, err := a.Catalog.ListVideos(r.Context(), catalog.ListParams{
		DriveID:  q.Get("driveId"),
		Page:     page,
		PageSize: size,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": items,
		"total": total,
		"page":  page,
		"size":  size,
	})
}

func (a *AdminServer) handleListTags(w http.ResponseWriter, r *http.Request) {
	tags, err := a.Catalog.ListTags(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, tags)
}

type createTagReq struct {
	Label   string   `json:"label"`
	Aliases []string `json:"aliases"`
}

func (a *AdminServer) handleCreateTag(w http.ResponseWriter, r *http.Request) {
	var body createTagReq
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	classified, err := a.Catalog.CreateTagAndClassify(r.Context(), body.Label, body.Aliases, "user")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"label":      body.Label,
		"classified": classified,
	})
}

type updateVideoReq struct {
	Title       string   `json:"title"`
	Author      string   `json:"author"`
	Tags        []string `json:"tags"`
	Category    string   `json:"category"`
	Badges      []string `json:"badges"`
	Description string   `json:"description"`
	Thumbnail   string   `json:"thumbnail"`
	Quality     string   `json:"quality"`
	DurationSec int      `json:"durationSeconds"`
}

func (a *AdminServer) handleUpdateVideo(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var body updateVideoReq
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	v, err := a.Catalog.GetVideo(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	if body.Title != "" {
		v.Title = body.Title
	}
	if body.Author != "" {
		v.Author = body.Author
	}
	if body.Category != "" {
		v.Category = body.Category
	}
	if body.Badges != nil {
		v.Badges = body.Badges
	}
	if body.Description != "" {
		v.Description = body.Description
	}
	if body.Thumbnail != "" {
		v.ThumbnailURL = body.Thumbnail
	}
	if body.Quality != "" {
		v.Quality = body.Quality
	}
	if body.DurationSec > 0 {
		v.DurationSeconds = body.DurationSec
	}
	if err := a.Catalog.UpsertVideo(r.Context(), v); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if body.Tags != nil {
		if err := a.Catalog.SetManualVideoTags(r.Context(), id, body.Tags); err != nil {
			if errors.Is(err, catalog.ErrUnknownTag) {
				writeErr(w, http.StatusBadRequest, err)
				return
			}
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		v, err = a.Catalog.GetVideo(r.Context(), id)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, v)
}

func (a *AdminServer) handleRegenPreview(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if a.OnRegenPreview != nil {
		a.OnRegenPreview(id)
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true})
}

func (a *AdminServer) handleRegenAllPreviews(w http.ResponseWriter, r *http.Request) {
	if a.OnRegenAllPreviews != nil {
		a.OnRegenAllPreviews()
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true})
}

func (a *AdminServer) handleRegenFailedPreviews(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if a.OnRegenFailedPreviews != nil {
		a.OnRegenFailedPreviews(id)
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true})
}

// ---------- Settings ----------

// settingsDTO 是 GET/PUT /admin/api/settings 的入参/出参。
//
// 注意：早期的全局 previewEnabled 字段已经下沉为每盘 teaser_enabled，
// 不再出现在这里；前端要切换某个盘的 teaser 生成请用 POST /admin/api/drives 上传
// teaserEnabled 字段。保留 settings 用作主题、spider91 上传目标这类全局配置。
type settingsDTO struct {
	Theme                 string `json:"theme"`
	Spider91UploadDriveID string `json:"spider91UploadDriveId"`
}

func (a *AdminServer) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	theme := "dark"
	if a.GetTheme != nil {
		if v := a.GetTheme(); v != "" {
			theme = v
		}
	}
	spider91UploadID := ""
	if a.GetSpider91UploadDriveID != nil {
		spider91UploadID = a.GetSpider91UploadDriveID()
	}
	writeJSON(w, http.StatusOK, settingsDTO{
		Theme:                 theme,
		Spider91UploadDriveID: spider91UploadID,
	})
}

func (a *AdminServer) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	// 用 map 区分"没传"和"传了空字符串"两种语义；空 spider91 上传 ID 表示
	// 清除显式设置（回退到自动模式）。
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}

	if v, ok := raw["theme"]; ok && a.SetTheme != nil {
		var theme string
		if err := json.Unmarshal(v, &theme); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		if theme != "" {
			if err := a.SetTheme(theme); err != nil {
				writeErr(w, http.StatusBadRequest, err)
				return
			}
		}
	}

	if v, ok := raw["spider91UploadDriveId"]; ok && a.SetSpider91UploadDriveID != nil {
		var driveID string
		if err := json.Unmarshal(v, &driveID); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		if err := a.SetSpider91UploadDriveID(driveID); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
	}

	// 回显当前值
	resp := settingsDTO{}
	if a.GetTheme != nil {
		resp.Theme = a.GetTheme()
	}
	if a.GetSpider91UploadDriveID != nil {
		resp.Spider91UploadDriveID = a.GetSpider91UploadDriveID()
	}
	writeJSON(w, http.StatusOK, resp)
}
