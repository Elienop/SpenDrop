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
  // `cmdk-item`, `cmdk-group-heading` and friends — and the attribute was
  // unknown to JSX (SonarQube S6747) and non-conforming HTML, harmless at
  // runtime but dead weight. It is now `data-slot`.
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
  //
  // Both sides are read with the same permissive name pattern, and both admit
  // either quote style or none. A pin that only recognised `[a-z-]+` inside an
  // UNQUOTED selector — which is what these were — stops coupling the attribute
  // to the selector the moment either one is written another legal way:
  // `[data-slot="foo"]` and a slot name carrying a digit both slipped the old
  // regexes, and an unmatched selector is not reported, it is simply never
  // checked. Capturing the NAME rather than the whole match is what lets the
  // two forms be compared at all.
  //
  // The rendered side needs the `(?<!\[)` too, and it is not decoration:
  // accepting a quoted name without it makes a QUOTED SELECTOR — legal
  // Tailwind, `[&_[data-slot='x']_svg]` — read as a rendered attribute, so the
  // selector would satisfy the check by naming itself. Caught by mutation: a
  // renamed, single-quoted selector passed the loop below until this was added.
  const RENDERED_SLOT = /(?<!\[)data-slot=["']([\w-]+)["']/g;
  const SELECTED_SLOT = /\[data-slot=["']?([\w-]+)["']?\]/g;

  const namesIn = (source: string, pattern: RegExp) =>
    Array.from(source.matchAll(pattern), (match) => match[1]);

  /**
   * The slot `CommandInput` actually renders around its input, read off the
   * DOM. Deliberately not a literal: a literal keeps naming the old slot after
   * a rename, and the coupling under test is between what the component
   * RENDERS and what `CommandDialog` SELECTS.
   */
  function wrapperSlotFromDom(): string {
    const { container } = render(
      <Command>
        <CommandInput placeholder="Search currency..." />
        <CommandList />
      </Command>,
    );
    const slot = container
      .querySelector('[cmdk-input]')
      ?.closest('[data-slot]')
      ?.getAttribute('data-slot');
    if (!slot) {
      throw new Error('CommandInput rendered no data-slot wrapper around its input');
    }
    return slot;
  }

  test('its selector names the same slot CommandInput renders', () => {
    const rendered = namesIn(commandSource, RENDERED_SLOT);
    const selected = namesIn(commandSource, SELECTED_SLOT);
    const wrapperSlot = wrapperSlotFromDom();

    // The specific coupling, and the positive control for both patterns at
    // once: the slot the component really renders has to be one the file
    // styles. A bare `selected.length > 0` is satisfied by ANY data-slot pair
    // in the file, so the moment command.tsx grows a second slot it stops
    // saying anything about the wrapper — which is the one CommandDialog's
    // icon sizing depends on, and the one with no call site to notice.
    expect(rendered).toContain(wrapperSlot);
    expect(selected).toContain(wrapperSlot);

    // The other direction: no selector may name a slot that is never rendered.
    for (const slot of selected) {
      expect(rendered).toContain(slot);
    }
  });

  test('the widened patterns catch the forms the narrow ones missed', () => {
    // Each of these is a legal way to write the pair that the previous
    // `[a-z-]+`, unquoted-only regexes did not match at all.
    expect(namesIn('data-slot="slot-2"', RENDERED_SLOT)).toEqual(['slot-2']);
    expect(namesIn("data-slot='slot-2'", RENDERED_SLOT)).toEqual(['slot-2']);
    expect(namesIn('[data-slot="slot-2"]', SELECTED_SLOT)).toEqual(['slot-2']);
    expect(namesIn("[data-slot='slot-2']", SELECTED_SLOT)).toEqual(['slot-2']);
    expect(namesIn('[data-slot=slot-2]', SELECTED_SLOT)).toEqual(['slot-2']);

    // ...and the one form that must NOT count as a rendered attribute, or a
    // selector could vouch for itself.
    expect(namesIn("[data-slot='slot-2']", RENDERED_SLOT)).toEqual([]);
    expect(namesIn('[data-slot="slot-2"]', RENDERED_SLOT)).toEqual([]);
  });

  test('the old attribute survives only as prose, never as markup', () => {
    // The name is still mentioned in the comment that explains the rename, so
    // a bare substring check would fail on correct code. These two forms are
    // the ones that would actually re-emit or re-select it.
    expect(commandSource).not.toContain('cmdk-input-wrapper=');
    expect(commandSource).not.toContain('[cmdk-input-wrapper]');
  });
});
