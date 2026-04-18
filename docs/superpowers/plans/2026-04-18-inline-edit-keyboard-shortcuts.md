# Inline Row-Edit Keyboard Shortcuts Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `Esc` (cancel), `Enter` (save when no child consumes), and `Cmd/Ctrl+Enter` (force-save) shortcuts to the inline transaction-edit row.

**Architecture:** Row-level `onKeyDown` on `<TableRow>` as receiver-of-last-resort. Child widgets (`AutocompleteInput`, `TagInput`, Radix `Select`, Radix `Popover`) follow a single contract: any key the child handles must be `preventDefault()` + `stopPropagation()`; unhandled keys bubble. `Cmd/Ctrl+Enter` is always bubbled by children so the row always sees it.

**Tech Stack:** React 19, TypeScript, Vitest + happy-dom, `@testing-library/react`, `@testing-library/user-event` v14. Test env already wired via `web/vite.config.ts` (`environment: 'happy-dom'`).

**Spec:** [`docs/superpowers/specs/2026-04-18-inline-edit-keyboard-shortcuts-design.md`](../specs/2026-04-18-inline-edit-keyboard-shortcuts-design.md)

---

## Working Directory and Branch

All work happens in the repo root (`D:\claude\SpenDrop`). The current branch is `feat/transaction-edit-ux`. **Do not push and do not open a PR** — the user handles that. Commit on this branch only.

All shell commands run from `web/` unless noted (the React app lives there). Bash on Windows — use forward slashes in paths, `/dev/null` not `NUL`.

---

## Task 0: Commit the spec and plan to the branch

`docs/superpowers/` is gitignored (see `.claude/CLAUDE.md`). The spec and plan files exist on disk but are not tracked on this branch. Force-add them now so they are part of the feature-branch commit history.

- [ ] **Step 1: Verify files exist on disk**

```bash
ls -la docs/superpowers/specs/2026-04-18-inline-edit-keyboard-shortcuts-design.md \
       docs/superpowers/plans/2026-04-18-inline-edit-keyboard-shortcuts.md
```

Expected: both files present (non-zero size).

- [ ] **Step 2: Force-add and commit**

```bash
git add -f docs/superpowers/specs/2026-04-18-inline-edit-keyboard-shortcuts-design.md
git add -f docs/superpowers/plans/2026-04-18-inline-edit-keyboard-shortcuts.md
git commit -m "docs(plan): inline row-edit keyboard shortcuts spec and plan"
```

Expected: one commit on `feat/transaction-edit-ux` containing both files.

---

## File Changes Summary

| File | Change |
|------|--------|
| `web/src/components/AutocompleteInput.tsx` | Rewrite key handling per contract; update JSDoc |
| `web/src/components/AutocompleteInput.test.tsx` | Add "key propagation contract" describe block |
| `web/src/components/TagInput.tsx` | Rewrite key handling per contract; update JSDoc |
| `web/src/components/TagInput.test.tsx` | Add "key propagation contract" describe block |
| `web/src/components/TransactionRow.tsx` | Add `handleRowKeyDown`; attach to `<TableRow>` in edit mode; comment date input |
| `web/src/components/TransactionRow.test.tsx` | Add "keyboard shortcuts" describe block (integration) |
| `web/src/components/AmountCurrencyInput.tsx` | Audited only; modified only if the audit in Task 3 reveals interference |

No backend/schema/API changes.

---

## Cross-Task Conventions

**Test helper for "parent receives keydown":** Several tests need to assert whether a key propagated past the child. Use a small wrapper with a spy on a parent `onKeyDown`:

```tsx
function renderWithParentKeydown(node: React.ReactNode) {
  const parentKeyDown = vi.fn();
  render(<div onKeyDown={parentKeyDown}>{node}</div>);
  return { parentKeyDown };
}
```

Assertions:
- **Key was consumed by child:** `expect(parentKeyDown).not.toHaveBeenCalled()` — the synthetic event's `stopPropagation()` prevents the React bubble.
- **Key bubbled:** `expect(parentKeyDown).toHaveBeenCalledTimes(1)`; optionally `expect(parentKeyDown.mock.calls[0][0].defaultPrevented).toBe(false)` to also verify `preventDefault` was NOT called by the child.

**Simulating modifier-Enter:** `@testing-library/user-event`'s modifier syntax has edge cases in happy-dom. Prefer `fireEvent.keyDown(input, { key: 'Enter', ctrlKey: true })` for modifier tests — deterministic across environments.

**Running a single test file:**
```bash
cd web && npx vitest run src/components/AutocompleteInput.test.tsx
```

**Running the whole web test suite:**
```bash
cd web && npm test
```

---

## Chunk 1: `AutocompleteInput` key-contract rewrite

### Task 1: `AutocompleteInput` — Enter/Escape contract

**Files:**
- Modify: `web/src/components/AutocompleteInput.tsx:115-160` (the `handleKeyDown` body)
- Modify: `web/src/components/AutocompleteInput.test.tsx` (append new describe block)

**Context for the implementer.** Today `AutocompleteInput.handleKeyDown` does **not** touch plain `Enter` at all — `Enter` currently flows to `onKeyDown?.(e)` at the end. It only handles `Tab`/`ArrowRight`/`End` (accept ghost) and `Escape` (set `suppressedPrefix`, then fall through to `onKeyDown`). This task:
1. Adds `Enter` handling: when ghost visible, accept it + consume; when no ghost, bubble unchanged.
2. Changes `Escape` handling so that when ghost was visible, the child consumes (`preventDefault + stopPropagation`) rather than falling through. When no ghost, bubble unchanged.
3. Adds an early-return for `Cmd/Ctrl+Enter` so it always bubbles regardless of ghost state.
4. Adds `stopPropagation()` to the Tab/ArrowRight/End branches for contract consistency (harmless — row handler ignores those keys).

- [ ] **Step 1: Add failing tests**

Append the following describe block to `web/src/components/AutocompleteInput.test.tsx` (after the existing `describe('AutocompleteInput — touch (coarse pointer)', …)` block):

```tsx
// ---------------------------------------------------------------------------
// Key propagation contract (see docs/superpowers/specs/2026-04-18-inline-edit-keyboard-shortcuts-design.md)
// ---------------------------------------------------------------------------
describe('AutocompleteInput — key propagation contract', () => {
  function renderWithParentKeydown(
    props: { suggestions: string[]; initial?: string; onAccept?: (v: string) => void },
  ) {
    const parentKeyDown = vi.fn();
    render(
      <div onKeyDown={parentKeyDown}>
        <ControlledAutocomplete {...props} />
      </div>,
    );
    return { parentKeyDown };
  }

  test('Enter with ghost visible: consumed (preventDefault + stopPropagation), accepts ghost', async () => {
    const user = userEvent.setup();
    const onAccept = vi.fn();
    const { parentKeyDown } = renderWithParentKeydown({ suggestions: ['groceries'], onAccept });
    const input = screen.getByRole('combobox');

    await user.type(input, 'gro');
    await user.keyboard('{Enter}');

    expect(onAccept).toHaveBeenCalledWith('groceries');
    expect(parentKeyDown).not.toHaveBeenCalled();
  });

  test('Enter without ghost: bubbles to parent, does not call onAccept', async () => {
    const user = userEvent.setup();
    const onAccept = vi.fn();
    const { parentKeyDown } = renderWithParentKeydown({ suggestions: ['groceries'], onAccept });
    const input = screen.getByRole('combobox');

    await user.type(input, 'xyz');
    await user.keyboard('{Enter}');

    expect(onAccept).not.toHaveBeenCalled();
    expect(parentKeyDown).toHaveBeenCalledTimes(1);
    expect(parentKeyDown.mock.calls[0][0].key).toBe('Enter');
    expect(parentKeyDown.mock.calls[0][0].defaultPrevented).toBe(false);
  });

  test('Ctrl+Enter bubbles even when ghost visible (modifier bypass)', async () => {
    const onAccept = vi.fn();
    const { parentKeyDown } = renderWithParentKeydown({ suggestions: ['groceries'], onAccept });
    const input = screen.getByRole('combobox');

    // Type prefix so ghost appears, then dispatch modifier-Enter directly.
    const user = userEvent.setup();
    await user.type(input, 'gro');
    expect(screen.getByTestId('autocomplete-ghost')).toHaveTextContent('ceries');

    fireEvent.keyDown(input, { key: 'Enter', ctrlKey: true });

    expect(onAccept).not.toHaveBeenCalled();
    expect(parentKeyDown).toHaveBeenCalledTimes(1);
    expect(parentKeyDown.mock.calls[0][0].ctrlKey).toBe(true);
    expect(parentKeyDown.mock.calls[0][0].defaultPrevented).toBe(false);
  });

  test('Meta+Enter bubbles even when ghost visible (Mac bypass)', async () => {
    const onAccept = vi.fn();
    const { parentKeyDown } = renderWithParentKeydown({ suggestions: ['groceries'], onAccept });
    const input = screen.getByRole('combobox');

    const user = userEvent.setup();
    await user.type(input, 'gro');

    fireEvent.keyDown(input, { key: 'Enter', metaKey: true });

    expect(onAccept).not.toHaveBeenCalled();
    expect(parentKeyDown).toHaveBeenCalledTimes(1);
    expect(parentKeyDown.mock.calls[0][0].metaKey).toBe(true);
  });

  test('Escape with ghost visible: consumed (does NOT bubble)', async () => {
    const user = userEvent.setup();
    const { parentKeyDown } = renderWithParentKeydown({ suggestions: ['groceries'] });
    const input = screen.getByRole('combobox');

    await user.type(input, 'gro');
    await user.keyboard('{Escape}');

    expect(parentKeyDown).not.toHaveBeenCalled();
    expect(screen.getByTestId('autocomplete-ghost')).toHaveTextContent('');
  });

  test('Escape without ghost: bubbles to parent', async () => {
    const user = userEvent.setup();
    const { parentKeyDown } = renderWithParentKeydown({ suggestions: ['groceries'] });
    const input = screen.getByRole('combobox');

    await user.type(input, 'xyz');
    await user.keyboard('{Escape}');

    expect(parentKeyDown).toHaveBeenCalledTimes(1);
    expect(parentKeyDown.mock.calls[0][0].key).toBe('Escape');
  });
});
```

Add `fireEvent` to the imports at the top of the file:

```tsx
import { render, screen, waitFor, fireEvent } from '@testing-library/react';
```

- [ ] **Step 2: Run the new tests — verify they fail**

```bash
cd web && npx vitest run src/components/AutocompleteInput.test.tsx
```

Expected: the six new tests in "key propagation contract" fail. Most will fail because `onAccept` is not called (no Enter handler) or because `parentKeyDown` is called when it shouldn't be (no stopPropagation on Escape with ghost).

- [ ] **Step 3: Implement the contract in `AutocompleteInput.tsx`**

Replace the `handleKeyDown` body (`web/src/components/AutocompleteInput.tsx:115-160`) with:

```ts
const handleKeyDown = useCallback(
  (e: KeyboardEvent<HTMLInputElement>) => {
    // Modifier bypass — row-level Cmd/Ctrl+Enter must always see the event.
    // This runs before any branch so no widget-local key handling can intercept.
    if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
      onKeyDown?.(e);
      return;
    }

    if (isCoarse) {
      // Touch: only Escape closes the popover. Consume when we actually close.
      if (e.key === 'Escape' && touchOpen) {
        e.preventDefault();
        e.stopPropagation();
        setTouchOpen(false);
        return;
      }
      onKeyDown?.(e);
      return;
    }

    // Desktop branch.
    const caretAtEnd =
      e.currentTarget.selectionStart === text.length &&
      e.currentTarget.selectionEnd === text.length;

    // Tab: accept ONLY when ghost visible; otherwise let native focus advance.
    if (e.key === 'Tab' && !e.shiftKey && ghostActive && match !== text) {
      e.preventDefault();
      e.stopPropagation();
      acceptMatch(match);
      return;
    }

    if (e.key === 'ArrowRight' && ghostActive && caretAtEnd) {
      e.preventDefault();
      e.stopPropagation();
      acceptMatch(match);
      return;
    }

    if (e.key === 'End' && ghostActive) {
      e.preventDefault();
      e.stopPropagation();
      acceptMatch(match);
      return;
    }

    // Enter with ghost visible: accept the ghost. Consumed — does not bubble.
    if (e.key === 'Enter' && ghostActive) {
      e.preventDefault();
      e.stopPropagation();
      acceptMatch(match);
      return;
    }

    // Enter without ghost: fall through — the row-level handler will save.
    // (No preventDefault, no stopPropagation, no return.)

    if (e.key === 'Escape' && ghostActive) {
      // Consume Escape when the ghost is actually visible: suppress it for this
      // prefix and STOP here. The prior "don't preventDefault so parent Escape
      // handlers fire" comment was wrong for the layered-edit model — letting
      // Escape bubble while also handling it causes the GitHub 2023 regression
      // (single Esc press closes picker AND cancels the row).
      e.preventDefault();
      e.stopPropagation();
      setSuppressedPrefix(text);
      return;
    }

    onKeyDown?.(e);
  },
  [isCoarse, ghostActive, match, text, touchOpen, onKeyDown, acceptMatch],
);
```

Also update the component-level JSDoc to document the contract. Replace the existing `/** AutocompleteInput — inline ghost-text completion (desktop) + popover list (touch). */` block with:

```ts
/**
 * AutocompleteInput — inline ghost-text completion (desktop) + popover list (touch).
 *
 * Accessibility tradeoff note:
 *   The desktop "ghost" is drawn as a sibling overlay `<span>` rather than as real
 *   selected text inside the input. This keeps the component controlled-friendly
 *   for React-Hook-Form (single source of truth on `value`). To compensate, we
 *   wire `role="combobox"` + `aria-autocomplete="inline"` on the input and mirror
 *   the full match into an `aria-live="polite"` span so screen readers announce
 *   the completion as the user types.
 *
 * Key-propagation contract (see spec 2026-04-18-inline-edit-keyboard-shortcuts):
 *   - Keys this component ACTS on (Enter/Escape with ghost, Tab/ArrowRight/End
 *     with ghost, Escape to close the touch popover) are consumed with BOTH
 *     preventDefault() and stopPropagation(), so the row-level keydown handler
 *     does NOT see them.
 *   - Keys this component does NOT act on (Enter without a ghost, Escape without
 *     a ghost, any Cmd/Ctrl+Enter) bubble to the parent unchanged.
 *   - Cmd/Ctrl+Enter short-circuits at the TOP of handleKeyDown so the row-level
 *     force-save can never be intercepted by widget-local Enter handling.
 */
```

- [ ] **Step 4: Run the tests — verify they pass**

```bash
cd web && npx vitest run src/components/AutocompleteInput.test.tsx
```

Expected: all tests in the file pass, including the six new ones. Existing tests in "desktop (fine pointer)" and "touch (coarse pointer)" must still pass.

- [ ] **Step 5: Commit**

```bash
cd web
git add src/components/AutocompleteInput.tsx src/components/AutocompleteInput.test.tsx
git commit -m "feat(autocomplete): enforce key-propagation contract for layered edit"
```

---

## Chunk 2: `TagInput` key-contract rewrite

### Task 2: `TagInput` — Enter/Escape contract

**Files:**
- Modify: `web/src/components/TagInput.tsx:133-206` (the `handleKeyDown` function body)
- Modify: `web/src/components/TagInput.test.tsx` (append new describe block)

**Context for the implementer.** Today `TagInput.handleKeyDown`:
- Touch branch: handles `Escape` (close popover) but never `preventDefault`s; handles `Enter`/`,` by preventDefault + addTag regardless of buffer length; handles `Backspace` to remove the last chip when buffer is empty.
- Desktop branch: handles `Tab`/`ArrowRight`/`End` (accept ghost), `Enter` (preventDefault + addTag always — even with empty buffer), `,` (preventDefault + addTag), `Escape` (setSuppressedPrefix but no preventDefault), `Backspace` (remove last chip).

The key change: `Enter` with empty buffer must **not** preventDefault so it bubbles to the row for save. Similarly `Escape` with neither ghost nor popover open must bubble. And every consumed key must add `stopPropagation()`.

- [ ] **Step 1: Add failing tests**

Append this describe block to the end of `web/src/components/TagInput.test.tsx`:

```tsx
// ---------------------------------------------------------------------------
// Key propagation contract (see docs/superpowers/specs/2026-04-18-inline-edit-keyboard-shortcuts-design.md)
// ---------------------------------------------------------------------------
describe('TagInput — key propagation contract', () => {
  function renderWithParentKeydown(node: ReactNode) {
    const parentKeyDown = vi.fn();
    render(<div onKeyDown={parentKeyDown}>{node}</div>);
    return { parentKeyDown };
  }

  test('Enter with typed buffer: consumed, addTag fires, does not bubble', async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    const { parentKeyDown } = renderWithParentKeydown(
      <ControlledTagInput onChange={onChange} />,
    );
    const input = screen.getByRole('combobox');

    await user.type(input, 'gro');
    await user.keyboard('{Enter}');

    expect(onChange).toHaveBeenCalledWith('gro');
    expect(parentKeyDown).not.toHaveBeenCalled();
  });

  test('Enter with empty buffer: NOT consumed, bubbles to parent', async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    const { parentKeyDown } = renderWithParentKeydown(
      <ControlledTagInput onChange={onChange} />,
    );
    const input = screen.getByRole('combobox');

    await user.click(input);
    await user.keyboard('{Enter}');

    expect(onChange).not.toHaveBeenCalled();
    expect(parentKeyDown).toHaveBeenCalledTimes(1);
    expect(parentKeyDown.mock.calls[0][0].key).toBe('Enter');
  });

  test('Ctrl+Enter with typed buffer: NOT consumed (modifier bypass), bubbles', async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    const { parentKeyDown } = renderWithParentKeydown(
      <ControlledTagInput onChange={onChange} />,
    );
    const input = screen.getByRole('combobox');

    await user.type(input, 'gro');
    fireEvent.keyDown(input, { key: 'Enter', ctrlKey: true });

    expect(onChange).not.toHaveBeenCalled();
    expect(parentKeyDown).toHaveBeenCalledTimes(1);
    expect(parentKeyDown.mock.calls[0][0].ctrlKey).toBe(true);
  });

  test('Meta+Enter with typed buffer: NOT consumed, bubbles', async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    const { parentKeyDown } = renderWithParentKeydown(
      <ControlledTagInput onChange={onChange} />,
    );
    const input = screen.getByRole('combobox');

    await user.type(input, 'gro');
    fireEvent.keyDown(input, { key: 'Enter', metaKey: true });

    expect(onChange).not.toHaveBeenCalled();
    expect(parentKeyDown).toHaveBeenCalledTimes(1);
    expect(parentKeyDown.mock.calls[0][0].metaKey).toBe(true);
  });

  test('Escape with ghost visible: consumed, does not bubble', async () => {
    const user = userEvent.setup();
    const { parentKeyDown } = renderWithParentKeydown(
      <ControlledTagInput suggestions={['groceries']} />,
    );
    const input = screen.getByRole('combobox');

    await user.type(input, 'gro');
    expect(screen.getByTestId('taginput-ghost')).toHaveTextContent('ceries');

    await user.keyboard('{Escape}');

    expect(parentKeyDown).not.toHaveBeenCalled();
  });

  test('Escape with no ghost and no popover: bubbles to parent', async () => {
    const user = userEvent.setup();
    const { parentKeyDown } = renderWithParentKeydown(<ControlledTagInput />);
    const input = screen.getByRole('combobox');

    await user.click(input);
    await user.keyboard('{Escape}');

    expect(parentKeyDown).toHaveBeenCalledTimes(1);
    expect(parentKeyDown.mock.calls[0][0].key).toBe('Escape');
  });
});
```

Ensure `ReactNode` and `fireEvent` are imported at the top of the file. The existing `import { useState } from 'react';` already exists — add `ReactNode` as a type-only import alongside it, and extend the `@testing-library/react` import to include `fireEvent`:

```tsx
import type { ReactNode } from 'react';
import { useState } from 'react';
import { render, screen, waitFor, fireEvent } from '@testing-library/react';
```

The helper uses the bare `ReactNode` identifier (not `React.ReactNode`). Do not add a default `import React from 'react'` — it is not needed and would trigger a no-unused-vars warning.

- [ ] **Step 2: Run the new tests — verify they fail**

```bash
cd web && npx vitest run src/components/TagInput.test.tsx
```

Expected: all six new tests fail. In particular "Enter with empty buffer: bubbles" fails because the current `Enter` handler always calls `preventDefault` and returns regardless of buffer.

- [ ] **Step 3: Implement the contract in `TagInput.tsx`**

Replace the `handleKeyDown` function body at `web/src/components/TagInput.tsx:133-206` with:

```ts
function handleKeyDown(e: KeyboardEvent<HTMLInputElement>) {
  // Modifier bypass — row-level Cmd/Ctrl+Enter must always see the event.
  if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
    return;
  }

  // Touch branch.
  if (isCoarse) {
    if (e.key === 'Escape' && touchOpen) {
      e.preventDefault();
      e.stopPropagation();
      setTouchOpen(false);
      return;
    }
    if (e.key === 'Enter') {
      // Commit typed buffer only if there is one; otherwise bubble so the row
      // handler can save. `addTag` would early-return on empty, but we also
      // must not call preventDefault in that case or Enter is swallowed.
      if (input.length > 0) {
        e.preventDefault();
        e.stopPropagation();
        addTag(input);
      }
      return;
    }
    if (e.key === ',' && input.length > 0) {
      e.preventDefault();
      e.stopPropagation();
      addTag(input);
      return;
    }
    if (e.key === 'Backspace' && input === '' && tags.length > 0) {
      removeTag(tags.length - 1);
    }
    return;
  }

  // Desktop branch.
  const caretAtEnd =
    e.currentTarget.selectionStart === input.length &&
    e.currentTarget.selectionEnd === input.length;

  if (e.key === 'Tab' && !e.shiftKey && ghostActive) {
    e.preventDefault();
    e.stopPropagation();
    addTag(match);
    return;
  }

  if (e.key === 'ArrowRight' && ghostActive && caretAtEnd) {
    e.preventDefault();
    e.stopPropagation();
    addTag(match);
    return;
  }

  if (e.key === 'End' && ghostActive) {
    e.preventDefault();
    e.stopPropagation();
    addTag(match);
    return;
  }

  if (e.key === 'Enter') {
    // Two commit paths:
    //   - ghost visible → commit the ghost suggestion (consumed)
    //   - buffer non-empty → commit the typed buffer (consumed)
    //   - buffer empty, no ghost → bubble so the row handler can save
    if (ghostActive) {
      e.preventDefault();
      e.stopPropagation();
      addTag(match);
      return;
    }
    if (input.length > 0) {
      e.preventDefault();
      e.stopPropagation();
      addTag(input);
      return;
    }
    return; // bubble
  }

  if (e.key === ',' && input.length > 0) {
    e.preventDefault();
    e.stopPropagation();
    addTag(input);
    return;
  }

  if (e.key === 'Escape' && ghostActive) {
    e.preventDefault();
    e.stopPropagation();
    setSuppressedPrefix(input);
    return;
  }

  if (e.key === 'Backspace' && input === '' && tags.length > 0) {
    removeTag(tags.length - 1);
  }
}
```

Also update the component JSDoc (at `web/src/components/TagInput.tsx:25-33`) to add the contract note:

```ts
/**
 * TagInput — comma-separated tag entry with autocomplete.
 *
 * Mirrors AutocompleteInput's a11y strategy: the desktop "ghost" is a sibling
 * overlay `<span>` (not selected text inside the input), and a `role="combobox"`
 * + `aria-live` mirror announce the completion to screen readers. Touch uses a
 * shadcn Popover + Command dropdown anchored to the input via PopoverAnchor so
 * the input keeps focus.
 *
 * Key-propagation contract (see spec 2026-04-18-inline-edit-keyboard-shortcuts):
 *   - Keys this component ACTS on (Enter/",", on a non-empty buffer or visible
 *     ghost; Tab/ArrowRight/End on ghost; Escape on ghost or open popover) are
 *     consumed with BOTH preventDefault() and stopPropagation().
 *   - Enter on an EMPTY buffer with no ghost bubbles — this is the "Enter twice
 *     after your last tag" pattern: first Enter commits the chip, second Enter
 *     reaches the row handler and saves.
 *   - Cmd/Ctrl+Enter short-circuits at the top so the row-level force-save is
 *     never intercepted.
 */
```

- [ ] **Step 4: Run the tests — verify they pass**

```bash
cd web && npx vitest run src/components/TagInput.test.tsx
```

Expected: all tests in the file pass, including the six new ones. The existing tests that check `Enter` commits the typed buffer must still pass.

- [ ] **Step 5: Commit**

```bash
cd web
git add src/components/TagInput.tsx src/components/TagInput.test.tsx
git commit -m "feat(tag-input): enforce key-propagation contract for layered edit"
```

---

## Chunk 3: `TransactionRow` row-level handler + primitive audits

### Task 3: `TransactionRow` — row-level keyboard handler

**Files:**
- Modify: `web/src/components/TransactionRow.tsx:147-253` (the `if (editing) { return <TableRow>…</TableRow>; }` branch)
- Modify: `web/src/components/TransactionRow.test.tsx` (append new describe block)
- Audit only (no change expected): `web/src/components/AmountCurrencyInput.tsx`. Change only if the integration tests below reveal the numeric input or the currency popover swallows `Enter`/`Escape`.

**Context for the implementer.** The edit branch of `TransactionRow` currently wires Save/Cancel via a `<form onSubmit={handleSave}>` that wraps ONLY the Save/Cancel buttons (line 227-230). The five input widgets live outside that form — that's why plain `Enter` in description/tags/amount does nothing today. This task attaches a row-level `onKeyDown` to the `<TableRow>` so `Enter` and `Esc` reach a handler regardless of which input has focus.

**Audit rules for Radix primitives (Select for category, Popover for currency picker):** the integration tests at Step 1 will expose whether Radix correctly stops propagation when handling Enter/Escape with its overlay open. In the versions we use (`@radix-ui/react-select ^2.2.6`, `@radix-ui/react-popover ^1.1.15`), Radix does stop propagation on keys it handles. If the tests prove otherwise, wrap the affected `SelectTrigger` / `PopoverTrigger` in a small div with `onKeyDownCapture` that swallows `Enter`/`Escape` when the overlay is open. Do NOT add that wrapper preemptively.

- [ ] **Step 1: Add failing integration tests**

Append this describe block to the end of `web/src/components/TransactionRow.test.tsx`:

```tsx
// ---------------------------------------------------------------------------
// Keyboard shortcuts for inline edit (see spec 2026-04-18)
// ---------------------------------------------------------------------------
describe('TransactionRow — keyboard shortcuts', () => {
  async function openEditMode(description = 'Weekly groceries') {
    const user = await openActionsMenu(description);
    await user.click(screen.getByRole('menuitem', { name: /edit/i }));
    return user;
  }

  it('Esc on a clean edit row cancels, does not call onUpdate', async () => {
    const onUpdate = vi.fn().mockResolvedValue(undefined);
    renderRow(makeTx({ description: 'Weekly groceries' }), onUpdate);
    const user = await openEditMode();

    // Focus the description input so Esc originates inside the row.
    const desc = screen.getByLabelText('Description') as HTMLInputElement;
    await user.click(desc);
    await user.keyboard('{Escape}');

    expect(onUpdate).not.toHaveBeenCalled();
    // Row should leave edit mode — Save button no longer present.
    expect(screen.queryByRole('button', { name: /save/i })).not.toBeInTheDocument();
  });

  it('Esc on a dirty edit row reverts silently to original values', async () => {
    const onUpdate = vi.fn().mockResolvedValue(undefined);
    renderRow(makeTx({ description: 'Weekly groceries' }), onUpdate);
    const user = await openEditMode();

    const desc = screen.getByLabelText('Description') as HTMLInputElement;
    await user.click(desc);
    await user.clear(desc);
    await user.type(desc, 'EDITED BUT ABANDONED');
    expect(desc.value).toBe('EDITED BUT ABANDONED');

    await user.keyboard('{Escape}');

    expect(onUpdate).not.toHaveBeenCalled();
    // Row renders the ORIGINAL description, not the typed-then-abandoned text.
    expect(screen.getByText('Weekly groceries')).toBeInTheDocument();
    expect(screen.queryByText('EDITED BUT ABANDONED')).not.toBeInTheDocument();
  });

  it('Enter from description input (no ghost) saves the row', async () => {
    const onUpdate = vi.fn().mockResolvedValue(undefined);
    renderRow(makeTx(), onUpdate);
    const user = await openEditMode();

    const desc = screen.getByLabelText('Description') as HTMLInputElement;
    await user.click(desc);
    await user.keyboard('{Enter}');

    await waitFor(() => expect(onUpdate).toHaveBeenCalledTimes(1));
  });

  it('Enter from description with a ghost visible accepts ghost, does NOT save', async () => {
    // Seed a description suggestion so typing the existing description produces a ghost.
    // Use a non-empty placeholder description so the Actions button's
    // aria-label is "Actions for x" (not "Actions for " with a trailing
    // space, which is fragile to match).
    const onUpdate = vi.fn().mockResolvedValue(undefined);
    render(
      <table>
        <tbody>
          <TransactionRow
            transaction={makeTx({ description: 'x' })}
            categories={mockCategories}
            onUpdate={onUpdate}
            onDelete={vi.fn().mockResolvedValue(undefined)}
            onError={vi.fn()}
            descriptionSuggestions={['groceries-weekly']}
          />
        </tbody>
      </table>,
    );
    const user = await openActionsMenu('x');
    await user.click(screen.getByRole('menuitem', { name: /edit/i }));

    const desc = screen.getByLabelText('Description') as HTMLInputElement;
    await user.clear(desc);
    await user.type(desc, 'gro');
    expect(screen.getByTestId('autocomplete-ghost')).toHaveTextContent('ceries-weekly');

    await user.keyboard('{Enter}');

    expect(onUpdate).not.toHaveBeenCalled();
    expect(desc.value).toBe('groceries-weekly');

    // A second Enter (ghost now gone) saves.
    await user.keyboard('{Enter}');
    await waitFor(() => expect(onUpdate).toHaveBeenCalledTimes(1));
  });

  it('Enter from tag input with typed buffer commits the chip, does NOT save', async () => {
    const onUpdate = vi.fn().mockResolvedValue(undefined);
    renderRow(makeTx({ tags: null }), onUpdate);
    const user = await openEditMode();

    const tagInput = screen.getByLabelText('Add tag') as HTMLInputElement;
    await user.click(tagInput);
    await user.type(tagInput, 'urgent');
    await user.keyboard('{Enter}');

    expect(onUpdate).not.toHaveBeenCalled();
    expect(screen.getByText('urgent')).toBeInTheDocument();
    expect(tagInput.value).toBe('');

    // Second Enter (empty buffer) saves.
    await user.keyboard('{Enter}');
    await waitFor(() =>
      expect(onUpdate).toHaveBeenCalledWith(expect.objectContaining({ tags: 'urgent' })),
    );
  });

  it('Enter from amount input saves the row', async () => {
    const onUpdate = vi.fn().mockResolvedValue(undefined);
    renderRow(makeTx(), onUpdate);
    const user = await openEditMode();

    const amount = screen.getByRole('spinbutton') as HTMLInputElement;
    await user.click(amount);
    await user.keyboard('{Enter}');

    await waitFor(() => expect(onUpdate).toHaveBeenCalledTimes(1));
  });

  it('Ctrl+Enter force-saves even when a ghost is visible', async () => {
    const onUpdate = vi.fn().mockResolvedValue(undefined);
    render(
      <table>
        <tbody>
          <TransactionRow
            transaction={makeTx()}
            categories={mockCategories}
            onUpdate={onUpdate}
            onDelete={vi.fn().mockResolvedValue(undefined)}
            onError={vi.fn()}
            descriptionSuggestions={['groceries-weekly']}
          />
        </tbody>
      </table>,
    );
    const user = await openActionsMenu('Weekly groceries');
    await user.click(screen.getByRole('menuitem', { name: /edit/i }));

    const desc = screen.getByLabelText('Description') as HTMLInputElement;
    await user.clear(desc);
    await user.type(desc, 'gro');
    expect(screen.getByTestId('autocomplete-ghost')).toHaveTextContent('ceries-weekly');

    // Modifier bypass: ghost must NOT be accepted.
    fireEvent.keyDown(desc, { key: 'Enter', ctrlKey: true });

    await waitFor(() => expect(onUpdate).toHaveBeenCalledTimes(1));
    // The description at save time is still the typed buffer, not the ghost.
    expect(onUpdate.mock.calls[0][0].description).toBe('gro');
  });

  it('Cmd+Enter behaves the same as Ctrl+Enter (macOS)', async () => {
    const onUpdate = vi.fn().mockResolvedValue(undefined);
    renderRow(makeTx(), onUpdate);
    const user = await openEditMode();

    const desc = screen.getByLabelText('Description') as HTMLInputElement;
    await user.click(desc);
    fireEvent.keyDown(desc, { key: 'Enter', metaKey: true });

    await waitFor(() => expect(onUpdate).toHaveBeenCalledTimes(1));
  });

  it('Esc inside open category Select closes the Select and does NOT cancel the row', async () => {
    const onUpdate = vi.fn().mockResolvedValue(undefined);
    renderRow(makeTx(), onUpdate);
    const user = await openEditMode();

    // The edit row has three `role=combobox` elements (description,
    // category, tag). Radix SelectTrigger uniquely sets
    // `aria-haspopup="listbox"`; the other two set `aria-autocomplete`.
    // Filter on `aria-haspopup` for an unambiguous, label-agnostic match.
    const trigger = screen
      .getAllByRole('combobox')
      .find((el) => el.getAttribute('aria-haspopup') === 'listbox');
    if (!trigger) throw new Error('Category SelectTrigger not found');
    await user.click(trigger);
    // An option is now visible in the portal.
    await screen.findByRole('option', { name: 'Groceries' });

    // First Escape: closes Select, row stays in edit mode.
    await user.keyboard('{Escape}');
    await waitFor(() =>
      expect(screen.queryByRole('option', { name: 'Groceries' })).not.toBeInTheDocument(),
    );
    expect(screen.getByRole('button', { name: /save/i })).toBeInTheDocument();
    expect(onUpdate).not.toHaveBeenCalled();

    // Second Escape: cancels the row (receiver of last resort).
    await user.keyboard('{Escape}');
    expect(screen.queryByRole('button', { name: /save/i })).not.toBeInTheDocument();
    expect(onUpdate).not.toHaveBeenCalled();
  });
});
```

Add `fireEvent` to the imports (the test file already imports `render, screen, fireEvent, waitFor` on line 1 — no change needed).

- [ ] **Step 2: Run the new tests — verify they fail**

```bash
cd web && npx vitest run src/components/TransactionRow.test.tsx
```

Expected: the new "keyboard shortcuts" block fails. Concretely: Esc tests fail because no row-level Escape handler exists; Enter tests fail because no row-level Enter handler exists; Ctrl/Cmd+Enter tests fail for the same reason; the Select-Esc two-tap test may fail on the *second* Escape if (and only if) no row handler exists yet.

- [ ] **Step 3: Refactor `handleSave` to take no arguments**

`handleSave` currently takes a `FormEvent` just to call `e.preventDefault()`. The row-level keyboard handler needs to invoke the same save logic with a `KeyboardEvent`, and casting between the two is a lint smell. Move `preventDefault` out to the `<form onSubmit>` callback so `handleSave` becomes callable from both places without a cast.

Replace:

```tsx
async function handleSave(e: FormEvent) {
  e.preventDefault();
  setSaving(true);
```

with:

```tsx
async function handleSave() {
  setSaving(true);
```

And change the `<form>` opening tag (around `web/src/components/TransactionRow.tsx:227-230`) from:

```tsx
<form
  onSubmit={(e) => void handleSave(e)}
  className="flex h-10 items-center justify-end gap-1"
>
```

to:

```tsx
<form
  onSubmit={(e) => {
    e.preventDefault();
    void handleSave();
  }}
  className="flex h-10 items-center justify-end gap-1"
>
```

Also, `FormEvent` is no longer imported for this signature — only `KeyboardEvent` typing is needed downstream. If the existing `import type { FormEvent } from 'react';` is now unused, remove it; otherwise leave the import alone.

- [ ] **Step 4: Add `handleRowKeyDown` to `TransactionRow.tsx`**

Inside the `TransactionRow` component body, **before** the `if (editing)` return (around `web/src/components/TransactionRow.tsx:147`), add:

```ts
function handleRowKeyDown(e: React.KeyboardEvent<HTMLTableRowElement>) {
  // Cmd/Ctrl+Enter — force-save. Children bypass the modifier at the top of
  // their own keydown handlers, so we are guaranteed to see this.
  if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
    e.preventDefault();
    e.stopPropagation();
    void handleSave();
    return;
  }
  // Plain Enter reaches us only if no child consumed it.
  if (e.key === 'Enter' && !e.metaKey && !e.ctrlKey && !e.shiftKey && !e.altKey) {
    e.preventDefault();
    e.stopPropagation(); // symmetry with Escape: the row fully owns the key here
    void handleSave();
    return;
  }
  // Plain Escape reaches us only if no child consumed it.
  if (e.key === 'Escape' && !e.metaKey && !e.ctrlKey && !e.shiftKey && !e.altKey) {
    e.preventDefault();
    e.stopPropagation(); // keep page-level Esc handlers from also firing
    handleCancel();
    return;
  }
}
```

Then change the edit-mode `<TableRow>` opening tag (at `web/src/components/TransactionRow.tsx:149`) from:

```tsx
<TableRow className="[&>td]:align-top">
```

to:

```tsx
<TableRow className="[&>td]:align-top" onKeyDown={handleRowKeyDown}>
```

- [ ] **Step 5: Add the date-input comment about key-swallowing**

At the date `<Input>` around `web/src/components/TransactionRow.tsx:166-170`, replace:

```tsx
<TableCell>
  <Input
    type="date"
    value={date}
    onChange={(e) => setDate(e.target.value)}
  />
</TableCell>
```

with:

```tsx
<TableCell>
  {/* Native `<input type="date">` has cross-browser key-swallowing quirks while
      its picker is open: Chrome/Edge and Firefox often do not bubble Enter/Esc
      out to React. That is documented and manually verified — do not attempt
      to force-normalize. Users close the picker (mouse or outside-click) and
      then press Enter/Esc in any other field to save/cancel. */}
  <Input
    type="date"
    value={date}
    onChange={(e) => setDate(e.target.value)}
  />
</TableCell>
```

- [ ] **Step 6: Run the tests — verify they pass**

```bash
cd web && npx vitest run src/components/TransactionRow.test.tsx
```

Expected: the nine new tests pass. All pre-existing tests in the file must still pass.

If the "Esc inside open Select" test fails on the first Escape (the Select did not close before the row did), Radix is not stopPropagating on its Escape. In that case, wrap the `<Select>` at line 182-193 with:

```tsx
<div
  onKeyDownCapture={(e) => {
    if ((e.key === 'Escape' || e.key === 'Enter') && document.querySelector('[data-state="open"][data-radix-select-content]')) {
      e.stopPropagation();
    }
  }}
>
  <Select value={categoryId} onValueChange={setCategoryId}>
    {/* …unchanged… */}
  </Select>
</div>
```

Only add the wrapper if the test proves it necessary. Re-run the test after any such change.

- [ ] **Step 7: Run the full web suite**

```bash
cd web && npm test
```

Expected: all suites pass. No regressions in `AutocompleteInput.test.tsx`, `TagInput.test.tsx`, or any other file.

- [ ] **Step 8: Commit**

```bash
cd web
git add src/components/TransactionRow.tsx src/components/TransactionRow.test.tsx
git commit -m "feat(transactions): Esc cancels + Enter/Cmd+Enter saves inline edit row"
```

---

## Chunk 4: Final verification

### Task 4: Type-check, lint, full suite, manual matrix

**Files:**
- No source changes unless verification exposes a defect.
- If manual tests expose a bug (e.g. Firefox swallows something we expected to bubble), file a follow-up note in the PR description — do not silently fix without discussion.

- [ ] **Step 1: Strict TypeScript build**

```bash
cd web && npx tsc -b
```

Expected: exit 0, no errors. `tsc -b` is the mode the Docker build uses; stricter than `tsc --noEmit` because it checks project references too.

- [ ] **Step 2: ESLint**

```bash
cd web && npx eslint .
```

Expected: exit 0, no errors.

- [ ] **Step 3: Full test suite**

```bash
cd web && npm test
```

Expected: every test in every file passes.

- [ ] **Step 4: Manual test matrix — native date input**

With the dev container running (`docker compose -f docker-compose.dev.yml up --build -d` from repo root), open `http://localhost:3535` and for each of Chrome, Firefox, Safari desktop:

| Action | Expected |
|--------|----------|
| Focus date input, press `Enter` (no picker open) | Row saves |
| Focus date input, press `Esc` (no picker open) | Row cancels |
| Focus date input, click the calendar icon to open picker, press `Enter` | Picker confirms highlighted date and closes; row does NOT save (browser swallows) — acceptable |
| Focus date input, click the calendar icon to open picker, press `Esc` | Picker closes; row does NOT cancel (browser swallows) — acceptable |

Record results in the PR description. Any browser where the "no picker open" row-level behavior fails is a real bug — return to Chunk 3 and debug.

- [ ] **Step 5: Manual test — desktop row-level shortcuts**

On desktop Chrome at `http://localhost:3535` Transactions page, open edit on a row and manually verify:

- [ ] `Esc` with no changes → row closes, no save.
- [ ] `Esc` after typing a new description → row closes, description reverts.
- [ ] `Enter` in description with no ghost → row saves.
- [ ] `Enter` in description with ghost visible → ghost accepted; next Enter saves.
- [ ] `Enter` in tag input with typed buffer → chip added; next Enter saves.
- [ ] `Enter` in amount input → row saves.
- [ ] `Ctrl+Enter` in description with ghost visible → row saves with the TYPED buffer (ghost not accepted).

- [ ] **Step 6: Manual test — touch popovers still work**

On one iOS Safari (device or simulator) and one Android Chrome device, tap-edit a transaction row and verify:

- [ ] Tapping a description suggestion in the popover commits the value as a completion (not as a tag, not as a save) — confirms the blur-guard from `fde3c75` is unaffected.
- [ ] Tapping a tag suggestion in the popover adds the chip and closes the popover.

This only re-verifies that the key-contract rewrite did not regress touch blur-guarding. No new touch-specific behavior to check.

- [ ] **Step 7: No commit — verification-only task**

This task produces no code changes. All verification results go in the PR description; any failures bounce back to Chunk 3.

---

## Success Criteria (from spec)

- [x] Covered by plan: `npx tsc -b` clean (Chunk 4 Step 1).
- [x] Covered by plan: `npx eslint .` clean (Chunk 4 Step 2).
- [x] Covered by plan: `npx vitest run` clean with new tests present (Chunks 1-3 + Chunk 4 Step 3).
- [x] Covered by plan: native-date manual matrix (Chunk 4 Step 4).
- [x] Covered by plan: code-reviewer subagent approves the diff — handled by `superpowers:subagent-driven-development`'s review loop after each task.

---

## Notes for the executor

1. **Spec is authoritative.** If anything in this plan conflicts with `docs/superpowers/specs/2026-04-18-inline-edit-keyboard-shortcuts-design.md`, the spec wins — stop and flag it.
2. **Do not re-scope.** If you find an easy-looking improvement in `AmountCurrencyInput` or the page-level Transactions shell, file it as a follow-up, don't bundle it.
3. **The contract is tight.** When in doubt about whether to add `stopPropagation()` in a child, apply the rule literally: did the child change state or call a callback in response to this key? If yes, `stopPropagation()`. If no, return without touching the event.
4. **Test the failure first.** Each task's Step 2 runs the tests BEFORE implementation so we see the red. Do not skip it — a test that passes without implementation means the test is wrong.
5. **Working directory is the repo root for git commands, `web/` for npm/npx.** Stay consistent so commit paths are right.
