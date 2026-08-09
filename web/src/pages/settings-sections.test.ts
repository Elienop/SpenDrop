import { describe, test, expect } from 'vitest';
import {
  SETTINGS_SECTIONS,
  VALID_TABS,
  isValidTab,
  resolveSettingsTab,
  settingsSectionLabel,
  visibleSettingsSections,
} from './settings-sections';

/**
 * Pure-function tests, on purpose — they hold two invariants that no
 * UI-level test in this repo can hold reliably.
 *
 * The first is a ROLE GATE. `resolveSettingsTab` is what stops a member
 * landing on an `adminOnly` section's panel, and the surface that needed it is
 * the phone Select, which renders ONE section resolved from the raw `?tab=`
 * value rather than mapping over the filtered list the way the desktop strip
 * does. A test at phone width would be the natural place to assert it and is
 * the wrong place: viewport-gated assertions in this codebase have gone
 * vacuously green before when the gated block never rendered at all, and here
 * the failure being guarded is a value, not a layout.
 *
 * The second is REACHABILITY: a member must keep a route to their own
 * password. That lives on the `account` section, and the one-line edit that
 * would remove it — adding `adminOnly: true` to that entry — is invisible in
 * any test that only checks what an ADMIN can see.
 */

describe('visibleSettingsSections', () => {
  test('the account section is reachable by a member', () => {
    // THE reachability invariant. `account` carries the change-password card,
    // and it is the only self-service route a member has to their own
    // credentials — `PUT /api/users/{id}` is admin-gated. Marking this section
    // `adminOnly` would silently strip that, with no other test failing.
    const memberValues = visibleSettingsSections(false).map((s) => s.value);
    expect(memberValues).toContain('account');
  });

  test('hides an adminOnly section from a member and shows it to an admin', () => {
    const adminOnly = SETTINGS_SECTIONS.filter((s) => s.adminOnly).map(
      (s) => s.value,
    );
    // Positive control: if nothing is adminOnly the two assertions below are
    // both vacuous, and this file would keep passing while the filter did
    // nothing at all.
    expect(adminOnly.length).toBeGreaterThan(0);

    const member = visibleSettingsSections(false).map((s) => s.value);
    const admin = visibleSettingsSections(true).map((s) => s.value);
    for (const value of adminOnly) {
      expect(member).not.toContain(value);
      expect(admin).toContain(value);
    }
  });

  test('an admin sees every declared section', () => {
    expect(visibleSettingsSections(true).map((s) => s.value)).toEqual([
      ...VALID_TABS,
    ]);
  });
});

describe('resolveSettingsTab', () => {
  const adminOnlyValue = SETTINGS_SECTIONS.find((s) => s.adminOnly)?.value;

  test('an adminOnly value does not resolve for a member', () => {
    // The control leak this function exists to close: the phone surface
    // rendered `renderSettingsSection(value, admin)` from the raw value, so a
    // member opening `?tab=users` mounted the whole household-administration
    // panel. Every control then failed against RequireAdmin — a control leak,
    // not a data leak, but the panel should never have mounted.
    expect(adminOnlyValue).toBeDefined();
    expect(resolveSettingsTab(adminOnlyValue!, false)).toBe('account');
  });

  test('the same value resolves untouched for an admin', () => {
    // The other half — a clamp that swallowed the value for BOTH roles would
    // pass the test above while breaking the admin's own deep link.
    expect(resolveSettingsTab(adminOnlyValue!, true)).toBe(adminOnlyValue);
  });

  test('a member keeps every section they are allowed to open', () => {
    for (const section of visibleSettingsSections(false)) {
      expect(resolveSettingsTab(section.value, false)).toBe(section.value);
    }
  });

  test.each([
    ['an unknown value', 'not-a-tab'],
    ['an empty string', ''],
    ['a value that is a prefix of a real one', 'accou'],
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
    // answers a different question (is this one of the six values) and is used
    // where the role is not in scope. The gate is resolveSettingsTab.
    expect(isValidTab(adminOnlyOrThrow())).toBe(true);
  });

  test('rejects anything outside the declared list', () => {
    expect(isValidTab('nope')).toBe(false);
    expect(isValidTab(null)).toBe(false);
  });
});

describe('settingsSectionLabel', () => {
  test('returns undefined for a section the role cannot see', () => {
    expect(settingsSectionLabel(adminOnlyOrThrow(), false)).toBeUndefined();
    expect(settingsSectionLabel(adminOnlyOrThrow(), true)).toBeDefined();
  });
});

function adminOnlyOrThrow() {
  const section = SETTINGS_SECTIONS.find((s) => s.adminOnly);
  if (!section) throw new Error('no adminOnly section to test against');
  return section.value;
}
