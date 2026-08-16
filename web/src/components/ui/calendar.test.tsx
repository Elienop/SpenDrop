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

    // The renderer's third arm, which the two above never reach: with
    // `captionLayout="dropdown"` react-day-picker renders one
    // `<Chevron orientation="down">` per dropdown caption (Dropdown.js), and
    // everything that is not "left" or "right" falls through to the DOWN icon.
    // Deleting that arm leaves the month and year captions with no glyph at
    // all, and only this assertion notices.
    const downs = Array.from(
      container.querySelectorAll('.rdp-caption_label svg'),
    );
    // Positive control: the dropdown captions really rendered, so the loop
    // below is not passing over an empty list.
    expect(downs.length).toBeGreaterThan(0);
    for (const down of downs) {
      expect(down).toHaveClass('size-4', 'lucide-chevron-down');
    }

    // WeekNumber wraps its content in the centring div rather than putting the
    // number straight in the cell.
    const cell = container.querySelector('td[role="rowheader"]');
    const inner = cell?.querySelector('div');
    expect(inner).not.toBeNull();
    expect(inner).toHaveClass('flex', 'items-center', 'justify-center');
    expect(inner?.textContent?.trim()).not.toBe('');
  });
});

describe('CalendarRoot keeps the two things the hoist could have dropped', () => {
  test('it renders a div, not an inline element', () => {
    const { container } = render(<Rerenderable />);

    const root = container.querySelector('[data-slot="calendar"]');
    // react-day-picker's own Root is a `div`, and everything keyed off the
    // root — `w-fit`, the flex column of months, the absolutely positioned nav
    // — assumes a block box. An inline element renders the same markup and
    // lays out wrongly, which no class assertion would catch.
    expect(root?.tagName).toBe('DIV');
  });

  test('it forwards rootRef, which is what react-day-picker animates through', () => {
    // `ref={rootRef}` looks decorative: react-day-picker only passes a real ref
    // when `animate` is set (`rootRef: props.animate ? rootElRef : undefined`
    // in DayPicker.js), so the calendar renders identically without it. What it
    // costs is the month transition — `useAnimation` bails on
    // `!rootElRef.current`, so dropping the ref silently disables the animation
    // the prop exists for, on the only route the ref has into the DOM.
    const { container, rerender } = render(
      <Calendar animate month={MONTH} onMonthChange={() => {}} />,
    );
    const root = container.querySelector<HTMLElement>('[data-slot="calendar"]');
    expect(root).not.toBeNull();
    // Positive control: nothing is animating before the month moves.
    expect(root?.style.isolation).toBe('');

    rerender(
      <Calendar
        animate
        month={new Date(2026, 1, 15)}
        onMonthChange={() => {}}
      />,
    );

    // Set inside `useAnimation`'s layout effect, which can only reach the
    // element through `rootElRef.current`.
    expect(root?.style.isolation).toBe('isolate');
  });
});
