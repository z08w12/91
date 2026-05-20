package api

import (
	"context"
	crand "crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand/v2"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/video-site/backend/internal/auth"
	"github.com/video-site/backend/internal/catalog"
	"github.com/video-site/backend/internal/drives/localupload"
	"github.com/video-site/backend/internal/proxy"
)

const localUploadDriveID = localupload.DriveID

var allowedUploadExtensions = map[string]struct{}{
	".avi":  {},
	".mkv":  {},
	".mov":  {},
	".mp4":  {},
	".webm": {},
}

var allowedUploadTags = map[string]struct{}{
	"奶子": {},
	"臀":  {},
	"口角": {},
	"女大": {},
	"人妻": {},
	"AV": {},
}

type Server struct {
	Catalog         *catalog.Catalog
	Proxy           *proxy.Proxy
	LocalDir        string
	UploadDir       string
	FFmpegPath      string
	OnVideoUploaded func(*catalog.Video)

	transcodeMu   sync.Mutex
	transcodeJobs map[string]bool
}

const (
	homePageSize = 12
)

// VideoDTO 是返回给前端的视频对象，字段名跟前端 VideoItem 对齐
type VideoDTO struct {
	ID              string   `json:"id"`
	Href            string   `json:"href"`
	Title           string   `json:"title"`
	Thumbnail       string   `json:"thumbnail"`
	PreviewSrc      string   `json:"previewSrc"`
	PreviewDuration int      `json:"previewDuration"`
	PreviewStrategy string   `json:"previewStrategy"`
	Duration        string   `json:"duration"`
	Badges          []string `json:"badges"`
	Quality         string   `json:"quality,omitempty"`
	SourceLabel     string   `json:"sourceLabel,omitempty"`
	Author          string   `json:"author"`
	Views           int      `json:"views"`
	Favorites       int      `json:"favorites"`
	Comments        int      `json:"comments"`
	Likes           int      `json:"likes"`
	Dislikes        int      `json:"dislikes"`
	PublishedAt     string   `json:"publishedAt"`
	Tags            []string `json:"tags,omitempty"`
	Category        string   `json:"category,omitempty"`
}

type VideoDetailDTO struct {
	VideoDTO
	VideoSrc      string        `json:"videoSrc"`
	Poster        string        `json:"poster"`
	Description   string        `json:"description"`
	EmbedURL      string        `json:"embedUrl"`
	Points        int           `json:"points,omitempty"`
	AuthorProfile AuthorProfile `json:"authorProfile"`
	RelatedVideos []VideoDTO    `json:"relatedVideos"`
	CommentsList  []Comment     `json:"commentsList"`
}

type AuthorProfile struct {
	ID     string   `json:"id"`
	Name   string   `json:"name"`
	Href   string   `json:"href"`
	Badges []string `json:"badges"`
}

type Comment struct {
	ID        string `json:"id"`
	Author    string `json:"author"`
	Body      string `json:"body"`
	CreatedAt string `json:"createdAt"`
	Likes     int    `json:"likes,omitempty"`
}

// RegisterRoutes 挂载前台 REST 路由。前台接口需要登录态。
func (s *Server) RegisterRoutes(r chi.Router, a *auth.Authenticator) {
	r.Group(func(r chi.Router) {
		r.Use(a.Required)
		r.Get("/api/home", s.handleHome)
		r.Get("/api/list", s.handleList)
		r.Get("/api/video/{id}", s.handleVideoDetail)
		r.Put("/api/video/{id}/tags", s.handleUpdateVideoTags)
		r.Post("/api/video/{id}/like", s.handleLike)
		r.Post("/api/video/{id}/view", s.handleView)
		r.Post("/api/video/{id}/hide", s.handleHideVideo)
		r.Post("/api/upload", s.handleUploadVideo)
		r.Get("/api/tags", s.handleTags)

		// 代理路由同样需要鉴权，防止绕过
		r.Get("/p/stream/{driveID}/{fileID}", s.handleStream)
		r.Get("/p/upload/{videoID}", s.handleUploadedVideo)
		r.Get("/p/transcode/{videoID}/status", s.handleTranscodeStatus)
		r.Post("/p/transcode/{videoID}/start", s.handleTranscodeStart)
		r.Get("/p/transcode/{videoID}", s.handleTranscode)
		r.Get("/p/preview/{videoID}", s.handlePreview)
		r.Get("/p/thumb/{videoID}", s.handleThumb)
	})
}

func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	// 拉一批候选（按发布时间倒序，覆盖最近 200 个），然后随机洗牌取前 homePageSize 个。
	// 如果库内不足 200 个会自动按实际数量返回，最后裁剪到 homePageSize。
	const candidatePool = 200
	items, _, err := s.Catalog.ListVideos(r.Context(), catalog.ListParams{
		Sort: "latest", Page: 1, PageSize: candidatePool,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	rand.Shuffle(len(items), func(i, j int) {
		items[i], items[j] = items[j], items[i]
	})
	if len(items) > homePageSize {
		items = items[:homePageSize]
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, mapVideos(items))
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	size, _ := strconv.Atoi(q.Get("size"))
	if size <= 0 {
		size = 24
	}
	params := catalog.ListParams{
		Keyword:  q.Get("q"),
		Tag:      q.Get("tag"),
		Category: q.Get("cat"),
		Sort:     q.Get("sort"),
		Page:     page,
		PageSize: size,
	}
	items, total, err := s.Catalog.ListVideos(r.Context(), params)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": mapVideos(items),
		"total": total,
		"page":  params.Page,
		"size":  params.PageSize,
	})
}

func (s *Server) handleVideoDetail(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	v, err := s.Catalog.GetVideo(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	if v.Hidden {
		writeErr(w, http.StatusNotFound, sql.ErrNoRows)
		return
	}
	related := s.pickRelatedVideos(r.Context(), v, 6)
	dto := mapVideo(v)
	if d, err := s.Catalog.GetDrive(r.Context(), v.DriveID); err == nil {
		dto.SourceLabel = driveKindLabel(d.Kind)
	}

	detail := VideoDetailDTO{
		VideoDTO:    dto,
		VideoSrc:    videoSource(v),
		Poster:      thumbnailURL(v),
		Description: v.Description,
		EmbedURL:    fmt.Sprintf(`<iframe src="/embed/%s" width="640" height="360" frameborder="0" allowfullscreen></iframe>`, v.ID),
		AuthorProfile: AuthorProfile{
			ID:     "author-" + v.Author,
			Name:   v.Author,
			Href:   "/author/" + v.Author,
			Badges: []string{},
		},
		RelatedVideos: mapVideos(related),
		CommentsList:  []Comment{},
	}
	// 推荐每次随机生成，禁止浏览器和中间层缓存详情响应
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, detail)
}

// pickRelatedVideos 选 total 个推荐视频。
// 一半（向上取整）来自同标签命中，剩下用全库随机补齐；不会重复，也不会包含当前视频。
func (s *Server) pickRelatedVideos(ctx context.Context, current *catalog.Video, total int) []*catalog.Video {
	if total <= 0 || current == nil {
		return nil
	}
	tagQuota := total / 2
	if tagQuota <= 0 && len(current.Tags) > 0 {
		tagQuota = 1
	}

	picked := make([]*catalog.Video, 0, total)
	seen := map[string]struct{}{current.ID: {}}

	// 1) 同标签候选：对每个 tag 取一批，合并去重，洗牌后取 tagQuota 个
	if tagQuota > 0 && len(current.Tags) > 0 {
		var tagPool []*catalog.Video
		for _, tag := range current.Tags {
			if tag == "" {
				continue
			}
			items, _, err := s.Catalog.ListVideos(ctx, catalog.ListParams{
				Tag: tag, Sort: "latest", Page: 1, PageSize: 30,
			})
			if err != nil {
				continue
			}
			for _, v := range items {
				if v == nil {
					continue
				}
				if _, ok := seen[v.ID]; ok {
					continue
				}
				seen[v.ID] = struct{}{}
				tagPool = append(tagPool, v)
			}
		}
		rand.Shuffle(len(tagPool), func(i, j int) {
			tagPool[i], tagPool[j] = tagPool[j], tagPool[i]
		})
		if len(tagPool) > tagQuota {
			tagPool = tagPool[:tagQuota]
		}
		picked = append(picked, tagPool...)
	}

	// 2) 随机补齐：从全库取一批（避开已选 ID），洗牌后取剩下的名额
	remaining := total - len(picked)
	if remaining > 0 {
		items, _, err := s.Catalog.ListVideos(ctx, catalog.ListParams{
			Sort: "latest", Page: 1, PageSize: 200,
		})
		if err == nil {
			var randomPool []*catalog.Video
			for _, v := range items {
				if v == nil {
					continue
				}
				if _, ok := seen[v.ID]; ok {
					continue
				}
				seen[v.ID] = struct{}{}
				randomPool = append(randomPool, v)
			}
			rand.Shuffle(len(randomPool), func(i, j int) {
				randomPool[i], randomPool[j] = randomPool[j], randomPool[i]
			})
			if len(randomPool) > remaining {
				randomPool = randomPool[:remaining]
			}
			picked = append(picked, randomPool...)
		}
	}

	return picked
}

func (s *Server) handleTags(w http.ResponseWriter, r *http.Request) {
	stats, err := s.Catalog.ListTags(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	type tag struct {
		ID    string `json:"id"`
		Label string `json:"label"`
		Count int    `json:"count"`
	}
	out := make([]tag, 0, len(stats))
	for _, stat := range stats {
		out = append(out, tag{ID: stat.Label, Label: stat.Label, Count: stat.Count})
	}
	writeJSON(w, http.StatusOK, out)
}

type updateVideoTagsReq struct {
	Tags []string `json:"tags"`
}

func (s *Server) handleUpdateVideoTags(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var body updateVideoTagsReq
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.Catalog.SetManualVideoTags(r.Context(), id, body.Tags); err != nil {
		if errors.Is(err, catalog.ErrUnknownTag) {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	v, err := s.Catalog.GetVideo(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, mapVideo(v))
}

func (s *Server) handleLike(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	likes, err := s.Catalog.IncrementLike(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"likes": likes})
}

func (s *Server) handleView(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	views, err := s.Catalog.IncrementView(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeErr(w, http.StatusNotFound, err)
			return
		}
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"views": views})
}

func (s *Server) handleHideVideo(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := s.Catalog.HideVideo(r.Context(), id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeErr(w, http.StatusNotFound, err)
			return
		}
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleUploadVideo(w http.ResponseWriter, r *http.Request) {
	if s.LocalDir == "" {
		writeErr(w, http.StatusInternalServerError, errors.New("local storage is not configured"))
		return
	}
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		writeErr(w, http.StatusBadRequest, errors.New("video file is required"))
		return
	}
	defer file.Close()

	originalName := filepath.Base(strings.TrimSpace(header.Filename))
	ext := strings.ToLower(filepath.Ext(originalName))
	if _, ok := allowedUploadExtensions[ext]; !ok {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("unsupported video extension: %s", ext))
		return
	}

	tags, err := parseUploadTags(uploadTagValues(r))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}

	now := time.Now()
	title := strings.TrimSpace(r.FormValue("title"))
	if title == "" {
		title = uploadTitleFromFileName(originalName)
	}

	uploadID, err := newUploadID(now)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	storedName := uploadID + ext
	dst, err := s.localUploadFilePath(storedName)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	size, copyErr := io.Copy(out, file)
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(dst)
		writeErr(w, http.StatusInternalServerError, copyErr)
		return
	}
	if closeErr != nil {
		_ = os.Remove(dst)
		writeErr(w, http.StatusInternalServerError, closeErr)
		return
	}
	if size <= 0 {
		_ = os.Remove(dst)
		writeErr(w, http.StatusBadRequest, errors.New("uploaded video is empty"))
		return
	}

	video := &catalog.Video{
		ID:            localUploadDriveID + "-" + uploadID,
		DriveID:       localUploadDriveID,
		FileID:        storedName,
		FileName:      originalName,
		Title:         title,
		Author:        "用户上传",
		Tags:          tags,
		Size:          size,
		Ext:           strings.TrimPrefix(ext, "."),
		PreviewStatus: "pending",
		PublishedAt:   now,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := s.Catalog.UpsertVideo(r.Context(), video); err != nil {
		_ = os.Remove(dst)
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if s.OnVideoUploaded != nil {
		s.OnVideoUploaded(video)
	}
	writeJSON(w, http.StatusCreated, mapVideo(video))
}

func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	driveID := chi.URLParam(r, "driveID")
	fileID := chi.URLParam(r, "fileID")
	s.Proxy.ServeStream(w, r, driveID, fileID)
}

func (s *Server) handleUploadedVideo(w http.ResponseWriter, r *http.Request) {
	videoID := chi.URLParam(r, "videoID")
	v, err := s.Catalog.GetVideo(r.Context(), videoID)
	if err != nil || v.Hidden || v.DriveID != localUploadDriveID {
		http.NotFound(w, r)
		return
	}
	path, err := s.localUploadFilePath(v.FileID)
	if err != nil {
		http.Error(w, "invalid upload file", http.StatusForbidden)
		return
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() || info.Size() == 0 {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=300")
	http.ServeFile(w, r, path)
}

func (s *Server) handleTranscode(w http.ResponseWriter, r *http.Request) {
	videoID := chi.URLParam(r, "videoID")
	v, err := s.Catalog.GetVideo(r.Context(), videoID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	path := s.transcodePath(v.ID)
	if s.transcodeStatus(v.ID) == "ready" {
		w.Header().Set("Content-Type", "video/mp4")
		w.Header().Set("Cache-Control", "private, max-age=86400")
		http.ServeFile(w, r, path)
		return
	}
	s.startTranscode(v)
	w.Header().Set("Retry-After", "3")
	writeJSON(w, http.StatusAccepted, map[string]string{"status": s.transcodeStatus(v.ID)})
}

func (s *Server) handleTranscodeStatus(w http.ResponseWriter, r *http.Request) {
	videoID := chi.URLParam(r, "videoID")
	if _, err := s.Catalog.GetVideo(r.Context(), videoID); err != nil {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": s.transcodeStatus(videoID)})
}

func (s *Server) handleTranscodeStart(w http.ResponseWriter, r *http.Request) {
	videoID := chi.URLParam(r, "videoID")
	v, err := s.Catalog.GetVideo(r.Context(), videoID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if s.transcodeStatus(v.ID) != "ready" {
		s.startTranscode(v)
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": s.transcodeStatus(v.ID)})
}

func (s *Server) startTranscode(v *catalog.Video) {
	if s.transcodeStatus(v.ID) == "ready" {
		return
	}
	s.transcodeMu.Lock()
	if s.transcodeJobs == nil {
		s.transcodeJobs = make(map[string]bool)
	}
	if s.transcodeJobs[v.ID] {
		s.transcodeMu.Unlock()
		return
	}
	s.transcodeJobs[v.ID] = true
	s.transcodeMu.Unlock()

	go func() {
		defer s.setTranscoding(v.ID, false)
		if err := s.generateTranscode(v); err != nil {
			log.Printf("[transcode] %s: %v", v.Title, err)
		}
	}()
}

func (s *Server) generateTranscode(v *catalog.Video) error {
	drv, ok := s.Proxy.Registry.Get(v.DriveID)
	if !ok {
		return fmt.Errorf("drive not found")
	}
	link, err := drv.StreamURL(context.Background(), v.FileID)
	if err != nil {
		return err
	}

	ffmpeg := s.FFmpegPath
	if ffmpeg == "" {
		ffmpeg = "ffmpeg"
	}
	args := []string{
		"-hide_banner",
		"-loglevel", "error",
		"-nostdin",
	}
	if h := buildFFmpegHeaders(link.Headers); h != "" {
		args = append(args, "-headers", h)
	}
	args = append(args,
		"-i", link.URL,
		"-map", "0:v:0",
		"-map", "0:a:0?",
		"-c:v", "libx264",
		"-preset", "veryfast",
		"-tune", "zerolatency",
		"-pix_fmt", "yuv420p",
		"-c:a", "aac",
		"-b:a", "128k",
		"-movflags", "+faststart",
		"-y",
	)

	dst := s.transcodePath(v.ID)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp := s.transcodeTempPath(v.ID)
	_ = os.Remove(tmp)
	args = append(args, tmp)
	cmd := exec.Command(ffmpeg, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("ffmpeg: %w, stderr: %s", err, string(out))
	}
	info, err := os.Stat(tmp)
	if err != nil {
		return err
	}
	if info.Size() == 0 {
		_ = os.Remove(tmp)
		return fmt.Errorf("ffmpeg produced empty file")
	}
	return os.Rename(tmp, dst)
}

func (s *Server) handlePreview(w http.ResponseWriter, r *http.Request) {
	videoID := chi.URLParam(r, "videoID")
	v, err := s.Catalog.GetVideo(r.Context(), videoID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if v.PreviewStatus != "ready" {
		http.Error(w, "preview not ready", http.StatusNotFound)
		return
	}
	if v.PreviewLocal != "" {
		if !strings.HasPrefix(filepath.Clean(v.PreviewLocal), filepath.Clean(s.LocalDir)) {
			http.Error(w, "invalid local path", http.StatusForbidden)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")
		s.Proxy.ServeLocal(w, r, v.PreviewLocal)
		return
	}
	http.NotFound(w, r)
}

func (s *Server) handleThumb(w http.ResponseWriter, r *http.Request) {
	videoID := chi.URLParam(r, "videoID")
	// 直接读本地 thumbs 目录中 <videoID>.jpg
	path := filepath.Join(s.LocalDir, "thumbs", videoID+".jpg")
	clean := filepath.Clean(path)
	if !strings.HasPrefix(clean, filepath.Clean(s.LocalDir)) {
		http.Error(w, "invalid path", http.StatusForbidden)
		return
	}
	if _, err := os.Stat(clean); err != nil {
		w.Header().Set("Cache-Control", "no-store")
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=86400")
	s.Proxy.ServeLocal(w, r, clean)
}

// ---------- helpers ----------

func mapVideo(v *catalog.Video) VideoDTO {
	badges := v.Badges
	if badges == nil {
		badges = []string{}
	}
	tags := v.Tags
	if tags == nil {
		tags = []string{}
	}
	return VideoDTO{
		ID:              v.ID,
		Href:            "/video/" + v.ID,
		Title:           v.Title,
		Thumbnail:       thumbnailURL(v),
		PreviewSrc:      previewURL(v),
		PreviewDuration: 12,
		PreviewStrategy: "teaser-file",
		Duration:        formatDuration(v.DurationSeconds),
		Badges:          badges,
		Quality:         v.Quality,
		Author:          v.Author,
		Views:           v.Views,
		Favorites:       v.Favorites,
		Comments:        v.Comments,
		Likes:           v.Likes,
		Dislikes:        v.Dislikes,
		PublishedAt:     v.PublishedAt.Format("2006-01-02"),
		Tags:            tags,
		Category:        v.Category,
	}
}

func previewURL(v *catalog.Video) string {
	base := "/p/preview/" + v.ID
	if v.UpdatedAt.IsZero() {
		return base
	}
	return base + "?v=" + strconv.FormatInt(v.UpdatedAt.UnixMilli(), 10)
}

func thumbnailURL(v *catalog.Video) string {
	if v.ThumbnailURL != "" {
		return v.ThumbnailURL
	}
	return "/p/thumb/" + v.ID
}

func videoSource(v *catalog.Video) string {
	if v.DriveID == localUploadDriveID {
		if needsBrowserTranscode(v.Ext) {
			return "/p/transcode/" + v.ID
		}
		return "/p/upload/" + v.ID
	}
	if needsBrowserTranscode(v.Ext) {
		return "/p/transcode/" + v.ID
	}
	return fmt.Sprintf("/p/stream/%s/%s", v.DriveID, v.FileID)
}

func needsBrowserTranscode(ext string) bool {
	switch strings.ToLower(strings.TrimPrefix(ext, ".")) {
	case "avi", "mkv":
		return true
	default:
		return false
	}
}

func driveKindLabel(kind string) string {
	switch kind {
	case "quark":
		return "夸克网盘"
	case "p115":
		return "115 网盘"
	case "pikpak":
		return "PikPak"
	case "wopan":
		return "联通沃盘"
	case "onedrive":
		return "OneDrive"
	default:
		return kind
	}
}

func buildFFmpegHeaders(h http.Header) string {
	if len(h) == 0 {
		return ""
	}
	var sb strings.Builder
	for k, vs := range h {
		for _, v := range vs {
			sb.WriteString(k)
			sb.WriteString(": ")
			sb.WriteString(v)
			sb.WriteString("\r\n")
		}
	}
	return sb.String()
}

func (s *Server) transcodeStatus(videoID string) string {
	if info, err := os.Stat(s.transcodePath(videoID)); err == nil && info.Size() > 0 {
		return "ready"
	}
	s.transcodeMu.Lock()
	defer s.transcodeMu.Unlock()
	if s.transcodeJobs != nil && s.transcodeJobs[videoID] {
		return "processing"
	}
	return "missing"
}

func (s *Server) setTranscoding(videoID string, processing bool) {
	s.transcodeMu.Lock()
	defer s.transcodeMu.Unlock()
	if s.transcodeJobs == nil {
		s.transcodeJobs = make(map[string]bool)
	}
	if processing {
		s.transcodeJobs[videoID] = true
		return
	}
	delete(s.transcodeJobs, videoID)
}

func (s *Server) transcodePath(videoID string) string {
	return filepath.Join(s.LocalDir, "transcodes", videoID+".mp4")
}

func (s *Server) transcodeTempPath(videoID string) string {
	return filepath.Join(s.LocalDir, "transcodes", videoID+".tmp.mp4")
}

func (s *Server) localUploadFilePath(fileID string) (string, error) {
	if strings.TrimSpace(fileID) == "" || filepath.Base(fileID) != fileID {
		return "", errors.New("invalid upload file id")
	}
	root := s.localUploadDir()
	if root == "" {
		return "", errors.New("local upload storage is not configured")
	}
	path := filepath.Join(root, fileID)
	cleanRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	cleanPath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if cleanPath != cleanRoot && !strings.HasPrefix(cleanPath, cleanRoot+string(os.PathSeparator)) {
		return "", errors.New("invalid upload file id")
	}
	return cleanPath, nil
}

func (s *Server) localUploadDir() string {
	if s.UploadDir != "" {
		return s.UploadDir
	}
	if s.LocalDir == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(s.LocalDir), "uploads")
}

func uploadTagValues(r *http.Request) []string {
	if r.MultipartForm == nil {
		return nil
	}
	values := append([]string{}, r.MultipartForm.Value["tags"]...)
	values = append(values, r.MultipartForm.Value["tag"]...)
	return values
}

func uploadTitleFromFileName(fileName string) string {
	name := strings.TrimSpace(filepath.Base(fileName))
	ext := filepath.Ext(name)
	if ext != "" {
		if trimmed := strings.TrimSuffix(name, ext); strings.TrimSpace(trimmed) != "" {
			return trimmed
		}
	}
	if name != "" {
		return name
	}
	return "upload-" + time.Now().Format("20060102150405")
}

func parseUploadTags(values []string) ([]string, error) {
	seen := make(map[string]struct{})
	out := make([]string, 0, len(values))
	for _, value := range values {
		for _, label := range splitUploadTags(value) {
			if _, ok := allowedUploadTags[label]; !ok {
				return nil, fmt.Errorf("unsupported upload tag: %s", label)
			}
			if _, ok := seen[label]; ok {
				continue
			}
			seen[label] = struct{}{}
			out = append(out, label)
		}
	}
	return out, nil
}

func splitUploadTags(value string) []string {
	fields := strings.FieldsFunc(value, func(r rune) bool {
		switch r {
		case ',', '，', ';', '；', '\n', '\r', '\t', ' ':
			return true
		default:
			return false
		}
	})
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		if label := strings.TrimSpace(field); label != "" {
			out = append(out, label)
		}
	}
	return out
}

func newUploadID(now time.Time) (string, error) {
	var suffix [6]byte
	if _, err := crand.Read(suffix[:]); err != nil {
		return "", err
	}
	return fmt.Sprintf("upload-%d-%s", now.UnixNano(), hex.EncodeToString(suffix[:])), nil
}

func mapVideos(vs []*catalog.Video) []VideoDTO {
	out := make([]VideoDTO, 0, len(vs))
	for _, v := range vs {
		out = append(out, mapVideo(v))
	}
	return out
}

func formatDuration(sec int) string {
	if sec <= 0 {
		return "00:00"
	}
	m := sec / 60
	s := sec % 60
	return fmt.Sprintf("%02d:%02d", m, s)
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

func writeErr(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}
