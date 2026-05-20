import { useState } from "react";
import { Navigate, useLocation, useNavigate } from "react-router-dom";
import { Play } from "lucide-react";
import { useAuth } from "./AuthContext";
import { useToast } from "./ToastContext";

export function LoginPage() {
  const { status, login } = useAuth();
  const [u, setU] = useState("");
  const [p, setP] = useState("");
  const [err, setErr] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const navigate = useNavigate();
  const location = useLocation();
  const { show } = useToast();

  if (status === "loading") {
    return (
      <div className="admin-loading-screen">
        检查登录状态...
      </div>
    );
  }

  // 已登录：回到来源页，或默认去首页
  if (status === "authed") {
    const from = (location.state as { from?: string } | null)?.from ?? "/";
    return <Navigate to={from} replace />;
  }

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setErr(null);
    setLoading(true);
    try {
      await login(u, p);
      show("登录成功", "success");
      const from = (location.state as { from?: string } | null)?.from ?? "/";
      navigate(from, { replace: true });
    } catch (e) {
      setErr(e instanceof Error ? e.message : "登录失败");
    } finally {
      setLoading(false);
    }
  }

  return (
    <div className="admin-login">
      <form className="admin-login__card" onSubmit={handleSubmit}>
        <h1 className="admin-login__title">
          <Play size={18} fill="currentColor" /> 登录
        </h1>
        <div className="admin-form">
          <div className="admin-form__row">
            <label>用户名</label>
            <input
              autoFocus
              value={u}
              onChange={(e) => setU(e.target.value)}
              autoComplete="username"
            />
          </div>
          <div className="admin-form__row">
            <label>密码</label>
            <input
              type="password"
              value={p}
              onChange={(e) => setP(e.target.value)}
              autoComplete="current-password"
            />
          </div>
          <button
            className="admin-btn is-primary"
            type="submit"
            disabled={loading || !u || !p}
          >
            {loading ? "登录中..." : "登录"}
          </button>
          {err && <div className="admin-login__error">{err}</div>}
        </div>
      </form>
    </div>
  );
}
