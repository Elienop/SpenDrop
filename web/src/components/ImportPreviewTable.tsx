import {
  Fragment,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type KeyboardEvent,
} from 'react';
import { AlertTriangle } from 'lucide-react';
import type { CollisionGroup, ImportPreview, PatchRowRequest } from '@/api/types';
import type { CellError } from '@/hooks/useImportSession';
import { activeFieldErrors } from '@/hooks/useImportSession';
import {
  fallbackFieldTooLongMessage,
  isEditableInPreview,
} from '@/lib/import-field-errors';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import { Input } from '@/components/ui/input';
import { Separator } from '@/components/ui/separator';

type EditableField = 'date' | 'description' | 'amount';
type EditingCell = { rowID: number; field: EditableField } | null;

/** DOM id of the explanation attached under a specific flagged row. */
const fieldErrorDetailId = (rowID: number) => `import-field-error-${rowID}`;

type RenderUnit =
  | { kind: 'field-error-bar'; rowIDs: number[] }
  | { kind: 'group-header'; group: CollisionGroup }
  | {
      kind: 'row';
      row: ImportPreview['rows'][number];
      isCollision: boolean;
      groupHeaderId?: string;
      hasFieldError: boolean;
      /**
       * Server-authored explanations for this row's over-length fields
       * that have NO cell of their own (tags, notes). Rendered verbatim
       * in a detail row beneath this one. Description errors are absent
       * here on purpose — they are shown in their own cell via
       * `cellErrors`, and repeating them would print the same sentence
       * twice for one row.
       */
      rowLevelMessages: string[];
    };

/**
 * Walks `preview` to produce the ordered render plan: every collision
 * group emits its header row immediately followed by its member rows (in
 * `member_row_ids` order), then every row that is not in any group is
 * emitted in its original `preview.rows` order. The upshot:
 *
 *   - Collision groups always render at the top of the table, so the
 *     user's attention lands on the work they need to do.
 *   - Rows are never emitted twice, even if a row appears in two groups
 *     (the backend never emits this today, but `emitted` is a cheap
 *     guard against any future regression).
 *   - Clean rows follow the same stable order as `preview.rows`, which
 *     means the component's visual row ordering stays predictable
 *     across re-renders.
 *   - Member rows carry the DOM id of their group header so the row's
 *     `aria-describedby` can point at the "N rows collide" message —
 *     screen readers then announce the group context on every row
 *     focus.
 *
 * Over-length fields are handled differently on purpose, in two ways.
 *
 * They are not pulled into a group of their own. Grouping them would
 * fight the collision layout in the one case where it matters most: two
 * rows with the same 5,000-character description are both a collision
 * AND both too long, and whichever section claimed them first would
 * leave the other rendering a header that counts rows it no longer
 * shows. Marking in place lets a row carry both signals at once.
 *
 * And their EXPLANATION travels with the row, not with the set. The
 * server writes one sentence per field error, phrased about a single
 * row ("This row's note is longer than…"), so it is rendered verbatim
 * in a detail row beneath the row it describes. Only the bulk control
 * at the top is aggregate, and it deliberately carries a count and a
 * button rather than an explanation — the sentences are the server's,
 * the framing around them is ours.
 */
function buildRenderPlan(preview: ImportPreview): RenderUnit[] {
  const byRowId = new Map<number, ImportPreview['rows'][number]>();
  for (const r of preview.rows) byRowId.set(r.row_id, r);

  const active = activeFieldErrors(preview.field_errors, preview.rows);
  const fieldErrorRowIDs = new Set(active.map((fe) => fe.row_id));
  // Explanations for fields with no cell, grouped by the row they
  // describe. A row can trip more than one (tags AND notes), so each
  // keeps its own sentence rather than being collapsed into a summary.
  const rowLevelMessages = new Map<number, string[]>();
  for (const fe of active) {
    if (isEditableInPreview(fe.field)) continue;
    const existing = rowLevelMessages.get(fe.row_id) ?? [];
    existing.push(fe.message || fallbackFieldTooLongMessage(fe.field));
    rowLevelMessages.set(fe.row_id, existing);
  }

  const emitted = new Set<number>();
  const units: RenderUnit[] = [];

  if (fieldErrorRowIDs.size > 0) {
    units.push({
      kind: 'field-error-bar',
      // Ordered by the preview's own row order so the bulk skip fires
      // its PATCH burst top-to-bottom, matching what the user sees.
      rowIDs: preview.rows
        .filter((r) => fieldErrorRowIDs.has(r.row_id))
        .map((r) => r.row_id),
    });
  }

  for (const group of preview.collision_groups) {
    const headerId = `collision-group-${group.group_id}`;
    units.push({ kind: 'group-header', group });
    for (const rowID of group.member_row_ids) {
      const row = byRowId.get(rowID);
      if (row && !emitted.has(rowID)) {
        units.push({
          kind: 'row',
          row,
          isCollision: true,
          groupHeaderId: headerId,
          hasFieldError: fieldErrorRowIDs.has(rowID),
          rowLevelMessages: rowLevelMessages.get(rowID) ?? [],
        });
        emitted.add(rowID);
      }
    }
  }

  for (const row of preview.rows) {
    if (!emitted.has(row.row_id)) {
      units.push({
        kind: 'row',
        row,
        isCollision: false,
        hasFieldError: fieldErrorRowIDs.has(row.row_id),
        rowLevelMessages: rowLevelMessages.get(row.row_id) ?? [],
      });
    }
  }

  return units;
}

export interface ImportPreviewTableProps {
  preview: ImportPreview;
  cellErrors: Record<string, CellError>;
  unresolvedCount: number;
  /**
   * Distinct category values still awaiting a decision. Passed in rather
   * than derived here, because resolving one is an act performed in the
   * mapping panel BELOW this table — the table can see the server's list
   * but not the user's answers to it.
   */
  unresolvedCategoryCount: number;
  canImport: boolean;
  pendingPatchCount: number;
  onPatchRow: (
    rowID: number,
    field: PatchRowRequest['field'],
    value: string | boolean,
  ) => Promise<void>;
  onConfirm: () => void;
  /**
   * Optional escape hatch. When provided, a Cancel button is rendered in
   * the footer next to Import so the primary and abort actions sit on the
   * same decision row. Pairing them here (instead of in a separate block
   * below the table) is a straight application of Fitts' — the user's
   * mouse is already near the Import button when they decide to back out.
   */
  onCancel?: () => void;
}

export function ImportPreviewTable(props: ImportPreviewTableProps) {
  const {
    preview,
    cellErrors,
    unresolvedCount,
    unresolvedCategoryCount,
    canImport,
    pendingPatchCount,
    onPatchRow,
    onConfirm,
    onCancel,
  } = props;

  // Local edit state — which cell is currently in edit mode and its
  // in-progress draft value. This is the ONLY local state the component
  // owns; every visual state otherwise derives from props on each render
  // (see renderPlan below). The draft is flushed to the server via
  // onPatchRow on commit, then the server's merged value replaces the
  // displayed text on the next render.
  const [editing, setEditing] = useState<EditingCell>(null);
  const [draft, setDraft] = useState<string>('');

  // Imperative mirror of `editing` — synchronously nulled by commitEdit /
  // cancelEdit BEFORE React re-renders. This is the gate that lets the
  // Input's onBlur handler (which fires inside the same tick as our
  // cellEl.focus() call) distinguish "a real blur that still needs to
  // commit" from "the blur that cancelEdit already handled". Without
  // this ref, React's batched state update would leave commitEdit's
  // closure seeing editing=<original target> and the PATCH would fire a
  // second time after Escape.
  const editingRef = useRef<EditingCell>(null);

  // Focus anchor: the <td> that opened the current edit. On Enter/Escape
  // we restore focus to this cell so keyboard users do not land on
  // document.body when the Input unmounts. Tab intentionally leaves the
  // anchor untouched — the browser's default focus-advance handles that.
  const editCellRef = useRef<HTMLTableCellElement | null>(null);

  // Scrollable container — the max-h/overflow-auto wrapper around the
  // Table. Scoping scrollIntoView to this container's first collision row
  // prevents the 409 scroll-into-view effect from mis-targeting if the
  // page ever renders two import tables at once.
  const scrollContainerRef = useRef<HTMLDivElement | null>(null);

  // Derived from `preview` with the same helper the hook's gate uses,
  // rather than passed in as a count prop beside `unresolvedCount`.
  // One definition of "still blocking" means the button cannot end up
  // disabled against rows this table is not highlighting. Declared up
  // here because the scroll effect below reads it.
  const fieldErrorRowCount = useMemo(
    () =>
      new Set(
        activeFieldErrors(preview.field_errors, preview.rows).map(
          (fe) => fe.row_id,
        ),
      ).size,
    [preview],
  );
  // Previous unresolvedCount snapshot for the 0 → >0 edge detection. We
  // do NOT scroll on initial mount (prev defaults to the current value),
  // and we do NOT scroll on N → N+1 transitions (un-skipping a row in an
  // already-visible group is its own user-initiated action and does not
  // need the page to jump).
  const prevBlockedRef = useRef(unresolvedCount + fieldErrorRowCount);
  useEffect(() => {
    // 409 UX: confirmImport returns 409 UNRESOLVED_COLLISIONS or 409
    // FIELD_TOO_LONG → the hook drops the fresh collision_groups or
    // field_errors into state → the blocked count flips 0 → >0.
    // Scrolling the first blocked row into view saves the user from
    // having to scan a long preview to find what stopped the import.
    // We defer to rAF so the newly-rendered rows settle into layout
    // before scrollIntoView reads their positions (without this, Safari
    // has been observed to scroll to the pre-layout offset).
    //
    // Summing the two counts rather than watching them separately keeps
    // this to a single 0 → >0 edge. Two effects would both fire when a
    // preview carries both kinds, and the second scroll would win —
    // landing the user on whichever row it happened to target rather
    // than the first problem in the table.
    const blockedCount = unresolvedCount + fieldErrorRowCount;
    if (prevBlockedRef.current === 0 && blockedCount > 0) {
      requestAnimationFrame(() => {
        const container = scrollContainerRef.current;
        // Comma selector, so this resolves to the first match in
        // DOCUMENT order — the topmost blocked row, whichever kind it
        // is — not the first branch of the selector.
        const firstBlockedRow = container?.querySelector<HTMLTableRowElement>(
          'tr[data-collision="true"], tr[data-field-error="true"]',
        );
        if (firstBlockedRow) {
          firstBlockedRow.scrollIntoView({ behavior: 'smooth', block: 'center' });
        }
      });
    }
    prevBlockedRef.current = blockedCount;
  }, [unresolvedCount, fieldErrorRowCount]);

  // Build the render plan from props on EVERY render. No useState, no
  // useEffect — structural guarantee against importcsv #16 (the
  // stale-style bug where a row that flipped collision → clean kept its
  // amber highlight until the next unrelated state change). `renderPlan`
  // also dictates row ORDER: collision groups always float to the top,
  // clean rows follow.
  const renderPlan = useMemo(() => buildRenderPlan(preview), [preview]);

  const skipAllRows = useCallback(
    async (rowIDs: number[]) => {
      // Sequential awaits — the hook's patchQueueRef already serializes
      // cross-row PATCHes, but awaiting here keeps the fire order stable
      // and makes the button's pending count settle predictably.
      for (const rowID of rowIDs) {
        await onPatchRow(rowID, 'skip', true);
      }
    },
    [onPatchRow],
  );

  // Both bulk escapes are the same burst over a different row set, so
  // they share one implementation: skipping a collision group and
  // skipping every over-length row differ only in how the ids were
  // chosen.
  const skipAllInGroup = useCallback(
    (group: CollisionGroup) => skipAllRows(group.member_row_ids),
    [skipAllRows],
  );

  const keepCount = preview.rows.filter((r) => !r.skip).length;

  const beginEdit = useCallback(
    (
      rowID: number,
      field: EditableField,
      current: string,
      cellEl: HTMLTableCellElement | null,
    ) => {
      editCellRef.current = cellEl;
      editingRef.current = { rowID, field };
      setEditing({ rowID, field });
      setDraft(current);
    },
    [],
  );

  const commitEdit = useCallback(
    async (restoreFocus: boolean) => {
      // Snapshot-and-clear via the imperative ref — the blur fired by
      // cellEl.focus() below will re-enter commitEdit, and the ref is
      // the only signal synchronous enough to short-circuit the second
      // call before React re-renders.
      const target = editingRef.current;
      if (!target) return;
      editingRef.current = null;
      const cellEl = editCellRef.current;
      setEditing(null);
      await onPatchRow(target.rowID, target.field, draft);
      // Restore focus to the cell only when the caller opted in
      // (Enter/blur). Tab explicitly opts OUT so the browser's native
      // focus-advance is not clobbered by an imperative `focus()` call.
      if (restoreFocus && cellEl) {
        cellEl.focus();
      }
    },
    [draft, onPatchRow],
  );

  const cancelEdit = useCallback(() => {
    // Null the ref BEFORE focus() so the Input's onBlur → commitEdit
    // chain sees "no active edit" and drops the PATCH on the floor.
    editingRef.current = null;
    const cellEl = editCellRef.current;
    setEditing(null);
    if (cellEl) cellEl.focus();
  }, []);

  const onInputKeyDown = useCallback(
    (e: KeyboardEvent<HTMLInputElement>) => {
      if (e.key === 'Enter') {
        // Prevent ancestor form submit on Enter.
        e.preventDefault();
        void commitEdit(true);
      } else if (e.key === 'Tab') {
        // Do NOT preventDefault — the browser must advance focus after we
        // unmount the Input. setEditing(null) drops the Input on the next
        // render, and the browser settles focus on the next tab-able
        // element in DOM order (the adjacent cells are tabIndex=0).
        // Shift+Tab lands on the containing <td> first; a second
        // Shift+Tab then moves to the previous cell — one extra keystroke
        // is the price of keeping the focus model native.
        void commitEdit(false);
      } else if (e.key === 'Escape') {
        cancelEdit();
      }
    },
    [commitEdit, cancelEdit],
  );

  // Keyboard entry into edit mode from an idle cell. Enter and F2 are
  // the two conventional spreadsheet-edit bindings (F2 matches Excel,
  // Enter matches Google Sheets). Either triggers beginEdit with the
  // cell element as the focus anchor.
  const onCellKeyDown = useCallback(
    (
      e: KeyboardEvent<HTMLTableCellElement>,
      rowID: number,
      field: EditableField,
      displayValue: string,
    ) => {
      if (e.key === 'Enter' || e.key === 'F2') {
        e.preventDefault();
        beginEdit(rowID, field, displayValue, e.currentTarget);
      }
    },
    [beginEdit],
  );

  const renderEditableCell = (
    row: ImportPreview['rows'][number],
    field: EditableField,
    displayValue: string,
    extraClass = '',
    extraAriaDescribedby?: string,
  ) => {
    const isEditing = editing?.rowID === row.row_id && editing.field === field;
    const errKey = `${row.row_id}:${field}`;
    const err = cellErrors[errKey];
    const errorMessageId = err ? `cell-error-${row.row_id}-${field}` : undefined;
    // Combine the optional group-header describedby with the optional
    // per-cell error describedby into a single space-separated token
    // list (ARIA spec §4.1.2 — IDREFS). Empty string collapses to
    // undefined so we never emit a useless attribute.
    const describedby =
      [extraAriaDescribedby, errorMessageId].filter(Boolean).join(' ') || undefined;
    // Description is the only free-text editable column, so it is the
    // only one whose value can be arbitrarily long — date and amount are
    // both parsed before they reach here. An unbounded cell renders the
    // whole value: a 5,500-character description measured 2,211px tall,
    // burying the banner that asks the user to act on it under two
    // screens of its own text. The rows that get flagged are exactly the
    // rows that would make the preview unreadable.
    const isFreeText = field === 'description';
    return (
      <TableCell
        className={`${extraClass} ${isFreeText ? 'max-w-[28rem]' : ''} focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring`}
        data-cell-error={err ? 'true' : undefined}
        // Cover both idle (cell is tabable) and edit (Input announces)
        // modes with the same describedby tokens so the group-header
        // context + any live validation error are announced regardless
        // of how the user reached the cell.
        aria-describedby={describedby}
        tabIndex={isEditing ? -1 : 0}
        onDoubleClick={(e) =>
          beginEdit(row.row_id, field, displayValue, e.currentTarget)
        }
        onKeyDown={(e) => {
          // Capture the cell element on keyboard-entry so Escape/Enter
          // can restore focus to it — e.currentTarget is the <td> even
          // when the key originates on a descendant span.
          if (!isEditing) onCellKeyDown(e, row.row_id, field, displayValue);
        }}
      >
        {isEditing ? (
          <Input
            autoFocus
            // One editor serves three columns, so the keypad hint has to be
            // per-field rather than on the element: only `amount` is numeric,
            // and without this the coarse-pointer tablet opens QWERTY on it.
            // There is no `type="number"` here to infer from — the draft is a
            // string for all three fields — which makes `inputMode` the only
            // lever. Reasoning: `<MonthlyBudgetCard>` in `pages/Budgets.tsx`.
            // The amount is signed (B10), so the minus-key caveat and its
            // diagnostic order apply here too — see the Amount-tab comment in
            // `FilterPanel.tsx`.
            inputMode={field === 'amount' ? 'decimal' : undefined}
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            onBlur={() => void commitEdit(false)}
            onKeyDown={onInputKeyDown}
            aria-describedby={describedby}
            aria-invalid={err ? true : undefined}
            className={`h-7 px-2 py-0 text-sm ${err ? 'ring-1 ring-destructive' : ''}`}
          />
        ) : (
          /*
            The bound lives on this span, NOT on the TableCell the way
            TransactionRow does it. `truncate` sets `white-space: nowrap`
            on whatever carries it, and this cell has a sibling below —
            the server's error sentence — which must wrap and be read in
            full. Putting `truncate` on the cell would clip that sentence
            to one line with an ellipsis, silently destroying the copy
            this flag exists to deliver.

            Truncation is presentational only: `beginEdit` is called with
            `displayValue`, never with the DOM's text, so double-clicking
            a truncated cell still opens the editor on the whole value.
            That matters because "shorten it here" is the remedy the
            message points at.
          */
          <span
            className={isFreeText ? 'block max-w-[28rem] truncate' : undefined}
            title={isFreeText ? displayValue : undefined}
          >
            {displayValue}
          </span>
        )}
        {err && (
          <p id={errorMessageId} className="text-xs text-destructive mt-0.5">
            {err.message}
          </p>
        )}
      </TableCell>
    );
  };

  // Status text is derived, but the aria-live container is persistent:
  // a single span that always exists, with its text content swapping
  // between the amber / emerald states. Ternary-ing between two
  // aria-live elements remounts the live region and screen readers
  // skip the update (AT-2024-07 regression).
  // Listed rather than branched so the two blockers compose: a preview
  // can carry collisions AND over-length rows at once, and naming only
  // one of them would send the user to fix half the problem and watch
  // the button stay disabled. The collisions-only wording is unchanged
  // from before over-length rows existed.
  const blockers: string[] = [];
  if (unresolvedCount > 0) {
    blockers.push(
      `${unresolvedCount} ${unresolvedCount === 1 ? 'collision' : 'collisions'}`,
    );
  }
  if (fieldErrorRowCount > 0) {
    blockers.push(
      `${fieldErrorRowCount} too-long ${fieldErrorRowCount === 1 ? 'row' : 'rows'}`,
    );
  }
  // Named separately from the other two because the remedy is somewhere
  // else — the mapping panel below the table, not a cell in it. "Fix or
  // skip" would point at the wrong thing, so this blocker carries its own
  // verb and the sentence composes the two halves.
  //
  // "choices", not "unmatched names": the count covers both a name that
  // matches nothing AND rows with an empty Category cell, and only the
  // panel below knows which is which. Wording that named one of the two
  // would be wrong for the other half of the time.
  const categoryBlocker =
    unresolvedCategoryCount > 0
      ? `${unresolvedCategoryCount} category ${unresolvedCategoryCount === 1 ? 'choice' : 'choices'} still needed below`
      : '';
  const rowBlockerText =
    blockers.length > 0
      ? `Fix or skip ${blockers.join(' and ')} to enable import`
      : '';
  const statusText =
    [rowBlockerText, categoryBlocker].filter(Boolean).join('. ') ||
    `Ready to import ${keepCount} rows`;
  const statusColor =
    blockers.length > 0 || unresolvedCategoryCount > 0
      ? 'text-amber-500'
      : 'text-emerald-500';

  return (
    <div className="flex flex-col gap-3">
      <div
        ref={scrollContainerRef}
        className="max-h-[480px] overflow-auto rounded-md border"
      >
        <Table>
          <TableHeader>
            <TableRow>
              {/* sticky thead keeps column labels visible while scrolling
                  through a long preview; bg-background + z-10 keeps it
                  readable above the first row that scrolls under it. */}
              <TableHead className="sticky top-0 z-10 bg-background">Date</TableHead>
              <TableHead className="sticky top-0 z-10 bg-background">Description</TableHead>
              <TableHead className="sticky top-0 z-10 bg-background">Category</TableHead>
              <TableHead className="sticky top-0 z-10 bg-background text-right">Amount</TableHead>
              <TableHead className="sticky top-0 z-10 bg-background w-12">Skip</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {renderPlan.map((unit) => {
              if (unit.kind === 'field-error-bar') {
                const count = unit.rowIDs.length;
                // Same guard as the collision group's Skip-all: two fast
                // clicks would otherwise double-fire the PATCH burst.
                const skipAllDisabled = pendingPatchCount > 0;
                return (
                  <TableRow
                    key="field-error-bar"
                    data-field-error-bar="true"
                    className="bg-amber-500/15 border-l-2 border-l-amber-500 hover:bg-amber-500/15"
                  >
                    <TableCell colSpan={5}>
                      <div className="flex items-center justify-between gap-3">
                        {/*
                          Count and action only. The explanation of WHY
                          any given row is too long is the server's
                          sentence and lives with that row — an aggregate
                          restatement here would be a second copy of the
                          same wording, phrased by us, free to drift.
                        */}
                        <div
                          role="heading"
                          aria-level={3}
                          className="flex items-center gap-2 text-sm"
                        >
                          <AlertTriangle
                            className="size-4 shrink-0 text-amber-500"
                            aria-hidden="true"
                          />
                          <span>
                            {count === 1
                              ? '1 row is too long to import'
                              : `${count} rows are too long to import`}
                          </span>
                        </div>
                        <Button
                          type="button"
                          variant="outline"
                          size="sm"
                          className="shrink-0"
                          disabled={skipAllDisabled}
                          onClick={() => void skipAllRows(unit.rowIDs)}
                        >
                          {count === 1
                            ? 'Skip this row'
                            : `Skip these ${count} rows`}
                        </Button>
                      </div>
                    </TableCell>
                  </TableRow>
                );
              }
              if (unit.kind === 'group-header') {
                const count = unit.group.member_row_ids.length;
                const matchesExisting = unit.group.reason === 'db_match';
                const headerId = `collision-group-${unit.group.group_id}`;
                // Disable Skip-all while any PATCH is in flight so two
                // fast clicks cannot double-fire the group's PATCH burst
                // and temporarily decouple the pendingPatchCount from
                // the backend's actual queue depth.
                const skipAllDisabled = pendingPatchCount > 0;
                return (
                  <TableRow
                    key={`group-${unit.group.group_id}`}
                    data-group-header="true"
                    className="bg-amber-500/15 border-l-2 border-l-amber-500 hover:bg-amber-500/15"
                  >
                    <TableCell colSpan={5}>
                      <div className="flex items-center justify-between gap-3">
                        <div
                          id={headerId}
                          role="heading"
                          aria-level={3}
                          className="flex items-center gap-2 text-sm"
                        >
                          <AlertTriangle
                            className="h-4 w-4 text-amber-500"
                            aria-hidden="true"
                          />
                          <span>
                            {`${count} rows collide`}
                            {matchesExisting ? ' (matches existing transaction)' : ''}
                          </span>
                        </div>
                        <Button
                          type="button"
                          variant="outline"
                          size="sm"
                          disabled={skipAllDisabled}
                          onClick={() => void skipAllInGroup(unit.group)}
                        >
                          {/*
                            Embedding the member count makes the
                            destructive scope explicit before the click —
                            "Skip all 2 in group" reads differently from
                            "Skip all in group" when the user's intent is
                            "I only want to skip one of these". This is a
                            UX safeguard, not a functional change: the
                            handler already skips every member row.
                          */}
                          {`Skip all ${count} in group`}
                        </Button>
                      </div>
                    </TableCell>
                  </TableRow>
                );
              }
              const {
                row,
                isCollision,
                groupHeaderId,
                hasFieldError,
                rowLevelMessages: rowMessages,
              } = unit;
              // A row can block the import for either reason, or both.
              // They share the amber treatment because they are the same
              // instruction to the user — this row needs attention
              // before Import will run.
              const rowBlocked = isCollision || hasFieldError;
              // Both descriptions are attached when both apply, so a
              // row that collides AND carries an uneditable over-length
              // field announces both. The field-error target is this
              // row's own detail line rather than the bar at the top:
              // the detail holds the sentence that applies to THIS row,
              // where the bar only holds a count.
              const rowDescribedby =
                [
                  groupHeaderId,
                  rowMessages.length > 0
                    ? fieldErrorDetailId(row.row_id)
                    : undefined,
                ]
                  .filter(Boolean)
                  .join(' ') || undefined;
              // Skipped rows render faded + struck-through so the user
              // can visually confirm which rows the confirm step will
              // drop without having to mentally intersect `skip` with
              // the table's row order.
              const skipClass = row.skip ? 'text-muted-foreground line-through' : '';
              // Pick a stable row-identifier for aria-label: the
              // description is what the user sees, so "Skip row Starbucks"
              // reads naturally and is unambiguous within the preview's
              // sort order. If the description is empty, fall back to the
              // row_id+1 as a last resort.
              const rowLabel = row.description.trim() || `row ${row.row_id + 1}`;
              return (
                <Fragment key={row.row_id}>
                <TableRow
                  data-row-id={row.row_id}
                  data-collision={isCollision ? 'true' : undefined}
                  data-field-error={hasFieldError ? 'true' : undefined}
                  className={rowBlocked ? 'bg-amber-500/10 border-l-2 border-l-amber-500' : ''}
                >
                  {renderEditableCell(row, 'date', row.date, skipClass, rowDescribedby)}
                  {renderEditableCell(row, 'description', row.description, skipClass, rowDescribedby)}
                  {/*
                    Category is free text out of the spreadsheet too, and
                    unlike description it is not length-checked by the
                    backend at all — so an absurd one can never be
                    flagged and would blow the row height out with no
                    banner to explain it. Bounded here with the plain
                    TransactionRow treatment: this cell has no error
                    sibling to clip, so `truncate` can sit on the cell.
                  */}
                  <TableCell
                    className={`max-w-[28rem] truncate text-muted-foreground ${skipClass}`}
                    title={row.category}
                  >
                    {row.category}
                  </TableCell>
                  {renderEditableCell(
                    row,
                    'amount',
                    row.amount.toFixed(2),
                    `text-right font-mono tabular-nums ${skipClass}`,
                    rowDescribedby,
                  )}
                  <TableCell>
                    {/*
                      Per-row Skip toggle. The checkbox is fully controlled
                      off `row.skip` — clicks fire `onPatchRow` and the
                      server-merged preview flips the prop on the next
                      render. We never track a local "pending" state:
                      that would re-introduce the importcsv #16 class of
                      bug where the UI drifts from the server's truth.
                      aria-label distinguishes the two directions so a
                      screen-reader user can confirm the effect of the
                      click before committing it.
                    */}
                    <Checkbox
                      checked={row.skip}
                      onCheckedChange={(v) => void onPatchRow(row.row_id, 'skip', Boolean(v))}
                      aria-label={row.skip ? `Unskip ${rowLabel}` : `Skip ${rowLabel}`}
                    />
                  </TableCell>
                </TableRow>
                {rowMessages.length > 0 && (
                  /*
                    Explanation for the fields this table has no cell
                    for. Rendered as its own row directly beneath the one
                    it describes, so the sentence the server wrote about
                    "this row" sits against that row. Not focusable and
                    not a control — the fix is the Skip checkbox above,
                    or the source spreadsheet.
                  */
                  <TableRow
                    data-field-error-detail={row.row_id}
                    className="bg-amber-500/10 border-l-2 border-l-amber-500 hover:bg-amber-500/10"
                  >
                    <TableCell colSpan={5} className="pt-0">
                      <div
                        id={fieldErrorDetailId(row.row_id)}
                        className="flex flex-col gap-0.5 text-xs text-muted-foreground"
                      >
                        {rowMessages.map((message) => (
                          <span key={message}>{message}</span>
                        ))}
                      </div>
                    </TableCell>
                  </TableRow>
                )}
                </Fragment>
              );
            })}
          </TableBody>
        </Table>
      </div>
      <Separator />
      <div className="flex items-center justify-between gap-3">
        <div className="text-sm" aria-live="polite" aria-atomic="true">
          <span className={statusColor}>{statusText}</span>
        </div>
        <div className="flex gap-2">
          {onCancel && (
            <Button type="button" variant="outline" onClick={onCancel}>
              Cancel
            </Button>
          )}
          <Button
            type="button"
            disabled={!canImport || pendingPatchCount > 0}
            onClick={onConfirm}
          >
            {`Import ${keepCount}`}
          </Button>
        </div>
      </div>
    </div>
  );
}
