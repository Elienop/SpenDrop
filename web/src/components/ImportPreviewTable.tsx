import {
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

type RenderUnit =
  | { kind: 'group-header'; group: CollisionGroup }
  | { kind: 'row'; row: ImportPreview['rows'][number]; isCollision: boolean; groupHeaderId?: string };

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
 */
function buildRenderPlan(preview: ImportPreview): RenderUnit[] {
  const byRowId = new Map<number, ImportPreview['rows'][number]>();
  for (const r of preview.rows) byRowId.set(r.row_id, r);

  const emitted = new Set<number>();
  const units: RenderUnit[] = [];

  for (const group of preview.collision_groups) {
    const headerId = `collision-group-${group.group_id}`;
    units.push({ kind: 'group-header', group });
    for (const rowID of group.member_row_ids) {
      const row = byRowId.get(rowID);
      if (row && !emitted.has(rowID)) {
        units.push({ kind: 'row', row, isCollision: true, groupHeaderId: headerId });
        emitted.add(rowID);
      }
    }
  }

  for (const row of preview.rows) {
    if (!emitted.has(row.row_id)) {
      units.push({ kind: 'row', row, isCollision: false });
    }
  }

  return units;
}

export interface ImportPreviewTableProps {
  preview: ImportPreview;
  cellErrors: Record<string, CellError>;
  unresolvedCount: number;
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
  // Previous unresolvedCount snapshot for the 0 → >0 edge detection. We
  // do NOT scroll on initial mount (prev defaults to the current value),
  // and we do NOT scroll on N → N+1 transitions (un-skipping a row in an
  // already-visible group is its own user-initiated action and does not
  // need the page to jump).
  const prevUnresolvedRef = useRef(unresolvedCount);
  useEffect(() => {
    // 409 UX: confirmImport returns 409 UNRESOLVED_COLLISIONS → the hook
    // drops the fresh collision_groups into state → unresolvedCount flips
    // 0 → >0. Scrolling the first collision row into view saves the user
    // from having to scan a long preview to find what blocked the import.
    // We defer to rAF so the newly-rendered rows settle into layout
    // before scrollIntoView reads their positions (without this, Safari
    // has been observed to scroll to the pre-layout offset).
    if (prevUnresolvedRef.current === 0 && unresolvedCount > 0) {
      requestAnimationFrame(() => {
        const container = scrollContainerRef.current;
        const firstCollisionRow = container?.querySelector<HTMLTableRowElement>(
          'tr[data-collision="true"]',
        );
        if (firstCollisionRow) {
          firstCollisionRow.scrollIntoView({ behavior: 'smooth', block: 'center' });
        }
      });
    }
    prevUnresolvedRef.current = unresolvedCount;
  }, [unresolvedCount]);

  // Build the render plan from props on EVERY render. No useState, no
  // useEffect — structural guarantee against importcsv #16 (the
  // stale-style bug where a row that flipped collision → clean kept its
  // amber highlight until the next unrelated state change). `renderPlan`
  // also dictates row ORDER: collision groups always float to the top,
  // clean rows follow.
  const renderPlan = useMemo(() => buildRenderPlan(preview), [preview]);

  const skipAllInGroup = useCallback(
    async (group: CollisionGroup) => {
      // Sequential awaits — the hook's patchQueueRef already serializes
      // cross-row PATCHes, but awaiting here keeps the fire order stable
      // and makes the button's pending count settle predictably.
      for (const rowID of group.member_row_ids) {
        await onPatchRow(rowID, 'skip', true);
      }
    },
    [onPatchRow],
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
    return (
      <TableCell
        className={`${extraClass} focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring`}
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
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            onBlur={() => void commitEdit(false)}
            onKeyDown={onInputKeyDown}
            aria-describedby={describedby}
            aria-invalid={err ? true : undefined}
            className={`h-7 px-2 py-0 text-sm ${err ? 'ring-1 ring-destructive' : ''}`}
          />
        ) : (
          <span>{displayValue}</span>
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
  const statusText =
    unresolvedCount > 0
      ? `Fix or skip ${unresolvedCount} ${unresolvedCount === 1 ? 'collision' : 'collisions'} to enable import`
      : `Ready to import ${keepCount} rows`;
  const statusColor =
    unresolvedCount > 0 ? 'text-amber-500' : 'text-emerald-500';

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
              const { row, isCollision, groupHeaderId } = unit;
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
                <TableRow
                  key={row.row_id}
                  data-row-id={row.row_id}
                  data-collision={isCollision ? 'true' : undefined}
                  className={isCollision ? 'bg-amber-500/10 border-l-2 border-l-amber-500' : ''}
                >
                  {renderEditableCell(row, 'date', row.date, skipClass, groupHeaderId)}
                  {renderEditableCell(row, 'description', row.description, skipClass, groupHeaderId)}
                  <TableCell className={`text-muted-foreground ${skipClass}`}>{row.category}</TableCell>
                  {renderEditableCell(
                    row,
                    'amount',
                    row.amount.toFixed(2),
                    `text-right font-mono tabular-nums ${skipClass}`,
                    groupHeaderId,
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
