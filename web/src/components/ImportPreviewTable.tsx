import {
  Fragment,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type KeyboardEvent,
  type ReactNode,
} from 'react';
import { AlertTriangle } from 'lucide-react';
import { Link } from 'react-router-dom';
import type {
  CollisionGroup,
  ImportCurrencySummary,
  ImportFieldErrorField,
  ImportPreview,
  ImportRow,
  PatchRowRequest,
} from '@/api/types';
import type { CellError } from '@/hooks/useImportSession';
import { activeFieldErrors, blockedRowIDs } from '@/hooks/useImportSession';
import {
  fallbackFieldErrorMessage,
  isEditableInPreview,
} from '@/lib/import-field-errors';
import { dollarsToCents } from '@/lib/currency';
import { formatAmount, formatRate } from '@/lib/format';
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

type EditableField = 'date' | 'description' | 'amount' | 'rate';
type EditingCell = { rowID: number; field: EditableField } | null;

/**
 * Columns in one body row, and therefore the colSpan of every full-width
 * bar. Named because the two have to agree: a bar one cell short leaves a
 * ragged edge on the right of the table, and one cell long widens the
 * table past its own header.
 */
const PREVIEW_COLUMN_COUNT = 6;

/**
 * DOM id of the explanation attached under a specific flagged row.
 *
 * NAMED FOR THE LENGTH FAMILY, USED BY BOTH. The id and the
 * `data-field-error-detail` attribute beside it now also carry the money
 * family's one uneditable sentence (an unknown currency), because "the
 * flags with no cell of their own" is one row-level surface and splitting
 * it in two would give a row that trips both of them two detail rows.
 * Left unrenamed on purpose — the attribute is a selector in this repo's
 * tests — but do not read either name as "over-length only".
 */
const fieldErrorDetailId = (rowID: number) => `import-field-error-${rowID}`;

/**
 * The bulk bars, as focus targets.
 *
 * A burst can unmount the button that fired it — skipping the last
 * over-length row removes its bar; applying a rate to the last row
 * waiting on one removes that button, or the whole bar. The browser then
 * drops focus to `document.body`, which strands a keyboard user at the
 * top of the document with no announcement. So each bar's heading is a
 * focus anchor, and every burst ends by moving focus deliberately: to the
 * heading if the bar survived, to the Import button if it did not.
 *
 * The `aria-live` status line is NEVER the target. Focusing a live region
 * makes screen readers announce it twice — once as a live update, once as
 * the focused element — and it is not a place a keyboard user can act
 * from.
 */
const LENGTH_BAR_KEY = 'length';
const MONEY_BAR_KEY = 'money';
const LENGTH_BAR_HEADING_ID = 'import-length-bar-heading';
const MONEY_BAR_HEADING_ID = 'import-money-bar-heading';
const collisionGroupBarKey = (groupID: string) => `group-${groupID}`;

/**
 * Where the unknown-currency remedy lives. A ROUTE, not a modal: adding a
 * currency is a Settings section of its own.
 *
 * WHAT SURVIVES THE TRIP, and why each part had to be made to. The card
 * that owns this table UNMOUNTS on the way — Settings renders one section
 * at a time — so nothing in React state comes back on its own:
 *   - the session and every row edit in it are the server's, resumed from
 *     the `import_id` in localStorage;
 *   - the money flags are recomputed against the currencies table as it
 *     stands, which is what clears the one the user just went to fix
 *     (and the hook re-reads the session if the table changes while this
 *     is still mounted);
 *   - the category destinations the user chose are persisted per session
 *     (`lib/import-decisions.ts`). They were NOT, and a trip through this
 *     link came back to a preview with every manual mapping gone.
 */
const CURRENCIES_SETTINGS_PATH = '/settings?tab=currencies';

/**
 * What the Amount cell shows for a row whose money the backend could not
 * resolve. The same em dash the read-only budget cells degrade to, for the
 * same reason: it says "no value here", where a formatted `0.00` says
 * "this row is worth nothing" — a claim about the user's money that the
 * import has explicitly refused to make.
 */
const UNRESOLVED_AMOUNT = '—';

/**
 * One server sentence about a row, with the field it was written about.
 * The field travels with the message because one of them — the unknown
 * currency — needs a control the others do not.
 */
interface RowDetail {
  field: ImportFieldErrorField;
  message: string;
}

/**
 * The rows waiting on one currency's rate, and the rate the user will be
 * shown for them. `rate` comes off the PREVIEW's own currencies snapshot
 * rather than a client-side currencies query, so the number on the button
 * is the number the PATCH sends and the server records.
 */
interface RateOffer {
  code: string;
  rate: number;
  rowIDs: number[];
}

/**
 * A row whose Amount cell disagrees with `original ÷ rate`, and the value
 * that division actually produces.
 */
interface ComputedAmount {
  rowID: number;
  amount: number;
}

type RenderUnit =
  | { kind: 'field-error-bar'; rowIDs: number[] }
  | {
      kind: 'money-error-bar';
      rowIDs: number[];
      rateOffers: RateOffer[];
      computedAmounts: ComputedAmount[];
    }
  | { kind: 'group-header'; group: CollisionGroup }
  | {
      kind: 'row';
      row: ImportPreview['rows'][number];
      isCollision: boolean;
      groupHeaderId?: string;
      hasFieldError: boolean;
      hasMoneyError: boolean;
      /**
       * The row is flagged on `rate` AND carries no amount — so the `0.00`
       * its Amount cell would otherwise render is not this row's value,
       * it is the absence of one. Decided here, with the flags, rather
       * than in the cell: the cell can see the number but not why it is
       * zero.
       */
      amountUnresolved: boolean;
      /**
       * Server-authored explanations for this row's flagged fields that
       * have NO cell of their own (tags, notes, original_currency).
       * Rendered verbatim in a detail row beneath this one. Description,
       * amount and rate errors are absent here on purpose — they are
       * shown in their own cells via `cellErrors`, and repeating them
       * would print the same sentence twice for one row.
       */
      rowLevelMessages: RowDetail[];
    };

/**
 * The currency row for a sheet's code, matched the way the backend
 * matches it: case-insensitively, because a spreadsheet says "lbp" as
 * often as "LBP" and the server canonicalises before it looks anything
 * up. Matching exactly here would offer no rate for exactly the rows a
 * lower-case sheet produces.
 */
function findCurrency(
  currencies: ImportCurrencySummary[] | undefined,
  code: string | undefined,
): ImportCurrencySummary | undefined {
  if (!currencies || !code) return undefined;
  const wanted = code.trim().toLowerCase();
  return currencies.find((c) => c.code.toLowerCase() === wanted);
}

/**
 * The muted second line under a converted Amount: what the sheet quoted,
 * and the rate it was divided by — `1,500,000.00 LBP @ 89,000`.
 *
 * Same muted register as the ledger's own foreign line
 * (`AmountDisplay`), and the same grouping — but NOT the same formatter,
 * which is a real difference and not worth papering over: the ledger
 * signs both of its lines (`formatSignedAmount(displayAmount(...))`),
 * because it knows the row's category type and therefore which direction
 * the money went. A preview row has a category NAME and no type, so there
 * is nothing here to derive a `+` from. This line uses `formatAmount`,
 * matching the Amount column beside it, and a negative original still
 * renders its own minus.
 *
 * The rate takes `formatRate` rather than the money formatter beside it
 * because a rate is not money — see `formatRate`.
 *
 * Returns null in the two cases where there is nothing true to say:
 *   - no original at all, which is most rows; and
 *   - an original in the BASE currency, which the backend collapses
 *     (matrix #7) and stores no original for. Echoing "5.00 USD" under a
 *     $5.00 row would describe a row the import is not going to write.
 *     Matched case-insensitively, as every currency lookup on the server
 *     is.
 *
 * `@ rate` is appended only when the sheet quoted one. A label-only row
 * was not priced by SpenDrop, and printing today's rate against it would
 * claim it was.
 */
function originalMoneyLine(
  row: ImportRow,
  currencies: ImportCurrencySummary[] | undefined,
): ReactNode {
  const original = row.original_amount;
  const code = row.original_currency;
  if (original == null || !code) return null;
  const currency = findCurrency(currencies, code);
  if (currency?.is_base) return null;
  const label = currency?.code ?? code;
  const rateSuffix = row.rate ? ` @ ${formatRate(row.rate)}` : '';
  return (
    <span
      data-testid="import-original-money"
      className="block text-xs font-normal text-muted-foreground"
    >
      {`${formatAmount(original)} ${label}${rateSuffix}`}
    </span>
  );
}

/**
 * The base-currency value a row's foreign money divides down to, or null
 * when this side cannot say.
 *
 * Rounded through `dollarsToCents`, which mirrors the wire edge's own
 * rounding (Go's half-away-from-zero, not JS's half-up) — so the figure
 * offered by "use the computed amount" is the figure the server stores
 * for it, to the cent, including for a negative original.
 *
 * Null when there is nothing to divide, and null when the result rounds
 * to zero cents: that is the backend's `amount_invalid` shape, where the
 * remedy is the sheet rather than a button that would PATCH a zero the
 * server refuses.
 */
function derivedAmount(row: ImportRow): number | null {
  const original = row.original_amount;
  const rate = row.rate;
  if (original == null || rate == null || !(rate > 0)) return null;
  const cents = dollarsToCents(original / rate);
  if (cents === 0) return null;
  return cents / 100;
}

/**
 * What the Rate cell shows, and what its editor opens on.
 *
 * Three states, and the ORDER matters: an unusable cell is echoed back
 * verbatim (`rate_raw` is emitted only for that case, so its presence is
 * the signal), a usable rate is grouped like the money it divides, and an
 * empty cell stays empty.
 *
 * The two differ for exactly one of the three: a grouped "89,000" reads
 * as the same quantity as the money beside it, while the draft is PATCHed
 * verbatim and the server parses a number, so the editor has to hold
 * "89000". For the raw case they are the same string on purpose — the
 * server's message asks the user to clear or correct THAT text, so that
 * text is what the editor must contain for the instruction to be true.
 */
function rateCellText(row: ImportRow): string {
  if (row.rate_raw) return row.rate_raw;
  return row.rate ? formatRate(row.rate) : '';
}

function rateCellEditValue(row: ImportRow): string {
  if (row.rate_raw) return row.rate_raw;
  return row.rate ? String(row.rate) : '';
}

/**
 * Joins the blocker phrases into one readable list: "a", "a and b",
 * "a, b and c".
 *
 * A plain `join(' and ')` was right while there were two of them and
 * produces "a and b and c" for three, which reads as a mistake rather
 * than as a list. The two-item form is unchanged on purpose — it is the
 * sentence the existing tests pin, and it was already correct.
 */
function joinBlockers(blockers: string[]): string {
  if (blockers.length <= 2) return blockers.join(' and ');
  return `${blockers.slice(0, -1).join(', ')} and ${blockers[blockers.length - 1]}`;
}

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
  // Split the same way the gate splits it, through the same helper: a
  // row flagged for money is not a row that is too long, and the two
  // bars offer different escapes.
  const { length: fieldErrorRowIDs, money: moneyErrorRowIDs } = blockedRowIDs(
    preview.field_errors,
    preview.rows,
  );
  // Explanations for fields with no cell, grouped by the row they
  // describe. A row can trip more than one (tags AND notes), so each
  // keeps its own sentence rather than being collapsed into a summary.
  const rowLevelMessages = new Map<number, RowDetail[]>();
  for (const fe of active) {
    if (isEditableInPreview(fe.field)) continue;
    const existing = rowLevelMessages.get(fe.row_id) ?? [];
    existing.push({
      field: fe.field,
      message: fe.message || fallbackFieldErrorMessage(fe.field),
    });
    rowLevelMessages.set(fe.row_id, existing);
  }

  // Rows whose Amount cell would render a zero that means "unresolved"
  // rather than "nothing". Built from the same active-flag walk as the
  // rest, so a skipped row — which is not flagged — shows its amount
  // again like any other row.
  const rateFlaggedRowIDs = new Set(
    active.filter((fe) => fe.field === 'rate').map((fe) => fe.row_id),
  );
  const amountUnresolvedFor = (row: ImportPreview['rows'][number]) =>
    rateFlaggedRowIDs.has(row.row_id) && row.amount === 0;

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

  if (moneyErrorRowIDs.size > 0) {
    // Which rows each bulk escape may touch is decided from the WIRE,
    // never from the server's sentence. The messages are prose written
    // for a person — reading "no rate" out of one would be a parser
    // against copy that is free to be reworded, and it would break the
    // moment it was.
    //
    // "Waiting on a rate" is therefore: flagged on `rate`, carrying an
    // original amount to convert, quoting a currency the household
    // actually has, and that currency is not the base. A row flagged on
    // `amount` already has a rate — offering to overwrite it would
    // re-price money the sheet had decided — and a row whose currency is
    // unknown has no rate to offer, which is precisely what its own flag
    // says.
    //
    // The ORIGINAL AMOUNT is a condition in its own right, not an
    // implication of the others: matrix #10 is a row with a currency and
    // a rate and nothing to apply them to, and it is flagged on `rate`
    // like the rest. Counting it into "apply today's rate to 3 rows"
    // promises a fix that cannot land — the PATCH succeeds and the row
    // stays blocked, because what it is missing is an amount.
    const offersByCode = new Map<string, RateOffer>();
    const computedAmounts: ComputedAmount[] = [];
    // Every burst fires in the PREVIEW's row order, not the order the
    // server happened to list its flags in — the same rule the bulk skip
    // above follows, so what the user sees happening matches the order
    // they are reading.
    const rowOrder = new Map<number, number>();
    preview.rows.forEach((r, i) => rowOrder.set(r.row_id, i));
    const byRowOrder = (a: number, b: number) =>
      (rowOrder.get(a) ?? 0) - (rowOrder.get(b) ?? 0);
    for (const fe of active) {
      const row = byRowId.get(fe.row_id);
      if (!row) continue;
      if (fe.field === 'rate') {
        if (row.original_amount == null) continue;
        const currency = findCurrency(preview.currencies, row.original_currency);
        if (!currency || currency.is_base || !(currency.rate_to_base > 0)) {
          continue;
        }
        const offer = offersByCode.get(currency.code) ?? {
          code: currency.code,
          rate: currency.rate_to_base,
          rowIDs: [],
        };
        offer.rowIDs.push(fe.row_id);
        offersByCode.set(currency.code, offer);
      } else if (fe.field === 'amount') {
        const amount = derivedAmount(row);
        if (amount !== null) computedAmounts.push({ rowID: fe.row_id, amount });
      }
    }
    units.push({
      kind: 'money-error-bar',
      rowIDs: preview.rows
        .filter((r) => moneyErrorRowIDs.has(r.row_id))
        .map((r) => r.row_id),
      rateOffers: [...offersByCode.values()]
        .map((offer) => ({
          ...offer,
          rowIDs: [...offer.rowIDs].sort(byRowOrder),
        }))
        // By code, so two offers keep a stable left-to-right order
        // between renders rather than following whichever row happened
        // to be flagged first.
        .sort((a, b) => a.code.localeCompare(b.code)),
      computedAmounts: [...computedAmounts].sort((a, b) =>
        byRowOrder(a.rowID, b.rowID),
      ),
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
          hasMoneyError: moneyErrorRowIDs.has(rowID),
          amountUnresolved: amountUnresolvedFor(row),
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
        hasMoneyError: moneyErrorRowIDs.has(row.row_id),
        amountUnresolved: amountUnresolvedFor(row),
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
  /**
   * Records one rate against a set of rows — the bulk half of the rate
   * cell. Separate from `onPatchRow` because the hook serializes the
   * burst through its own queue, and REQUIRED rather than optional: a
   * caller that forgot it would render a preview whose "apply today's
   * rate" offer silently did nothing.
   */
  onApplyRate: (rowIDs: number[], rate: number) => Promise<void>;
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
    onApplyRate,
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

  // The primary action, as the last-resort focus target for a burst that
  // removed the bar it was fired from. Outside the scroll container on
  // purpose: it is the one control on this surface that never unmounts
  // while the preview is up.
  const importButtonRef = useRef<HTMLButtonElement | null>(null);

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
  const blocked = useMemo(
    () => blockedRowIDs(preview.field_errors, preview.rows),
    [preview],
  );
  const fieldErrorRowCount = blocked.length.size;
  const moneyErrorRowCount = blocked.money.size;
  // Previous unresolvedCount snapshot for the 0 → >0 edge detection. We
  // do NOT scroll on initial mount (prev defaults to the current value),
  // and we do NOT scroll on N → N+1 transitions (un-skipping a row in an
  // already-visible group is its own user-initiated action and does not
  // need the page to jump).
  const prevBlockedRef = useRef(
    unresolvedCount + fieldErrorRowCount + moneyErrorRowCount,
  );
  useEffect(() => {
    // 409 UX: confirmImport returns 409 UNRESOLVED_COLLISIONS, 409
    // FIELD_TOO_LONG or 409 MONEY_ERRORS → the hook drops the fresh
    // collision_groups or field_errors into state → the blocked count
    // flips 0 → >0.
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
    const blockedCount =
      unresolvedCount + fieldErrorRowCount + moneyErrorRowCount;
    if (prevBlockedRef.current === 0 && blockedCount > 0) {
      requestAnimationFrame(() => {
        const container = scrollContainerRef.current;
        // Comma selector, so this resolves to the first match in
        // DOCUMENT order — the topmost blocked row, whichever kind it
        // is — not the first branch of the selector.
        const firstBlockedRow = container?.querySelector<HTMLTableRowElement>(
          'tr[data-collision="true"], tr[data-field-error="true"], tr[data-money-error="true"]',
        );
        if (firstBlockedRow) {
          firstBlockedRow.scrollIntoView({ behavior: 'smooth', block: 'center' });
        }
      });
    }
    prevBlockedRef.current = blockedCount;
  }, [unresolvedCount, fieldErrorRowCount, moneyErrorRowCount]);

  // Build the render plan from props on EVERY render. No useState, no
  // useEffect — structural guarantee against importcsv #16 (the
  // stale-style bug where a row that flipped collision → clean kept its
  // amber highlight until the next unrelated state change). `renderPlan`
  // also dictates row ORDER: collision groups always float to the top,
  // clean rows follow.
  const renderPlan = useMemo(() => buildRenderPlan(preview), [preview]);

  /**
   * Where focus goes when a burst finishes.
   *
   * Deferred to rAF for the same reason the scroll effect is: the parent
   * re-renders from the last PATCH response, and this has to read the DOM
   * that results, not the one that fired the click. The bar's heading is
   * the first choice — the user is still in the context they acted on,
   * and it announces what is left to do; the Import button is the
   * fallback, because a bar that has vanished means the work it described
   * is done and Import is what to do next.
   */
  const focusAfterBurst = useCallback((headingKey: string) => {
    requestAnimationFrame(() => {
      const heading = scrollContainerRef.current?.querySelector<HTMLElement>(
        `[data-bulk-heading="${headingKey}"]`,
      );
      if (heading) {
        heading.focus();
        return;
      }
      importButtonRef.current?.focus();
    });
  }, []);

  const skipAllRows = useCallback(
    async (rowIDs: number[], headingKey: string) => {
      // Sequential awaits — the hook's patchQueueRef already serializes
      // cross-row PATCHes, but awaiting here keeps the fire order stable
      // and makes the button's pending count settle predictably.
      for (const rowID of rowIDs) {
        // A rejected row does NOT abort the burst, and the rejection does
        // not escape into a `void`-ed promise. `onPatchRow` re-throws
        // after recording the message against that row's cell, so a
        // session that expired mid-burst — or one row the server refuses
        // — would otherwise leave every row AFTER it silently untouched,
        // with nothing on screen saying which ones the click reached.
        // Same policy as `applyRateToRows` in useImportSession.
        try {
          await onPatchRow(rowID, 'skip', true);
        } catch {
          /* surfaced on that row's cell by onPatchRow */
        }
      }
      focusAfterBurst(headingKey);
    },
    [onPatchRow, focusAfterBurst],
  );

  // Both bulk escapes are the same burst over a different row set, so
  // they share one implementation: skipping a collision group and
  // skipping every over-length row differ only in how the ids were
  // chosen.
  const skipAllInGroup = useCallback(
    (group: CollisionGroup) =>
      skipAllRows(group.member_row_ids, collisionGroupBarKey(group.group_id)),
    [skipAllRows],
  );

  /**
   * "Use the computed amounts": the same burst again, over the amount
   * field, carrying the value each row's own original ÷ rate produces.
   *
   * `toFixed(2)` for the same shape the Amount cell shows, so what is
   * sent reads as the money it is. It is NOT what makes the value
   * correct — `derivedAmount` has already rounded to whole cents, and a
   * cents-derived number stringifies to at most two decimals on its own.
   * Deliberately noted: a mutation replacing this with `String(...)`
   * survives the suite, and it survives because the two differ only in a
   * trailing zero that the server parses identically.
   */
  const applyComputedAmounts = useCallback(
    async (targets: ComputedAmount[]) => {
      for (const target of targets) {
        // Contained per row for the reason `skipAllRows` above is, and
        // this burst is the one that reaches it in normal use: a matrix
        // #12 row whose derived value is out of range comes back 400, and
        // an uncaught rejection here would drop every later row on the
        // floor along with the message explaining the first.
        try {
          await onPatchRow(target.rowID, 'amount', target.amount.toFixed(2));
        } catch {
          /* surfaced on that row's amount cell by onPatchRow */
        }
      }
      focusAfterBurst(MONEY_BAR_KEY);
    },
    [onPatchRow, focusAfterBurst],
  );

  /**
   * "Apply today's rate" for one currency's rows. The burst itself lives
   * in the hook (one serialized PATCH per row, each row's failure
   * contained); this wrapper exists so the bulk actions all end the same
   * way — with focus placed somewhere the user can act from.
   */
  const applyRate = useCallback(
    async (offer: RateOffer) => {
      await onApplyRate(offer.rowIDs, offer.rate);
      focusAfterBurst(MONEY_BAR_KEY);
    },
    [onApplyRate, focusAfterBurst],
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
      editValue: string,
    ) => {
      if (e.key === 'Enter' || e.key === 'F2') {
        e.preventDefault();
        beginEdit(rowID, field, editValue, e.currentTarget);
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
    /**
     * A muted second line under the value — today the foreign original
     * behind a converted Amount. Rendered inside the cell so the two
     * halves of one figure stay in one column, and omitted entirely
     * (rather than rendered empty) when there is nothing to say, so a
     * row with no foreign money produces exactly the markup it did
     * before this column existed.
     */
    metaLine?: ReactNode,
    /**
     * What the EDITOR opens on, when that is not what the cell shows.
     *
     * The two are the same for most cells and deliberately not for the
     * rate: a grouped "89,000" reads as the same quantity as the money
     * beside it, while the editor has to hold "89000", because the draft
     * is PATCHed verbatim and the server parses a number. Same principle
     * as the truncated description below — what is on screen is a
     * rendering of the value, never the value itself — but carried in a
     * separate string rather than by CSS, since grouping is not something
     * an ellipsis can undo.
     */
    editValue?: string,
  ) => {
    const isEditing = editing?.rowID === row.row_id && editing.field === field;
    const seed = editValue ?? displayValue;
    const errKey = `${row.row_id}:${field}`;
    const err = cellErrors[errKey];
    const errorMessageId = err ? `cell-error-${row.row_id}-${field}` : undefined;
    // Combine the optional group-header describedby with the optional
    // per-cell error describedby into a single space-separated token
    // list (ARIA spec §4.1.2 — IDREFS). Empty string collapses to
    // undefined so we never emit a useless attribute.
    const describedby =
      [extraAriaDescribedby, errorMessageId].filter(Boolean).join(' ') || undefined;
    // Free text = a value that reached this table UNPARSED, and so with
    // no bound on its length. Description always; the Rate cell when it
    // is echoing back a cell the parser rejected, which is the one way a
    // numeric column here can hold arbitrary sheet text (date and amount
    // are both parsed before they reach the table, and a usable rate is
    // a number).
    //
    // An unbounded cell renders the whole value: a 5,500-character
    // description measured 2,211px tall, burying the banner that asks
    // the user to act on it under two screens of its own text. The rows
    // that get flagged are exactly the rows that would make the preview
    // unreadable — and a rejected rate cell is a flagged row by
    // definition.
    const isFreeText =
      field === 'description' || (field === 'rate' && !!row.rate_raw);
    return (
      <TableCell
        className={`${extraClass} ${isFreeText ? 'max-w-[28rem]' : ''} focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring`}
        // Only the NEW column carries this marker, and only because
        // something has to identify it: the positive-control test strips
        // exactly these nodes and compares what is left against the DOM
        // this table produced before the Rate column existed. Marking
        // every column would put the attribute on that baseline too and
        // turn the comparison into a snapshot of the change itself.
        data-import-col={field === 'rate' ? 'rate' : undefined}
        data-cell-error={err ? 'true' : undefined}
        // Cover both idle (cell is tabable) and edit (Input announces)
        // modes with the same describedby tokens so the group-header
        // context + any live validation error are announced regardless
        // of how the user reached the cell.
        aria-describedby={describedby}
        tabIndex={isEditing ? -1 : 0}
        onDoubleClick={(e) =>
          beginEdit(row.row_id, field, seed, e.currentTarget)
        }
        onKeyDown={(e) => {
          // Capture the cell element on keyboard-entry so Escape/Enter
          // can restore focus to it — e.currentTarget is the <td> even
          // when the key originates on a descendant span.
          if (!isEditing) onCellKeyDown(e, row.row_id, field, seed);
        }}
      >
        {isEditing ? (
          <Input
            autoFocus
            // One editor serves four columns, so the keypad hint has to be
            // per-field rather than on the element: `amount` and `rate` are
            // numeric, and without this the coarse-pointer tablet opens QWERTY
            // on them. There is no `type="number"` here to infer from — the
            // draft is a string for every field — which makes `inputMode` the
            // only lever. Reasoning: `<MonthlyBudgetCard>` in
            // `pages/Budgets.tsx`; `decimal` covers a rate as well as money,
            // per the numeric-input rule in the design guide. The amount is
            // signed (B10), so the minus-key caveat and its diagnostic order
            // apply here too — see the Amount-tab comment in `FilterPanel.tsx`.
            inputMode={
              field === 'amount' || field === 'rate' ? 'decimal' : undefined
            }
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
        {metaLine}
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
  // Named as rows WITH a problem rather than as "money rows": what is
  // wrong differs per row (no rate, an unknown currency, an amount that
  // disagrees with its own division) and the server's sentence on each
  // row says which. The status line is a count and a verb.
  if (moneyErrorRowCount > 0) {
    blockers.push(
      moneyErrorRowCount === 1
        ? '1 row with a money problem'
        : `${moneyErrorRowCount} rows with money problems`,
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
      ? `Fix or skip ${joinBlockers(blockers)} to enable import`
      : '';
  const statusText =
    [rowBlockerText, categoryBlocker].filter(Boolean).join('. ') ||
    `Ready to import ${keepCount} ${keepCount === 1 ? 'row' : 'rows'}`;
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
              {/* Beside the Amount it divides, not at the end of the row:
                  the rate and the figure it produced are two halves of
                  one statement about this row's money.

                  "Rate to base", the same words Settings → Currencies
                  uses for the same number ("Rate to Base"), because a
                  bare "Rate" does not say which way round it goes — and
                  the upload copy spends a whole sentence closing exactly
                  that ambiguity.

                  ALWAYS RENDERED, even for a sheet with no rate column
                  and no foreign row: a table whose shape depends on its
                  data moves the Skip checkbox under the user's cursor
                  between one upload and the next, and the empty cells
                  are where a rate is TYPED (that is the fix for a
                  rate-missing row, and it has to be reachable before the
                  row has a rate). */}
              <TableHead
                data-import-col="rate"
                className="sticky top-0 z-10 bg-background text-right"
              >
                Rate to base
              </TableHead>
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
                    <TableCell colSpan={PREVIEW_COLUMN_COUNT}>
                      <div className="flex items-center justify-between gap-3">
                        {/*
                          Count and action only. The explanation of WHY
                          any given row is too long is the server's
                          sentence and lives with that row — an aggregate
                          restatement here would be a second copy of the
                          same wording, phrased by us, free to drift.
                        */}
                        <div
                          id={LENGTH_BAR_HEADING_ID}
                          data-bulk-heading={LENGTH_BAR_KEY}
                          // A focus ANCHOR, not a tab stop: the burst this
                          // bar fires can unmount the button that fired it,
                          // and focus has to land somewhere deliberate.
                          tabIndex={-1}
                          role="heading"
                          aria-level={3}
                          className="flex items-center gap-2 text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
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
                          // Two bars can offer a "Skip these N rows" at
                          // once, with the same count. The visible label
                          // stays short because it reads in context; the
                          // description supplies that context to a screen
                          // reader, which does not have the column of
                          // amber to look at.
                          aria-describedby={LENGTH_BAR_HEADING_ID}
                          disabled={skipAllDisabled}
                          onClick={() => void skipAllRows(unit.rowIDs, LENGTH_BAR_KEY)}
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
              if (unit.kind === 'money-error-bar') {
                const count = unit.rowIDs.length;
                // Same guard as every other bulk control here: two fast
                // clicks would double-fire the PATCH burst.
                const bulkDisabled = pendingPatchCount > 0;
                return (
                  <TableRow
                    key="money-error-bar"
                    data-money-error-bar="true"
                    className="bg-amber-500/15 border-l-2 border-l-amber-500 hover:bg-amber-500/15"
                  >
                    <TableCell colSpan={PREVIEW_COLUMN_COUNT}>
                      {/* `flex-wrap`, unlike the bar above it: this one can
                          carry three controls at once, and a preview table
                          is the surface most likely to be read on a narrow
                          window. */}
                      <div className="flex flex-wrap items-center justify-between gap-3">
                        {/*
                          Count and actions only — the sentence explaining
                          any particular row is the server's and lives with
                          that row, exactly as in the over-length bar.
                        */}
                        <div
                          id={MONEY_BAR_HEADING_ID}
                          data-bulk-heading={MONEY_BAR_KEY}
                          // Focus anchor — see the same attribute on the
                          // bar above.
                          tabIndex={-1}
                          role="heading"
                          aria-level={3}
                          className="flex items-center gap-2 text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                        >
                          <AlertTriangle
                            className="size-4 shrink-0 text-amber-500"
                            aria-hidden="true"
                          />
                          <span>
                            {count === 1
                              ? '1 row has a money problem'
                              : `${count} rows have money problems`}
                          </span>
                        </div>
                        <div className="flex flex-wrap items-center gap-2">
                          {unit.rateOffers.map((offer) => {
                            const rows = offer.rowIDs.length;
                            return (
                              <Button
                                key={offer.code}
                                type="button"
                                variant="outline"
                                size="sm"
                                className="shrink-0"
                                aria-describedby={MONEY_BAR_HEADING_ID}
                                disabled={bulkDisabled}
                                onClick={() => void applyRate(offer)}
                              >
                                {/*
                                  The rate is IN the label because it is
                                  what the click records: accepting it
                                  writes that number to every named row as
                                  its booked rate, and a button that said
                                  only "apply today's rate" would ask the
                                  user to agree to a figure they cannot see.

                                  So is the CODE, unconditionally — even
                                  when only one currency is waiting. A rate
                                  is a quantity with a direction (the
                                  column header says "Rate to base" and
                                  Settings calls the same number "Rate to
                                  Base"), and a bare 89,000 is exactly the
                                  ambiguity the upload copy warns about.
                                  Naming the currency only when two are on
                                  screen would make the clear version the
                                  rare one.
                                */}
                                {`Apply today's ${formatRate(offer.rate)} ${offer.code} to ${rows} ${
                                  rows === 1 ? 'row' : 'rows'
                                }`}
                              </Button>
                            );
                          })}
                          {unit.computedAmounts.length > 0 && (
                            <Button
                              type="button"
                              variant="outline"
                              size="sm"
                              className="shrink-0"
                              aria-describedby={MONEY_BAR_HEADING_ID}
                              disabled={bulkDisabled}
                              onClick={() =>
                                void applyComputedAmounts(unit.computedAmounts)
                              }
                            >
                              {unit.computedAmounts.length === 1
                                ? `Use the computed ${formatAmount(unit.computedAmounts[0].amount)} for this row`
                                : `Use the computed amounts for ${unit.computedAmounts.length} rows`}
                            </Button>
                          )}
                          {/*
                            The bar must never be actionless. Every money
                            flag has a fix, but two of them (an unknown
                            currency, a sheet that has to be corrected) are
                            fixed somewhere else — so a bar can carry a
                            count with no other button on it, while the
                            status line beside it says "Fix or SKIP". This
                            is the skip half, in the same words the
                            over-length bar uses.
                          */}
                          <Button
                            type="button"
                            variant="outline"
                            size="sm"
                            className="shrink-0"
                            aria-describedby={MONEY_BAR_HEADING_ID}
                            disabled={bulkDisabled}
                            onClick={() =>
                              void skipAllRows(unit.rowIDs, MONEY_BAR_KEY)
                            }
                          >
                            {count === 1
                              ? 'Skip this row'
                              : `Skip these ${count} rows`}
                          </Button>
                        </div>
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
                    <TableCell colSpan={PREVIEW_COLUMN_COUNT}>
                      <div className="flex items-center justify-between gap-3">
                        <div
                          id={headerId}
                          data-bulk-heading={collisionGroupBarKey(
                            unit.group.group_id,
                          )}
                          // Focus anchor — see LENGTH_BAR_KEY above.
                          tabIndex={-1}
                          role="heading"
                          aria-level={3}
                          className="flex items-center gap-2 text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                        >
                          <AlertTriangle
                            className="size-4 shrink-0 text-amber-500"
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
                hasMoneyError,
                amountUnresolved,
                rowLevelMessages: rowMessages,
              } = unit;
              // A row can block the import for any of the three reasons,
              // or several at once. They share the amber treatment
              // because they are the same instruction to the user — this
              // row needs attention before Import will run.
              const rowBlocked = isCollision || hasFieldError || hasMoneyError;
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
                  data-money-error={hasMoneyError ? 'true' : undefined}
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
                  {/*
                    A row whose money the backend could not resolve has no
                    amount to show, and `0.00` is not "unknown" — it is a
                    claim that this row is worth nothing, one cell away
                    from the empty rate that is the actual problem. The
                    editor still opens on `0.00`, because typing over a
                    dash is not editing a number.
                  */}
                  {renderEditableCell(
                    row,
                    'amount',
                    amountUnresolved ? UNRESOLVED_AMOUNT : row.amount.toFixed(2),
                    `text-right font-mono tabular-nums ${skipClass}`,
                    rowDescribedby,
                    originalMoneyLine(row, preview.currencies),
                    row.amount.toFixed(2),
                  )}
                  {/*
                    The divisor, grouped like the money it divides —
                    "89,000", the same string the second line and the bulk
                    button show, because three renderings of one number
                    that differ by a comma read as three numbers. The
                    EDITOR still opens on the raw "89000": the draft is
                    PATCHed verbatim and the server parses a number.

                    UNLESS the sheet held something unusable there, in
                    which case the cell shows exactly that text
                    (`rate_raw`) and the editor opens on it too. Nothing
                    else can be shown — an unusable cell parses to no
                    number at all — and showing nothing puts an empty box
                    beside a message about a value the user cannot see.
                    Verbatim, and unformatted: it is not a rate, it is
                    what someone typed where a rate goes.

                    Empty when the sheet quoted no rate — no dash and no
                    zero here, unlike the Amount beside it. This is the
                    cell the user TYPES the missing rate into, and a
                    placeholder in the primary editing target reads as a
                    value that is already there.
                  */}
                  {renderEditableCell(
                    row,
                    'rate',
                    rateCellText(row),
                    `text-right font-mono tabular-nums ${skipClass}`,
                    rowDescribedby,
                    undefined,
                    rateCellEditValue(row),
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
                    <TableCell colSpan={PREVIEW_COLUMN_COUNT} className="pt-0">
                      <div className="flex flex-col items-start gap-0.5 text-xs text-muted-foreground">
                        {/*
                          The IDREF target is the SENTENCES, and nothing
                          else. Every editable cell in this row points at
                          it, so anything inside is announced once per
                          cell — with the link in here, a screen reader
                          read "Open Settings, Currencies, link" four
                          times over on the way across one row. The link
                          is a sibling: still in the detail, still one Tab
                          away, no longer part of the description.
                        */}
                        <div
                          id={fieldErrorDetailId(row.row_id)}
                          className="flex flex-col items-start gap-0.5"
                        >
                          {rowMessages.map((detail) => (
                            <span key={`${detail.field}:${detail.message}`}>
                              {detail.message}
                            </span>
                          ))}
                        </div>
                        {rowMessages.some(
                          (detail) => detail.field === 'original_currency',
                        ) && (
                          /*
                            The one row-level flag with somewhere to go.
                            An unknown currency cannot be fixed in this
                            table at all — it is added under Settings →
                            Currencies — so the sentence gets the control
                            that carries the user there instead of asking
                            them to find it.

                            A Button wrapping a router Link, not a bare
                            anchor: `asChild` keeps the element an <a>
                            (right-click, middle-click, and the role a
                            screen reader announces) while the button base
                            supplies the 44px coarse-pointer floor. `h-auto`
                            drops the mouse-desktop height back to the text
                            it sits beside — a different tailwind-merge
                            group from `coarse:min-h-11`, so the touch
                            floor survives it.
                          */
                          <Button
                            asChild
                            variant="link"
                            size="sm"
                            className="h-auto px-0 text-xs"
                          >
                            <Link to={CURRENCIES_SETTINGS_PATH}>
                              Open Settings → Currencies
                            </Link>
                          </Button>
                        )}
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
            ref={importButtonRef}
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
