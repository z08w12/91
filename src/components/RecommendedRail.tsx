import type { VideoItem } from "@/types";
import { VideoCard } from "./VideoCard";

type Props = {
  videos: VideoItem[];
};

export function RecommendedRail({ videos }: Props) {
  if (!videos || videos.length === 0) return null;
  return (
    <aside className="detail-side" aria-label="推荐视频">
      <div className="detail-side__header">推荐视频</div>
      <div className="detail-side__list">
        {videos.map((v) => (
          <VideoCard key={v.id} video={v} />
        ))}
      </div>
    </aside>
  );
}
