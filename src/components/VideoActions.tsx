import { useState } from "react";
import { EyeOff, ThumbsDown, ThumbsUp } from "lucide-react";
import type { VideoDetail } from "@/types";
import { formatCount } from "@/lib/format";

type Props = {
  video: VideoDetail;
  onHideVideo: () => void;
  hideSaving?: boolean;
};

export function VideoActions({ video, onHideVideo, hideSaving }: Props) {
  const [likes, setLikes] = useState(video.likes ?? 0);
  const [dislikes, setDislikes] = useState(video.dislikes ?? 0);
  const [bursting, setBursting] = useState(false);

  async function handleLike() {
    setLikes((n) => n + 1);
    setBursting(true);
    window.setTimeout(() => setBursting(false), 240);

    try {
      const res = await fetch(
        `/api/video/${encodeURIComponent(video.id)}/like`,
        { method: "POST", credentials: "include" }
      );
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const data = (await res.json()) as { likes: number };
      if (typeof data.likes === "number") {
        setLikes(data.likes);
      }
    } catch {
      setLikes((n) => Math.max(0, n - 1));
    }
  }

  return (
    <div className="video-actions">
      <button
        className={`video-actions__btn video-actions__like ${bursting ? "is-bursting" : ""}`}
        onClick={handleLike}
        aria-label="点赞"
      >
        <ThumbsUp size={14} />
        点赞 · {formatCount(likes)}
      </button>
      <button
        className="video-actions__btn is-danger"
        onClick={() => setDislikes((n) => n + 1)}
        aria-label="点踩"
      >
        <ThumbsDown size={14} />
        点踩 · {formatCount(dislikes)}
      </button>
      <button
        className="video-actions__btn is-danger"
        onClick={onHideVideo}
        disabled={hideSaving}
      >
        <EyeOff size={14} />
        {hideSaving ? "隐藏中" : "不再展示"}
      </button>
    </div>
  );
}
