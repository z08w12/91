import { useEffect, useRef, useState } from "react";

type Props = {
  src: string;
  poster: string;
  title: string;
  /**
   * 用户首次按下播放时触发。同一个 VideoPlayer 实例只会触发一次；
   * 后续暂停-继续不会重复触发。换 src 时会重置（详情页切换视频用）。
   */
  onFirstPlay?: () => void;
};

export function VideoPlayer({ src, poster, title, onFirstPlay }: Props) {
  const isTranscode = src.includes("/p/transcode/");
  const [playbackSrc, setPlaybackSrc] = useState(isTranscode ? "" : src);
  const [transcodeStatus, setTranscodeStatus] = useState<
    "idle" | "processing" | "error"
  >("idle");
  const playedRef = useRef(false);

  useEffect(() => {
    // 切换视频时重置首次播放标记
    playedRef.current = false;
  }, [src]);

  useEffect(() => {
    if (!isTranscode) {
      setPlaybackSrc(src);
      setTranscodeStatus("idle");
      return;
    }

    let active = true;
    let timer: number | null = null;

    async function poll(start: boolean) {
      try {
        const statusResp = await fetch(`${src}/status`, {
          credentials: "include",
        });
        if (!statusResp.ok) throw new Error("status failed");
        const statusBody = (await statusResp.json()) as { status?: string };
        if (!active) return;

        if (statusBody.status === "ready") {
          setPlaybackSrc(src);
          setTranscodeStatus("idle");
          return;
        }

        if (start) {
          await fetch(`${src}/start`, {
            method: "POST",
            credentials: "include",
          });
        }

        setPlaybackSrc("");
        setTranscodeStatus("processing");
        timer = window.setTimeout(() => poll(false), 3000);
      } catch {
        if (!active) return;
        setPlaybackSrc("");
        setTranscodeStatus("error");
      }
    }

    setPlaybackSrc("");
    setTranscodeStatus("processing");
    void poll(true);

    return () => {
      active = false;
      if (timer) window.clearTimeout(timer);
    };
  }, [isTranscode, src]);

  function handlePlay() {
    if (playedRef.current) return;
    playedRef.current = true;
    onFirstPlay?.();
  }

  return (
    <div className="video-player">
      <video
        src={playbackSrc || undefined}
        poster={poster}
        controls
        preload="metadata"
        playsInline
        aria-label={title}
        onPlay={handlePlay}
      />
      {isTranscode && !playbackSrc && (
        <div className="video-player__status">
          {transcodeStatus === "error"
            ? "转码启动失败，请稍后重试"
            : "正在准备可快进版本..."}
        </div>
      )}
    </div>
  );
}
