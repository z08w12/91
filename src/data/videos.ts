import type { VideoDetail, VideoItem } from "@/types";

// 真实后端接口调用。未配置网盘时，各接口返回空数据。
export function fetchHomeVideos(): Promise<VideoItem[]> {
  return apiGet<VideoItem[]>("/api/home").catch(() => []);
}

export function fetchListing(
  page: number,
  pageSize: number,
  params?: { q?: string; tag?: string; cat?: string; sort?: string }
): Promise<{ items: VideoItem[]; total: number }> {
  const qs = new URLSearchParams({
    page: String(page),
    size: String(pageSize),
  });
  if (params?.q) qs.set("q", params.q);
  if (params?.tag) qs.set("tag", params.tag);
  if (params?.cat) qs.set("cat", params.cat);
  if (params?.sort) qs.set("sort", params.sort);
  return apiGet<{ items: VideoItem[]; total: number }>(
    `/api/list?${qs.toString()}`
  ).catch(() => ({ items: [], total: 0 }));
}

export function fetchVideoDetail(id: string): Promise<VideoDetail | null> {
  return apiGet<VideoDetail>(`/api/video/${encodeURIComponent(id)}`).catch(
    () => null
  );
}

export function updateVideoTags(
  id: string,
  tags: string[]
): Promise<VideoItem> {
  return apiJSON<VideoItem>(`/api/video/${encodeURIComponent(id)}/tags`, {
    method: "PUT",
    body: JSON.stringify({ tags }),
  });
}

export function hideVideo(id: string): Promise<{ ok: boolean }> {
  return apiJSON<{ ok: boolean }>(
    `/api/video/${encodeURIComponent(id)}/hide`,
    { method: "POST" }
  );
}

export function recordView(id: string): Promise<{ views: number }> {
  return apiJSON<{ views: number }>(
    `/api/video/${encodeURIComponent(id)}/view`,
    { method: "POST" }
  );
}

export type UploadVideoInput = {
  file: File;
  title: string;
  tags: string[];
};

export function uploadVideo(input: UploadVideoInput): Promise<VideoItem> {
  const body = new FormData();
  body.append("file", input.file);
  if (input.title.trim()) {
    body.append("title", input.title.trim());
  }
  for (const tag of input.tags) {
    body.append("tags", tag);
  }
  return apiForm<VideoItem>("/api/upload", body);
}

export type TagItem = { id: string; label: string; count?: number };

export function fetchTags(): Promise<TagItem[]> {
  return apiGet<TagItem[]>("/api/tags").catch(() => []);
}

async function apiGet<T>(path: string): Promise<T> {
  const res = await fetch(path, { credentials: "include" });
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  return res.json();
}

async function apiJSON<T>(path: string, init: RequestInit): Promise<T> {
  const res = await fetch(path, {
    credentials: "include",
    headers: { "Content-Type": "application/json" },
    ...init,
  });
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  return res.json();
}

async function apiForm<T>(path: string, body: FormData): Promise<T> {
  const res = await fetch(path, {
    method: "POST",
    credentials: "include",
    body,
  });
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  return res.json();
}
