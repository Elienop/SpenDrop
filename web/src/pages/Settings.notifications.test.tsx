import { describe, test, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import { NotificationsSection } from './Settings';
import type { UseWebPush } from '@/hooks/useWebPush';
import type { UseNotificationPrefs } from '@/hooks/useNotificationPrefs';
import type { NotificationSettings } from '@/api/types';

const mockHook: UseWebPush = {
  supported: true,
  permission: 'default',
  subscribed: false,
  busy: false,
  enable: vi.fn(),
  disable: vi.fn(),
  sendTest: vi.fn(),
};

vi.mock('@/hooks/useWebPush', () => ({
  useWebPush: () => mockHook,
}));

const SETTINGS: NotificationSettings = {
  over_budget: true,
  txn_added: false,
  txn_deleted: false,
  txn_edited: false,
  large_txn: false,
  large_txn_threshold_dollars: 500,
  digest_mode: 'off',
  digest_time: '08:00',
  quiet_start: '',
  quiet_end: '',
  quiet_tz: 'UTC',
  quiet_allow_over_budget: true,
};

const prefsHook: UseNotificationPrefs = {
  settings: SETTINGS,
  loading: false,
  error: '',
  canEdit: true,
  update: vi.fn().mockResolvedValue(undefined),
};

vi.mock('@/hooks/useNotificationPrefs', () => ({
  useNotificationPrefs: () => prefsHook,
}));

beforeEach(() => {
  mockHook.supported = true;
  mockHook.permission = 'default';
  mockHook.subscribed = false;
  mockHook.busy = false;
  prefsHook.settings = { ...SETTINGS };
  prefsHook.loading = false;
  prefsHook.error = '';
  prefsHook.canEdit = true;
  prefsHook.update = vi.fn().mockResolvedValue(undefined);
});

describe('NotificationsSection — per-device master toggle', () => {
  test('Send test is disabled until granted AND subscribed', () => {
    render(<NotificationsSection />);
    expect(
      screen.getByRole('button', { name: /send test/i }),
    ).toBeDisabled();
  });

  test('Send test is enabled when granted and subscribed', () => {
    mockHook.permission = 'granted';
    mockHook.subscribed = true;
    render(<NotificationsSection />);
    expect(
      screen.getByRole('button', { name: /send test/i }),
    ).toBeEnabled();
  });

  test('shows a denied hint when permission is denied', () => {
    mockHook.permission = 'denied';
    render(<NotificationsSection />);
    expect(screen.getByText(/blocked notifications/i)).toBeInTheDocument();
  });
});

describe('NotificationsSection — household notification types', () => {
  test('admin sees an editable Switch per type and a threshold input', () => {
    prefsHook.canEdit = true;
    render(<NotificationsSection />);

    for (const name of [
      /over budget/i,
      /transaction added/i,
      /transaction deleted/i,
      /transaction edited/i,
      /large transaction/i,
    ]) {
      expect(screen.getByRole('switch', { name })).toBeEnabled();
    }
    expect(
      screen.getByLabelText(/large transaction threshold/i),
    ).toBeEnabled();
    expect(
      screen.queryByText(/managed by your admin/i),
    ).not.toBeInTheDocument();
  });

  test('non-admin sees the type toggles disabled with an admin note', () => {
    prefsHook.canEdit = false;
    render(<NotificationsSection />);

    expect(
      screen.getByRole('switch', { name: /over budget/i }),
    ).toBeDisabled();
    expect(
      screen.getByRole('switch', { name: /large transaction/i }),
    ).toBeDisabled();
    expect(
      screen.getByLabelText(/large transaction threshold/i),
    ).toBeDisabled();
    expect(
      screen.getByText(/managed by your admin/i),
    ).toBeInTheDocument();
  });

  test('renders the error message instead of Loading when prefs fail to load', () => {
    prefsHook.settings = null;
    prefsHook.loading = false;
    prefsHook.error = 'Failed to load preferences';
    render(<NotificationsSection />);

    expect(
      screen.getByText(/failed to load preferences/i),
    ).toBeInTheDocument();
    expect(screen.queryByText(/^loading…$/i)).not.toBeInTheDocument();
    // The type toggles must not render when the load failed.
    expect(
      screen.queryByRole('switch', { name: /over budget/i }),
    ).not.toBeInTheDocument();
  });

  test('clearing the threshold input does not push a $0 update', () => {
    prefsHook.canEdit = true;
    const update = vi.fn().mockResolvedValue(undefined);
    prefsHook.update = update;
    render(<NotificationsSection />);

    const input = screen.getByLabelText(/large transaction threshold/i);
    fireEvent.change(input, { target: { value: '' } });
    fireEvent.blur(input);

    expect(update).not.toHaveBeenCalled();
  });
});

describe('NotificationsSection — household controls without local push', () => {
  test('household policy block renders even when web push is unsupported', () => {
    mockHook.supported = false;
    render(<NotificationsSection />);

    // The device-agnostic household controls must remain reachable.
    expect(
      screen.getByRole('switch', { name: /over budget/i }),
    ).toBeInTheDocument();
    expect(
      screen.getByLabelText(/large transaction threshold/i),
    ).toBeInTheDocument();
    expect(screen.getByLabelText('Daily digest')).toBeInTheDocument();
    expect(screen.getByLabelText('Quiet hours start')).toBeInTheDocument();
  });

  test('per-device push toggle + Send test are hidden when unsupported', () => {
    mockHook.supported = false;
    render(<NotificationsSection />);

    expect(
      screen.getByText(/does not support web push/i),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole('switch', {
        name: /push notifications on this device/i,
      }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole('button', { name: /send test/i }),
    ).not.toBeInTheDocument();
  });
});

describe('NotificationsSection — digest + quiet hours', () => {
  test('renders the digest mode control', () => {
    render(<NotificationsSection />);
    expect(screen.getByLabelText('Daily digest')).toBeInTheDocument();
  });

  test('hides the digest send time when digest mode is off', () => {
    prefsHook.settings = { ...SETTINGS, digest_mode: 'off' };
    render(<NotificationsSection />);
    expect(screen.queryByLabelText('Digest send time')).not.toBeInTheDocument();
  });

  test('shows the digest send time when daily and PUTs a changed time', () => {
    prefsHook.settings = {
      ...SETTINGS,
      digest_mode: 'daily',
      digest_time: '08:00',
    };
    render(<NotificationsSection />);
    const input = screen.getByLabelText('Digest send time');
    fireEvent.change(input, { target: { value: '09:30' } });
    fireEvent.blur(input);
    expect(prefsHook.update).toHaveBeenCalledWith({ digest_time: '09:30' });
  });

  test('does not PUT a cleared (empty) digest send time', () => {
    prefsHook.settings = {
      ...SETTINGS,
      digest_mode: 'daily',
      digest_time: '08:00',
    };
    render(<NotificationsSection />);
    const input = screen.getByLabelText('Digest send time');
    fireEvent.change(input, { target: { value: '' } });
    fireEvent.blur(input);
    expect(prefsHook.update).not.toHaveBeenCalled();
  });

  test('reverts a cleared digest send time to the server value and fires no PUT', () => {
    prefsHook.settings = {
      ...SETTINGS,
      digest_mode: 'daily',
      digest_time: '08:00',
    };
    render(<NotificationsSection />);
    const input = screen.getByLabelText('Digest send time');
    fireEvent.change(input, { target: { value: '' } });
    fireEvent.blur(input);
    // A cleared field must snap back to the persisted server value rather than
    // linger blank showing a value that no longer matches the server.
    expect(
      (screen.getByLabelText('Digest send time') as HTMLInputElement).value,
    ).toBe('08:00');
    expect(prefsHook.update).not.toHaveBeenCalled();
  });

  test('explains that quiet hours need both bounds to take effect', () => {
    render(<NotificationsSection />);
    expect(screen.getByText(/a single bound is ignored/i)).toBeInTheDocument();
  });

  test('warns while exactly one quiet bound is set', () => {
    render(<NotificationsSection />);
    expect(
      screen.queryByText(/fill in the other field/i),
    ).not.toBeInTheDocument();
    const start = screen.getByLabelText('Quiet hours start');
    fireEvent.change(start, { target: { value: '22:30' } });
    expect(screen.getByText(/fill in the other field/i)).toBeInTheDocument();
  });

  test('toggling "Allow over-budget during quiet hours" PUTs the change', () => {
    prefsHook.settings = { ...SETTINGS, quiet_allow_over_budget: true };
    render(<NotificationsSection />);
    fireEvent.click(
      screen.getByRole('switch', { name: /allow over-budget during quiet hours/i }),
    );
    expect(prefsHook.update).toHaveBeenCalledWith({ quiet_allow_over_budget: false });
  });

  test('a lone quiet-start does not PUT a half-set window', () => {
    // From the both-empty default, filling only the start is a transitional
    // half-set state the backend rejects (400). The UI must not send it.
    render(<NotificationsSection />);
    const start = screen.getByLabelText('Quiet hours start');
    fireEvent.change(start, { target: { value: '22:30' } });
    fireEvent.blur(start);
    expect(prefsHook.update).not.toHaveBeenCalled();
  });

  test('setting both bounds PUTs the combined quiet window', () => {
    render(<NotificationsSection />);
    const start = screen.getByLabelText('Quiet hours start');
    const end = screen.getByLabelText('Quiet hours end');
    fireEvent.change(start, { target: { value: '22:30' } });
    fireEvent.blur(start);
    fireEvent.change(end, { target: { value: '08:00' } });
    fireEvent.blur(end);
    expect(prefsHook.update).toHaveBeenCalledTimes(1);
    expect(prefsHook.update).toHaveBeenCalledWith({
      quiet_start: '22:30',
      quiet_end: '08:00',
    });
  });

  test('clearing both bounds PUTs an empty quiet window', () => {
    prefsHook.settings = { ...SETTINGS, quiet_start: '22:00', quiet_end: '08:00' };
    render(<NotificationsSection />);
    const start = screen.getByLabelText('Quiet hours start');
    const end = screen.getByLabelText('Quiet hours end');
    fireEvent.change(start, { target: { value: '' } });
    fireEvent.blur(start);
    fireEvent.change(end, { target: { value: '' } });
    fireEvent.blur(end);
    expect(prefsHook.update).toHaveBeenCalledTimes(1);
    expect(prefsHook.update).toHaveBeenCalledWith({
      quiet_start: '',
      quiet_end: '',
    });
  });
});

/**
 * happy-dom lays nothing out, so the pixels below are ARITHMETIC over the class
 * strings the components actually rendered — not a constant restated from the
 * source. That distinction is the point: a test that pinned `coarse:gap-5`
 * would have gone on passing while `Switch` grew its band from `-inset-y-2.5`
 * to `-inset-y-3` underneath it, which is exactly what happened. Here the
 * switch's own classes supply the band and the container supplies the pitch, so
 * a change to either side that closes the clearance fails this file.
 *
 * The geometry, once: an absolutely-positioned pseudo resolves its insets
 * against the PADDING box, so `border-2` on a `h-6` switch leaves 20px for
 * `-inset-y-3` to grow from — a 44px band that reaches (44 − 24) / 2 = 10px
 * past the border box top and bottom. Browser-measured at 44px on the built app
 * (elementFromPoint, 2026-08-14); what this file adds is that the two numbers
 * stay consistent with the gap between the rows.
 */
describe('NotificationsSection — adjacent switch targets do not overlap', () => {
  /** The capture from the first class matching `pattern`, or a loud failure. */
  function capture(el: Element, pattern: RegExp, what: string): string {
    for (const token of el.className.split(/\s+/)) {
      const hit = pattern.exec(token);
      if (hit) return hit[1];
    }
    throw new Error(
      `no ${what} class on <${el.tagName.toLowerCase()} class="${el.className}">`,
    );
  }

  /** Tailwind's SPACING scale in px, refusing anything it does not recognise. */
  function spacingPx(el: Element, pattern: RegExp, what: string): number {
    const raw = capture(el, pattern, what);
    const arbitrary = /^\[(\d+(?:\.\d+)?)px\]$/.exec(raw);
    if (arbitrary) return Number(arbitrary[1]);
    if (raw === 'px') return 1;
    const steps = Number(raw);
    // Loud rather than NaN: a silently-unparsed token would make every
    // comparison below false-y and the whole file vacuous.
    if (!Number.isFinite(steps)) {
      throw new Error(`unrecognised Tailwind spacing length: "${raw}"`);
    }
    return steps * 4;
  }

  /** The coarse-pointer tap band of one Switch, derived from its own classes. */
  function bandOf(sw: Element) {
    const box = spacingPx(sw, /^h-(.+)$/, 'height');
    // Border widths are their OWN scale — `border-2` is 2px, not the 8px the
    // ×4 spacing scale would give. Read the wrong way round this put the band
    // at 32px, which is how the distinction earned a comment.
    const border = Number(capture(sw, /^border-(\d+)$/, 'border width'));
    const inset = spacingPx(sw, /^coarse:before:-inset-y-(.+)$/, 'coarse band');
    const height = box - 2 * border + 2 * inset;
    return { height, overhang: (height - box) / 2 };
  }

  test('every switch in the section still carries a 44px coarse band', () => {
    render(<NotificationsSection />);
    const switches = screen.getAllByRole('switch');
    // The per-device toggle, five household types, and the quiet-hours
    // over-budget allowance. A count pin so a section that lost a row cannot
    // make the loop below pass by iterating over fewer targets.
    expect(switches).toHaveLength(7);
    for (const sw of switches) {
      expect(bandOf(sw).height).toBeGreaterThanOrEqual(44);
    }
  });

  test('the stacked toggle rows leave positive clearance on a touch screen', () => {
    render(<NotificationsSection />);
    const row = screen.getByLabelText('Over budget').closest('div');
    const container = row?.parentElement;
    if (!container) throw new Error('switch-row container not found');

    // These rows are exactly switch-height — a one-line Label is 14px against
    // the switch's 24px — so the whole overhang lands in the gap between them.
    const { overhang } = bandOf(screen.getByLabelText('Over budget'));
    const coarseGap = spacingPx(container, /^coarse:gap-(.+)$/, 'coarse gap');
    const clearance = coarseGap - 2 * overhang;
    // STRICTLY positive. `gap-5` made the two bands tile edge to edge, and a
    // shared boundary is not clearance: the tap resolves to whichever row
    // painted its pseudo last, which is the later switch — the reported bug.
    expect(clearance).toBeGreaterThan(0);

    // Fine pointers keep the original rhythm: no band exists there, so the
    // wider gap must stay behind the `coarse:` gate rather than replacing it.
    expect(spacingPx(container, /^gap-(.+)$/, 'base gap')).toBe(16);

    // The container holds the switch rows and nothing else: a threshold or
    // digest row swept in here would get the coarse gap for no reason.
    expect(container.querySelectorAll('input,button[role="switch"]').length).toBe(
      container.querySelectorAll('button[role="switch"]').length,
    );
  });
});

describe('NotificationsSection — the time inputs fit their own value', () => {
  test('digest, quiet-start and quiet-end carry w-36, not the clipping w-28', () => {
    prefsHook.settings = { ...SETTINGS, digest_mode: 'daily' };
    render(<NotificationsSection />);
    for (const label of [
      'Digest send time',
      'Quiet hours start',
      'Quiet hours end',
    ]) {
      const input = screen.getByLabelText(label);
      // Exact tokens via classList, and the token IS the assertion: happy-dom
      // lays nothing out. Measured in a real browser: the 12h "hh:mm AM"
      // rendering needs ~140px at the phone's 16px font, and `w-28` (112px)
      // clipped the value (scrollWidth 128-134 against clientWidth 110).
      expect(input.classList.contains('w-36')).toBe(true);
      expect(input.classList.contains('w-28')).toBe(false);
    }
  });
});
