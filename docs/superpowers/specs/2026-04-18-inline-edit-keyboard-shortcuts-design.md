# Inline Row-Edit Keyboard Shortcuts — Design Spec

## Overview

SpenDrop's transaction table opens individual rows into inline edit mode (not a modal — the row itself becomes editable via `TransactionRow.tsx`). Today the user must click the **Save** or **Cancel** button to exit edit mode. Pressing `Enter` in most inputs does nothing structural because the inputs are not inside the `<form>` element that owns the Save button; pressing `Escape` does nothing at all.

This spec adds three keyboard shortcuts to the edit row:

- **`Esc`** → cancel and revert (silent, no confirm).
- **`Enter`** → save (only reached when no child widget consumed the key).
- **`Cmd/Ctrl+Enter`** → save (force — bypasses any widget that owns plain `Enter`).

The edit row contains five input widgets — a native date input, an `AutocompleteInput` (description, with desktop ghost completion and a touch popover), a Radix `<Select>` (category), a `TagInput` (tags, with desktop ghost completion and a touch popover), and an `AmountCurrencyInput` (number + currency picker). Two of those — `AutocompleteInput` and `TagInput` — already own `Enter` for their own UX (accept ghost suggestion, add a chip). Any row-level `Enter` handler must therefore coordinate with them, not fight them.

**Design axis:** match the dominant inline-edit convention across mainstream tools — **layered keyboard events**, the innermost widget wins plain `Enter`/`Esc`, the row-level handler only fires when the child didn't consume. `Cmd/Ctrl+Enter` is the universal power-user escape hatch.

**Frontend-only change.** No backend, schema, or API changes.

---

## Non-Goals / Out of Scope

- **`TransactionEntryRow` (add-new row)** — uses the same child components, so the component-level changes benefit it automatically, but we do **not** add a row-level keyboard handler to the entry row in this cut. The entry row already works: Enter from most fields submits via form association, and there is no "cancel and revert" concept for a row that hasn't saved yet.
- **Tab / Shift+Tab to move between cells** (spreadsheet convention) — out of scope. Web users expect Tab to be native focus traversal, and fighting that breaks screen-reader navigation.
- **Two-tap Esc / inline confirm / undo snackbar** — rejected based on research (see § Research findings). No mainstream tool implements these for inline grid edit; the cost of implementing does not match the observed user benefit.
- **Dirty-state guard** — the row reverts silently on `Esc`. This matches Airtable, Google Sheets, Excel, GitHub, Google Docs, Figma, and Telegram. Revisit only if description ever grows into a multi-paragraph notes field (Jira's 2018 anti-pattern was specifically triggered by long-form text, not short grid rows).
- **Replacing the native `<input type="date">`** — out of scope. The native picker has known cross-browser key-swallowing quirks (see § Traps) which we will document and manual-test, but replacing the control is a separate decision.
- **Changes to the existing global `Escape` handling** on the Transactions page (if any, e.g. for closing filter panels) — the row handler must not collide with those. Concretely, `Esc` inside edit mode **must not** also close an open filter popover on the same press. `stopPropagation` on the row's `Esc` handler covers this.

---

## Research Findings (Why This Design)

Two rounds of research were done, covering Airtable, Linear, Notion, Google Sheets, Microsoft Excel, GitHub, Jira, Google Docs, Figma, Gmail, Slack, Discord, Telegram, Coda, and several others. Key citations inline below.

1. **Layered Enter/Esc is the dominant mental model** for tools that mix text inputs with pickers. Airtable traps focus inside an edited cell and makes `Esc` the exit key (`support.airtable.com/docs/airtable-keyboard-shortcuts`). Notion's plain `Enter` commits a cell; `Shift+Enter` inserts a newline (`notion.com/help/keyboard-shortcuts`). GitHub's comment edit uses `Cmd/Ctrl+Enter` to submit and plain Enter for newline (`docs.github.com/en/get-started/accessibility/keyboard-shortcuts`).

2. **Silent revert on `Esc` is the dominant dirty-state behavior**, not a confirm prompt. Airtable, Google Sheets, Excel, GitHub, Google Docs, Figma, and Telegram all revert silently. No mainstream tool implements two-tap Esc, inline confirms, or undo snackbars for inline grid edit. The only exception is Jira's inline description/comment edit, which after 2018 makes `Esc` a no-op (`jira.atlassian.com/browse/JRASERVER-61439`) — but that remediation was driven by long-form text, not five-field expense rows.

3. **`Cmd/Ctrl+Enter` force-submits** in every textarea/comment context we found (GitHub comments, Google Docs comments, Gmail compose, Linear comments). It is **wrong** in a true spreadsheet grid — Excel and Google Sheets use `Ctrl+Enter` for "fill selected range." SpenDrop has no fill-range semantics, so there is no collision.

4. **The Escape-bubble trap.** Our current `AutocompleteInput` and `TagInput` have comments that say "don't preventDefault — parent Escape handlers are expected to fire." That comment is correct in spirit but wrong in implementation for the layered model: a single `Esc` press would close the picker **and** cancel the row. GitHub had the same regression in 2023 when an emoji-picker Esc also closed the comment. The fix is to `stopPropagation` on `Esc` when the child actually handles it, and bubble only when the child has nothing to cancel.

---

## Architecture

### The row-level handler (dumb)

`TransactionRow.tsx` adds a single `onKeyDown` to the `<TableRow>` element in edit mode:

```ts
function handleRowKeyDown(e: React.KeyboardEvent<HTMLTableRowElement>) {
  // Cmd/Ctrl+Enter: force-submit regardless of inner-widget state.
  if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
    e.preventDefault();
    e.stopPropagation();
    void handleSave(e);
    return;
  }
  // Plain Enter reaches us only if no child widget consumed it.
  if (e.key === 'Enter' && !e.metaKey && !e.ctrlKey && !e.shiftKey && !e.altKey) {
    e.preventDefault();
    void handleSave(e);
    return;
  }
  // Plain Esc reaches us only if no child widget consumed it.
  if (e.key === 'Escape' && !e.metaKey && !e.ctrlKey && !e.shiftKey && !e.altKey) {
    e.preventDefault();
    e.stopPropagation(); // don't let the page-level Esc close filter popovers etc.
    handleCancel();
    return;
  }
}
```

The handler is deliberately thin. It makes no attempt to introspect child state ("is there a ghost?", "is the select open?"); that logic lives in each child. The row handler is a **receiver of last resort**.

### The child contract

Any child component that **does something** with a key MUST call both `preventDefault()` and `stopPropagation()`. Keys the child does not handle MUST bubble — the child does nothing (no preventDefault, no stopPropagation, no handler at all).

This is the single rule that makes the design work. It is written into each component's JSDoc in the changes below.

**Corollary — `Cmd/Ctrl+Enter` always bubbles.** Children check for the modifier at the top of their keydown handler and return early without calling `preventDefault`/`stopPropagation`, so the row-level handler always sees `Cmd/Ctrl+Enter`.

---

## Per-Component Changes

### `AutocompleteInput` (`web/src/components/AutocompleteInput.tsx`)

Current behavior:
- `Enter` → always `preventDefault()`. If ghost visible, call `onAccept(match)`; else no-op.
- `Escape` → if ghost visible, set `suppressedPrefix`; never `preventDefault`; comment says "let parent Escape handlers fire."

New behavior:
- **Modifier bypass first.** At the top of `handleKeyDown`: `if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') return;` — do nothing so the row handler gets it.
- `Enter` with ghost visible → `preventDefault() + stopPropagation() + onAccept(match)`. Consumed. Does **not** bubble.
- `Enter` without ghost → **no handler, no preventDefault, no stopPropagation.** Bubbles to the row handler which saves.
- `Escape` with ghost visible → `preventDefault() + stopPropagation() + setSuppressedPrefix(input)`. Consumed. The prior "let parent Escape handlers fire" comment is deleted; it was wrong for the layered model.
- `Escape` without ghost → **no handler.** Bubbles to the row handler which cancels.
- Touch popover open → pressing `Escape` closes the popover and `stopPropagation`s. Pressing `Enter` with a focused item already goes through the popover's own handler; we audit that it stopPropagates, and if not, we add it.

### `TagInput` (`web/src/components/TagInput.tsx`)

Current behavior:
- `Enter` → always `preventDefault() + addTag(input)`. If `input` is empty, `addTag` returns without doing anything, but Enter is still consumed.
- `Escape` → if touch popover is open, close it. Never `preventDefault`. Ghost suppression on desktop similarly does not preventDefault.

New behavior:
- **Modifier bypass first** — same as above.
- `Enter` with `input.length > 0` → `preventDefault() + stopPropagation() + addTag(input)`. Consumed.
- `Enter` with `input === ''` → **no handler.** Bubbles to the row handler which saves. This is the "press Enter twice after your last tag" pattern — first Enter commits the tag (consumed), second Enter saves the row (bubbles).
- `Escape` with touch popover open → `preventDefault() + stopPropagation() + setTouchOpen(false)`. Consumed.
- `Escape` with desktop ghost visible → `preventDefault() + stopPropagation() + setSuppressedPrefix(input)`. Consumed.
- `Escape` otherwise → **no handler.** Bubbles.
- Keep the existing `Backspace` behavior (pops the last chip when input is empty) unchanged.

### Category `<Select>` (shadcn/Radix)

Verify during implementation that Radix's Select primitive already calls `e.stopPropagation()` when it handles `Enter` (opens/picks) or `Escape` (closes). Radix does this by default in our version (`@radix-ui/react-select`). No code change expected, but we add one integration test that asserts `Enter` inside an open Select picks the option rather than saving the row.

If Radix does **not** stopPropagation correctly in our version, we wrap the Select trigger with a small wrapper that catches those keys when the Select is open and swallows them.

### Native `<input type="date">`

Accept the known cross-browser trap:
- **Chrome/Edge**: native date picker opens on focus or on click on the calendar icon. Pressing `Enter` with the picker open confirms a highlighted date and closes the picker; the `keydown` event often does not bubble to React. Pressing `Escape` closes the picker and similarly may not bubble.
- **Firefox**: date picker is a separate popup; `Enter` and `Esc` are mostly swallowed while the picker is open.
- **Safari/WebKit**: `Esc` may close nothing (there is no native popup on macOS Safari) and bubbles to us.

**Decision**: we accept this divergence. The date input is rarely the focus during Esc-to-cancel or Enter-to-save (users type the date and tab away). The documented user workflow — "close the native date picker with the mouse or outside-click, then press Enter/Esc" — is consistent.

**Manual test matrix** (part of the verification checklist, not automated):
- Chrome, Firefox, Safari × { Enter in date input, Esc in date input, Enter with picker open, Esc with picker open }.

Document the observed behavior in a comment on the date `<Input>` element. Do not attempt to force-normalize it in this cut.

### `AmountCurrencyInput` (`web/src/components/AmountCurrencyInput.tsx`)

Audit during implementation:
1. The numeric `<Input>` must not intercept `Enter`/`Esc`. Expected: it does not — it has no custom keydown logic. No change.
2. The currency picker Popover trigger is Radix — same expected behavior as the Category Select. When the Popover is open, `Enter` picks and stopPropagates; `Esc` closes and stopPropagates.

If either assumption fails, add the needed stopPropagation at the trigger level.

---

## Traps and Edge Cases

1. **Escape-bubble trap (primary).** Pre-existing comments in `AutocompleteInput` and `TagInput` say "don't preventDefault — parent Escape handlers are expected to fire." Those comments get deleted and replaced with the new contract. Without this change, one `Esc` press would close the picker **and** revert the entire row — the GitHub 2023 regression.

2. **Native date picker key-swallowing.** Documented above. Accept; manual test.

3. **Cmd/Ctrl+Enter while a widget is "busy."** The child components MUST bypass modifier-Enter (return at the top of their handler). Otherwise a user pressing `Cmd+Enter` with a ghost visible in AutocompleteInput would have the ghost accepted instead of the row saved.

4. **Enter in an empty TagInput after just adding a chip.** After `addTag` the input clears. A second `Enter` with the now-empty input bubbles to the row handler and saves. This is intentional, matches Airtable, and is covered by a test.

5. **Select focus-after-close.** After Radix Select closes via Esc or picks via Enter, focus returns to the SelectTrigger. A *subsequent* Esc on that trigger has no picker to close — it bubbles to the row handler and cancels. This is the correct layered-Esc behavior but may surprise users who expect "one Esc = close select, a second Esc does nothing." Mitigation: document the behavior, and observe whether real users hit it. Not a blocker.

6. **Page-level Esc collisions.** The page has (or will have) Esc handlers for closing filter panels, the replace-all bar, etc. The row's Esc handler `stopPropagation`s, so a press inside edit mode never bubbles to the page. The page handlers only fire when edit mode is not active.

7. **Save button still works.** The existing Save/Cancel buttons remain as the mouse/screen-reader affordance. They are not removed.

---

## Test Plan

### Unit tests

**`web/src/components/AutocompleteInput.test.tsx`** — add a "key propagation contract" section:
- Enter with ghost visible → handler called once, `e.preventDefault` and `e.stopPropagation` called, `onAccept` fired with match.
- Enter without ghost → `onKeyDown` on parent receives the Enter, `preventDefault` NOT called, `stopPropagation` NOT called.
- Enter with `metaKey: true` → parent receives the Enter (short-circuit), `onAccept` not called.
- Escape with ghost visible → `stopPropagation` called, `suppressedPrefix` state updated.
- Escape without ghost → parent receives the Escape.

**`web/src/components/TagInput.test.tsx`** — add the same matrix:
- Enter with `input = 'gro'` → addTag fires with 'gro', parent does not receive Enter.
- Enter with `input = ''` → addTag not called, parent receives Enter.
- Enter with `input = 'gro'` and metaKey → addTag not called, parent receives Enter.
- Escape with touch popover open → popover closes, parent does not receive Escape.
- Escape with desktop ghost visible → ghost suppressed, parent does not receive Escape.
- Escape with no popover and no ghost → parent receives Escape.

### Integration tests

**`web/src/components/TransactionRow.test.tsx`** (new file if absent, else extend) or **`web/src/pages/Transactions.test.tsx`**:

1. **Esc on a clean edit row** cancels edit mode, `onUpdate` not called.
2. **Esc on a dirty edit row** reverts silently to original values, no confirm prompt, `onUpdate` not called, the table cell shows the original values again.
3. **Enter from the description input (no ghost)** calls `onUpdate` with the current edit values.
4. **Enter from the description input with a ghost visible** accepts the ghost into the description field, does NOT call `onUpdate`. A *second* Enter (ghost now gone) calls `onUpdate`.
5. **Enter from the tag input with typed buffer 'groceries'** adds the chip, `onUpdate` NOT called. A *second* Enter (empty buffer) calls `onUpdate`.
6. **Enter from the amount input** calls `onUpdate`.
7. **Enter from the date input** — manual test only (see date trap § above). Not automated because jsdom does not render the native date picker.
8. **Cmd+Enter from any field (including with ghost visible or tag buffer typed)** calls `onUpdate` without accepting the ghost / without committing the tag.
9. **Ctrl+Enter behaves the same as Cmd+Enter** (cross-platform coverage).
10. **Esc with the category Select open** closes the Select, does NOT cancel the edit row. A *second* Esc cancels.

### Manual verification checklist

- Chrome, Firefox, Safari on desktop — the matrix from § Native date input.
- One iOS Safari and one Android Chrome check of touch-tap-to-pick on AutocompleteInput and TagInput popovers, to confirm the blur-guard ref fix from commit `fde3c75` still holds.

---

## Files Affected

Modified:
- `web/src/components/TransactionRow.tsx` — add `onKeyDown` to `<TableRow>` in edit mode; add `handleRowKeyDown`.
- `web/src/components/AutocompleteInput.tsx` — rewrite key handling per the contract; update JSDoc.
- `web/src/components/TagInput.tsx` — rewrite key handling per the contract; update JSDoc.
- `web/src/components/AutocompleteInput.test.tsx` — add key propagation tests.
- `web/src/components/TagInput.test.tsx` — add key propagation tests.
- `web/src/pages/Transactions.test.tsx` (or new `TransactionRow.test.tsx`) — integration tests.

Audited, possibly modified:
- `web/src/components/AmountCurrencyInput.tsx` — only if audit reveals it eats Enter/Esc.

Unchanged:
- `web/src/components/TransactionEntryRow.tsx` — benefits automatically from the component-level changes; no row-level handler added.
- Backend. Nothing.

---

## Success Criteria

- `npx tsc -b` clean (the stricter build mode Docker uses — not just `tsc --noEmit`).
- `npx eslint .` clean.
- `npx vitest run` — all existing tests pass; new tests in place and passing.
- Manual test matrix for the native date input documented with results.
- Code-reviewer subagent approves the diff.

---

## Sources

Primary research reports saved to this repository are the two agent dispatches from the 2026-04-18 conversation (not archived to a file); the load-bearing citations are inline above. Key URLs:

- `support.airtable.com/docs/airtable-keyboard-shortcuts`
- `support.airtable.com/docs/accessibility-in-airtable`
- `notion.com/help/keyboard-shortcuts`
- `support.google.com/docs/answer/181110`
- `support.microsoft.com/en-us/office/keyboard-shortcuts-in-excel-1798d9d5-842a-42b8-9c99-9b7213f0040f`
- `docs.github.com/en/get-started/accessibility/keyboard-shortcuts`
- `support.google.com/docs/answer/6239410`
- `support.google.com/mail/answer/6594`
- `linear.app/docs/editor`
- `jira.atlassian.com/browse/JRASERVER-61439`
- `jira.atlassian.com/browse/JRASERVER-36670`
- `slack.com/help/articles/360017820374`
- `help.figma.com/hc/en-us/articles/360040328653`
