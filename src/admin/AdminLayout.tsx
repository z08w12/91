import { NavLink, Outlet, useNavigate } from "react-router-dom";
import { HardDrive, Film, LogOut, Play, Home, Tags, Palette } from "lucide-react";
import { useAuth } from "./AuthContext";
import { useToast } from "./ToastContext";

export function AdminLayout() {
  const { logout } = useAuth();
  const navigate = useNavigate();
  const { show } = useToast();

  async function handleLogout() {
    try {
      await logout();
      show("已退出登录", "success");
      navigate("/login", { replace: true });
    } catch {
      show("退出失败", "error");
    }
  }

  return (
    <div className="admin-shell">
      <aside className="admin-sidebar">
        <div className="admin-sidebar__brand">
          <span className="admin-sidebar__brand-mark">
            <Play size={14} fill="#000" />
          </span>
          视频站后台
        </div>
        <nav className="admin-nav">
          <NavLink to="/" className="admin-nav__link">
            <Home size={16} /> 返回主站
          </NavLink>
          <NavLink
            to="/admin/drives"
            className={({ isActive }) =>
              `admin-nav__link ${isActive ? "is-active" : ""}`
            }
          >
            <HardDrive size={16} /> 网盘管理
          </NavLink>
          <NavLink
            to="/admin/videos"
            className={({ isActive }) =>
              `admin-nav__link ${isActive ? "is-active" : ""}`
            }
          >
            <Film size={16} /> 视频管理
          </NavLink>
          <NavLink
            to="/admin/tags"
            className={({ isActive }) =>
              `admin-nav__link ${isActive ? "is-active" : ""}`
            }
          >
            <Tags size={16} /> 标签管理
          </NavLink>
          <NavLink
            to="/admin/theme"
            className={({ isActive }) =>
              `admin-nav__link ${isActive ? "is-active" : ""}`
            }
          >
            <Palette size={16} /> 外观
          </NavLink>
        </nav>
        <div className="admin-sidebar__footer">
          <button className="admin-sidebar__logout" onClick={handleLogout}>
            <LogOut size={14} style={{ verticalAlign: -2, marginRight: 4 }} />
            退出登录
          </button>
        </div>
      </aside>
      <main className="admin-main">
        <Outlet />
      </main>
    </div>
  );
}
