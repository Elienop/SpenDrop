import { describe, test, expect, afterEach } from 'vitest';
import { render, cleanup, fireEvent } from '@testing-library/react';
import { useState } from 'react';

import { Calendar } from './calendar';

afterEach(cleanup);

// A fixed month so nothing here depends on today's date.
const MONTH = new Date(2026, 0, 15);

/** Renders a Calendar under a parent that can be forced to re-render. */
function Rerenderable() {
  const [tick, setTick] = useState(0);
  return (
    <div>
      <button type="button" onClick={() => setTick((n) => n + 1)}>
        bump {tick}
      </button>
      <Calendar
        month={MONTH}
        onMonthChange={() => {}}
        showWeekNumber
        captionLayout="dropdown"
        startMonth={new Date(2025, 0)}
        endMonth={new Date(2027, 11)}
      />
    </div>
  );
}

describe('Calendar custom renderers survive a parent re-render', () => {
  // `Root`, `Chevron` and `WeekNumber` used to be arrow functions written
  // inline inside `components={{ … }}` (SonarQube S6478). A component created
  // during render is a NEW component TYPE every time, so React cannot
  // reconcile it with the previous one — it unmounts the old subtree and
  // mounts a fresh one, throwing away DOM state and focus underneath it on
  // every keystroke that re-renders the parent.
  //
  // That is invisible in a snapshot: the markup is identical either way. Node
  // IDENTITY is what distinguishes an update from a remount, so that is what
  // this asserts. Re-inlining any of the three is the mutant it kills.
  test('Root, Chevron and WeekNumber DOM nodes are updated, not replaced', () => {
    const { container, getByText } = render(<Rerenderable />);

    const root = container.querySelector('[data-slot="calendar"]');
    const chevron = container.querySelector('.rdp-button_previous svg');
    const weekNumber = container.querySelector('td[role="rowheader"]');

    // Positive control: all three renderers are actually on screen, so the
    // identity checks below are comparing real nodes rather than null to null.
    expect(root).not.toBeNull();
    expect(chevron).not.toBeNull();
    expect(weekNumber).not.toBeNull();

    // `fireEvent` rather than a bare `.click()`: it wraps the dispatch in
    // `act`, so the state update is flushed before the assertions below.
    fireEvent.click(getByText(/^bump/));
    expect(getByText(/^bump 1$/)).toBeInTheDocument(); // the re-render happened

    expect(container.querySelector('[data-slot="calendar"]')).toBe(root);
    expect(container.querySelector('.rdp-button_previous svg')).toBe(chevron);
    expect(container.querySelector('td[role="rowheader"]')).toBe(weekNumber);
  });

  test('the hoisted renderers still produce the shapes the classNames target', () => {
    const { container } = render(<Rerenderable />);

    // Root carries the data-slot the calendar's own class list keys off
    // (`[[data-slot=popover-content]_&]` and friends), on the element
    // react-day-picker gives its root className to.
    const root = container.querySelector('[data-slot="calendar"]');
    expect(root?.className).toContain('rdp-root');

    // Chevron is the lucide icon, sized by the renderer rather than by the
    // button — `size-4` comes from `cn("size-4", className)`.
    const prev = container.querySelector('.rdp-button_previous svg');
    expect(prev).toHaveClass('size-4', 'lucide-chevron-left');
    expect(container.querySelector('.rdp-button_next svg')).toHaveClass(
      'size-4',
      'lucide-chevron-right',
    );

    // WeekNumber wraps its content in the centring div rather than putting the
    // number straight in the cell.
    const cell = container.querySelector('td[role="rowheader"]');
    const inner = cell?.querySelector('div');
    expect(inner).not.toBeNull();
    expect(inner).toHaveClass('flex', 'items-center', 'justify-center');
    expect(inner?.textContent?.trim()).not.toBe('');
  });
});
