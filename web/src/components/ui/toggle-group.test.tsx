import { describe, test, expect, afterEach, vi } from 'vitest';
import { render, screen, cleanup, fireEvent } from '@testing-library/react';
import { memo, useState } from 'react';

// `ToggleGroupItem` is the only reader of `ToggleGroupContext`, and this module
// exports no hook for that context — so the render count of the CONSUMER is not
// directly observable. `toggleVariants` is: `ToggleGroupItem` calls it once per
// render, in its render body. Wrapping it (delegating to the real cva function,
// so every class string is unchanged) turns it into that render counter.
const variantCalls = vi.hoisted(() => ({ count: 0 }));

vi.mock('@/components/ui/toggle', async (importOriginal) => {
  const actual = await importOriginal<typeof import('./toggle')>();
  return {
    ...actual,
    toggleVariants: (...args: Parameters<typeof actual.toggleVariants>) => {
      variantCalls.count += 1;
      return actual.toggleVariants(...args);
    },
  };
});

import { ToggleGroup, ToggleGroupItem } from './toggle-group';

afterEach(cleanup);

// `ToggleGroup` memoises the value it puts on `ToggleGroupContext` (SonarQube
// S6481). The memo's dependency list is `[variant, size]` because those are the
// only two things `ToggleGroupItem` reads off it — and if that list ever
// narrows to `[]`, the group keeps handing out the variant it was FIRST
// rendered with while its own prop moves on. Nothing throws; the buttons just
// stop matching the group.
describe('ToggleGroup variant context', () => {
  const tree = (variant: 'default' | 'outline', size: 'default' | 'sm') => (
    <ToggleGroup type="single" variant={variant} size={size} defaultValue="b">
      <ToggleGroupItem value="a">A</ToggleGroupItem>
      <ToggleGroupItem value="b">B</ToggleGroupItem>
    </ToggleGroup>
  );

  test('items take their variant and size from the group', () => {
    render(tree('outline', 'sm'));
    // Neither item declares a variant of its own — `border-input` and `h-9`
    // can only have come through the context.
    for (const label of ['A', 'B']) {
      expect(screen.getByText(label)).toHaveClass('border', 'border-input', 'h-9');
    }
  });

  test('a changed variant reaches the items instead of being cached', () => {
    const { rerender } = render(tree('outline', 'sm'));
    // Positive control: the first variant genuinely rendered, so the assertion
    // below is a change and not just an absence.
    expect(screen.getByText('A')).toHaveClass('border-input');

    rerender(tree('default', 'default'));
    expect(screen.getByText('A')).not.toHaveClass('border-input');
    expect(screen.getByText('A')).toHaveClass('h-10');
    expect(screen.getByText('A')).not.toHaveClass('h-9');
  });

  // Moving BOTH props at once, as the test above does, cannot tell the two
  // halves of `[variant, size]` apart: a `[variant]`-only list still picks up
  // the new size, because the variant changed too and the memo recomputed
  // wholesale. Both narrowings survived it. One prop at a time is what
  // separates them, so there is a test per half.
  test('changing ONLY the size reaches the items', () => {
    const { rerender } = render(tree('outline', 'sm'));
    // Positive control: `sm` really rendered, so the assertion below is a
    // change rather than an absence.
    expect(screen.getByText('A')).toHaveClass('h-9');

    rerender(tree('outline', 'default'));
    expect(screen.getByText('A')).toHaveClass('h-10');
    expect(screen.getByText('A')).not.toHaveClass('h-9');
    // The variant did not move, and must not have been dropped on the way.
    expect(screen.getByText('A')).toHaveClass('border-input');
  });

  test('changing ONLY the variant reaches the items', () => {
    const { rerender } = render(tree('outline', 'sm'));
    expect(screen.getByText('A')).toHaveClass('border-input');

    rerender(tree('default', 'sm'));
    expect(screen.getByText('A')).not.toHaveClass('border-input');
    // The size did not move.
    expect(screen.getByText('A')).toHaveClass('h-9');
  });
});

describe('ToggleGroup context identity', () => {
  // The describe above pins the memo's dependency LIST; this pins the memo
  // itself. Removing it changes no markup — only how often every item under the
  // group re-renders — so a render count is the only thing that can see it.
  //
  // `React.memo` with no props is the wall: nothing above the probe can
  // re-render it, so an item render can only have come from the context handing
  // out a new value. Measured against a memoless build: 5 parent re-renders
  // produce 5 extra item renders, on a group that rebuilds every item's class
  // string each time.
  const Probe = memo(function Probe() {
    return <ToggleGroupItem value="a">A</ToggleGroupItem>;
  });

  function Harness({ variant }: { variant: 'default' | 'outline' }) {
    const [tick, setTick] = useState(0);
    return (
      <div>
        <button type="button" onClick={() => setTick((n) => n + 1)}>
          bump {tick}
        </button>
        <ToggleGroup type="single" variant={variant} size="sm">
          <Probe />
        </ToggleGroup>
      </div>
    );
  }

  test('a parent re-render does not re-render the items under it', () => {
    // The counter is module-scoped, so zero it here rather than inheriting
    // whatever the tests above left behind.
    variantCalls.count = 0;
    render(<Harness variant="outline" />);

    // Positive control: the item really rendered and the counter is live, so
    // the comparison below is not 0 against 0.
    const afterMount = variantCalls.count;
    expect(afterMount).toBeGreaterThan(0);

    for (let i = 0; i < 5; i += 1) {
      fireEvent.click(screen.getByText(/^bump/));
    }
    // Positive control: the parent really re-rendered five times.
    expect(screen.getByText('bump 5')).toBeInTheDocument();

    expect(variantCalls.count).toBe(afterMount);
  });

  test('a changed variant still reaches the items', () => {
    // Keeps the test above honest: an item that never re-renders at all would
    // satisfy it too. A real dependency change has to get through.
    variantCalls.count = 0;
    const { rerender } = render(<Harness variant="outline" />);
    const afterMount = variantCalls.count;

    rerender(<Harness variant="default" />);
    expect(variantCalls.count).toBeGreaterThan(afterMount);
  });
});
