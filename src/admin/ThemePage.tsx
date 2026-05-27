import { useEffect, useState } from "react";
import { Check, Loader2, Moon, Sparkles } from "lucide-react";
import * as api from "./api";
import type { Theme } from "./api";
import { useToast } from "./ToastContext";
import { applyTheme, getCurrentTheme } from "@/lib/theme";

function isTheme(value: unknown): value is Theme {
  return value === "dark" || value === "pink";
}

type Option = {
  id: Theme;
  title: string;
  subtitle: string;
  description: string;
  icon: typeof Moon;
};

const OPTIONS: Option[] = [
  {
    id: "dark",
    title: "暗黑 + 暖橙",
    subtitle: "Cinema Dark",
    description: "深邃灰阶 + 暖橙主色，适合夜间观影、长时间浏览。",
    icon: Moon,
  },
  {
    id: "pink",
    title: "奶油白 + 樱花粉",
    subtitle: "Sakura Cream",
    description: "柔和奶白底 + 樱花粉主色，清爽温柔，日间使用更舒适。",
    icon: Sparkles,
  },
];

/**
 * 外观（主题）设置页。
 * - 全站统一主题：管理员选什么，所有访客看到什么
 * - 切换流程：先本地 applyTheme() 即时生效，再 PUT settings 持久化；失败回滚
 */
export function ThemePage() {
  const [active, setActive] = useState<Theme>(getCurrentTheme());
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState<Theme | null>(null);
  const { show } = useToast();

  // 从服务端拿权威值（避免 localStorage 和服务端不一致时显示错的"已选中"）
  useEffect(() => {
    let active = true;
    api
      .getSettings()
      .then((s) => {
        if (!active) return;
        // 旧后端没有 theme 字段，s.theme 会是 undefined。这种情况保留
        // getCurrentTheme() 返回的本地值，不要把 undefined 写出去。
        if (isTheme(s.theme)) {
          setActive(s.theme);
          applyTheme(s.theme);
        }
      })
      .catch(() => {
        // 失败时保留 DOM 当前值
      })
      .finally(() => {
        if (active) setLoading(false);
      });
    return () => {
      active = false;
    };
  }, []);

  async function handleSelect(next: Theme) {
    if (next === active || saving) return;
    const previous = active;
    // 先本地立即生效，体验流畅
    setActive(next);
    applyTheme(next);
    setSaving(next);
    try {
      // PUT 时只带 theme 即可：后端按字段存在与否判断是否变更，不会顺手改其它设置。
      const resp = await api.updateSettings({
        theme: next,
      });
      // 以服务端响应为准（但只在响应里返了合法值时才覆盖；旧后端不识别
      // theme 字段，resp.theme 可能是 undefined / ""，此时维持已经设好的 next）
      if (isTheme(resp.theme)) {
        setActive(resp.theme);
        applyTheme(resp.theme);
      }
      show("主题已更新，全站访客将看到新主题", "success");
    } catch (e) {
      // 回滚
      setActive(previous);
      applyTheme(previous);
      show(e instanceof Error ? e.message : "保存失败", "error");
    } finally {
      setSaving(null);
    }
  }

  return (
    <div className="theme-page">
      <header className="theme-page__head">
        <h1 className="theme-page__title">外观</h1>
        <p className="theme-page__sub">
          切换全站主题。所有访客都会看到这里选定的主题，本设置会写入数据库永久保存。
        </p>
      </header>

      <div className="theme-grid">
        {OPTIONS.map((opt) => {
          const Icon = opt.icon;
          const isActive = active === opt.id;
          const isSaving = saving === opt.id;
          return (
            <button
              key={opt.id}
              type="button"
              className={`theme-card${isActive ? " is-active" : ""}`}
              data-preview={opt.id}
              onClick={() => handleSelect(opt.id)}
              disabled={loading || saving !== null}
              aria-pressed={isActive}
              aria-label={`切换到${opt.title}主题`}
            >
              {/* 缩略预览：用 data-preview 做主题独立配色 */}
              <div className="theme-card__preview" aria-hidden="true">
                <span className="theme-card__bar" />
                <div className="theme-card__player" />
                <div className="theme-card__lines">
                  <span className="theme-card__line theme-card__line--lg" />
                  <span className="theme-card__line theme-card__line--md" />
                </div>
                <div className="theme-card__chips">
                  <span className="theme-card__chip" />
                  <span className="theme-card__chip" />
                  <span className="theme-card__chip theme-card__chip--accent" />
                </div>
              </div>

              <div className="theme-card__body">
                <div className="theme-card__head">
                  <span className="theme-card__icon">
                    <Icon size={16} />
                  </span>
                  <div className="theme-card__title-wrap">
                    <span className="theme-card__title">{opt.title}</span>
                    <span className="theme-card__subtitle">{opt.subtitle}</span>
                  </div>
                  <span className="theme-card__state" aria-hidden="true">
                    {isSaving ? (
                      <Loader2 size={16} className="theme-card__spin" />
                    ) : isActive ? (
                      <Check size={16} />
                    ) : null}
                  </span>
                </div>
                <p className="theme-card__desc">{opt.description}</p>
              </div>
            </button>
          );
        })}
      </div>
    </div>
  );
}
