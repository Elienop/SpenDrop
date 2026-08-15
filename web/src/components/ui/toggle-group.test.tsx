import { describe, test, expect, afterEach } from 'vitest';
import { render, screen, cleanup } from '@testing-library/react';

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
    // And the size half of the same memo moved too — `sm`'s h-9 gave way to
    // the default h-10.
    expect(screen.getByText('A')).toHaveClass('h-10');
    expect(screen.getByText('A')).not.toHaveClass('h-9');
  });
});
