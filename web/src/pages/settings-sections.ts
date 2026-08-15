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
  /**
   * Hidden entirely from a member, control and panel alike.
   *
   * NO SECTION USES THIS TODAY, and that is a decision rather than an
   * oversight. `adminOnly` is all-or-nothing by construction, so it cannot
   * express "this section minus one card" — which is what both remaining
   * admin-gated surfaces need: `data` shows a member the Export card without
   * the Import one, and `account` shows a member their own card without the
   * household table. Every such gate therefore sits inside the panel.
   *
   * The mechanism is kept because the NEXT admin-only section (a whole panel a
   * member has no card in) is one line away, and the phone surface needs the
   * clamp in `resolveSettingsTab` the moment that line is written — it renders
   * ONE section resolved from a raw `?tab=` value rather than mapping over the
   * filtered list. Deleting the field would take the clamp with it and leave
   * that leak to be re-derived. Both are pinned against an injected fixture in
   * `settings-sections.test.ts` rather than against a live section, so neither
   * goes vacuous while no section is marked.
   */
  adminOnly?: boolean;
}

export const SETTINGS_SECTIONS: readonly SettingsSection[] = [
  {
    // `users` is retired INTO this value. `account` is the one a member can
    // reach, so it is the value in a member's bookmark and the hard-coded
    // fallback below; keeping it means `?tab=users` degrades to a section that
    // still holds the household table for an admin.
    //
    // The word "Account" is load-bearing beyond taste. `password_change_handlers.go`
    // ships the string "use the Account page to change your own password", and
    // README documents legacy bookmarks landing here — renaming the visible
    // half to anything without "Account" in it is a cross-stack edit.
    //
    // Same `data` precedent as below: the VALUE never narrows, only the label.
    // A member's panel is their own account card and nothing else, so offering
    // them the word "users" would name a capability the panel cannot deliver.
    value: 'account',
    label: (admin) => (admin ? 'Account & users' : 'Account'),
  },
  { value: 'currencies', label: () => 'Currencies' },
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
export function visibleSettingsSections(
  admin: boolean,
  sections: readonly SettingsSection[] = SETTINGS_SECTIONS,
): SettingsSection[] {
  return sections.filter((s) => admin || !s.adminOnly);
}

/**
 * The tab a given role may actually land on, given a raw `?tab=` value.
 *
 * THIS IS THE ROLE GATE FOR THE PHONE SURFACE, not a convenience. `isValidTab`
 * answers "is this one of the five values" and is deliberately role-blind, so it
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
 * Falls back to `account` — the section that carries a member's own account
 * card, so it is reachable by every role, and already the hard-coded default
 * for an absent or unrecognised `?tab=`. It is also where a retired `?tab=users`
 * bookmark lands, which is the whole reason `users` was merged INTO this value
 * rather than renamed.
 *
 * `sections` is injectable for one reason: no section is `adminOnly` today, so
 * the clamp below cannot be exercised against the production list, and a
 * fallback branch that no test can reach is a mutant waiting to survive. The
 * fixture in the test file supplies one. No production caller passes it.
 */
export function resolveSettingsTab(
  value: string | null,
  admin: boolean,
  sections: readonly SettingsSection[] = SETTINGS_SECTIONS,
): SettingsTab {
  if (!isValidTab(value)) return 'account';
  const reachable = visibleSettingsSections(admin, sections).some(
    (s) => s.value === value,
  );
  return reachable ? value : 'account';
}

/** The label for one tab value, or `undefined` if the role cannot see it. */
export function settingsSectionLabel(
  value: SettingsTab,
  admin: boolean,
  sections: readonly SettingsSection[] = SETTINGS_SECTIONS,
): string | undefined {
  return visibleSettingsSections(admin, sections)
    .find((s) => s.value === value)
    ?.label(admin);
}
