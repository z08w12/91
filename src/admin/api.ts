// 管理后台 API 客户端
// 所有请求都带 cookie，401 会抛错让路由守卫跳登录
const BASE = "/admin/api";

export class UnauthorizedError extends Error {
  constructor() {
    super("unauthorized");
  }
}

async function request<T>(
  path: string,
  init: RequestInit = {}
): Promise<T> {
  const res = await fetch(BASE + path, {
    credentials: "include",
    headers: {
      "Content-Type": "application/json",
      ...(init.headers ?? {}),
    },
    ...init,
  });
  if (res.status === 401) {
    throw new UnauthorizedError();
  }
  if (!res.ok) {
    const text = await res.text().catch(() => "");
    throw new Error(text || `HTTP ${res.status}`);
  }
  if (res.status === 204) return undefined as T;
  const ct = res.headers.get("content-type") ?? "";
  if (ct.includes("application/json")) {
    return (await res.json()) as T;
  }
  return (await res.text()) as unknown as T;
}

export function login(username: string, password: string) {
  return request<{ ok: boolean }>("/login", {
    method: "POST",
    body: JSON.stringify({ username, password }),
  });
}

export function logout() {
  return request<{ ok: boolean }>("/logout", { method: "POST" });
}

export function me() {
  return request<{ authenticated: boolean }>("/me");
}

// ---------- Drives ----------

export type AdminDrive = {
  id: string;
  kind: "quark" | "p115" | "pikpak" | "wopan" | "onedrive" | "spider91";
  name: string;
  rootId: string;
  scanRootId: string;
  status: string;
  lastError?: string;
  hasCredential: boolean;
  /** 当前是否给该盘生成 teaser/封面（per-drive 开关，替代旧的全局 preview.enabled）。 */
  teaserEnabled: boolean;
  // spider91 上次成功爬取时间（unix 秒）；其它 kind 留空。
  lastCrawlAt?: number;
  thumbnailGenerationStatus?: DriveGenerationStatus;
  previewGenerationStatus?: DriveGenerationStatus;
  thumbnailReadyCount: number;
  thumbnailPendingCount: number;
  thumbnailFailedCount: number;
  teaserReadyCount: number;
  teaserPendingCount: number;
  teaserFailedCount: number;
};

export type DriveGenerationStatus = {
  state: string;
  currentTitle?: string;
  queueLength: number;
  cooldownUntil?: string;
};

export function listDrives() {
  return request<AdminDrive[]>("/drives");
}

export type DriveStorageUsage = {
  thumbnailBytes: number;
  teaserBytes: number;
  totalBytes: number;
};

export type AdminDriveStorage = DriveStorageUsage & {
  availableBytes: number;
  capacityBytes: number;
  drives: Record<string, DriveStorageUsage>;
};

export function getDriveStorage() {
  return request<AdminDriveStorage>("/drives/storage");
}

export type UpsertDriveInput = {
  id: string;
  kind: "quark" | "p115" | "pikpak" | "wopan" | "onedrive" | "spider91";
  name: string;
  rootId: string;
  scanRootId: string;
  credentials: Record<string, string>;
};

export function upsertDrive(body: UpsertDriveInput) {
  return request<{ ok: boolean; warning?: string }>("/drives", {
    method: "POST",
    body: JSON.stringify(body),
  });
}

export function deleteDrive(id: string) {
  return request<{ ok: boolean }>(`/drives/${encodeURIComponent(id)}`, {
    method: "DELETE",
  });
}

export function rescan(id: string) {
  return request<{ ok: boolean }>(
    `/drives/${encodeURIComponent(id)}/rescan`,
    { method: "POST" }
  );
}

/**
 * 切换某个云盘的 teaser 生成开关。点击网盘列表里行内的 toggle 按钮时调用。
 *
 * 后端会写 catalog.drives.teaser_enabled，并在从关到开时立刻补扫该盘 pending teaser；
 * 关闭分支不补做任何事，新的入队判断会自动停。
 */
export function setDriveTeaserEnabled(id: string, enabled: boolean) {
  return request<{ ok: boolean; teaserEnabled: boolean }>(
    `/drives/${encodeURIComponent(id)}/teaser-enabled`,
    {
      method: "POST",
      body: JSON.stringify({ enabled }),
    }
  );
}

export function regenFailedPreviews(id: string) {
  return request<{ ok: boolean }>(
    `/drives/${encodeURIComponent(id)}/previews/failed/regenerate`,
    { method: "POST" }
  );
}

// ---------- Videos ----------

export type AdminVideo = {
  id: string;
  driveId: string;
  fileId: string;
  title: string;
  author: string;
  tags: string[];
  durationSeconds: number;
  size: number;
  ext: string;
  quality: string;
  thumbnailUrl: string;
  previewStatus: string;
  views: number;
  favorites: number;
  comments: number;
  likes: number;
  category: string;
  badges: string[];
  description: string;
  publishedAt: string;
  updatedAt: string;
};

export type AdminVideoList = {
  items: AdminVideo[];
  total: number;
  page: number;
  size: number;
};

export function listVideos(params: { driveId?: string; page?: number; size?: number } = {}) {
  const qs = new URLSearchParams();
  if (params.driveId) qs.set("driveId", params.driveId);
  if (params.page) qs.set("page", String(params.page));
  if (params.size) qs.set("size", String(params.size));
  const suffix = qs.toString() ? `?${qs.toString()}` : "";
  return request<AdminVideoList>(`/videos${suffix}`);
}

export type UpdateVideoInput = Partial<{
  title: string;
  author: string;
  tags: string[];
  category: string;
  badges: string[];
  description: string;
  thumbnail: string;
  quality: string;
  durationSeconds: number;
}>;

export function updateVideo(id: string, body: UpdateVideoInput) {
  return request<AdminVideo>(`/videos/${encodeURIComponent(id)}`, {
    method: "PUT",
    body: JSON.stringify(body),
  });
}

export function regenPreview(id: string) {
  return request<{ ok: boolean }>(
    `/videos/${encodeURIComponent(id)}/regen-preview`,
    { method: "POST" }
  );
}

// ---------- Tags ----------

export type AdminTag = {
  id: number;
  label: string;
  aliases?: string[];
  source: string;
  count: number;
};

export function listTags() {
  return request<AdminTag[]>("/tags");
}

export function createTag(label: string, aliases: string[]) {
  return request<{ label: string; classified: number }>("/tags", {
    method: "POST",
    body: JSON.stringify({ label, aliases }),
  });
}

// ---------- Settings ----------

export type Theme = "dark" | "pink";

export type Settings = {
  theme: Theme;
  /**
   * spider91 视频迁移到云盘时的目标 drive ID（必须是已挂载的 pikpak 或 p115 drive）。
   * - 空字符串：自动模式。系统中如果只挂着一个 pikpak/p115 drive 就用它；多个并存时迁移会跳过。
   * - 非空：显式指定。后端会校验 drive 存在且 kind ∈ {pikpak, p115}。
   */
  spider91UploadDriveId: string;
};

export function getSettings() {
  return request<Settings>("/settings");
}

/**
 * 更新设置。后端按字段存在与否判断是否变更，所以可以传 Partial 局部更新。
 *
 * 例：只切换主题，其它字段保持原状：
 *   updateSettings({ theme: "pink" })
 */
export function updateSettings(body: Partial<Settings>) {
  return request<Settings>("/settings", {
    method: "PUT",
    body: JSON.stringify(body),
  });
}
