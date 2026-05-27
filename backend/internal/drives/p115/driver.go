package p115

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	sdk "github.com/SheltonZhu/115driver/pkg/driver"
	"github.com/video-site/backend/internal/drives"
)

type Driver struct {
	id     string
	cookie string
	rootID string
	client *sdk.Pan115Client
	ua     string

	listMu       sync.Mutex
	lastListAt   time.Time
	listInterval time.Duration
}

type Config struct {
	ID     string
	Cookie string // 形如 "UID=xxx; CID=xxx; SEID=xxx; KID=xxx"
	RootID string // 默认 "0"
	UA     string // 默认 UA115Browser
}

func New(c Config) *Driver {
	rootID := c.RootID
	if rootID == "" {
		rootID = "0"
	}
	ua := c.UA
	if ua == "" {
		ua = sdk.UA115Browser
	}
	return &Driver{
		id:           c.ID,
		cookie:       c.Cookie,
		rootID:       rootID,
		ua:           ua,
		listInterval: 2 * time.Second,
	}
}

func (d *Driver) Kind() string   { return "p115" }
func (d *Driver) ID() string     { return d.id }
func (d *Driver) RootID() string { return d.rootID }

func (d *Driver) Init(ctx context.Context) error {
	cr := &sdk.Credential{}
	if err := cr.FromCookie(d.cookie); err != nil {
		return fmt.Errorf("parse cookie: %w", err)
	}
	d.client = sdk.New(sdk.UA(d.ua)).ImportCredential(cr)
	return d.client.LoginCheck()
}

func (d *Driver) List(ctx context.Context, dirID string) ([]drives.Entry, error) {
	files, err := d.listWithRetry(ctx, dirID)
	if err != nil {
		return nil, fmt.Errorf("115 list: %w", err)
	}
	if files == nil {
		return nil, nil
	}
	out := make([]drives.Entry, 0, len(*files))
	for _, f := range *files {
		out = append(out, fileToEntry(&f, dirID))
	}
	return out, nil
}

func (d *Driver) listWithRetry(ctx context.Context, dirID string) (*[]sdk.File, error) {
	d.listMu.Lock()
	defer d.listMu.Unlock()

	cooldowns := []time.Duration{30 * time.Minute, 30 * time.Minute, 30 * time.Minute}
	var lastErr error
	for attempt := 0; ; attempt++ {
		if err := d.waitForListSlotLocked(ctx); err != nil {
			return nil, err
		}

		files, err := d.client.ListWithLimit(dirID, sdk.MaxDirPageLimit)
		if err == nil {
			return files, nil
		}
		lastErr = err
		if !isTransient115ListError(err) || attempt >= len(cooldowns) {
			break
		}
		cooldown := cooldowns[attempt]
		log.Printf("[p115] list cooling down drive=%s dir=%s cooldown=%s attempt=%d/%d", d.id, dirID, cooldown, attempt+1, len(cooldowns))
		if err := sleepContext(ctx, cooldown); err != nil {
			return nil, err
		}
	}
	return nil, lastErr
}

func (d *Driver) waitForListSlotLocked(ctx context.Context) error {
	if d.listInterval <= 0 || d.lastListAt.IsZero() {
		d.lastListAt = time.Now()
		return ctx.Err()
	}

	next := d.lastListAt.Add(d.listInterval)
	now := time.Now()
	if now.Before(next) {
		if err := sleepContext(ctx, next.Sub(now)); err != nil {
			return err
		}
	}
	d.lastListAt = time.Now()
	return ctx.Err()
}

func sleepContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func isTransient115ListError(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "405") ||
		strings.Contains(text, "429") ||
		strings.Contains(text, "too many request") ||
		strings.Contains(text, "too many requests") ||
		strings.Contains(text, "blocked") ||
		strings.Contains(text, "security") ||
		strings.Contains(text, "waf") ||
		strings.Contains(text, "unexpected error") ||
		strings.Contains(text, "访问被阻断") ||
		strings.Contains(text, "安全威胁")
}

func (d *Driver) Stat(ctx context.Context, fileID string) (*drives.Entry, error) {
	f, err := d.client.GetFile(fileID)
	if err != nil {
		return nil, fmt.Errorf("115 stat: %w", err)
	}
	if f == nil {
		return nil, errors.New("115 stat: not found")
	}
	e := fileToEntry(f, f.ParentID)
	return &e, nil
}

func (d *Driver) StreamURL(ctx context.Context, fileID string) (*drives.StreamLink, error) {
	return d.streamURLWithUA(ctx, fileID, d.ua)
}

func (d *Driver) StreamURLWithHeader(ctx context.Context, fileID string, header http.Header) (*drives.StreamLink, error) {
	return d.streamURLWithUA(ctx, fileID, header.Get("User-Agent"))
}

func (d *Driver) streamURLWithUA(ctx context.Context, fileID string, ua string) (*drives.StreamLink, error) {
	// 需要先拿到 pickCode
	f, err := d.client.GetFile(fileID)
	if err != nil {
		return nil, fmt.Errorf("115 get file: %w", err)
	}
	info, ua, err := d.downloadInfo(f.PickCode, ua)
	if err != nil {
		return nil, fmt.Errorf("115 download url: %w", err)
	}
	if info == nil || info.Url.Url == "" {
		return nil, errors.New("115 download url: empty")
	}

	headers := http.Header{}
	// 115 直链会返回一组 Cookie / Referer，info.Header 里带了
	for k, vs := range info.Header {
		for _, v := range vs {
			headers.Add(k, v)
		}
	}
	if headers.Get("User-Agent") == "" {
		headers.Set("User-Agent", ua)
	}

	return &drives.StreamLink{
		URL:     info.Url.Url,
		Headers: headers,
		Expires: time.Now().Add(25 * time.Minute), // 115 直链 30 分钟过期，留余量
	}, nil
}

func (d *Driver) downloadInfo(pickCode string, ua string) (*sdk.DownloadInfo, string, error) {
	ua = strings.TrimSpace(ua)
	if ua == "" {
		ua = d.ua
	}
	info, err := d.client.DownloadWithUA(pickCode, ua)
	if err != nil {
		return nil, "", err
	}
	return info, ua, nil
}

func (d *Driver) Upload(ctx context.Context, parentID, name string, r io.Reader, size int64) (string, error) {
	res, err := d.UploadAndReportSha1(ctx, parentID, name, r, size)
	if err != nil {
		return "", err
	}
	return res.FileID, nil
}

// UploadResult 是 UploadAndReportSha1 的返回值。
//
// FileID: 上传后 115 给该文件分配的 ID（在父目录里能查到）。
// Sha1:   文件的 SHA1（HEX 大写，与 115 的 sha1 格式一致），可直接写入 catalog.content_hash。
// Size:   实际上传的字节数（如调用时 size>0 应当与传入一致）。
type UploadResult struct {
	FileID string
	Sha1   string
	Size   int64
}

// UploadAndReportSha1 把 r 上传到 parentID 目录下的指定文件名，返回新文件元数据。
//
// 实现要点（参考 OpenList drivers/115）：
//
//  1. 把 r 全量缓冲到本地临时文件并同时算 SHA1，避免在内存里堆 100MB 视频；
//     拿到 *os.File 后才能进 SDK 的 RapidUploadOrByMultipart 分片上传通道。
//  2. SDK 的 RapidUploadOrByMultipart 内部会：
//       a. 调 /upload/init 走 ECDH 加密的秒传协议，命中即结束；
//       b. 对 status=7 自动做范围 SHA1 二次校验后重试；
//       c. 未命中且 size<=1KB 走 OSS PutObject；否则按 fileSize<i*GB → i*1000 片切分调 OSS multipart。
//  3. SDK 不返回 fileID。我们在上传完成后用 GetFiles 列父目录，按 SHA1 + 文件名匹配新文件。
//     列父目录时按时间倒序拉前 500 条，刚上传的文件会在最前面，几乎不会漏。
//
// 该方法不会按 SHA1 跨目录复用 fileID —— 同一文件如果父目录里已经有同名同 sha1 文件，
// 115 服务端会直接秒传成功并把已有文件视为本次结果，列目录时也仍然能找到，行为一致。
func (d *Driver) UploadAndReportSha1(ctx context.Context, parentID, name string, r io.Reader, size int64) (UploadResult, error) {
	if d.client == nil {
		return UploadResult{}, errors.New("p115 upload: driver not initialized")
	}
	if r == nil {
		return UploadResult{}, errors.New("p115 upload: nil reader")
	}
	if size < 0 {
		return UploadResult{}, fmt.Errorf("p115 upload: invalid size %d", size)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return UploadResult{}, errors.New("p115 upload: empty file name")
	}
	parentID = strings.TrimSpace(parentID)
	if parentID == "" {
		parentID = d.rootID
	}

	tmp, sha1Hex, written, err := bufferAndHashSha1(r, size)
	if err != nil {
		return UploadResult{}, err
	}
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
	}()

	// 把流位置回到开头交给 SDK；SDK 会自己 Digest 一次（重复算一次 SHA1，
	// 但代码上可以避免侵入 SDK 内部状态机）。
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return UploadResult{}, fmt.Errorf("p115 upload: seek tmp: %w", err)
	}

	if err := ctx.Err(); err != nil {
		return UploadResult{}, err
	}

	if err := d.client.RapidUploadOrByMultipart(parentID, name, written, tmp); err != nil {
		return UploadResult{}, fmt.Errorf("p115 upload: %w", err)
	}

	fileID, err := d.findUploadedFileID(ctx, parentID, name, sha1Hex)
	if err != nil {
		return UploadResult{}, err
	}

	return UploadResult{FileID: fileID, Sha1: sha1Hex, Size: written}, nil
}

// findUploadedFileID 列出 parentID 目录，按 (sha1, name) 找到新上传的文件并返回 fileID。
// 列目录用时间倒序 + Limit=500，新上传的文件几乎一定在前 500 条里。
//
// 失败时返回包装好的错误，由上层决定是否重试。
//
// 注：sdk.GetFiles 返回的 FileInfo 是 115 API 的原始结构，里面没有显式的 IsDirectory 字段；
// 文件的 FileID 非空、目录的 FileID 为空（目录是 CategoryID 自身）。
func (d *Driver) findUploadedFileID(ctx context.Context, parentID, name, sha1Hex string) (string, error) {
	req := d.client.NewRequest().ForceContentType("application/json;charset=UTF-8")
	resp, err := sdk.GetFiles(req, parentID,
		sdk.WithOrder(sdk.FileOrderByTime),
		sdk.WithShowDirEnable(false),
		sdk.WithAsc(false),
		sdk.WithLimit(500),
	)
	if err != nil {
		return "", fmt.Errorf("p115 upload verify: %w", err)
	}
	if resp == nil {
		return "", fmt.Errorf("p115 upload: empty list response for parent %q", parentID)
	}
	// 优先按 sha1 + name 双匹配；少数情况下名字含特殊字符被 115 服务端二次处理（比如折叠空白），
	// 仍然以 sha1 命中即认可。
	var sha1Hit string
	for _, f := range resp.Files {
		if f.FileID == "" {
			continue // 目录
		}
		if !strings.EqualFold(f.Sha1, sha1Hex) {
			continue
		}
		if f.Name == name {
			return f.FileID, nil
		}
		if sha1Hit == "" {
			sha1Hit = f.FileID
		}
	}
	if sha1Hit != "" {
		return sha1Hit, nil
	}
	// 兜底：仅按 name 找
	for _, f := range resp.Files {
		if f.FileID != "" && f.Name == name {
			return f.FileID, nil
		}
	}
	return "", fmt.Errorf("p115 upload: uploaded file %q not found in parent %q", name, parentID)
}

// Rename 调用 115 SDK 把指定 fileID 重命名为 newName。
// 包装错误信息，方便日志定位是 115 端的失败。
func (d *Driver) Rename(ctx context.Context, fileID, newName string) error {
	if d.client == nil {
		return errors.New("p115 rename: driver not initialized")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	fileID = strings.TrimSpace(fileID)
	newName = strings.TrimSpace(newName)
	if fileID == "" {
		return errors.New("p115 rename: empty fileID")
	}
	if newName == "" {
		return errors.New("p115 rename: empty newName")
	}
	if err := d.client.Rename(fileID, newName); err != nil {
		return fmt.Errorf("p115 rename: %w", err)
	}
	return nil
}

// bufferAndHashSha1 把 r 全量复制到一个临时文件，同时计算 SHA1。
// 返回临时文件（位置在末尾，需调用方 Seek 回 0）、SHA1 hex 大写、实际字节数。
//
// 调用方负责 Close + Remove 临时文件。
func bufferAndHashSha1(r io.Reader, declaredSize int64) (*os.File, string, int64, error) {
	tmp, err := os.CreateTemp("", "p115-upload-*.bin")
	if err != nil {
		return nil, "", 0, fmt.Errorf("p115 upload: create tmp: %w", err)
	}

	h := sha1.New()
	mw := io.MultiWriter(tmp, h)
	written, err := io.Copy(mw, r)
	if err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return nil, "", 0, fmt.Errorf("p115 upload: buffer body: %w", err)
	}
	if declaredSize > 0 && written != declaredSize {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return nil, "", 0, fmt.Errorf("p115 upload: size mismatch: declared %d, copied %d", declaredSize, written)
	}
	sha1Hex := strings.ToUpper(hex.EncodeToString(h.Sum(nil)))
	return tmp, sha1Hex, written, nil
}

func (d *Driver) EnsureDir(ctx context.Context, pathFromRoot string) (string, error) {
	parts := splitPath(pathFromRoot)
	currentID := d.rootID
	for _, name := range parts {
		childID, err := d.findChildDir(ctx, currentID, name)
		if err != nil {
			return "", err
		}
		if childID == "" {
			id, err := d.client.Mkdir(currentID, name)
			if err != nil {
				return "", fmt.Errorf("115 mkdir %s: %w", name, err)
			}
			childID = id
		}
		currentID = childID
	}
	return currentID, nil
}

func (d *Driver) findChildDir(ctx context.Context, parent, name string) (string, error) {
	entries, err := d.List(ctx, parent)
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if e.IsDir && e.Name == name {
			return e.ID, nil
		}
	}
	return "", nil
}

func splitPath(p string) []string {
	p = strings.Trim(p, "/")
	if p == "" {
		return nil
	}
	return strings.Split(p, "/")
}

func fileToEntry(f *sdk.File, parentID string) drives.Entry {
	return drives.Entry{
		ID:           f.FileID,
		Name:         f.Name,
		Size:         f.Size,
		Hash:         f.Sha1,
		IsDir:        f.IsDirectory,
		ParentID:     parentID,
		MimeType:     guessMime(f.Name),
		ModTime:      f.UpdateTime,
		ThumbnailURL: f.ThumbURL,
	}
}

func guessMime(name string) string {
	ext := strings.ToLower(path.Ext(name))
	switch ext {
	case ".mp4":
		return "video/mp4"
	case ".mkv":
		return "video/x-matroska"
	case ".mov":
		return "video/quicktime"
	case ".webm":
		return "video/webm"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	}
	return "application/octet-stream"
}

var _ drives.Drive = (*Driver)(nil)
