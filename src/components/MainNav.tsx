import { useState } from "react";
import { NavLink } from "react-router-dom";
import {
  Film,
  Menu,
  Play,
  Upload,
  X,
} from "lucide-react";

const navItems = [
  { to: "/upload", label: "上传", icon: Upload },
  { to: "/list", label: "视频", icon: Film },
];

export function MainNav() {
  const [open, setOpen] = useState(false);

  return (
    <nav className={`main-nav ${open ? "is-open" : ""}`}>
      <div className="container main-nav__inner">
        <NavLink to="/" className="main-nav__logo">
          <span className="main-nav__logo-mark">
            <Play size={16} fill="#000" />
          </span>
          视频站
        </NavLink>

        <ul className="main-nav__list" role="menubar">
          {navItems.map(({ to, label, icon: Icon }) => (
            <li key={to} role="none">
              <NavLink
                to={to}
                role="menuitem"
                className={({ isActive }) =>
                  `main-nav__link ${isActive ? "is-active" : ""}`
                }
                onClick={() => setOpen(false)}
              >
                <Icon size={16} />
                {label}
              </NavLink>
            </li>
          ))}
        </ul>

        <button
          className="main-nav__toggle"
          aria-label={open ? "关闭菜单" : "打开菜单"}
          aria-expanded={open}
          onClick={() => setOpen((v) => !v)}
        >
          {open ? <X size={22} /> : <Menu size={22} />}
        </button>
      </div>
    </nav>
  );
}
