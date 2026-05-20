import { useEffect, useState } from "react";
import { AppShell } from "@/components/AppShell";
import { PromoStrip } from "@/components/PromoStrip";
import { SearchPanel } from "@/components/SearchPanel";
import { TagCloud } from "@/components/TagCloud";
import { SectionHeader } from "@/components/SectionHeader";
import { VideoGrid } from "@/components/VideoGrid";
import { fetchHomeVideos, fetchListing } from "@/data/videos";
import type { VideoItem } from "@/types";

const DESKTOP_COUNT = 12;
const MOBILE_COUNT = 8;

function useIsMobile() {
  const [mobile, setMobile] = useState(window.innerWidth <= 640);
  useEffect(() => {
    const mq = window.matchMedia("(max-width: 640px)");
    const handler = () => setMobile(mq.matches);
    mq.addEventListener("change", handler);
    return () => mq.removeEventListener("change", handler);
  }, []);
  return mobile;
}

// 模块级缓存：SPA 生命周期内保持，刷新页面时重置
let cachedRanking: VideoItem[] | null = null;
let cachedLatest: VideoItem[] | null = null;

export default function HomePage() {
  const [rankingVideos, setRankingVideos] = useState<VideoItem[]>(cachedRanking ?? []);
  const [latestVideos, setLatestVideos] = useState<VideoItem[]>(cachedLatest ?? []);
  const [loading, setLoading] = useState(cachedRanking === null);
  const isMobile = useIsMobile();

  useEffect(() => {
    document.title = "首页 · 视频聚合站";

    // 有缓存说明是 SPA 内导航返回，不重新请求
    if (cachedRanking !== null) return;

    let active = true;
    setLoading(true);
    Promise.all([
      fetchHomeVideos(),
      fetchListing(1, DESKTOP_COUNT, { sort: "latest" }),
    ]).then(([rankingItems, latestResult]) => {
      if (!active) return;
      cachedRanking = rankingItems;
      cachedLatest = latestResult.items;
      setRankingVideos(rankingItems);
      setLatestVideos(latestResult.items);
      setLoading(false);
    });
    return () => { active = false; };
  }, []);

  const displayCount = isMobile ? MOBILE_COUNT : DESKTOP_COUNT;
  const ranking = rankingVideos.slice(0, displayCount);
  const latest = latestVideos.slice(0, displayCount);

  return (
    <AppShell>
      <div className="container page-section">
        <PromoStrip />
        <SearchPanel />
        <TagCloud />
      </div>

      <div className="container page-section">
        <SectionHeader title="随机推荐" extra={`随机展示 ${ranking.length} 个作品`} />
        <VideoGrid videos={ranking} loading={loading} skeletonCount={displayCount} />
      </div>

      <div className="container page-section">
        <SectionHeader title="最新视频" extra={latest.length > 0 ? `共 ${latest.length} 个` : undefined} />
        <VideoGrid videos={latest} loading={loading} skeletonCount={displayCount} />
      </div>
    </AppShell>
  );
}
