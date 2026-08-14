import { useEffect, useMemo, useState } from 'react';
import { Link, NavLink, useLocation } from 'react-router-dom';
import { Menu, LogOut } from 'lucide-react';
import { useAuth } from '../hooks/useAuth';
import { useTrashCount } from '../hooks/useTrashCount';
import { LogoWordmark } from '@/components/LogoWordmark';
import { Avatar, AvatarFallback } from '@/components/ui/avatar';
import { Button } from '@/components/ui/button';
import { Separator } from '@/components/ui/separator';
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
  SheetTrigger,
} from '@/components/ui/sheet';
import { ModeToggle } from '@/components/ModeToggle';
import { ColorThemePicker } from '@/components/ColorThemePicker';
import { cn } from '@/lib/utils';
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

/**
 * Mirrors Tailwind's `md` breakpoint, which is where `Sidebar` becomes
 * visible and the top bar below goes `md:hidden`. Kept as one constant so
 * the JS-side and CSS-side notions of "desktop" cannot drift.
 */
const DESKTOP_QUERY = '(min-width: 768px)';

/**
 * Phone-width navigation: a sticky top bar whose single control opens a
 * left slide-out drawer. Replaces the desktop `Sidebar`, which is
 * `hidden` below `md` because its fixed 48–240px column leaves only
 * 262px (or 70px when expanded) of a 390px viewport for content.
 *
 * Everything the sidebar offers is here, driven by the same
 * `nav-items.ts` arrays — every row carries a VISIBLE text label, because
 * the sidebar's collapsed icon-only mode leans on Tooltips, and Radix
 * tooltips never open on a touch device.
 *
 * The drawer deliberately reads none of the persisted
 * `spendrop-sidebar` expanded/collapsed state: that value describes a
 * surface a phone never sees, and a device where it happens to be 'true'
 * must not render differently here.
 */
export function MobileNav() {
  const { user, logout } = useAuth();
  const { pathname } = useLocation();

  /*
    The drawer is open only for the route it was opened on, so ANY route
    change closes it — including ones this component never sees, like
    Android's back button popping history while the Sheet is still mounted.
    Storing the route instead of a boolean makes that fall out of rendering
    rather than needing an effect to chase `pathname` after the fact.

    `null` cannot collide with a real value: a router pathname is always a
    non-empty string beginning with '/'.

    Tapping the row for the CURRENT route is the one close that this does not
    cover (the pathname does not change), which is why the rows still call
    `closeDrawer` on navigate.
  */
  const [openedAt, setOpenedAt] = useState<string | null>(null);
  const open = openedAt === pathname;
  const setOpen = (next: boolean) => {
    setOpenedAt(next ? pathname : null);
  };

  // Rotating a phone to landscape (or unfolding a foldable) can cross `md`,
  // which reveals the desktop sidebar — leaving this drawer stranded on top
  // of it. Only the crossing INTO desktop closes it; shrinking back to phone
  // width must not, or the query would fight the user's own tap.
  useEffect(() => {
    if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') {
      return;
    }
    const mql = window.matchMedia(DESKTOP_QUERY);
    const onChange = (e: MediaQueryListEvent) => {
      if (e.matches) setOpenedAt(null);
    };
    mql.addEventListener('change', onChange);
    return () => mql.removeEventListener('change', onChange);
  }, []);

  // Same query key as the sidebar's copy, so the two mounts share one
  // TanStack cache entry (and one request) rather than double-fetching.
  const { count: trashCount } = useTrashCount(user != null);
  const trashItems: MenuItem[] = useMemo(
    () => buildTrashItems(trashCount),
    [trashCount],
  );

  const initial = avatarInitial(user?.display_name);

  const closeDrawer = () => {
    setOpenedAt(null);
  };

  return (
    <Sheet open={open} onOpenChange={setOpen}>
      {/*
        Sticky rather than fixed: in normal flow the bar contributes its
        own height, so no page needs a matching top offset — and at `md`
        it is display:none, contributing nothing at all.
      */}
      <header className="sticky top-0 z-40 flex h-14 shrink-0 items-center gap-1 border-b border-border bg-background/95 px-2 backdrop-blur supports-[backdrop-filter]:bg-background/80 md:hidden">
        <SheetTrigger asChild>
          {/*
            size-11 (44px) rather than the icon variant's 40px — this is
            the only way into navigation on a phone, so it gets a full
            touch target. Radix supplies aria-expanded / aria-haspopup.
          */}
          <Button
            variant="ghost"
            size="icon"
            aria-label="Open navigation menu"
            className="size-11 text-muted-foreground"
          >
            <Menu className="size-5" />
          </Button>
        </SheetTrigger>
        {/*
          The wordmark is the expected way home on a phone. LogoWordmark's
          svg is aria-hidden decoration, so the link carries the whole
          accessible name itself. It stays keyboard-reachable — a home link
          that cannot be focused fails WCAG 2.1.1 — and sits after the menu
          button so the primary control is still the first tab stop.
        */}
        <Link
          to="/"
          aria-label="SpenDrop dashboard"
          className="flex min-h-11 items-center rounded-md px-1 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background"
        >
          <LogoWordmark className="h-6" />
        </Link>
      </header>

      {/*
        `gap-0` + `p-0` replace the sheet variant's own spacing so each
        section below can own its padding and draw a full-bleed border. With
        `gap-0` the header sits flush against the scroller exactly as it did
        when it was the scroller's first child: the body's `-m-1 p-1` cancel,
        so moving the header out of the scroller moves nothing on screen.
      */}
      <SheetContent
        side="left"
        className="w-72 gap-0 bg-card p-0 sm:max-w-none"
        // Outside the body scroller, not a child of it. The drawer's own
        // content is ten 44px rows plus the appearance controls — a little
        // over 600px before the header — so it scrolls on a 360x780 phone as
        // soon as the browser's URL bar is showing, and in any landscape.
        // Until now the bordered block naming who is signed in scrolled with
        // it, which is the version of this defect a household member meets
        // most often: this drawer is the only way into navigation on a phone.
        //
        // `space-y-0` is not redundant: SheetHeader's own base carries
        // `space-y-2`, and tailwind-merge treats space-y and gap as separate
        // groups, so `gap-3` alone would stack a 12px gap on top of an 8px
        // margin.
        header={
          /*
            `relative bg-card` is what stops the body painting over this
            header's bottom edge, and both halves are load-bearing.

            The overlap is real and specific to `gap-0`: the body scroller
            carries `-m-1` (it widens its clip box so focus rings on edge
            children survive), and with no gap to absorb it that pulls the
            scroller's PADDING box 4px up, over this header's bottom 4px —
            the `border-b` line included. Overflow clips at the padding box,
            so scrolled rows can paint in that strip, and the scroller comes
            later in the DOM, so they paint on top. The other three sheets
            keep their `-4px` inside a `gap-4`/`gap-6` and never see it.

            `relative` moves this header into the positioned-descendants
            paint layer, above the scroller's in-flow content. `bg-card`
            makes it opaque in the SAME surface token SheetContent sets
            above — positioned but transparent, the strip still shows
            through.

            NO z-index, deliberately. SheetContent's own Close button is
            `absolute right-4 top-4`, i.e. inside this header's box, and it
            is positioned LATER in the DOM — so at equal (auto) z-index it
            paints above this header and stays tappable. Any positive z-index
            here would bury the drawer's X behind the header and swallow its
            taps.
          */
          <SheetHeader className="relative gap-3 space-y-0 border-b border-border bg-card p-4 text-left">
            <SheetTitle className="flex items-center">
              <LogoWordmark className="h-6" />
              <span className="sr-only">SpenDrop navigation</span>
            </SheetTitle>
            <SheetDescription className="sr-only">
              Jump to a section of SpenDrop, or sign out.
            </SheetDescription>
            <div className="flex items-center gap-3">
              <Avatar className="size-8 text-sm font-medium">
                <AvatarFallback>{initial}</AvatarFallback>
              </Avatar>
              {user && (
                <div className="min-w-0 flex-1">
                  <p className="truncate text-sm font-medium">
                    {user.display_name}
                  </p>
                  <p className="truncate text-xs text-muted-foreground">
                    @{user.username}
                  </p>
                </div>
              )}
            </div>
          </SheetHeader>
        }
      >
        <nav aria-label="Primary" className="flex flex-col gap-2 py-2">
          <div className="flex flex-col gap-0.5 px-2">
            {menuItems.map((item) => (
              <MobileNavLink
                key={item.path}
                item={item}
                onNavigate={closeDrawer}
              />
            ))}
            {trashItems.map((item) => (
              <MobileNavLink
                key={item.path}
                item={item}
                onNavigate={closeDrawer}
              />
            ))}
          </div>

          <div className="px-2">
            <Separator />
          </div>

          <div className="flex flex-col gap-0.5 px-2">
            {bottomItems.map((item) => (
              <MobileNavLink
                key={item.path}
                item={item}
                onNavigate={closeDrawer}
              />
            ))}
            <Button
              variant="ghost"
              onClick={() => {
                closeDrawer();
                void logout();
              }}
              className="min-h-11 w-full justify-start gap-3 px-3 py-2 text-sm font-normal text-muted-foreground [&>svg]:size-5"
            >
              <LogOut aria-hidden="true" />
              Log out
            </Button>
          </div>
        </nav>

        {/*
          Appearance controls close the list rather than sitting in a pinned
          footer — outside <nav> because neither one navigates anywhere.
        */}
        <div className="flex flex-col gap-2 px-2 pb-2">
          <Separator />
          {/*
            Both controls are pushed to the 44px floor — at their desktop
            sizes (32px select, 40px button) they were the two smallest
            targets on a surface that is only ever touched.
          */}
          <div className="flex items-center gap-2 px-1">
            <div className="min-w-0 flex-1">
              <ColorThemePicker className="h-11 text-sm" />
            </div>
            <ModeToggle className="size-11" />
          </div>
        </div>
      </SheetContent>
    </Sheet>
  );
}

function MobileNavLink({
  item,
  onNavigate,
}: {
  item: MenuItem;
  onNavigate: () => void;
}) {
  const Icon = item.icon;
  const displayBadge = formatBadgeText(item.badge);
  const hasBadge = item.badge !== undefined && item.badge > 0;
  // Same reason as SidebarLink: NavLink's className function form is
  // stringified rather than called under Vite's prod minification on this
  // stack, so isActive is computed here and passed as a plain string.
  const { pathname } = useLocation();
  const isActive = item.end
    ? pathname === item.path
    : pathname === item.path || pathname.startsWith(item.path + '/');
  return (
    <NavLink
      to={item.path}
      end={item.end}
      aria-label={navItemAriaLabel(item)}
      onClick={onNavigate}
      className={cn(
        // min-h-11 gives every row a 44px touch target; the icon is one
        // step larger than the desktop sidebar's for the same reason.
        'flex min-h-11 items-center gap-3 rounded-md px-3 py-2 text-sm text-muted-foreground hover:bg-muted hover:text-foreground [&>svg]:size-5 [&>svg]:shrink-0',
        isActive && 'bg-muted text-foreground',
      )}
    >
      <Icon aria-hidden="true" />
      <span className="truncate">{item.label}</span>
      {hasBadge && <span className={navBadgeClass}>{displayBadge}</span>}
    </NavLink>
  );
}
