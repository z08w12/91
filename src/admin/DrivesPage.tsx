import { useEffect, useMemo, useState } from "react";
import { Download, Plus, Power, PowerOff, RefreshCw, RotateCcw, Trash2 } from "lucide-react";
import * as api from "./api";
import { useToast } from "./ToastContext";
import { Modal } from "./Modal";
import { formatBytes } from "./storageFormat";

const kindLabel: Record<string, string> = {
  quark: "夸克网盘",
  p115: "115 网盘",
  pikpak: "PikPak",
  wopan: "联通沃盘",
  onedrive: "OneDrive",
  spider91: "91 爬虫",
};

type Kind = api.AdminDrive["kind"];

type FormState = {
  id: string;
  kind: Kind;
  name: string;
  rootId: string;
  scanRootId: string;
  creds: Record<string, string>;
  /**
   * spider91 专用字段：把视频迁移到云盘的目标 drive ID。
   * 实际值不会和 creds 一起 POST 到 /admin/api/drives，而是在 handleSave 里
   * 单独通过 PUT /admin/api/settings 写到全局 setting。在 form state 里维护它
   * 是为了让 DriveForm 能读写同一份编辑状态。
   *
   * 空字符串 = 自动模式（系统中唯一的 pikpak/p115 drive）。
   */
  spider91UploadDriveId: string;
};

const emptyForm: FormState = {
  id: "",
  kind: "quark",
  name: "",
  rootId: "0",
  scanRootId: "0",
  creds: {},
  spider91UploadDriveId: "",
};

export function DrivesPage() {
  const [list, setList] = useState<api.AdminDrive[]>([]);
  const [storage, setStorage] = useState<api.AdminDriveStorage | null>(null);
  const [settings, setSettings] = useState<api.Settings | null>(null);
  const [loading, setLoading] = useState(true);
  const [modalOpen, setModalOpen] = useState(false);
  const [form, setForm] = useState<FormState>(emptyForm);
  const [saving, setSaving] = useState(false);
  const [regenFailedId, setRegenFailedId] = useState("");
  // togglingTeaserId 在请求未返回前禁用按钮，避免连点导致两次切换互相覆盖。
  const [togglingTeaserId, setTogglingTeaserId] = useState("");
  const { show } = useToast();

  // 当前系统中可作为 spider91 上传目标的 drive 列表（pikpak ∪ p115）。
  // 用户保存 spider91 drive 时从这里挑一个；空表示走"自动"模式。
  const uploadTargets = useMemo(
    () => list.filter((d) => d.kind === "pikpak" || d.kind === "p115"),
    [list]
  );

  async function refresh() {
    setLoading(true);
    try {
      const [data, storageData, settingsData] = await Promise.all([
        api.listDrives(),
        api.getDriveStorage(),
        api.getSettings().catch(() => null),
      ]);
      setList(data ?? []);
      setStorage(storageData);
      if (settingsData) setSettings(settingsData);
    } catch (e) {
      show(e instanceof Error ? e.message : "加载失败", "error");
    } finally {
      setLoading(false);
    }
  }

  async function refreshDriveList() {
    try {
      const data = await api.listDrives();
      setList(data ?? []);
    } catch {
      // 保持当前页面状态，下一次轮询或手动操作再刷新。
    }
  }

  useEffect(() => {
    refresh();
    const timer = window.setInterval(() => {
      refreshDriveList();
    }, 5000);
    return () => window.clearInterval(timer);
  }, []);

  function openCreate() {
    // 创建时把全局 setting 当前值带进表单，方便用户在新建第一个 spider91 drive 时
    // 直接看到当前的上传目标选择（一般是空 = 自动）。
    setForm({
      ...emptyForm,
      spider91UploadDriveId: settings?.spider91UploadDriveId ?? "",
    });
    setModalOpen(true);
  }

  function openEdit(d: api.AdminDrive) {
    setForm({
      id: d.id,
      kind: d.kind,
      name: d.name,
      rootId: d.rootId,
      scanRootId: d.scanRootId || d.rootId,
      creds: {},
      spider91UploadDriveId: settings?.spider91UploadDriveId ?? "",
    });
    setModalOpen(true);
  }

  async function handleSave() {
    if (!form.id || !form.kind) {
      show("请填 ID 和类型", "error");
      return;
    }
    // 若编辑且没有提供凭证，提示一下但仍允许保存（不改凭证）
    setSaving(true);
    try {
      const resp = await api.upsertDrive({
        id: form.id,
        kind: form.kind,
        name: form.name || form.id,
        rootId: form.rootId || defaultRootId(form.kind),
        scanRootId: form.scanRootId || form.rootId || defaultRootId(form.kind),
        credentials: form.creds,
      });

      // 仅当编辑/新建的是 spider91 drive 时，才同步全局上传目标 setting。
      // 避免动其它类型 drive 的表单顺手覆盖了这个独立设置。
      if (form.kind === "spider91" && form.spider91UploadDriveId !== (settings?.spider91UploadDriveId ?? "")) {
        try {
          const updated = await api.updateSettings({
            spider91UploadDriveId: form.spider91UploadDriveId,
          });
          setSettings(updated);
        } catch (settingsErr) {
          // 不阻断主流程：drive 已经存了，setting 没存上，由 toast 提示用户手动重试
          show(
            settingsErr instanceof Error
              ? `Drive 已保存，但上传目标设置失败：${settingsErr.message}`
              : "上传目标设置失败",
            "error"
          );
          setModalOpen(false);
          refresh();
          return;
        }
      }

      if (resp.warning) {
        show(`已保存，但 driver 初始化失败：${resp.warning}`, "error");
      } else {
        show("已保存", "success");
      }
      setModalOpen(false);
      refresh();
    } catch (e) {
      show(e instanceof Error ? e.message : "保存失败", "error");
    } finally {
      setSaving(false);
    }
  }

  async function handleDelete(d: api.AdminDrive) {
    if (!window.confirm(`确定删除 ${d.name || d.id}？\n这会移除盘配置，但不会删除其中的视频元数据。`)) return;
    try {
      await api.deleteDrive(d.id);
      show("已删除", "success");
      refresh();
    } catch (e) {
      show(e instanceof Error ? e.message : "删除失败", "error");
    }
  }

  async function handleRescan(d: api.AdminDrive) {
    try {
      await api.rescan(d.id);
      if (d.kind === "spider91") {
        show("已触发抓取任务，需要 2-4 分钟，可稍后刷新视频列表查看", "success");
      } else {
        show("已触发扫描，可稍后刷新视频列表查看", "success");
      }
    } catch (e) {
      show(e instanceof Error ? e.message : "触发失败", "error");
    }
  }

  async function handleRegenFailed(d: api.AdminDrive) {
    setRegenFailedId(d.id);
    try {
      await api.regenFailedPreviews(d.id);
      show("已触发失败 teaser 重新生成", "success");
      refresh();
    } catch (e) {
      show(e instanceof Error ? e.message : "触发失败", "error");
    } finally {
      setRegenFailedId("");
    }
  }

  async function handleToggleTeaser(d: api.AdminDrive) {
    const next = !d.teaserEnabled;
    setTogglingTeaserId(d.id);
    // 乐观更新本地状态，操作流畅；失败再回滚。
    setList((prev) =>
      prev.map((item) =>
        item.id === d.id ? { ...item, teaserEnabled: next } : item
      )
    );
    try {
      const resp = await api.setDriveTeaserEnabled(d.id, next);
      show(
        resp.teaserEnabled
          ? `已开启「${d.name || d.id}」的 Teaser 生成`
          : `已关闭「${d.name || d.id}」的 Teaser 生成`,
        "success"
      );
      // 以服务端响应为准（防止极端竞态）；并刷新计数等附属数据。
      setList((prev) =>
        prev.map((item) =>
          item.id === d.id ? { ...item, teaserEnabled: resp.teaserEnabled } : item
        )
      );
      refreshDriveList();
    } catch (e) {
      // 回滚乐观更新
      setList((prev) =>
        prev.map((item) =>
          item.id === d.id ? { ...item, teaserEnabled: d.teaserEnabled } : item
        )
      );
      show(e instanceof Error ? e.message : "切换失败", "error");
    } finally {
      setTogglingTeaserId("");
    }
  }

  return (
    <section>
      <header className="admin-page__header">
        <h1 className="admin-page__title">网盘管理</h1>
        <button className="admin-btn is-primary" onClick={openCreate}>
          <Plus size={14} /> 新建
        </button>
      </header>

      {storage && <StorageSummary storage={storage} />}

      {loading ? (
        <div className="admin-empty">加载中...</div>
      ) : list.length === 0 ? (
        <div className="admin-card admin-empty">
          还没有配置任何网盘。点击右上角「新建」，选择夸克 / 115 / PikPak / 沃盘 / OneDrive，填入凭证即可。
        </div>
      ) : (
        <table className="admin-table admin-drives-table">
          <thead>
            <tr>
              <th>名称</th>
              <th>类型</th>
              <th>ID</th>
              <th>状态</th>
              <th>生成状态</th>
              <th>扫描根</th>
              <th>本地占用</th>
              <th>封面</th>
              <th>Teaser</th>
              <th className="is-actions">操作</th>
            </tr>
          </thead>
          <tbody>
            {list.map((d) => (
              <tr key={d.id}>
                <td data-label="名称">{d.name || <span className="admin-text-faint">（未命名）</span>}</td>
                <td data-label="类型">{kindLabel[d.kind] ?? d.kind}</td>
                <td data-label="ID" className="admin-mono-cell">{d.id}</td>
                <td data-label="状态">
                  <StatusTag kind={d.kind} status={d.status} error={d.lastError} hasCred={d.hasCredential} />
                </td>
                <td data-label="生成状态">
                  <GenerationStatusCell drive={d} />
                </td>
                <td data-label="扫描根" className="admin-mono-cell">
                  {d.kind === "spider91" ? (
                    <span className="admin-text-faint">
                      {d.lastCrawlAt
                        ? `上次抓取 ${formatRelativeTime(d.lastCrawlAt)}`
                        : "尚未抓取"}
                    </span>
                  ) : (
                    d.scanRootId || d.rootId
                  )}
                </td>
                <td data-label="本地占用">
                  <StorageCell usage={storage?.drives[d.id]} />
                </td>
                <td data-label="封面">
                  <GenerationCounts
                    ready={d.thumbnailReadyCount}
                    pending={d.thumbnailPendingCount}
                    failed={d.thumbnailFailedCount}
                  />
                </td>
                <td data-label="Teaser">
                  <GenerationCounts
                    ready={d.teaserReadyCount}
                    pending={d.teaserPendingCount}
                    failed={d.teaserFailedCount}
                  />
                </td>
                <td className="is-actions" data-label="操作">
                  <button className="admin-btn" onClick={() => handleRescan(d)}>
                    {d.kind === "spider91" ? (
                      <>
                        <Download size={13} /> 立即抓取
                      </>
                    ) : (
                      <>
                        <RefreshCw size={13} /> 重扫
                      </>
                    )}
                  </button>{" "}
                  <button
                    className={`admin-btn ${d.teaserEnabled ? "is-success" : ""}`}
                    onClick={() => handleToggleTeaser(d)}
                    disabled={togglingTeaserId === d.id}
                    title={
                      d.teaserEnabled
                        ? "本盘 Teaser 生成已开启，点击关闭"
                        : "本盘 Teaser 生成已关闭，点击开启"
                    }
                  >
                    {d.teaserEnabled ? <Power size={13} /> : <PowerOff size={13} />}
                    <span className="admin-btn__label">
                      {togglingTeaserId === d.id
                        ? "切换中..."
                        : d.teaserEnabled
                        ? "Teaser: 开"
                        : "Teaser: 关"}
                    </span>
                  </button>{" "}
                  <button
                    className="admin-btn"
                    disabled={(d.teaserFailedCount ?? 0) <= 0 || regenFailedId === d.id}
                    onClick={() => handleRegenFailed(d)}
                  >
                    <RotateCcw size={13} />
                    <span className="admin-btn__label">
                      {regenFailedId === d.id ? "触发中..." : "重试失败 Teaser"}
                    </span>
                  </button>{" "}
                  <button className="admin-btn" onClick={() => openEdit(d)}>
                    编辑
                  </button>{" "}
                  <button className="admin-btn is-danger" onClick={() => handleDelete(d)}>
                    <Trash2 size={13} />
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      <Modal
        open={modalOpen}
        title={form.id && list.find((x) => x.id === form.id) ? "编辑网盘" : "新建网盘"}
        onClose={() => setModalOpen(false)}
        footer={
          <>
            <button className="admin-btn" onClick={() => setModalOpen(false)}>
              取消
            </button>
            <button
              className="admin-btn is-primary"
              onClick={handleSave}
              disabled={saving}
            >
              {saving ? "保存中..." : "保存"}
            </button>
          </>
        }
      >
        <DriveForm
          form={form}
          onChange={setForm}
          isEdit={!!list.find((x) => x.id === form.id)}
          uploadTargets={uploadTargets}
        />
      </Modal>
    </section>
  );
}

function StorageSummary({ storage }: { storage: api.AdminDriveStorage }) {
  return (
    <section className="admin-card admin-storage-summary" aria-label="本地媒体存储">
      <div className="admin-storage-summary__metric">
        <span>封面占用</span>
        <strong>{formatBytes(storage.thumbnailBytes)}</strong>
      </div>
      <div className="admin-storage-summary__metric">
        <span>Teaser 占用</span>
        <strong>{formatBytes(storage.teaserBytes)}</strong>
      </div>
      <div className="admin-storage-summary__metric">
        <span>本地媒体合计</span>
        <strong>{formatBytes(storage.totalBytes)}</strong>
      </div>
      <div className="admin-storage-summary__metric">
        <span>磁盘可用</span>
        <strong>{formatBytes(storage.availableBytes)}</strong>
      </div>
    </section>
  );
}

function StorageCell({ usage }: { usage?: api.DriveStorageUsage }) {
  if (!usage || usage.totalBytes <= 0) {
    return <span className="admin-storage-cell__empty">0 B</span>;
  }
  return (
    <div className="admin-storage-cell">
      <strong>{formatBytes(usage.totalBytes)}</strong>
      <span>封面 {formatBytes(usage.thumbnailBytes)}</span>
      <span>Teaser {formatBytes(usage.teaserBytes)}</span>
    </div>
  );
}

function GenerationCounts({
  ready,
  pending,
  failed,
}: {
  ready?: number;
  pending?: number;
  failed?: number;
}) {
  return (
    <div className="admin-generation-counts">
      <span className="admin-drive-teaser__metric is-ready">
        就绪 {ready ?? 0}
      </span>
      <span className="admin-drive-teaser__metric is-pending">
        待生成 {pending ?? 0}
      </span>
      <span className="admin-drive-teaser__metric is-failed">
        失败 {failed ?? 0}
      </span>
    </div>
  );
}

function GenerationStatusCell({ drive }: { drive: api.AdminDrive }) {
  return (
    <div className="admin-generation-statuses">
      <GenerationStatusLine label="封面" status={drive.thumbnailGenerationStatus} />
      <GenerationStatusLine label="预览" status={drive.previewGenerationStatus} />
    </div>
  );
}

function GenerationStatusLine({
  label,
  status,
}: {
  label: string;
  status?: api.DriveGenerationStatus;
}) {
  const state = status?.state || "idle";
  const queueLength = status?.queueLength ?? 0;
  const detail = generationDetail(status);
  const title = generationTitle(status, detail);
  const countText = queueLength > 0 ? `${label === "封面" ? "剩余" : "队列"} ${queueLength}` : "";

  return (
    <div className="admin-generation-row" title={title}>
      <span className="admin-generation-kind">{label}</span>
      <span className={`admin-status admin-generation-state is-${generationStateClass(state)}`}>
        {generationStateLabel(state)}
      </span>
      {(detail || queueLength > 0) && (
        <span className="admin-generation-detail">
          {[detail, countText].filter(Boolean).join(" / ")}
        </span>
      )}
    </div>
  );
}

function generationStateLabel(state: string): string {
  if (state === "generating") return "生成中";
  if (state === "cooling") return "冷却中";
  if (state === "queued") return "排队中";
  return "空闲";
}

function generationStateClass(state: string): string {
  if (state === "generating" || state === "cooling" || state === "queued") {
    return state;
  }
  return "idle";
}

function generationDetail(status?: api.DriveGenerationStatus): string {
  if (!status) return "";
  if (status.state === "cooling" && status.cooldownUntil) {
    return `剩余 ${formatCooldownRemaining(status.cooldownUntil)}`;
  }
  if (status.currentTitle) {
    return status.currentTitle;
  }
  return "";
}

function generationTitle(status: api.DriveGenerationStatus | undefined, detail: string): string | undefined {
  if (!status) return detail || undefined;
  if (status.state === "cooling" && status.cooldownUntil) {
    return `冷却至 ${formatClock(status.cooldownUntil)}`;
  }
  return status.currentTitle || detail || undefined;
}

function formatCooldownRemaining(value: string): string {
  const d = new Date(value);
  if (Number.isNaN(d.getTime())) return value;
  const totalSeconds = Math.max(0, Math.ceil((d.getTime() - Date.now()) / 1000));
  const hours = Math.floor(totalSeconds / 3600);
  const minutes = Math.floor((totalSeconds % 3600) / 60);
  const seconds = totalSeconds % 60;
  if (hours > 0) return `${hours}小时${minutes}分`;
  if (minutes > 0) return `${minutes}分${seconds}秒`;
  return `${seconds}秒`;
}

function formatClock(value: string): string {
  const d = new Date(value);
  if (Number.isNaN(d.getTime())) return value;
  return d.toLocaleTimeString("zh-CN", { hour: "2-digit", minute: "2-digit" });
}

function StatusTag({
  kind,
  status,
  error,
  hasCred,
}: {
  kind: string;
  status: string;
  error?: string;
  hasCred: boolean;
}) {
  // spider91 没有用户凭证概念，直接看 status；保存后默认就是 "ok"
  if (kind !== "spider91" && !hasCred) {
    return <span className="admin-status is-pending">未配置凭证</span>;
  }
  if (status === "ok") {
    if (kind === "spider91") {
      return <span className="admin-status is-ok">已就绪</span>;
    }
    return <span className="admin-status is-ok">已连接</span>;
  }
  if (status === "error")
    return (
      <span className="admin-status is-error" title={error}>
        错误
      </span>
    );
  return <span className="admin-status">{status || "未连接"}</span>;
}

function DriveForm({
  form,
  onChange,
  isEdit,
  uploadTargets,
}: {
  form: FormState;
  onChange: (f: FormState) => void;
  isEdit: boolean;
  uploadTargets: api.AdminDrive[];
}) {
  const fields = useMemo(() => credentialFields(form.kind), [form.kind]);

  function set<K extends keyof FormState>(k: K, v: FormState[K]) {
    onChange({ ...form, [k]: v });
  }
  function setCred(k: string, v: string) {
    onChange({ ...form, creds: { ...form.creds, [k]: v } });
  }
  function setKind(v: Kind) {
    onChange({
      ...form,
      kind: v,
      rootId: defaultRootId(v),
      scanRootId: defaultRootId(v),
      creds: {},
    });
  }

  return (
    <div className="admin-form">
      <div className="admin-form__row">
        <label>ID（英文，唯一）</label>
        <input
          value={form.id}
          onChange={(e) => set("id", e.target.value)}
          placeholder="例如 my-quark"
          disabled={isEdit}
        />
        {isEdit && <div className="admin-form__help">已创建的盘 ID 不能修改</div>}
      </div>
      <div className="admin-form__row">
        <label>名称</label>
        <input
          value={form.name}
          onChange={(e) => set("name", e.target.value)}
          placeholder="给这个盘起个名字"
        />
      </div>
      <div className="admin-form__row">
        <label>类型</label>
        <select
          value={form.kind}
          onChange={(e) => setKind(e.target.value as Kind)}
          disabled={isEdit}
        >
          <option value="quark">夸克网盘</option>
          <option value="p115">115 网盘</option>
          <option value="pikpak">PikPak</option>
          <option value="wopan">联通沃盘</option>
          <option value="onedrive">OneDrive</option>
          <option value="spider91">91 爬虫</option>
        </select>
      </div>
      {form.kind !== "spider91" && (
        <>
          <div className="admin-form__row">
            <label>根目录 ID</label>
            <input
              value={form.rootId}
              onChange={(e) => set("rootId", e.target.value)}
              placeholder={form.kind === "pikpak" ? "留空表示根目录" : form.kind === "onedrive" ? "root" : "0"}
            />
          </div>
          <div className="admin-form__row">
            <label>扫描起点目录 ID</label>
            <input
              value={form.scanRootId}
              onChange={(e) => set("scanRootId", e.target.value)}
              placeholder="留空则使用根目录"
            />
            <div className="admin-form__help">
              可以指定一个子目录作为视频库入口，避免扫描整个网盘
            </div>
          </div>
        </>
      )}

      <hr className="admin-form__divider" />

      <div className="admin-form__help admin-form__help--lead">
        {credentialHelp(form.kind, isEdit)}
      </div>

      {fields.map((f) => (
        <div key={f.key} className="admin-form__row">
          <label>{f.label}{f.required && " *"}</label>
          {f.multiline ? (
            <textarea
              value={form.creds[f.key] ?? ""}
              onChange={(e) => setCred(f.key, e.target.value)}
              placeholder={f.placeholder}
            />
          ) : (
            <input
              value={form.creds[f.key] ?? ""}
              onChange={(e) => setCred(f.key, e.target.value)}
              placeholder={f.placeholder}
            />
          )}
          {f.help && <div className="admin-form__help">{f.help}</div>}
        </div>
      ))}

      {form.kind === "spider91" && (
        <>
          <hr className="admin-form__divider" />
          <Spider91UploadTargetField
            value={form.spider91UploadDriveId}
            onChange={(v) => set("spider91UploadDriveId", v)}
            uploadTargets={uploadTargets}
          />
        </>
      )}
    </div>
  );
}

/**
 * Spider91UploadTargetField 是 spider91 drive 表单专属的"上传目标"下拉。
 *
 * 行为：
 *   - 选项 = "（自动）" + 系统中所有 pikpak/p115 drive
 *   - "自动" 模式（value=""）下，后端在迁移 worker 启动时挑唯一的目标盘；
 *     系统中如果同时挂着 pikpak 和 p115 drive 必须显式选定，否则不会上传
 *   - 没有任何 pikpak/p115 drive 时给一行提示文字，告诉用户先去添加目标盘
 *   - 该字段写入的是全局 setting `spider91.upload_drive_id`，不是 drive 自己的
 *     credentials —— 所有 spider91 drive 共享同一个上传目标
 */
function Spider91UploadTargetField({
  value,
  onChange,
  uploadTargets,
}: {
  value: string;
  onChange: (v: string) => void;
  uploadTargets: api.AdminDrive[];
}) {
  // 文案根据系统中实际挂载的目标盘 kind 自适应：
  //   - 只挂了 PikPak  → 文案只讲 "PikPak"
  //   - 只挂了 115     → 文案只讲 "115 网盘"
  //   - 两类都挂       → 文案讲 "PikPak / 115 网盘"
  // 这样在单一类型场景下用户不会被另一类的字样干扰。
  const kindsPresent = new Set(uploadTargets.map((d) => d.kind));
  const hasPikPak = kindsPresent.has("pikpak");
  const has115 = kindsPresent.has("p115");
  const presentLabel =
    hasPikPak && has115
      ? "PikPak / 115 网盘"
      : hasPikPak
      ? "PikPak"
      : has115
      ? "115 网盘"
      : "PikPak 或 115 网盘";

  return (
    <div className="admin-form__row">
      <label>视频上传目标</label>
      {uploadTargets.length === 0 ? (
        <>
          <select value="" disabled>
            <option value="">（请先添加 {presentLabel}）</option>
          </select>
          <div className="admin-form__help">
            spider91 爬下来的视频会保留在本地最近 15 个，更旧的会自动上传到选定的云盘。
            目前系统里还没有 {presentLabel} drive，可以先把 spider91 保存好；之后再回来挂一个目标盘。
          </div>
        </>
      ) : (
        <>
          <select value={value} onChange={(e) => onChange(e.target.value)}>
            <option value="">（自动：唯一的 {presentLabel}）</option>
            {uploadTargets.map((d) => (
              <option key={d.id} value={d.id}>
                {kindLabel[d.kind] ?? d.kind} · {d.name || d.id}
              </option>
            ))}
          </select>
          <div className="admin-form__help">
            选定后，spider91 视频会被周期性上传到该云盘对应的根目录。
            该设置全局生效；
            {uploadTargets.length > 1
              ? `如果同时挂着多个 ${presentLabel} drive，"自动"模式不会工作，必须显式选定一个。`
              : `当前只挂着 1 个 ${presentLabel}，"自动"模式会直接选用它。`}
          </div>
        </>
      )}
    </div>
  );
}

function credentialHelp(kind: Kind, isEdit: boolean): string {
  const note = isEdit ? "如不修改凭证，留空即可，保存时会沿用旧值。" : "";
  switch (kind) {
    case "quark":
      return `在 pan.quark.cn 登录后，F12 → Network → 任意请求 → Request Headers 里复制整段 Cookie 粘贴到下方。${note}`;
    case "p115":
      return `登录 115.com 后复制 Cookie，形如 "UID=...; CID=...; SEID=...; KID=..."。${note}`;
    case "pikpak":
      return `参考 OpenList 的 PikPak 登录方式。可填用户名和密码首次登录，也可填 refresh_token；如返回验证码链接，打开验证后把 captcha_token 粘贴回来。${note}`;
    case "wopan":
      return `需要 access_token 和 refresh_token。后续会加扫码/短信登录入口，第一版只能手工粘贴。${note}`;
    case "onedrive":
      return `按 OpenList 默认方式，通过 api.oplist.org 在线刷新 token。只需要 refresh_token；保存后会自动回写新的 access_token / refresh_token。${note}`;
    case "spider91":
      return `91 爬虫源：每天凌晨自动跑 91VideoSpider/spider_91porn.py，从本月最热第 1 页起翻页，遇到已爬过的 viewkey 自动跳过，凑够 target_new（默认 15）个新视频后停止。需要服务器装好 python3 + requests + beautifulsoup4 + lxml。${note}`;
    default:
      return "";
  }
}

function credentialFields(kind: Kind): Array<{
  key: string;
  label: string;
  placeholder: string;
  multiline?: boolean;
  required?: boolean;
  help?: string;
}> {
  switch (kind) {
    case "quark":
      return [
        {
          key: "cookie",
          label: "Cookie",
          placeholder: "__pus=...; __puus=...; ...",
          multiline: true,
          required: true,
        },
      ];
    case "p115":
      return [
        {
          key: "cookie",
          label: "Cookie",
          placeholder: "UID=xxx; CID=xxx; SEID=xxx; KID=xxx",
          multiline: true,
          required: true,
        },
      ];
    case "pikpak":
      return [
        {
          key: "username",
          label: "用户名 / 邮箱（无 refresh_token 时必填）",
          placeholder: "user@example.com",
        },
        {
          key: "password",
          label: "密码（无 refresh_token 时必填）",
          placeholder: "PikPak 密码",
        },
        {
          key: "platform",
          label: "platform",
          placeholder: "web（可选：android / web / pc）",
          help: "默认 web；如果登录或直链异常，可尝试 android 或 pc。",
        },
        {
          key: "refresh_token",
          label: "refresh_token（可选）",
          placeholder: "已有 token 时可直接粘贴",
          multiline: true,
        },
        {
          key: "captcha_token",
          label: "captcha_token（可选）",
          placeholder: "遇到验证码校验时粘贴",
          multiline: true,
        },
        {
          key: "device_id",
          label: "device_id（可选）",
          placeholder: "留空自动生成并保存",
        },
        {
          key: "disable_media_link",
          label: "disable_media_link",
          placeholder: "true",
          help: "默认 true，使用原始下载链接；填 false 可尝试使用媒体缓存链接。",
        },
      ];
    case "wopan":
      return [
        {
          key: "access_token",
          label: "access_token",
          placeholder: "",
          required: true,
        },
        {
          key: "refresh_token",
          label: "refresh_token",
          placeholder: "",
          required: true,
        },
        {
          key: "family_id",
          label: "family_id（家庭空间可选）",
          placeholder: "留空走个人空间",
        },
      ];
    case "onedrive":
      return [
        {
          key: "refresh_token",
          label: "refresh_token",
          placeholder: "OpenList OneDrive refresh_token",
          multiline: true,
          required: true,
        },
        {
          key: "access_token",
          label: "access_token（可选）",
          placeholder: "留空也可以，保存时会在线刷新",
          multiline: true,
        },
        {
          key: "api_url_address",
          label: "api_url_address（可选）",
          placeholder: "https://api.oplist.org/onedrive/renewapi",
          help: "默认使用 OpenList 的在线刷新 API；除非你有自建兼容服务，否则留空。",
        },
        {
          key: "region",
          label: "region（可选）",
          placeholder: "global（可选：global / cn / us / de）",
          help: "默认 global；世纪互联填 cn，美国政府云填 us，德国云填 de。",
        },
        {
          key: "is_sharepoint",
          label: "is_sharepoint（可选）",
          placeholder: "false",
          help: "普通 OneDrive 留空或 false；SharePoint 文档库填 true，并填写 site_id。",
        },
        {
          key: "site_id",
          label: "site_id（SharePoint 必填）",
          placeholder: "SharePoint site id",
        },
      ];
    case "spider91":
      return [
        {
          key: "target_new",
          label: "每次爬取的新视频数",
          placeholder: "15",
          help: "默认 15。从 91porn 本月最热第 1 页起翻页，遇到已爬过的 viewkey 自动跳过，凑够这么多个新视频后停止。",
        },
        {
          key: "crawl_hour",
          label: "凌晨触发的小时（0-23）",
          placeholder: "0",
          help: "默认 0，即在 00:00-00:59 之间触发。距离上次成功爬取至少 12 小时才会再触发。",
        },
        {
          key: "proxy",
          label: "下载代理（可选）",
          placeholder: "留空则使用 HTTPS_PROXY 环境变量",
          help: "91porn CDN 在海外，国内服务器直连通常很慢。可填 http://127.0.0.1:7890 这样的本地代理；留空则自动读 backend 进程的 HTTPS_PROXY 环境变量。",
        },
        {
          key: "python_path",
          label: "python 可执行文件",
          placeholder: "python3",
          help: "默认 python3；可填绝对路径，例如 /usr/bin/python3 或 conda 环境路径。",
        },
        {
          key: "script_path",
          label: "spider_91porn.py 路径（可选）",
          placeholder: "留空自动定位 91VideoSpider/spider_91porn.py",
          help: "服务启动时会从 backend/ 的父目录推断。如果脚本被你挪到了别处，请填绝对路径。",
        },
      ];
  }
}

function defaultRootId(kind: Kind): string {
  if (kind === "pikpak") return "";
  if (kind === "onedrive") return "root";
  if (kind === "spider91") return "/";
  return "0";
}

// formatRelativeTime 把 unix 秒格式化成"刚刚 / N 分钟前 / N 小时前 / N 天前"，
// 用于网盘列表里显示 spider91 的 lastCrawlAt。
function formatRelativeTime(unixSeconds: number): string {
  if (!unixSeconds || unixSeconds <= 0) return "尚未抓取";
  const nowMs = Date.now();
  const thenMs = unixSeconds * 1000;
  const deltaSec = Math.max(0, Math.floor((nowMs - thenMs) / 1000));
  if (deltaSec < 60) return "刚刚";
  const m = Math.floor(deltaSec / 60);
  if (m < 60) return `${m} 分钟前`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h} 小时前`;
  const d = Math.floor(h / 24);
  if (d < 30) return `${d} 天前`;
  // 太久了直接给个本地化的日期
  try {
    return new Date(thenMs).toLocaleDateString();
  } catch {
    return `${d} 天前`;
  }
}
