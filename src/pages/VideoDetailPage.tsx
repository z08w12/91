import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { AppShell } from "@/components/AppShell";
import { VideoPlayer } from "@/components/VideoPlayer";
import { VideoActions } from "@/components/VideoActions";
import { VideoInfoPanel } from "@/components/VideoInfoPanel";
import { RecommendedRail } from "@/components/RecommendedRail";
import {
  fetchTags,
  fetchVideoDetail,
  hideVideo,
  recordView,
  updateVideoTags,
} from "@/data/videos";
import type { TagItem, VideoDetail } from "@/types";

export default function VideoDetailPage() {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const [detail, setDetail] = useState<VideoDetail | null>(null);
  const [tags, setTags] = useState<TagItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [tagSaving, setTagSaving] = useState(false);
  const [hideSaving, setHideSaving] = useState(false);
  const detailTopRef = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    if (!id) return;
    let active = true;
    window.scrollTo({ top: 0, behavior: "auto" });
    setLoading(true);
    Promise.all([fetchVideoDetail(id), fetchTags()]).then(([d, tagList]) => {
      if (!active) return;
      setDetail(d);
      setTags(tagList);
      setLoading(false);
      document.title = d ? `${d.title} · 视频聚合站` : "视频不存在";
    });
    return () => {
      active = false;
    };
  }, [id]);

  useLayoutEffect(() => {
    if (loading || !detail) return;
    window.requestAnimationFrame(() => {
      detailTopRef.current?.scrollIntoView({
        block: "start",
        behavior: "auto",
      });
    });
  }, [loading, detail?.id]);

  async function handleTagsChange(nextTags: string[]) {
    if (!detail) return;
    setTagSaving(true);
    try {
      const updated = await updateVideoTags(detail.id, nextTags);
      setDetail({ ...detail, tags: updated.tags ?? [] });
    } finally {
      setTagSaving(false);
    }
  }

  async function handleHideVideo() {
    if (!detail || hideSaving) return;
    if (!window.confirm("确定以后不再展示这个视频吗？")) return;
    setHideSaving(true);
    try {
      await hideVideo(detail.id);
      navigate("/list", { replace: true });
    } catch {
      setHideSaving(false);
      window.alert("隐藏失败，请稍后重试");
    }
  }

  function handleFirstPlay() {
    if (!detail) return;
    const id = detail.id;
    // 失败静默忽略，不打扰用户播放体验
    recordView(id).catch(() => undefined);
  }

  if (loading) {
    return (
      <AppShell>
        <div className="container page-section">
          <div className="video-grid-loading">
            <div className="skeleton-card" />
            <div className="skeleton-card" />
            <div className="skeleton-card" />
            <div className="skeleton-card" />
          </div>
        </div>
      </AppShell>
    );
  }

  if (!detail) {
    return (
      <AppShell>
        <div className="container page-section">
          <div className="video-grid-empty">视频不存在或已被移除</div>
        </div>
      </AppShell>
    );
  }

  return (
    <AppShell>
      <div className="container page-section">
        <div className="detail-layout">
          <div className="detail-main" ref={detailTopRef}>
            <div className="detail-player-card">
              <div className="detail-title-bar">{detail.title}</div>
              <VideoPlayer
                src={detail.videoSrc}
                poster={detail.poster}
                title={detail.title}
                onFirstPlay={handleFirstPlay}
              />
              <VideoActions
                video={detail}
                onHideVideo={handleHideVideo}
                hideSaving={hideSaving}
              />
            </div>
            <VideoInfoPanel
              video={detail}
              availableTags={tags}
              tagSaving={tagSaving}
              onTagsChange={handleTagsChange}
            />
          </div>
          <RecommendedRail videos={detail.relatedVideos} />
        </div>
      </div>

      <div style={{ height: 40 }} />
    </AppShell>
  );
}
