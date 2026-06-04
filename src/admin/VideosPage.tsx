import { useEffect, useId, useState } from "react";
import { ChevronDown, Edit, RefreshCw, Search, CheckSquare, Square, Image, Trash2 } from "lucide-react";
import * as api from "./api";
import { useToast } from "./ToastContext";
import { Modal } from "./Modal";
import { ConfirmModal } from "./ConfirmModal";
import { formatBytes } from "./storageFormat";

const DESKTOP_VIDEOS_PAGE_SIZE = 50;
const MOBILE_VIDEOS_PAGE_SIZE = 20;
const VIDEOS_MOBILE_QUERY = "(max-width: 640px)";

export function VideosPage() {
  const [list, setList] = useState<api.AdminVideo[]>([]);
  const [drives, setDrives] = useState<api.AdminDrive[]>([]);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState("");
  const [keyword, setKeyword] = useState("");
  const [searchKeyword, setSearchKeyword] = useState("");
  const [driveId, setDriveId] = useState("");
  const [page, setPage] = useState(1);
  const [total, setTotal] = useState(0);
  const [editing, setEditing] = useState<api.AdminVideo | null>(null);
  const [availableTags, setAvailableTags] = useState<api.AdminTag[]>([]);
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set());
  const [batchRegenOpen, setBatchRegenOpen] = useState(false);
  const [batchRegening, setBatchRegening] = useState(false);
  const [batchDeleteOpen, setBatchDeleteOpen] = useState(false);
  const [batchDeleting, setBatchDeleting] = useState(false);
  const [deleteTarget, setDeleteTarget] = useState<api.AdminVideo | null>(null);
  const [deleting, setDeleting] = useState(false);
  const pageSize = useVideosPageSize();
  const { show } = useToast();

  async function refresh() {
    setLoading(true);
    setLoadError("");
    try {
      const [r, tagList, driveList] = await Promise.all([
        api.listVideos({ driveId, page, size: pageSize, keyword: searchKeyword }),
        api.listTags(),
        api.listDrives(),
      ]);
      setList(r.items ?? []);
      setTotal(r.total ?? 0);
      setAvailableTags(tagList);
      setDrives(driveList ?? []);
      setSelectedIds(new Set());
    } catch (e) {
      const message = e instanceof Error ? e.message : "加载失败";
      setLoadError(message);
      show(message, "error");
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    refresh();
  }, [driveId, page, searchKeyword, pageSize]);

  useEffect(() => {
    setPage(1);
  }, [pageSize]);

  useEffect(() => {
    if (keyword === searchKeyword) return;
    const timer = window.setTimeout(() => {
      setSearchKeyword(keyword);
      setPage(1);
    }, 300);
    return () => window.clearTimeout(timer);
  }, [keyword]);

  const driveNameMap = new Map(
    drives.map((d) => [d.id, d.name || d.id])
  );

  const listItems = list;
  const totalPages = Math.max(1, Math.ceil(total / pageSize));
  const pageStart = total === 0 ? 0 : (page - 1) * pageSize + 1;
  const pageEnd = Math.min(total, page * pageSize);
  const listSummary = driveId
    ? `${driveNameMap.get(driveId) ?? driveId}：共 ${total} 个视频，第 ${page} / ${totalPages} 页，显示 ${pageStart}-${pageEnd}`
    : `全部网盘：共 ${total} 个视频，第 ${page} / ${totalPages} 页，显示 ${pageStart}-${pageEnd}`;

  async function handleRegen(v: api.AdminVideo) {
    try {
      await api.regenPreview(v.id);
      show("已触发预览视频重生", "success");
    } catch (e) {
      show(e instanceof Error ? e.message : "触发失败", "error");
    }
  }

  async function handleBatchRegen() {
    if (selectedIds.size === 0) return;
    setBatchRegenOpen(true);
  }

  async function handleBatchDelete() {
    if (selectedIds.size === 0) return;
    setBatchDeleteOpen(true);
  }

  async function confirmBatchRegen() {
    const ids = [...selectedIds];
    setBatchRegening(true);
    let success = 0;
    try {
      const results = await Promise.allSettled(
        ids.map((id) => api.regenPreview(id))
      );
      for (const r of results) {
        if (r.status === "fulfilled") success++;
      }
      show(`批量触发完成，成功 ${success} / ${ids.length} 个`, success === ids.length ? "success" : "info");
      setSelectedIds(new Set());
      setBatchRegenOpen(false);
    } finally {
      setBatchRegening(false);
    }
  }

  async function confirmDeleteVideo() {
    if (!deleteTarget) return;
    const target = deleteTarget;
    setDeleting(true);
    try {
      const result = await api.deleteVideo(target.id);
      setDeleteTarget(null);
      setSelectedIds((ids) => {
        const next = new Set(ids);
        next.delete(target.id);
        return next;
      });
      show(result.deletedSource ? "已删除视频，并清理 91Spider 源文件" : "已删除视频", "success");
      if (listItems.length === 1 && page > 1) {
        setPage((p) => Math.max(1, p - 1));
      } else {
        refresh();
      }
    } catch (e) {
      show(e instanceof Error ? e.message : "删除失败", "error");
    } finally {
      setDeleting(false);
    }
  }

  async function confirmBatchDelete() {
    const ids = [...selectedIds];
    if (ids.length === 0) return;
    setBatchDeleting(true);
    try {
      const results = await Promise.allSettled(
        ids.map((id) => api.deleteVideo(id))
      );
      let success = 0;
      let deletedSources = 0;
      for (const r of results) {
        if (r.status !== "fulfilled") continue;
        success++;
        if (r.value.deletedSource) deletedSources++;
      }
      const failed = ids.length - success;
      if (failed === 0) {
        const extra = deletedSources > 0 ? `，其中 ${deletedSources} 个清理了 91Spider 源文件` : "";
        show(`批量删除完成，成功 ${success} 个${extra}`, "success");
      } else {
        show(`批量删除完成，成功 ${success} / ${ids.length} 个，失败 ${failed} 个`, success > 0 ? "info" : "error");
      }
      setSelectedIds(new Set());
      setBatchDeleteOpen(false);
      if (success >= listItems.length && page > 1) {
        setPage((p) => Math.max(1, p - 1));
      } else {
        refresh();
      }
    } finally {
      setBatchDeleting(false);
    }
  }

  const toggleSelectAll = () => {
    if (selectedIds.size === listItems.length && listItems.length > 0) {
      setSelectedIds(new Set());
    } else {
      setSelectedIds(new Set(listItems.map(v => v.id)));
    }
  };

  const toggleSelect = (id: string) => {
    const next = new Set(selectedIds);
    if (next.has(id)) next.delete(id);
    else next.add(id);
    setSelectedIds(next);
  };

  function handleSearchSubmit(e: React.FormEvent) {
    e.preventDefault();
    setSearchKeyword(keyword);
    setPage(1);
  }

  return (
    <section>
      <header className="admin-page__header">
        <h1 className="admin-page__title">视频管理</h1>
        <div className="admin-page__actions admin-videos-filter">
          <div className="admin-videos-filter__select-wrap">
            <select
              className="admin-videos-filter__select"
              value={driveId}
              onChange={(e) => {
                setDriveId(e.target.value);
                setPage(1);
              }}
            >
              <option value="">全部网盘</option>
              {drives.map((d) => (
                <option key={d.id} value={d.id}>
                  {d.name || d.id}（已生成 {d.teaserReadyCount ?? 0}，待生成{" "}
                  {d.teaserPendingCount ?? 0}）
                </option>
              ))}
            </select>
            <ChevronDown size={15} className="admin-videos-filter__select-icon" aria-hidden="true" />
          </div>
          <form className="admin-videos-filter__search" onSubmit={handleSearchSubmit}>
            <Search size={14} className="admin-videos-filter__search-icon" />
            <input
              aria-label="搜索标题或作者"
              value={keyword}
              onChange={(e) => setKeyword(e.target.value)}
              placeholder="搜索标题 / 作者"
            />
          </form>
          <button type="button" className="admin-btn" onClick={refresh}>
            <RefreshCw size={13} /> 刷新
          </button>
        </div>
      </header>

      {!loading && (
        <div className="admin-videos-list-toolbar">
          <div className="admin-videos-summary">{listSummary}</div>
          {selectedIds.size > 0 && (
            <div className="admin-videos-bulk-actions">
              <span className="admin-videos-bulk-actions__count">
                已选择 {selectedIds.size} 项
              </span>
              <button type="button" className="admin-btn is-primary admin-videos-bulk-actions__btn" onClick={handleBatchRegen}>
                <RefreshCw size={13} /> 批量重生预览视频
              </button>
              <button type="button" className="admin-btn is-danger admin-videos-bulk-actions__btn" onClick={handleBatchDelete}>
                <Trash2 size={13} /> 批量删除
              </button>
            </div>
          )}
        </div>
      )}

      {loading ? (
        <div className="admin-loading-state">
          <RefreshCw size={20} className="admin-spin" />
          <span>加载中...</span>
        </div>
      ) : loadError ? (
        <div className="admin-error-state">
          <strong>视频加载失败</strong>
          <span>{loadError}</span>
          <button type="button" className="admin-btn" onClick={refresh}>
            <RefreshCw size={13} /> 重试
          </button>
        </div>
      ) : listItems.length === 0 ? (
        <div className="admin-empty-state">
          <div className="admin-empty-state__icon">
            <Image size={48} />
          </div>
          <div className="admin-empty-state__text">
            {driveId
              ? "这个网盘下还没有可显示的视频，或未匹配到搜索结果。"
              : "还没有视频。先在「网盘管理」里配置好盘并触发扫描，或调整搜索词。"}
          </div>
        </div>
      ) : (
        <>
          <table className="admin-table is-selectable admin-videos-table">
            <thead>
              <tr>
                <th className="is-checkbox" style={{ width: '40px' }}>
                  <button
                    type="button"
                    className="admin-table-checkbox-btn"
                    onClick={toggleSelectAll}
                    aria-label={selectedIds.size > 0 && selectedIds.size === listItems.length ? "清空当前页选择" : "选择当前页视频"}
                  >
                    {selectedIds.size > 0 && selectedIds.size === listItems.length ? <CheckSquare size={16} /> : <Square size={16} />}
                  </button>
                </th>
                <th>标题</th>
                <th>作者</th>
                <th>标签</th>
                <th>时长</th>
                <th>预览视频</th>
                <th>来源</th>
                <th className="is-actions">操作</th>
              </tr>
            </thead>
            <tbody>
              {listItems.map((v) => (
                <tr key={v.id} className={selectedIds.has(v.id) ? "is-selected" : ""}>
                  <td className="is-checkbox">
                    <button
                      type="button"
                      className="admin-table-checkbox-btn"
                      onClick={() => toggleSelect(v.id)}
                      aria-label={`${selectedIds.has(v.id) ? "取消选择" : "选择"}视频 ${v.title}`}
                    >
                      {selectedIds.has(v.id) ? <CheckSquare size={16} color="var(--accent)" /> : <Square size={16} color="var(--border-strong)" />}
                    </button>
                  </td>
                  <td data-label="标题">
                    <div className="admin-video-title">{v.title}</div>
                    {fileMeta(v) && (
                      <div className="admin-video-filemeta">
                        {fileMeta(v)}
                      </div>
                    )}
                    <VideoFileMetaPills video={v} />
                  </td>
                  <td data-label="作者">{v.author || <span className="admin-text-faint">—</span>}</td>
                  <td data-label="标签">
                    <div className="admin-pills">
                      {(v.tags ?? []).map((t) => (
                        <span key={t} className="admin-pill">
                          {t}
                        </span>
                      ))}
                    </div>
                  </td>
                  <td data-label="时长">{formatDur(v.durationSeconds)}</td>
                  <td data-label="预览视频">
                    <PreviewStatus s={v.previewStatus} />
                  </td>
                  <td data-label="来源" className="admin-mono-cell">
                    {driveNameMap.get(v.driveId) ?? v.driveId}
                  </td>
                  <td className="is-actions" data-label="操作">
                    <button type="button" className="admin-btn" onClick={() => setEditing(v)}>
                      <Edit size={13} /> 编辑
                    </button>{" "}
                    <button type="button" className="admin-btn" onClick={() => handleRegen(v)} title="重生预览视频">
                      <RefreshCw size={13} />
                    </button>{" "}
                    <button type="button" className="admin-btn is-danger" onClick={() => setDeleteTarget(v)} title="删除视频">
                      <Trash2 size={13} />
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          <div className="admin-table-pagination">
            <button
              type="button"
              className="admin-btn"
              onClick={() => setPage(1)}
              disabled={page <= 1}
            >
              首页
            </button>
            <button
              type="button"
              className="admin-btn"
              onClick={() => setPage((p) => Math.max(1, p - 1))}
              disabled={page <= 1}
            >
              上一页
            </button>
            <span className="admin-table-pagination__info">
              第 {page} / {totalPages} 页，每页 {pageSize} 个
            </span>
            <button
              type="button"
              className="admin-btn"
              onClick={() => setPage((p) => Math.min(totalPages, p + 1))}
              disabled={page >= totalPages}
            >
              下一页
            </button>
            <button
              type="button"
              className="admin-btn"
              onClick={() => setPage(totalPages)}
              disabled={page >= totalPages}
            >
              末页
            </button>
          </div>
        </>
      )}

      {editing && (
        <EditVideoModal
          video={editing}
          availableTags={availableTags}
          onClose={() => setEditing(null)}
          onSaved={() => {
            setEditing(null);
            refresh();
          }}
        />
      )}
      <ConfirmModal
        open={batchRegenOpen}
        title="批量重生预览视频"
        message={`确定要为当前页选中的 ${selectedIds.size} 个视频重新生成预览视频吗？`}
        confirmText="确认重生"
        loading={batchRegening}
        onCancel={() => {
          if (!batchRegening) setBatchRegenOpen(false);
        }}
        onConfirm={confirmBatchRegen}
      />
      <ConfirmModal
        open={deleteTarget !== null}
        title="删除视频"
        message={deleteTarget ? `确定要删除「${deleteTarget.title}」吗？` : ""}
        confirmText="删除视频"
        danger
        centerMessage
        modalClassName="admin-modal--delete-confirm"
        loading={deleting}
        onCancel={() => {
          if (!deleting) setDeleteTarget(null);
        }}
        onConfirm={confirmDeleteVideo}
      />
      <ConfirmModal
        open={batchDeleteOpen}
        title="批量删除视频"
        message={`确定要删除当前页选中的 ${selectedIds.size} 个视频吗？`}
        confirmText="批量删除"
        danger
        centerMessage
        modalClassName="admin-modal--delete-confirm"
        loading={batchDeleting}
        onCancel={() => {
          if (!batchDeleting) setBatchDeleteOpen(false);
        }}
        onConfirm={confirmBatchDelete}
      />
    </section>
  );
}

function PreviewStatus({ s }: { s: string }) {
  if (s === "ready") return <span className="admin-status is-ok">就绪</span>;
  if (s === "failed") return <span className="admin-status is-error">失败</span>;
  if (s === "skipped") return <span className="admin-status">跳过</span>;
  return <span className="admin-status is-pending">待生成</span>;
}

function VideoFileMetaPills({ video }: { video: api.AdminVideo }) {
  const parts = fileMetaParts(video);
  const category = (video.category ?? "").trim();
  if (parts.length === 0 && !category) return null;

  return (
    <div className="admin-video-filemeta-pills" aria-label="视频文件信息">
      {parts.map((part, index) => (
        <span key={`${part}-${index}`} className="admin-video-filemeta-pill">
          {part}
        </span>
      ))}
      {category && (
        <span className="admin-video-filemeta-pill is-category">
          {category}
        </span>
      )}
    </div>
  );
}

function formatDur(sec: number): string {
  if (!sec) return "—";
  const m = Math.floor(sec / 60);
  const s = sec % 60;
  return `${m.toString().padStart(2, "0")}:${s.toString().padStart(2, "0")}`;
}

function useVideosPageSize() {
  const [pageSize, setPageSize] = useState(() =>
    window.matchMedia(VIDEOS_MOBILE_QUERY).matches
      ? MOBILE_VIDEOS_PAGE_SIZE
      : DESKTOP_VIDEOS_PAGE_SIZE
  );

  useEffect(() => {
    const media = window.matchMedia(VIDEOS_MOBILE_QUERY);
    const update = () => {
      setPageSize(media.matches ? MOBILE_VIDEOS_PAGE_SIZE : DESKTOP_VIDEOS_PAGE_SIZE);
    };
    update();
    media.addEventListener("change", update);
    return () => media.removeEventListener("change", update);
  }, []);

  return pageSize;
}

function EditVideoModal({
  video,
  availableTags,
  onClose,
  onSaved,
}: {
  video: api.AdminVideo;
  availableTags: api.AdminTag[];
  onClose: () => void;
  onSaved: () => void;
}) {
  const idPrefix = useId();
  const [title, setTitle] = useState(video.title);
  const [author, setAuthor] = useState(video.author ?? "");
  const [selectedTags, setSelectedTags] = useState(video.tags ?? []);
  const [category, setCategory] = useState(video.category ?? "");
  const [badges, setBadges] = useState((video.badges ?? []).join(", "));
  const [description, setDescription] = useState(video.description ?? "");
  const [thumbnail, setThumbnail] = useState(video.thumbnailUrl ?? "");
  const [quality, setQuality] = useState(video.quality ?? "");
  const [durationSec, setDurationSec] = useState(String(video.durationSeconds || 0));
  const [saving, setSaving] = useState(false);
  const { show } = useToast();

  async function handleSave() {
    setSaving(true);
    try {
      await api.updateVideo(video.id, {
        title: title.trim(),
        author: author.trim(),
        tags: selectedTags,
        category: category.trim(),
        badges: splitList(badges),
        description,
        thumbnail: thumbnail.trim(),
        quality: quality.trim(),
        durationSeconds: Number(durationSec) || 0,
      });
      show("已保存", "success");
      onSaved();
    } catch (e) {
      show(e instanceof Error ? e.message : "保存失败", "error");
    } finally {
      setSaving(false);
    }
  }

  return (
    <Modal
      open
      title={`编辑视频 · ${video.title}`}
      onClose={onClose}
      footer={
        <>
          <button type="button" className="admin-btn" onClick={onClose}>
            取消
          </button>
          <button type="button" className="admin-btn is-primary" onClick={handleSave} disabled={saving}>
            {saving ? "保存中..." : "保存"}
          </button>
        </>
      }
    >
      <div className="admin-form">
        <div className="admin-form__row">
          <label htmlFor={`${idPrefix}-video-title`}>标题</label>
          <input id={`${idPrefix}-video-title`} value={title} onChange={(e) => setTitle(e.target.value)} />
        </div>
        <div className="admin-form__row">
          <label htmlFor={`${idPrefix}-video-author`}>作者</label>
          <input id={`${idPrefix}-video-author`} value={author} onChange={(e) => setAuthor(e.target.value)} />
        </div>
        <div className="admin-form__row">
          <div className="admin-form__label">标签</div>
          <div className="admin-tag-picker">
            {availableTags.map((tag) => (
              <label key={tag.id} className="admin-check">
                <input
                  type="checkbox"
                  checked={selectedTags.includes(tag.label)}
                  onChange={() => setSelectedTags(toggleTag(selectedTags, tag.label))}
                />
                <span>{tag.label}</span>
                <em>{tag.count}</em>
              </label>
            ))}
          </div>
        </div>
        <div className="admin-form__row">
          <label htmlFor={`${idPrefix}-video-category`}>分类</label>
          <input id={`${idPrefix}-video-category`} value={category} onChange={(e) => setCategory(e.target.value)} />
        </div>
        <div className="admin-form__row">
          <label htmlFor={`${idPrefix}-video-badges`}>徽标（逗号分隔，例如 精选, 原创）</label>
          <input id={`${idPrefix}-video-badges`} value={badges} onChange={(e) => setBadges(e.target.value)} />
        </div>
        <div className="admin-form__row">
          <label htmlFor={`${idPrefix}-video-quality`}>质量</label>
          <select id={`${idPrefix}-video-quality`} value={quality} onChange={(e) => setQuality(e.target.value)}>
            <option value="">未设置</option>
            <option value="HD">HD</option>
            <option value="SD">SD</option>
          </select>
        </div>
        <div className="admin-form__row">
          <label htmlFor={`${idPrefix}-video-duration`}>时长（秒）</label>
          <input
            id={`${idPrefix}-video-duration`}
            value={durationSec}
            onChange={(e) => setDurationSec(e.target.value)}
            inputMode="numeric"
          />
        </div>
        <div className="admin-form__row">
          <label htmlFor={`${idPrefix}-video-thumbnail`}>封面 URL</label>
          <div className="admin-thumbnail-preview">
            <input id={`${idPrefix}-video-thumbnail`} value={thumbnail} onChange={(e) => setThumbnail(e.target.value)} />
            {thumbnail && (
              <img 
                src={thumbnail} 
                alt="封面预览" 
                className="admin-thumbnail-img" 
                onError={(e) => (e.currentTarget.style.display = 'none')} 
                onLoad={(e) => (e.currentTarget.style.display = 'block')} 
              />
            )}
          </div>
        </div>
        <div className="admin-form__row">
          <label htmlFor={`${idPrefix}-video-description`}>描述</label>
          <textarea
            id={`${idPrefix}-video-description`}
            value={description}
            onChange={(e) => setDescription(e.target.value)}
          />
        </div>
        <dl className="admin-kv" style={{ marginTop: 8 }}>
          <dt>来源盘</dt>
          <dd>{video.driveId}</dd>
          <dt>文件信息</dt>
          <dd>{fileMeta(video) || "—"}</dd>
          <dt>预览视频</dt>
          <dd>
            <PreviewStatus s={video.previewStatus} />
          </dd>
        </dl>
        <details className="admin-form__help" style={{ marginTop: 8 }}>
          <summary>技术信息（排查用）</summary>
          <dl className="admin-kv" style={{ marginTop: 8 }}>
            <dt>内部视频 ID</dt>
            <dd style={{ fontFamily: "ui-monospace", fontSize: 12 }}>{video.id}</dd>
            <dt>网盘文件 ID</dt>
            <dd style={{ fontFamily: "ui-monospace", fontSize: 12 }}>{video.fileId}</dd>
          </dl>
        </details>
      </div>
    </Modal>
  );
}

function fileMeta(v: api.AdminVideo): string {
  return fileMetaParts(v).join(" · ");
}

function fileMetaParts(v: api.AdminVideo): string[] {
  return [
    normalizeExt(v.ext),
    v.quality,
    v.size > 0 ? formatBytes(v.size) : "",
  ].filter(Boolean);
}

function normalizeExt(ext: string): string {
  const value = (ext ?? "").replace(/^\./, "").trim();
  return value ? value.toUpperCase() : "";
}

function splitList(s: string): string[] {
  return s
    .split(/[,，、\s]+/)
    .map((x) => x.trim())
    .filter(Boolean);
}

function toggleTag(tags: string[], label: string): string[] {
  return tags.includes(label)
    ? tags.filter((tag) => tag !== label)
    : [...tags, label];
}
