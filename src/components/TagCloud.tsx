import { useEffect, useMemo, useRef, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { fetchTags, type TagItem } from "@/data/videos";

const TAG_PLACEHOLDER_COUNT = 16;

type TagCloudProps = {
  linkBasePath?: string;
};

export function TagCloud({ linkBasePath = "/list" }: TagCloudProps) {
  const [params] = useSearchParams();
  const activeTag = params.get("tag");
  const [tags, setTags] = useState<TagItem[]>([]);
  const [loaded, setLoaded] = useState(false);
  const containerRef = useRef<HTMLDivElement>(null);
  const visibleTags = useMemo(
    () => tags.filter((tag) => typeof tag.count !== "number" || tag.count > 0),
    [tags]
  );

  useEffect(() => {
    let active = true;
    fetchTags()
      .then((list) => {
        if (active) setTags(list);
      })
      .finally(() => {
        if (active) setLoaded(true);
      });
    return () => {
      active = false;
    };
  }, []);

  useEffect(() => {
    const slider = containerRef.current;
    if (!slider) return;

    let isDown = false;
    let startX = 0;
    let scrollLeft = 0;
    let isDragging = false;

    const handleMouseDown = (e: MouseEvent) => {
      isDown = true;
      isDragging = false;
      slider.classList.add("is-dragging");
      startX = e.pageX - slider.offsetLeft;
      scrollLeft = slider.scrollLeft;
    };

    const handleMouseLeave = () => {
      isDown = false;
      slider.classList.remove("is-dragging");
    };

    const handleMouseUp = () => {
      isDown = false;
      slider.classList.remove("is-dragging");
    };

    const handleMouseMove = (e: MouseEvent) => {
      if (!isDown) return;
      e.preventDefault();
      const x = e.pageX - slider.offsetLeft;
      const walk = (x - startX) * 1.5;
      if (Math.abs(x - startX) > 10) {
        isDragging = true;
      }
      slider.scrollLeft = scrollLeft - walk;
    };

    const handleWheel = (e: WheelEvent) => {
      if (e.deltaY !== 0) {
        e.preventDefault();
        slider.scrollLeft += e.deltaY;
      }
    };

    const handleClick = (e: MouseEvent) => {
      if (isDragging) {
        e.preventDefault();
        e.stopPropagation();
        isDragging = false;
      }
    };

    slider.addEventListener("mousedown", handleMouseDown);
    slider.addEventListener("mouseleave", handleMouseLeave);
    slider.addEventListener("mouseup", handleMouseUp);
    slider.addEventListener("mousemove", handleMouseMove);
    slider.addEventListener("wheel", handleWheel, { passive: false });
    slider.addEventListener("click", handleClick, { capture: true });

    return () => {
      slider.removeEventListener("mousedown", handleMouseDown);
      slider.removeEventListener("mouseleave", handleMouseLeave);
      slider.removeEventListener("mouseup", handleMouseUp);
      slider.removeEventListener("mousemove", handleMouseMove);
      slider.removeEventListener("wheel", handleWheel);
      slider.removeEventListener("click", handleClick, { capture: true });
    };
  }, [visibleTags]);

  if (loaded && visibleTags.length === 0) return null;

  const loading = !loaded && visibleTags.length === 0;

  const renderTag = (tag: TagItem) => (
    <Link
      key={tag.id}
      to={`${linkBasePath}?tag=${encodeURIComponent(tag.label)}`}
      className={`tag-chip ${activeTag === tag.label ? "is-active" : ""}`}
    >
      {tag.label}
    </Link>
  );

  return (
    <div
      className={`tag-cloud-container ${loading ? "is-loading" : ""}`}
      aria-label="热门标签"
      aria-busy={loading ? "true" : undefined}
    >
      <div className="tag-cloud__grid" ref={containerRef}>
        <div className="tag-cloud__row">
          {loading
            ? Array.from({ length: TAG_PLACEHOLDER_COUNT }, (_, item) => (
                <span
                  key={item}
                  className="tag-chip tag-chip--placeholder"
                  aria-hidden="true"
                />
              ))
            : visibleTags.map(renderTag)}
        </div>
      </div>
    </div>
  );
}
