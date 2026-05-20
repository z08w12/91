import { useEffect, useState } from "react";
import { Plus, RefreshCw, Tags } from "lucide-react";
import * as api from "./api";
import { useToast } from "./ToastContext";

export function TagsPage() {
  const [tags, setTags] = useState<api.AdminTag[]>([]);
  const [label, setLabel] = useState("");
  const [aliases, setAliases] = useState("");
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const { show } = useToast();

  async function refresh() {
    setLoading(true);
    try {
      setTags(await api.listTags());
    } catch (e) {
      show(e instanceof Error ? e.message : "加载标签失败", "error");
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    refresh();
  }, []);

  async function handleCreate() {
    const cleanLabel = label.trim();
    if (!cleanLabel) return;
    setSaving(true);
    try {
      const r = await api.createTag(cleanLabel, splitList(aliases));
      show(`已添加标签，自动归类 ${r.classified} 个视频`, "success");
      setLabel("");
      setAliases("");
      await refresh();
    } catch (e) {
      show(e instanceof Error ? e.message : "添加标签失败", "error");
    } finally {
      setSaving(false);
    }
  }

  return (
    <section>
      <header className="admin-page__header">
        <h1 className="admin-page__title">标签管理</h1>
        <button className="admin-btn" onClick={refresh}>
          <RefreshCw size={13} /> 刷新
        </button>
      </header>

      <div className="admin-card">
        <div className="admin-card__title">
          <Tags size={15} /> 新增标签
        </div>
        <div className="admin-form">
          <div className="admin-form__row">
            <label>标签名</label>
            <input
              value={label}
              onChange={(e) => setLabel(e.target.value)}
              placeholder="例如：清纯"
            />
          </div>
          <div className="admin-form__row">
            <label>别名</label>
            <input
              value={aliases}
              onChange={(e) => setAliases(e.target.value)}
              placeholder="逗号分隔，例如：纯欲, 清新, 乖巧"
            />
            <div className="admin-form__help">
              新增后会按标签名和别名匹配已有视频的标题、作者和目录。
            </div>
          </div>
          <button
            className="admin-btn is-primary"
            onClick={handleCreate}
            disabled={saving || !label.trim()}
          >
            <Plus size={13} /> {saving ? "添加中..." : "添加并归类"}
          </button>
        </div>
      </div>

      {loading ? (
        <div className="admin-empty">加载中...</div>
      ) : (
        <table className="admin-table">
          <thead>
            <tr>
              <th>标签</th>
              <th>视频数</th>
              <th>来源</th>
              <th>别名</th>
            </tr>
          </thead>
          <tbody>
            {tags.map((tag) => (
              <tr key={tag.id}>
                <td>
                  <span className="admin-pill">{tag.label}</span>
                </td>
                <td>{tag.count}</td>
                <td>{sourceLabel(tag.source)}</td>
                <td>
                  {(tag.aliases ?? []).length > 0 ? (
                    <div className="admin-pills">
                      {(tag.aliases ?? []).map((alias) => (
                        <span key={alias} className="admin-pill">
                          {alias}
                        </span>
                      ))}
                    </div>
                  ) : (
                    <span className="admin-text-faint">—</span>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </section>
  );
}

function splitList(s: string): string[] {
  return s
    .split(/[,，、\s]+/)
    .map((x) => x.trim())
    .filter(Boolean);
}

function sourceLabel(source: string): string {
  if (source === "system") return "系统";
  if (source === "collection") return "合集";
  if (source === "legacy") return "旧数据";
  return "用户";
}
