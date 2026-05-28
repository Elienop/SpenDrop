import { useState, useEffect, useMemo, useRef } from 'react';
import { NavLink, useLocation } from 'react-router-dom';
import {
  Zap,
  LayoutGrid,
  ArrowLeftRight,
  Wallet,
  PiggyBank,
  ChartNoAxesColumnIncreasing,
  Tag,
  Settings as SettingsIcon,
  Trash2,
  ChevronLeft,
  LogOut,
} from 'lucide-react';
import { useAuth } from '../hooks/useAuth';
import { useTrashCount } from '../hooks/useTrashCount';
import { isAdmin } from '@/lib/roles';
import { Logo } from '@/components/Logo';
import { LogoWordmark } from '@/components/LogoWordmark';
import { Separator } from '@/components/ui/separator';
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from '@/components/ui/tooltip';
import { Avatar, AvatarFallback } from '@/components/ui/avatar';
import { ModeToggle } from '@/components/ModeToggle';
import { ColorThemePicker } from '@/components/ColorThemePicker';
import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';
import { STORAGE_KEYS } from '@/lib/storage-keys';

interface MenuItem {
  path: string;
  label: string;
  icon: React.ElementType;
  end?: boolean;
  /** When true, the item is only rendered for admin users. */
  adminOnly?: boolean;
  /**
   * Optional numeric badge rendered next to the label (expanded
   * sidebar only). When undefined or 0, no badge is drawn — the
   * sidebar stays calm by default.
   */
  badge?: number;
}

// Flat top-section nav. Reports moves between Transactions and Budgets
// (was after Savings) — Reports is a daily-review surface and the older
// Menu/Admin/General triple-grouping (with hidden titles in collapsed
// mode) gave inconsistent vertical rhythm across modes. A single
// Separator below splits this section from the bottom-section
// Settings + Log out without touching icon spacing.
const menuItems: MenuItem[] = [
  // Fast-capture entry. Routes to the full-screen /quick screen, which lives
  // OUTSIDE AppShell (no sidebar) — the "Full app" link there returns here.
  { path: '/quick', label: 'Quick add', icon: Zap },
  { path: '/', label: 'Dashboard', icon: LayoutGrid, end: true },
  { path: '/transactions', label: 'Transactions', icon: ArrowLeftRight },
  { path: '/reports', label: 'Reports', icon: ChartNoAxesColumnIncreasing },
  { path: '/budgets', label: 'Budgets', icon: Wallet },
  { path: '/savings', label: 'Savings', icon: PiggyBank },
  { path: '/categories', label: 'Categories', icon: Tag },
];

// Settings lives in the bottom section, alongside the Log out button.
// Kept as a separate constant so future bottom-section items (e.g. a
// What's New link) slot in cleanly without restructuring the JSX.
const bottomItems: MenuItem[] = [
  { path: '/settings', label: 'Settings', icon: SettingsIcon },
];

export function Sidebar() {
  const { user, logout } = useAuth();
  const admin = isAdmin(user);
  // Tombstoned-transaction count for the Trash badge. Gated on
  // `admin` so non-admin sessions don't issue a 403. The hook is
  // safe to call unconditionally — it bails internally when
  // `enabled` is false.
  const { count: trashCount } = useTrashCount(admin);

  // Admin-only recovery surface. Kept in its own group below the main
  // menu so it sits visually adjacent to Settings — both are operator
  // tools — rather than mixing it into the day-to-day navigation and
  // cluttering the sidebar for members who never see it.
  //
  // Computed inside the component (vs module scope) because the Trash
  // badge needs the live `trashCount` from the hook. `useMemo` keeps
  // the array reference stable across renders that don't change the
  // count — guards against a future `React.memo` on `SidebarLink`
  // silently busting on a fresh-each-render `item` prop.
  const adminItems: MenuItem[] = useMemo(
    () => [
      {
        path: '/trash',
        label: 'Trash',
        icon: Trash2,
        adminOnly: true,
        badge: trashCount,
      },
    ],
    [trashCount],
  );
  // Hide the admin section entirely for non-admins so a member never
  // even has the link rendered in the DOM. The backend still enforces
  // 403 on the underlying routes — this is purely UX cleanup. Every
  // item in `adminItems` is currently admin-only, so the `adminOnly`
  // flag on `MenuItem` is kept as a forward-compatible hook in case a
  // future entry in this section should be visible to members.
  const visibleAdminItems = admin ? adminItems : [];
  const [expanded, setExpanded] = useState(
    () => localStorage.getItem(STORAGE_KEYS.sidebar) === 'true',
  );

  const didMountRef = useRef(false);
  useEffect(() => {
    if (!didMountRef.current) {
      didMountRef.current = true;
      return;
    }
    localStorage.setItem(STORAGE_KEYS.sidebar, String(expanded));
    window.dispatchEvent(new Event('sidebar-toggle'));
  }, [expanded]);

  const initial = user?.display_name?.[0]?.toUpperCase() ?? '?';

  return (
    <TooltipProvider delayDuration={200}>
      <aside
        role="complementary"
        className={cn(
          'fixed left-0 top-0 flex h-screen flex-col border-r border-border bg-card transition-[width] duration-200 ease-linear',
          expanded ? 'w-60' : 'w-12',
        )}
      >
        {/* Header — matches SidebarHeader: gap-2 p-2 */}
        <div
          className={cn(
            'flex h-14 shrink-0 items-center border-b border-border p-2',
            expanded ? 'justify-between px-4' : 'justify-center',
          )}
        >
          {expanded ? (
            <>
              <LogoWordmark className="h-6" />
              <Button
                variant="ghost"
                size="icon"
                aria-label="Toggle sidebar"
                aria-expanded={expanded}
                onClick={() => setExpanded((v) => !v)}
                className="size-8 text-muted-foreground"
              >
                <ChevronLeft className="h-4 w-4" />
              </Button>
            </>
          ) : (
            <Button
              variant="ghost"
              size="icon"
              aria-label="Toggle sidebar"
              aria-expanded={expanded}
              onClick={() => setExpanded((v) => !v)}
              className="size-8 text-muted-foreground"
            >
              <Logo className="size-5" />
              <span className="sr-only">SpenDrop</span>
            </Button>
          )}
        </div>

        {/* Content — matches SidebarContent: gap-2, overflow-hidden when collapsed */}
        <nav
          className={cn(
            'flex min-h-0 flex-1 flex-col gap-2',
            expanded ? 'overflow-auto' : 'overflow-hidden',
          )}
          aria-label="Primary"
        >
          {/*
            Top section — flat list of all primary nav items plus the
            admin-only Trash entry (rendered inline; no section title or
            separate group). The previous Menu/Admin/General grouping
            hid its section titles in collapsed mode but still consumed
            vertical rhythm, making the icon column look uneven across
            states.
          */}
          <div className={cn('flex flex-col gap-0.5 p-2', !expanded && 'items-center')}>
            {menuItems.map((item) => (
              <SidebarLink key={item.path} item={item} expanded={expanded} />
            ))}
            {visibleAdminItems.map((item) => (
              <SidebarLink key={item.path} item={item} expanded={expanded} />
            ))}
          </div>

          {/*
            Wrapper carries the horizontal inset; the Separator itself is
            full-width inside it. Putting `mx-2` directly on Separator gives
            `w-full + 8px*2` of margin, which overflows the 240px sidebar
            and pops a horizontal scrollbar in the nav.
          */}
          <div className="px-2">
            <Separator />
          </div>

          {/*
            Bottom section — Settings + Log out. Pinned visually
            beneath the divider, mirroring the canonical sidebar
            "primary nav up top, app controls down bottom" pattern.
          */}
          <div className={cn('flex flex-col gap-0.5 p-2', !expanded && 'items-center')}>
            {bottomItems.map((item) => (
              <SidebarLink key={item.path} item={item} expanded={expanded} />
            ))}
            <Tooltip>
              <TooltipTrigger asChild>
                <Button
                  variant="ghost"
                  onClick={() => void logout()}
                  className={cn(
                    'flex items-center overflow-hidden text-muted-foreground [&>svg]:size-4 [&>svg]:shrink-0',
                    expanded ? 'w-full justify-start gap-2 px-3 py-2' : 'size-8 p-2',
                  )}
                >
                  <LogOut aria-hidden="true" />
                  <span className={expanded ? 'truncate' : 'sr-only'}>
                    Log out
                  </span>
                </Button>
              </TooltipTrigger>
              {!expanded && (
                <TooltipContent side="right">Log out</TooltipContent>
              )}
            </Tooltip>
          </div>
        </nav>

        {/* Color theme select — above footer */}
        {expanded && (
          <div className="px-3 py-3">
            <ColorThemePicker />
          </div>
        )}

        {/* Footer — matches SidebarFooter: gap-2 p-2 */}
        <div
          className={cn(
            'flex items-center border-t border-border p-2',
            expanded ? 'gap-3 px-3 py-3' : 'flex-col gap-2 py-3',
          )}
        >
          <Avatar className="size-8 text-sm font-medium">
            <AvatarFallback>{initial}</AvatarFallback>
          </Avatar>
          {expanded && user && (
            <div className="min-w-0 flex-1">
              <p className="truncate text-sm font-medium">
                {user.display_name}
              </p>
              <p className="truncate text-xs text-muted-foreground">
                @{user.username}
              </p>
            </div>
          )}
          <ModeToggle />
        </div>
      </aside>
    </TooltipProvider>
  );
}

function SidebarLink({
  item,
  expanded,
}: {
  item: MenuItem;
  expanded: boolean;
}) {
  const Icon = item.icon;
  const hasBadge = item.badge !== undefined && item.badge > 0;
  // Numeric pill only renders in the expanded sidebar; in collapsed
  // mode a tiny dot lives on the icon instead (no room for a number).
  const showPill = expanded && hasBadge;
  const showDot = !expanded && hasBadge;
  // Visible value is capped at "99+" so a 3+ digit count can't widen
  // the pill and push the row out of alignment. aria-label below
  // keeps the real number so screen readers stay accurate.
  const displayBadge =
    item.badge !== undefined && item.badge > 99 ? '99+' : item.badge;
  // aria-label overrides the link's text-derived accessible name so
  // SR users hear "Trash, 7 items" instead of "Trash 7" or "Trash
  // 99+". Active in BOTH collapsed and expanded modes so SR users
  // get the count regardless of sidebar state.
  const ariaLabel = hasBadge
    ? `${item.label}, ${item.badge} item${item.badge === 1 ? '' : 's'}`
    : undefined;
  // Compute isActive ourselves rather than via NavLink's className function
  // form. The function-form pattern (`className={({ isActive }) => ...}`)
  // does not survive Vite's prod minification on this stack — the function
  // is stringified into the class attribute literally instead of being
  // called, so `relative` (which anchors the collapsed-mode dot) and the
  // active-route highlight both silently fail in production. Passing a
  // plain string keeps it deterministic.
  const { pathname } = useLocation();
  const isActive = item.end
    ? pathname === item.path
    : pathname === item.path || pathname.startsWith(item.path + '/');
  const linkClassName = cn(
    // `relative` anchors the absolute-positioned collapsed-mode dot.
    'relative flex items-center overflow-hidden rounded-md text-sm text-muted-foreground hover:bg-muted hover:text-foreground [&>svg]:size-4 [&>svg]:shrink-0',
    isActive && 'bg-muted text-foreground',
    expanded ? 'w-full gap-2 px-3 py-2' : '!size-8 p-2 justify-center',
  );
  const link = (
    <NavLink
      to={item.path}
      end={item.end}
      aria-label={ariaLabel}
      className={linkClassName}
    >
      <Icon aria-hidden="true" />
      <span className={expanded ? 'truncate' : 'sr-only'}>
        {item.label}
      </span>
      {showPill && (
        // Inline styled span rather than shadcn `Badge variant="secondary"`:
        // on the active row (`bg-muted`) the secondary variant blends in.
        // Using the primary accent at 15% gives a soft theme-tinted pill
        // that stays distinct on both active and inactive rows AND tracks
        // the user's ColorThemePicker choice (Violet/Blue/etc.) — the
        // pill picks up the same hue as the active-route highlight in
        // the rest of the app.
        <span className="ml-auto inline-flex h-5 min-w-[1.25rem] items-center justify-center rounded-md bg-primary/15 px-1.5 text-xs font-medium tabular-nums text-primary">
          {displayBadge}
        </span>
      )}
      {showDot && (
        // Collapsed-mode "has content" indicator. Uses the primary accent
        // so it tracks the user's chosen theme (Violet/Blue/etc.) instead
        // of being a hardcoded red — matches the pill's hue above. The
        // `ring-2 ring-card` halo keeps the dot legible against the
        // sidebar background in any theme.
        <span
          data-testid="trash-dot"
          aria-hidden="true"
          className="pointer-events-none absolute right-1 top-1 h-2 w-2 rounded-full bg-primary ring-2 ring-card"
        />
      )}
    </NavLink>
  );
  if (expanded) return link;
  return (
    <Tooltip>
      <TooltipTrigger asChild>{link}</TooltipTrigger>
      <TooltipContent side="right">{item.label}</TooltipContent>
    </Tooltip>
  );
}
