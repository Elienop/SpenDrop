import type { ElementType } from 'react';
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
} from 'lucide-react';

/**
 * The surface-agnostic layer behind the two navigation components: the
 * desktop `Sidebar` and the phone-width `MobileNav` drawer. Destinations,
 * badge presentation and the avatar initial all live here, so anything both
 * surfaces must agree on has exactly one definition. Nothing in this module
 * knows which surface is rendering it.
 */

/**
 * A navigation destination. Both surfaces render the same arrays — a
 * destination added here appears in both, and there is deliberately no
 * second hand-maintained list to keep in sync.
 */
export interface MenuItem {
  path: string;
  label: string;
  icon: ElementType;
  end?: boolean;
  /**
   * Optional numeric badge rendered next to the label (expanded
   * sidebar / mobile drawer only). When undefined or 0, no badge is
   * drawn — the navigation stays calm by default.
   */
  badge?: number;
}

// Flat top-section nav. Reports moves between Transactions and Budgets
// (was after Savings) — Reports is a daily-review surface and the older
// Menu/Admin/General triple-grouping (with hidden titles in collapsed
// mode) gave inconsistent vertical rhythm across modes. A single
// Separator below splits this section from the bottom-section
// Settings + Log out without touching icon spacing.
export const menuItems: MenuItem[] = [
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
export const bottomItems: MenuItem[] = [
  { path: '/settings', label: 'Settings', icon: SettingsIcon },
];

/**
 * Recovery surface, visible to every role since B5 (members see and restore
 * only their own rows; admins keep purge). Built from the live trash count
 * rather than declared as a constant, so both nav surfaces show the same
 * badge. Callers wrap this in `useMemo` keyed on the count so the array
 * reference stays stable across unrelated renders.
 */
export function buildTrashItems(trashCount: number): MenuItem[] {
  return [{ path: '/trash', label: 'Trash', icon: Trash2, badge: trashCount }];
}

/**
 * Visible badge value, capped at "99+" so a 3+ digit count can't widen the
 * pill and push the row out of alignment. Screen readers get the real number
 * from `navItemAriaLabel` instead.
 */
export function formatBadgeText(badge: number | undefined): string | undefined {
  if (badge === undefined) return undefined;
  return badge > 99 ? '99+' : String(badge);
}

/**
 * Accessible name for a badged nav item. Overrides the link's text-derived
 * name so SR users hear "Trash, 7 items" instead of "Trash 7" or the capped
 * "Trash 99+". Returns undefined for unbadged items so they keep their
 * natural text name.
 */
export function navItemAriaLabel(item: MenuItem): string | undefined {
  if (item.badge === undefined || item.badge <= 0) return undefined;
  return `${item.label}, ${item.badge} item${item.badge === 1 ? '' : 's'}`;
}

/**
 * The numeric badge pill, shared verbatim by both surfaces.
 *
 * An inline-styled span rather than shadcn `Badge variant="secondary"`: on
 * the active row (`bg-muted`) the secondary variant blends into the
 * background. A solid `bg-primary` + `text-primary-foreground` follows the
 * user's ColorThemePicker choice (Violet/Blue/…) AND keeps the number
 * readable — a soft `bg-primary/15` tint left the same-hue `text-primary`
 * without enough contrast.
 */
export const navBadgeClass =
  'ml-auto inline-flex h-5 min-w-5 items-center justify-center rounded-full bg-primary px-1.5 pt-px text-xs font-medium leading-none tabular-nums text-primary-foreground';

/**
 * Single uppercase character for the account avatar, or '?' when there is no
 * name to take one from.
 *
 * Indexes by CODE POINT (`[...name]`) rather than `name[0]`, which returns a
 * lone surrogate for any name starting outside the BMP — an emoji or a
 * mathematical letter renders as U+FFFD in the avatar. Note this is still not
 * grapheme-aware: a flag or a ZWJ sequence is several code points and only
 * the first survives. `Intl.Segmenter` would be the complete fix if that
 * turns out to matter for a real display name.
 */
export function avatarInitial(displayName: string | undefined): string {
  return [...(displayName ?? '')][0]?.toUpperCase() ?? '?';
}
