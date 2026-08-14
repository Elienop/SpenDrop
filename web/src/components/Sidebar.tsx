import { useState, useEffect, useMemo, useRef } from 'react';
import { NavLink, useLocation } from 'react-router-dom';
import { ChevronLeft, LogOut } from 'lucide-react';
import { useAuth } from '../hooks/useAuth';
import { useTrashCount } from '../hooks/useTrashCount';
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
import { TOUCH_TARGET_SQUARE } from '@/lib/touch-target';
import { STORAGE_KEYS } from '@/lib/storage-keys';
import {
  type MenuItem,
  menuItems,
  bottomItems,
  buildTrashItems,
  formatBadgeText,
  navItemAriaLabel,
  navBadgeClass,
  avatarInitial,
} from '@/components/nav-items';

export function Sidebar() {
  const { user, logout } = useAuth();
  // Tombstoned-transaction count for the Trash badge. The backend scopes
  // the total to the caller's own rows for members (household-wide for
  // admins), so the badge is per-role for free. Enabled whenever a user
  // is present — the endpoint stopped being admin-only in B5.
  const { count: trashCount } = useTrashCount(user != null);

  // Recovery surface, visible to every role since B5 (members see and
  // restore only their own rows; admins keep purge). Kept in its own
  // group below the main menu so it sits visually adjacent to Settings —
  // both are housekeeping rather than day-to-day navigation.
  //
  // Built inside the component (vs module scope) because the Trash badge
  // needs the live `trashCount` from the hook. `useMemo` keeps the array
  // reference stable across renders that don't change the count — guards
  // against a future `React.memo` on `SidebarLink` silently busting on a
  // fresh-each-render `item` prop.
  const trashItems: MenuItem[] = useMemo(
    () => buildTrashItems(trashCount),
    [trashCount],
  );
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

  const initial = avatarInitial(user?.display_name);

  return (
    <TooltipProvider delayDuration={200}>
      {/*
        Desktop-only surface. Below `md` the fixed aside would eat 48–240px
        of a 390px viewport, so it is removed from layout entirely and
        `MobileNav` (a top bar + slide-out drawer) takes over. `hidden`
        also drops it from the accessibility tree, so a phone never sees
        two copies of the same navigation — and the persisted
        expanded/collapsed state below cannot reach phone layout, because
        every width and padding class that reads it is `md:`-gated (here
        and in AppShell).
      */}
      <aside
        role="complementary"
        className={cn(
          'fixed left-0 top-0 hidden h-screen flex-col border-r border-border bg-card transition-[width] duration-200 ease-linear md:flex',
          expanded ? 'w-60' : 'w-12',
        )}
      >
        {/*
          Header — explicit p-4 so the logo (and the slightly taller
          collapse toggle, size-8 = 32px) sit with 16px above and
          below, matching the 16px rhythm we use for the top-section
          first item, the section separator's neighbours, and the
          footer below.
        */}
        <div
          className={cn(
            'flex shrink-0 items-center border-b border-border p-4',
            expanded ? 'justify-between' : 'justify-center',
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

        {/* Content — matches SidebarContent: gap-2, x-clipped when collapsed */}
        <nav
          className={cn(
            'flex min-h-0 flex-1 flex-col gap-2',
            // Collapsed, the y-axis SCROLLS and only the x-axis clips. Both
            // axes used to be `hidden`, which cost nothing while the rail's
            // rows were 32px — the column could not outgrow a landscape
            // viewport. At the 44px coarse floor the nine rows and the footer
            // add ~120px (arithmetic, not measured — the browser pass has the
            // real number), which is enough to overflow the household tablet
            // once a URL bar is taking its share, and `hidden` does not scroll:
            // it would cut Settings and Log out off the bottom with no way to
            // reach either.
            //
            // Clipping X is still wanted — the aside animates its width, so a
            // row is briefly wider than the rail containing it. `overflow-y-*`
            // on its own would not keep that: CSS promotes the other axis from
            // `visible` to `auto`, which is the horizontal scrollbar this
            // avoids. Both axes have to be named.
            expanded ? 'overflow-auto' : 'overflow-x-hidden overflow-y-auto',
          )}
          aria-label="Primary"
        >
          {/*
            Top section — flat list of all primary nav items plus the
            Trash entry, visible to every role since B5 (rendered inline;
            no section title or separate group). The previous
            Menu/Admin/General grouping hid its section titles in
            collapsed mode but still consumed vertical rhythm, making the
            icon column look uneven across states.
          */}
          {/*
            `pt-4` above the first nav item so the space below the header
            border matches the 16px gap between the last item and the
            section Separator (`p-2` bottom 8px + nav `gap-2` 8px).
            Without this the header sat noticeably closer to Quick add
            than the separator sat to Trash.
          */}
          <div
            className={cn(
              'flex flex-col gap-0.5 px-2 pb-2 pt-4',
              !expanded && 'items-center',
            )}
          >
            {menuItems.map((item) => (
              <SidebarLink key={item.path} item={item} expanded={expanded} />
            ))}
            {trashItems.map((item) => (
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
                    // font-normal: Log out is a nav row, not an emphasis
                    // control, and the Button variant's font-medium made it
                    // read heavier than the links above it. Matches the
                    // mobile drawer's copy of this same control.
                    'flex items-center overflow-hidden font-normal text-muted-foreground [&>svg]:size-4 [&>svg]:shrink-0',
                    // Collapsed, this is a square icon button that never says
                    // `size="icon"`, so Button's floor gives it height only —
                    // 44x32 on the tablet, taller but still a miss. The width
                    // half has to come from the call site; see
                    // `@/lib/touch-target`. Expanded it is a full-width row and
                    // the min-width is inert.
                    expanded
                      ? 'w-full justify-start gap-2 px-3 py-2'
                      : cn('size-8 p-2', TOUCH_TARGET_SQUARE),
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

        {/*
          Color theme select — above footer. Slightly more vertical
          padding (py-5 = 20px vs the standard 16px elsewhere) because
          the Select trigger and the avatar row below have shorter
          content than the nav rows; the extra 4px lets these two
          bottom blocks read at the same visual weight as the header
          and the nav sections.
        */}
        {expanded && (
          <div className="px-4 py-5">
            <ColorThemePicker />
          </div>
        )}

        {/* Footer — same py-5 px-4 as the theme picker above. */}
        <div
          className={cn(
            'flex items-center border-t border-border px-4 py-5',
            expanded ? 'gap-3' : 'flex-col gap-2',
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
  // Capped visible value and the un-capped screen-reader name both come
  // from nav-items.ts, so the desktop sidebar and the mobile drawer can
  // never drift on this contract. The aria-label is active in BOTH
  // collapsed and expanded modes so SR users get the count regardless of
  // sidebar state.
  const displayBadge = formatBadgeText(item.badge);
  const ariaLabel = navItemAriaLabel(item);
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
    //
    // `coarse:min-h-11` sits on the base rather than in either branch, mirroring
    // where `Button` keeps its own height floor. A NavLink is a raw anchor, so
    // no primitive supplies one and this is the only place it can come from —
    // and a branch added later inherits it instead of having to remember.
    // `coarse:` and not `md:`: this column only renders from `md` up, which is
    // precisely why the tablet was the surface still missing the floor (~1130px
    // in landscape takes the desktop side of every width gate while a finger is
    // still doing the tapping). A mouse keeps the 36px expanded row and the
    // 32px collapsed square at every width. See `@/lib/touch-target`.
    'relative flex items-center overflow-hidden rounded-md text-sm text-muted-foreground coarse:min-h-11 hover:bg-muted hover:text-foreground [&>svg]:size-4 [&>svg]:shrink-0',
    isActive && 'bg-muted text-foreground',
    expanded
      ? // Full-width row, so the base height floor is the whole story here and
        // a width floor would be inert.
        'w-full gap-2 px-3 py-2'
      : // Collapsed the row is a square, so it needs the width half too — a
        // height-only floor leaves a 44x32 target that is taller and still a
        // miss for a thumb. `min-width` clamps the used width up even against
        // the `!important` on `!size-8`: min/max clamping is applied after the
        // cascade and the two are separate properties. 44px still fits inside
        // the 48px `w-12` rail, so nothing needs to move to make room.
        cn('!size-8 p-2 justify-center', TOUCH_TARGET_SQUARE),
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
        // Pill styling lives in nav-items.ts — the mobile drawer draws the
        // same badge and the two must not drift.
        <span className={navBadgeClass}>{displayBadge}</span>
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
      <TooltipContent side="right">
        {hasBadge ? `${item.label} · ${displayBadge}` : item.label}
      </TooltipContent>
    </Tooltip>
  );
}
