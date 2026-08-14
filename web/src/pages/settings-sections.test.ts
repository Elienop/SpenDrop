import { describe, test, expect } from 'vitest';
import {
  SETTINGS_SECTIONS,
  VALID_TABS,
  isValidTab,
  resolveSettingsTab,
  settingsSectionLabel,
  visibleSettingsSections,
  type SettingsSection,
} from './settings-sections';

/**
 * Pure-function tests, on purpose — they hold three invariants that no
 * UI-level test in this repo can hold reliably.
 *
 * The first is REACHABILITY: a member must keep a route to their own password.
 * That lives on the `account` section, and the one-line edit that would remove
 * it — adding `adminOnly: true` to that entry — is invisible in any test that
 * only checks what an ADMIN can see. Since the Users section was merged into
 * `account`, the same entry now also carries the household table, so that
 * one-line edit is a plausible way to try to hide the admin half.
 *
 * The second is the ROLE GATE. `resolveSettingsTab` is what stops a member
 * landing on an `adminOnly` section's panel, and the surface that needed it is
 * the phone Select, which renders ONE section resolved from the raw `?tab=`
 * value rather than mapping over the filtered list the way the desktop strip
 * does. A test at phone width would be the natural place to assert it and is
 * the wrong place: viewport-gated assertions in this codebase have gone
 * vacuously green before when the gated block never rendered at all, and here
 * the failure being guarded is a value, not a layout.
 *
 * NO PRODUCTION SECTION IS `adminOnly` ANY MORE, which is why the gate is
 * driven against FIXTURE below rather than against whatever section happens to
 * carry the flag. Testing it against the live list was fine while `users`
 * existed and would silently evaporate now — `SETTINGS_SECTIONS.find(s =>
 * s.adminOnly)` returns undefined, and every assertion built on it becomes a
 * statement about nothing.
 *
 * The third is ROUTING: `users` is retired, and a bookmark pointing at it must
 * land on the section that absorbed it rather than anywhere else. This is the
 * oracle the UI tests cannot supply — the merged admin panel CONTAINS the
 * users table, so a page that resolved `?tab=users` to the wrong value, or
 * kept a sixth tab alive, still renders DOM those tests find.
 */

/**
 * A section list that exercises the `adminOnly` mechanism, since the real one
 * no longer does. Values have to be real `SettingsTab`s (the functions are
 * typed on them), so this reuses two — what is synthetic is the FLAG, which is
 * the thing under test.
 */
const FIXTURE: readonly SettingsSection[] = [
  { value: 'account', label: () => 'Account' },
  { value: 'notifications', label: () => 'Notifications', adminOnly: true },
];

describe('the retired `users` value', () => {
  test('is no longer a valid tab', () => {
    // The routing oracle, established before anything else. If `users` were
    // still valid, `?tab=users` would resolve to itself and
    // `renderSettingsSection` would have to answer for it.
    expect(isValidTab('users')).toBe(false);
    expect(VALID_TABS).not.toContain('users');
  });

  test('a `?tab=users` bookmark lands on account for both roles', () => {
    // `account` is where the Users section was merged TO, so an admin's old
    // bookmark still opens the household table and a member's opens their own
    // account card. Landing anywhere else would be a broken bookmark.
    expect(resolveSettingsTab('users', true)).toBe('account');
    expect(resolveSettingsTab('users', false)).toBe('account');
  });

  test('the section list has exactly five entries and no `users`', () => {
    // A count, not just an absence: the failure this catches is `users` being
    // retired from VALID_TABS but left in SETTINGS_SECTIONS (or the reverse),
    // which the two assertions above would each pass individually.
    expect(SETTINGS_SECTIONS.map((s) => s.value)).toEqual([
      'account',
      'currencies',
      'api-tokens',
      'notifications',
      'data',
    ]);
    expect(SETTINGS_SECTIONS).toHaveLength(VALID_TABS.length);
  });
});

describe('visibleSettingsSections', () => {
  test('the account section is reachable by a member', () => {
    // THE reachability invariant. `account` carries the change-password card
    // and the display-name editor, and it is the only self-service route a
    // member has to their own credentials — `PUT /api/users/{id}` is
    // admin-gated. Marking this section `adminOnly` would silently strip that,
    // with no other test failing.
    const memberValues = visibleSettingsSections(false).map((s) => s.value);
    expect(memberValues).toContain('account');
  });

  test('a member sees every section an admin does', () => {
    // True only because no section is `adminOnly` any more. Stated as its own
    // assertion so that adding one is a deliberate act that fails here and has
    // to be justified, rather than a quiet change of shape.
    expect(visibleSettingsSections(false)).toEqual(
      visibleSettingsSections(true),
    );
    expect(visibleSettingsSections(true).map((s) => s.value)).toEqual([
      ...VALID_TABS,
    ]);
  });

  test('hides an adminOnly section from a member and shows it to an admin', () => {
    // The MECHANISM, driven against the fixture. Kept alive on purpose: the
    // next whole-panel admin section is one line away, and this filter plus the
    // clamp below are what make that line safe.
    const member = visibleSettingsSections(false, FIXTURE).map((s) => s.value);
    const admin = visibleSettingsSections(true, FIXTURE).map((s) => s.value);
    expect(member).toEqual(['account']);
    expect(admin).toEqual(['account', 'notifications']);
  });
});

describe('resolveSettingsTab', () => {
  test('an adminOnly value does not resolve for a member', () => {
    // The control leak this function exists to close: the phone surface renders
    // `renderSettingsSection(value, admin)` from the raw value, so a member
    // opening an admin-only `?tab=` mounted the whole panel. Every control then
    // failed against RequireAdmin — a control leak, not a data leak, but the
    // panel should never have mounted.
    expect(resolveSettingsTab('notifications', false, FIXTURE)).toBe('account');
  });

  test('the same value resolves untouched for an admin', () => {
    // The other half — a clamp that swallowed the value for BOTH roles would
    // pass the test above while breaking the admin's own deep link.
    expect(resolveSettingsTab('notifications', true, FIXTURE)).toBe(
      'notifications',
    );
  });

  test('every real section resolves for both roles today', () => {
    for (const section of SETTINGS_SECTIONS) {
      expect(resolveSettingsTab(section.value, false)).toBe(section.value);
      expect(resolveSettingsTab(section.value, true)).toBe(section.value);
    }
  });

  test.each([
    ['an unknown value', 'not-a-tab'],
    ['an empty string', ''],
    ['a value that is a prefix of a real one', 'accou'],
    ['the retired users value', 'users'],
    ['null', null],
  ])('%s falls back to account', (_label, value) => {
    expect(resolveSettingsTab(value, true)).toBe('account');
    expect(resolveSettingsTab(value, false)).toBe('account');
  });

  test('the fallback is itself reachable by both roles', () => {
    // Otherwise the clamp would send a member to a section they cannot open,
    // which is the original bug wearing a different hat.
    const fallback = resolveSettingsTab('not-a-tab', false);
    expect(visibleSettingsSections(false).map((s) => s.value)).toContain(
      fallback,
    );
    expect(visibleSettingsSections(true).map((s) => s.value)).toContain(
      fallback,
    );
  });
});

describe('isValidTab', () => {
  test('is deliberately role-blind, which is why it cannot carry the gate', () => {
    // Pinned so nobody "fixes" the leak by making isValidTab role-aware: it
    // answers a different question (is this one of the declared values) and is
    // used where the role is not in scope. The gate is resolveSettingsTab.
    for (const value of VALID_TABS) {
      expect(isValidTab(value)).toBe(true);
    }
  });

  test('rejects anything outside the declared list', () => {
    expect(isValidTab('nope')).toBe(false);
    expect(isValidTab(null)).toBe(false);
  });
});

describe('settingsSectionLabel', () => {
  // The merged section is the second place a label narrows by role, after
  // `data`. Exact strings, not regexes: `/account/i` matches BOTH of these, so
  // a substring assertion here would pass whichever one the function returned.
  test('the account label names the household half only for an admin', () => {
    expect(settingsSectionLabel('account', true)).toBe('Account & users');
    expect(settingsSectionLabel('account', false)).toBe('Account');
  });

  test('the word "Account" survives in both labels', () => {
    // Cross-stack, not cosmetic: `password_change_handlers.go` tells a user to
    // "use the Account page to change your own password", and README documents
    // legacy bookmarks landing there. A label without the word breaks both.
    expect(settingsSectionLabel('account', true)).toContain('Account');
    expect(settingsSectionLabel('account', false)).toContain('Account');
  });

  test('returns undefined for a section the role cannot see', () => {
    expect(settingsSectionLabel('notifications', false, FIXTURE)).toBeUndefined();
    expect(settingsSectionLabel('notifications', true, FIXTURE)).toBe(
      'Notifications',
    );
  });
});
