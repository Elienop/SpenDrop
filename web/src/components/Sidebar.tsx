import { useState } from 'react';
import { NavLink } from 'react-router-dom';
import {
  LayoutGrid,
  ArrowLeftRight,
  BarChart3,
  Tag,
  Settings,
  Moon,
  Sun,
  Monitor,
  ChevronLeft,
  ChevronRight,
  LogOut,
} from 'lucide-react';
import { useAuth } from '../hooks/useAuth';
import { useTheme } from '../hooks/useTheme';
import styles from '../styles/Sidebar.module.css';

const menuItems = [
  { to: '/', label: 'Dashboard', icon: LayoutGrid },
  { to: '/transactions', label: 'Transactions', icon: ArrowLeftRight, badge: undefined as number | undefined },
  { to: '/reports', label: 'Reports', icon: BarChart3 },
  { to: '/categories', label: 'Categories', icon: Tag },
];

const generalItems = [
  { to: '/settings', label: 'Settings', icon: Settings },
];

const themeIcons = { dark: Moon, light: Sun, system: Monitor } as const;
const themeOrder: Array<'dark' | 'light' | 'system'> = ['dark', 'light', 'system'];

export function Sidebar() {
  const { user, logout } = useAuth();
  const { theme, setTheme } = useTheme();
  const [expanded, setExpanded] = useState(() => {
    return localStorage.getItem('spendrop-sidebar') === 'true';
  });

  const ThemeIcon = themeIcons[theme];

  const toggleSidebar = () => {
    setExpanded(prev => {
      const next = !prev;
      localStorage.setItem('spendrop-sidebar', String(next));
      window.dispatchEvent(new Event('sidebar-toggle'));
      return next;
    });
  };

  const cycleTheme = () => {
    const idx = themeOrder.indexOf(theme);
    setTheme(themeOrder[(idx + 1) % themeOrder.length]);
  };

  const initial = user?.display_name?.charAt(0).toUpperCase() ?? '?';

  return (
    <aside
      className={`${styles.sidebar}${expanded ? ` ${styles.expanded}` : ''}`}
    >
      <div className={styles.header}>
        <span className={styles.logoMark}>S</span>
        <span className={styles.logoText}>SpenDrop</span>
        <button
          className={styles.toggleButton}
          onClick={toggleSidebar}
          aria-label="Toggle sidebar"
        >
          {expanded ? <ChevronLeft size={14} /> : <ChevronRight size={14} />}
        </button>
      </div>

      <nav className={styles.nav} aria-label="Main navigation">
        <div className={styles.navSection}>
          <div className={styles.navSectionLabel}>Menu</div>
          <ul className={styles.navList}>
            {menuItems.map((item) => (
              <li key={item.to}>
                <NavLink
                  to={item.to}
                  end={item.to === '/'}
                  className={({ isActive }) =>
                    `${styles.navLink}${isActive ? ` ${styles.active}` : ''}`
                  }
                >
                  <span className={styles.navIcon} aria-hidden="true">
                    <item.icon size={20} strokeWidth={1.5} />
                  </span>
                  <span className={styles.navLabel}>{item.label}</span>
                </NavLink>
              </li>
            ))}
          </ul>
        </div>

        <div className={styles.navSection}>
          <div className={styles.navSectionLabel}>General</div>
          <ul className={styles.navList}>
            {generalItems.map((item) => (
              <li key={item.to}>
                <NavLink
                  to={item.to}
                  className={({ isActive }) =>
                    `${styles.navLink}${isActive ? ` ${styles.active}` : ''}`
                  }
                >
                  <span className={styles.navIcon} aria-hidden="true">
                    <item.icon size={20} strokeWidth={1.5} />
                  </span>
                  <span className={styles.navLabel}>{item.label}</span>
                </NavLink>
              </li>
            ))}
          </ul>
        </div>
      </nav>

      <div className={styles.bottomSection}>
        <button
          className={styles.themeToggle}
          onClick={cycleTheme}
          aria-label={`Theme: ${theme}. Click to change.`}
        >
          <span className={styles.navIcon}>
            <ThemeIcon size={20} strokeWidth={1.5} />
          </span>
          <span className={styles.navLabel}>{theme.charAt(0).toUpperCase() + theme.slice(1)}</span>
        </button>

        <div className={styles.userRow}>
          <span className={styles.avatar}>{initial}</span>
          <span className={styles.userName}>{user?.display_name}</span>
        </div>

        <button
          className={styles.logoutButton}
          onClick={() => void logout()}
          aria-label="Log out"
        >
          <span className={styles.navIcon}>
            <LogOut size={20} strokeWidth={1.5} />
          </span>
          <span className={styles.navLabel}>Log out</span>
        </button>
      </div>
    </aside>
  );
}
