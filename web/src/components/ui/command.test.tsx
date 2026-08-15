import { describe, test, expect, afterEach } from 'vitest';
import { render, cleanup } from '@testing-library/react';

import commandSource from './command.tsx?raw';
import {
  Command,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from './command';

afterEach(cleanup);

describe('CommandInput wrapper attribute', () => {
  // The wrapper used to carry a bare `cmdk-input-wrapper=""` attribute copied
  // from shadcn's template. cmdk itself never emits or reads that name — the
  // attributes the library really writes are `cmdk-root`, `cmdk-input`,
  // `cmdk-item`, `cmdk-group-heading` and friends — and a non-`data-` custom
  // attribute is not valid HTML (SonarQube S6747). It is now `data-slot`.
  function tree() {
    return render(
      <Command>
        <CommandInput placeholder="Search currency..." />
        <CommandList>
          <CommandGroup heading="Currencies">
            <CommandItem value="usd">USD</CommandItem>
          </CommandGroup>
        </CommandList>
      </Command>,
    );
  }

  test('the search icon and the input share one data-slot wrapper', () => {
    const { container } = tree();

    const wrapper = container.querySelector('[data-slot="command-input-wrapper"]');
    expect(wrapper).not.toBeNull();
    expect(container.querySelector('[cmdk-input-wrapper]')).toBeNull();

    // The wrapper is what the `[&_[data-slot=…]_svg]` rules size, so the icon
    // and the input both have to be inside it.
    expect(wrapper?.querySelector('svg')).not.toBeNull();
    expect(wrapper?.querySelector('[cmdk-input]')).not.toBeNull();
  });

  test("cmdk's own attributes are untouched", () => {
    // Guards the other direction: the rename must not have swept up the
    // attributes cmdk genuinely emits, which the rest of the class list
    // targets.
    const { container } = tree();
    for (const attr of [
      'cmdk-root',
      'cmdk-input',
      'cmdk-list',
      'cmdk-group-heading',
      'cmdk-item',
    ]) {
      expect(container.querySelector(`[${attr}]`)).not.toBeNull();
    }
  });
});

describe('CommandDialog styles the slot that actually exists', () => {
  // WIRING SEAM. `CommandDialog` has no call site in this app, so nothing
  // renders it and no DOM assertion can reach it — but it is the ONE place
  // that selects the input wrapper, and the attribute and the selector have to
  // move together or its icon sizing silently stops applying. Renaming one and
  // not the other is exactly the mutant this kills.
  test('its selector names the same slot CommandInput renders', () => {
    const rendered = commandSource.match(/data-slot="([a-z-]+)"/g) ?? [];
    // Positive control: if the attribute is renamed or reformatted out of
    // reach of this regex, fail here rather than passing over an empty list.
    expect(rendered).toContain('data-slot="command-input-wrapper"');

    const selectors = commandSource.match(/\[data-slot=([a-z-]+)\]/g) ?? [];
    expect(selectors.length).toBeGreaterThan(0);
    for (const selector of selectors) {
      const slot = selector.slice('[data-slot='.length, -1);
      expect(rendered).toContain(`data-slot="${slot}"`);
    }
  });

  test('the old attribute survives only as prose, never as markup', () => {
    // The name is still mentioned in the comment that explains the rename, so
    // a bare substring check would fail on correct code. These two forms are
    // the ones that would actually re-emit or re-select it.
    expect(commandSource).not.toContain('cmdk-input-wrapper=');
    expect(commandSource).not.toContain('[cmdk-input-wrapper]');
  });
});
