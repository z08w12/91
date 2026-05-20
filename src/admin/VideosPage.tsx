import { useEffect, useState } from "react";
import { Edit, RefreshCw, Search } from "lucide-react";
import * as api from "./api";
import { useToast } from "./ToastContext";
import { Modal } from "./Modal";

const PAGE_SIZE = 100;

export function VideosPage() {
  const [list, setList] = useState<api.AdminVideo[]>([]);
  const [drives, setDrives] = useState<api.AdminDrive[]>([]);
  const [loading, setLoading] = useState(true);
  const [keyword, setKeyword] = useState("");
  const [driveId, setDriveId] = useState("");
  const [page, setPage] = useState(1);
  const [total, setTotal] = useState(0);
  const [editing, setEditing] = useState<api.AdminVideo | null>(null);
  const [availableTags, setAvailableTags] = useState<api.AdminTag[]>([]);
  const { show } = useToast();

  async function refresh() {
    setLoading(true);
    try {
      const [r, tagList, driveList] = await Promise.all([
        api.listVideos({ driveId, page, size: PAGE_SIZE }),
        api.listTags(),
        api.listDrives(),
      ]);
      setList(r.items ?? []);
      setTotal(r.total ?? 0);
      setAvailableTags(tagList);
      setDrives(driveList ?? []);
    } catch (e) {
      show(e instanceof Error ? e.message : "加载失败", "error");
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    refresh();
  }, [driveId, page]);

  const driveNameMap = new Map(
    drives.map((d) => [d.id, d.name || d.id])
  );

  const filtered = keyword.trim()
    ? list.filter((v) => {
        const k = keyword.toLowerCase();
        return (
          v.title.toLowerCase().includes(k) ||
          (v.author ?? "").toLowerCase().includes(k)
        );
      })
    : list;
  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE));
  const pageStart = total === 0 ? 0 : (page - 1) * PAGE_SIZE + 1;
  const pageEnd = Math.min(total, page * PAGE_SIZE);

  async function handleRegen(v: api.AdminVideo) {
    try {
      await api.regenPreview(v.id);
      show("已触发 teaser 重生", "success");
    } catch (e) {
      show(e instanceof Error ? e.message : "触发失败", "error");
    }
  }

  return (
    <section>
      <header className="admin-page__header">
        <h1 className="admin-page__title">视频管理</h1>
        <div className="admin-page__actions admin-videos-filter">
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
          <div className="admin-videos-filter__search">
            <Search size={14} className="admin-videos-filter__search-icon" />
            <input
              value={keyword}
              onChange={(e) => setKeyword(e.target.value)}
              placeholder="搜索标题 / 作者"
            />
          </div>
          <button className="admin-btn" onClick={refresh}>
            <RefreshCw size={13} /> 刷新
          </button>
        </div>
      </header>

      {drives.length > 0 && (
        <div className="admin-drive-teasers" aria-label="网盘 Teaser 统计">
          {drives.map((d) => (
            <button
              key={d.id}
              type="button"
              className={`admin-drive-teaser${
                driveId === d.id ? " is-active" : ""
              }`}
              onClick={() => {
                setDriveId(d.id);
                setPage(1);
              }}
            >
              <span className="admin-drive-teaser__name">{d.name || d.id}</span>
              <span className="admin-drive-teaser__metric is-ready">
                已生成 {d.teaserReadyCount ?? 0}
              </span>
              <span className="admin-drive-teaser__metric is-pending">
                待生成 {d.teaserPendingCount ?? 0}
              </span>
              {(d.teaserFailedCount ?? 0) > 0 && (
                <span className="admin-drive-teaser__metric is-failed">
                  失败 {d.teaserFailedCount}
                </span>
              )}
            </button>
          ))}
        </div>
      )}

      {!loading && (
        <div className="admin-videos-summary">
          {driveId
            ? `${driveNameMap.get(driveId) ?? driveId}：共 ${total} 个视频，第 ${page} / ${totalPages} 页，显示 ${pageStart}-${pageEnd}`
            : `全部网盘：共 ${total} 个视频，第 ${page} / ${totalPages} 页，显示 ${pageStart}-${pageEnd}`}
        </div>
      )}

      {loading ? (
        <div className="admin-empty">加载中...</div>
      ) : filtered.length === 0 ? (
        <div className="admin-card admin-empty">
          {driveId
            ? "这个网盘下还没有可显示的视频。可以在「网盘管理」里触发重扫。"
            : "还没有视频。先在「网盘管理」里配置好盘并触发扫描。"}
        </div>
      ) : (
        <>
          <table className="admin-table">
            <thead>
              <tr>
                <th>标题</th>
                <th>作者</th>
                <th>标签</th>
                <th>时长</th>
                <th>Teaser</th>
                <th>来源</th>
                <th className="is-actions">操作</th>
              </tr>
            </thead>
            <tbody>
              {filtered.map((v) => (
                <tr key={v.id}>
                  <td>
                    <div className="admin-video-title">{v.title}</div>
                    {fileMeta(v) && (
                      <div className="admin-video-filemeta">
                        {fileMeta(v)}
                      </div>
                    )}
                  </td>
                  <td>{v.author || <span className="admin-text-faint">—</span>}</td>
                  <td>
                    <div className="admin-pills">
                      {(v.tags ?? []).map((t) => (
                        <span key={t} className="admin-pill">
                          {t}
                        </span>
                      ))}
                    </div>
                  </td>
                  <td>{formatDur(v.durationSeconds)}</td>
                  <td>
                    <PreviewStatus s={v.previewStatus} />
                  </td>
                  <td className="admin-mono-cell">
                    {driveNameMap.get(v.driveId) ?? v.driveId}
                  </td>
                  <td className="is-actions">
                    <button className="admin-btn" onClick={() => setEditing(v)}>
                      <Edit size={13} /> 编辑
                    </button>{" "}
                    <button className="admin-btn" onClick={() => handleRegen(v)}>
                      <RefreshCw size={13} /> 重生 teaser
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          <div className="admin-table-pagination">
            <button
              className="admin-btn"
              onClick={() => setPage(1)}
              disabled={page <= 1}
            >
              首页
            </button>
            <button
              className="admin-btn"
              onClick={() => setPage((p) => Math.max(1, p - 1))}
              disabled={page <= 1}
            >
              上一页
            </button>
            <span className="admin-table-pagination__info">
              第 {page} / {totalPages} 页，每页 {PAGE_SIZE} 个
            </span>
            <button
              className="admin-btn"
              onClick={() => setPage((p) => Math.min(totalPages, p + 1))}
              disabled={page >= totalPages}
            >
              下一页
            </button>
            <button
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
    </section>
  );
}

function PreviewStatus({ s }: { s: string }) {
  if (s === "ready") return <span className="admin-status is-ok">就绪</span>;
  if (s === "failed") return <span className="admin-status is-error">失败</span>;
  if (s === "skipped") return <span className="admin-status">跳过</span>;
  return <span className="admin-status is-pending">待生成</span>;
}

function formatDur(sec: number): string {
  if (!sec) return "—";
  const m = Math.floor(sec / 60);
  const s = sec % 60;
  return `${m.toString().padStart(2, "0")}:${s.toString().padStart(2, "0")}`;
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
          <button className="admin-btn" onClick={onClose}>
            取消
          </button>
          <button className="admin-btn is-primary" onClick={handleSave} disabled={saving}>
            {saving ? "保存中..." : "保存"}
          </button>
        </>
      }
    >
      <div className="admin-form">
        <div className="admin-form__row">
          <label>标题</label>
          <input value={title} onChange={(e) => setTitle(e.target.value)} />
        </div>
        <div className="admin-form__row">
          <label>作者</label>
          <input value={author} onChange={(e) => setAuthor(e.target.value)} />
        </div>
        <div className="admin-form__row">
          <label>标签</label>
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
          <label>分类</label>
          <input value={category} onChange={(e) => setCategory(e.target.value)} />
        </div>
        <div className="admin-form__row">
          <label>徽标（逗号分隔，例如 精选, 原创）</label>
          <input value={badges} onChange={(e) => setBadges(e.target.value)} />
        </div>
        <div className="admin-form__row">
          <label>质量</label>
          <select value={quality} onChange={(e) => setQuality(e.target.value)}>
            <option value="">未设置</option>
            <option value="HD">HD</option>
            <option value="SD">SD</option>
          </select>
        </div>
        <div className="admin-form__row">
          <label>时长（秒）</label>
          <input
            value={durationSec}
            onChange={(e) => setDurationSec(e.target.value)}
            inputMode="numeric"
          />
        </div>
        <div className="admin-form__row">
          <label>封面 URL</label>
          <input value={thumbnail} onChange={(e) => setThumbnail(e.target.value)} />
        </div>
        <div className="admin-form__row">
          <label>描述</label>
          <textarea
            value={description}
            onChange={(e) => setDescription(e.target.value)}
          />
        </div>
        <dl className="admin-kv" style={{ marginTop: 8 }}>
          <dt>来源盘</dt>
          <dd>{video.driveId}</dd>
          <dt>文件信息</dt>
          <dd>{fileMeta(video) || "—"}</dd>
          <dt>Teaser</dt>
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
  const parts = [
    normalizeExt(v.ext),
    v.quality,
    formatBytes(v.size),
  ].filter(Boolean);
  return parts.join(" · ");
}

function normalizeExt(ext: string): string {
  const value = (ext ?? "").replace(/^\./, "").trim();
  return value ? value.toUpperCase() : "";
}

function formatBytes(size: number): string {
  if (!size || size <= 0) return "";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let value = size;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit += 1;
  }
  const digits = unit === 0 || value >= 100 ? 0 : 1;
  return `${value.toFixed(digits)} ${units[unit]}`;
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
