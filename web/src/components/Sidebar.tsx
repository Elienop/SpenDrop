import { NavLink } from 'react-router-dom';
import { useAuth } from '../hooks/useAuth';
import styles from '../styles/Sidebar.module.css';

const navItems = [
  { to: '/', label: 'Dashboard', icon: '\u{1F4CA}' },
  { to: '/reports', label: 'Reports', icon: '\u{1F4C8}' },
  { to: '/transactions', label: 'Transactions', icon: '\u{1F4B3}' },
  { to: '/categories', label: 'Categories', icon: '\u{1F3F7}\uFE0F' },
  { to: '/settings', label: 'Settings', icon: '\u2699\uFE0F' },
];

export function Sidebar() {
  const { user, logout } = useAuth();

  return (
    <aside className={styles.sidebar}>
      <div className={styles.logo}>
        <span className={styles.logoTitle}>SpenDrop</span>
      </div>

      <nav className={styles.nav} aria-label="Main navigation">
        <ul className={styles.navList}>
          {navItems.map((item) => (
            <li key={item.to}>
              <NavLink
                to={item.to}
                end={item.to === '/'}
                className={({ isActive }) =>
                  `${styles.navLink}${isActive ? ` ${styles.active}` : ''}`
                }
              >
                <span className={styles.navIcon} aria-hidden="true">
                  {item.icon}
                </span>
                {item.label}
              </NavLink>
            </li>
          ))}
        </ul>
      </nav>

      <div className={styles.userSection}>
        <span className={styles.userName}>{user?.display_name}</span>
        <button
          className={styles.logoutButton}
          onClick={() => void logout()}
          aria-label="Log out"
        >
          Log out
        </button>
      </div>
    </aside>
  );
}
