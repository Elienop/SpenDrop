/**
 * The surface-agnostic layer behind Settings' two navigation controls: the
 * desktop tab strip and the phone-width Select. Both render from these arrays,
 * so a section added here appears in both and there is deliberately no second
 * hand-maintained list to keep in sync.
 *
 * Same shape and the same reason as `components/nav-items.ts`. Nothing here
 * knows which surface is rendering it.
 */

/**
 * The tab values that survive in a `?tab=` bookmark. Order is the rendered
 * order on both surfaces.
 */
export const VALID_TABS = [
  'account',
  'currencies',
  'users',
  'api-tokens',
  'notifications',
  'data',
] as const;

export type SettingsTab = (typeof VALID_TABS)[number];

export function isValidTab(value: string | null): value is SettingsTab {
  return value !== null && (VALID_TABS as readonly string[]).includes(value);
}

export interface SettingsSection {
  value: SettingsTab;
  /**
   * A function rather than a string because one label narrows by role. The
   * VALUE never does — see `data` below.
   */
  label: (admin: boolean) => string;
  /** Hidden entirely from a member, control and panel alike. */
  adminOnly?: boolean;
}

export const SETTINGS_SECTIONS: readonly SettingsSection[] = [
  { value: 'account', label: () => 'Account' },
  { value: 'currencies', label: () => 'Currencies' },
  { value: 'users', label: () => 'Users', adminOnly: true },
  { value: 'api-tokens', label: () => 'API tokens' },
  { value: 'notifications', label: () => 'Notifications' },
  {
    // The VALUE stays "data" for both roles so existing `?tab=data` bookmarks
    // keep resolving; only the visible label narrows. Import is admin-only
    // (see `<ImportCard>`), and a member whose panel holds nothing but the
    // Export card should not be told the tab offers import.
    value: 'data',
    label: (admin) => (admin ? 'Import / Export' : 'Export'),
  },
];

/**
 * The sections a given role may actually open.
 *
 * Both surfaces MUST filter through this rather than each writing its own
 * `{admin && …}` guard. The phone Select is the reason it exists as a
 * function: a tab strip can drop a trigger inline and the panel simply never
 * mounts, but an option list that offers a section the user cannot open is a
 * dead end with no feedback — the value would be set and nothing would render.
 */
export function visibleSettingsSections(admin: boolean): SettingsSection[] {
  return SETTINGS_SECTIONS.filter((s) => admin || !s.adminOnly);
}

/**
 * The tab a given role may actually land on, given a raw `?tab=` value.
 *
 * THIS IS THE ROLE GATE FOR THE PHONE SURFACE, not a convenience. `isValidTab`
 * answers "is this one of the six values" and is deliberately role-blind, so it
 * cannot carry the gate: the desktop strip renders its panels by mapping over
 * `visibleSettingsSections(admin)`, which filters, but the phone renders ONE
 * section resolved from the raw value. Passing an unfiltered value there
 * mounted an `adminOnly` section's panel for a member — a member opening
 * `?tab=users` on a phone got the whole household-administration UI, every
 * control of which then failed against `RequireAdmin`. Backend refused the
 * data, so it was a control leak rather than a data leak, but the fix belongs
 * here at the value rather than at either render site: clamp once, where the
 * value enters state, and neither surface can drift from the other again.
 *
 * Falls back to `account` — the one section with no `adminOnly`, so it is
 * reachable by every role, and already the hard-coded default for an absent or
 * unrecognised `?tab=`.
 */
export function resolveSettingsTab(
  value: string | null,
  admin: boolean,
): SettingsTab {
  if (!isValidTab(value)) return 'account';
  const reachable = visibleSettingsSections(admin).some(
    (s) => s.value === value,
  );
  return reachable ? value : 'account';
}

/** The label for one tab value, or `undefined` if the role cannot see it. */
export function settingsSectionLabel(
  value: SettingsTab,
  admin: boolean,
): string | undefined {
  return visibleSettingsSections(admin)
    .find((s) => s.value === value)
    ?.label(admin);
}
