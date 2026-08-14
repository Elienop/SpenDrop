import {
  useCallback,
  useMemo,
  useRef,
  useState,
  type CSSProperties,
  type KeyboardEvent,
} from 'react';
import { format as formatDate, parseISO } from 'date-fns';
import type { HeatmapEntry } from '@/api/types';
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from '@/components/ui/tooltip';
import { useFinePointerDesktop } from '@/hooks/useFinePointerDesktop';
import {
  FORMAT_DATE_SHORT,
  MONTH_NAMES_FULL,
  MONTH_NAMES_SHORT,
  formatYYYYMMDD,
} from '@/lib/dates';
import { cn } from '@/lib/utils';
import { HeatmapDaySheet, type HeatmapDay } from './HeatmapDaySheet';
import {
  OPACITY_STOPS,
  buildIntensityScale,
  chipFill,
  generateMonthDates,
  generateYearDates,
  heatmapCellLabel,
  heaviestDay,
  isUpcoming,
  monthLabelColumns,
  spendingSummaryPhrase,
  toWeekRows,
  toWeekdayRows,
} from './heatmapGrid';

interface SpendingHeatmapProps {
  data: HeatmapEntry[];
  year: number;
  /**
   * Currency formatter bound to the household's base currency. Injected
   * by the parent (PatternsTab) so the heatmap stays agnostic about
   * which currency the household uses.
   */
  format: (amount: number) => string;
}

/**
 * Monday-first weekday headers for the phone month grid. The visible letter
 * is ambiguous on its own ("T" twice, "S" twice), so the full name rides
 * along for screen readers.
 */
const WEEKDAYS = [
  { short: 'M', long: 'Monday' },
  { short: 'T', long: 'Tuesday' },
  { short: 'W', long: 'Wednesday' },
  { short: 'T', long: 'Thursday' },
  { short: 'F', long: 'Friday' },
  { short: 'S', long: 'Saturday' },
  { short: 'S', long: 'Sunday' },
] as const;

/** Shared by both grids' cells and the phone's month buttons. */
/**
 * `outline`, not `ring`, and that is forced rather than chosen: an element gets
 * ONE ring, and this component has already spent it twice — on the today marker
 * and on the selected month. An outline is a second, independent indicator.
 *
 * `outline-offset-2` matches every other focusable surface in the app. It was
 * `offset-0` while the cells were the only place using an outline, which made
 * this the one screen where the focus indicator sat ON the element edge instead
 * of 2px outside it — so tabbing from the tab strip into the grid changed the
 * affordance mid-page.
 *
 * `[outline-style:solid]` RATHER THAN THE BARE `outline` UTILITY, and this is
 * not stylistic. `tailwind-merge` puts bare `outline` and `outline-2` in the
 * same conflict group, so `cn()` DROPS the style and keeps only the width —
 * verified against the rendered DOM, where the class list came back as
 * `outline-2 outline-offset-2 outline-ring` with no style at all. The element
 * still showed a ring, which is why this survived several browser passes: with
 * no author style, Chrome's UA `:focus-visible { outline: auto }` painted its
 * own. The arbitrary property is in a group of its own and survives the merge.
 */
const FOCUS_RING =
  'focus-visible:[outline-style:solid] focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring';

/**
 * The day number's colour, per intensity stop.
 *
 * A SINGLE THRESHOLD CANNOT WORK HERE, and the reason is worth stating because
 * a threshold is the obvious thing to reach for. The chip is `--primary`
 * composited over `--card`, and those two tokens SWAP between themes — so the
 * middle stop lands on mid-grey in BOTH (`#8c8c8d` light, `#888889` dark).
 * Mid-grey needs a dark number, but "dark" is `--foreground` in light mode and
 * `--primary-foreground` in dark mode: opposite tokens at the same stop. The
 * previous `opacity >= OPACITY_STOPS[2]` rule handed the 0.50 stop
 * `text-foreground`, which is dark in light mode (5.89:1, fine) and LIGHT in
 * dark mode — measured at 3.03:1 against a 4.5:1 bar, on 12px/500 text.
 * Lowering the threshold to `OPACITY_STOPS[1]` only moves the failure into
 * light mode (`#fafafa` on `#8c8c8d` ≈ 3.2:1). Hence the `dark:` variant on
 * that one stop.
 *
 * Measured in Chrome at 360px, number vs its own composited chip — light /
 * dark. Re-measured after D1 by PAINTING both colours onto a canvas and
 * reading the bytes back, rather than compositing them in JS, because the fill
 * is now a `color-mix` that Chrome resolves in oklab; the figures moved by at
 * most 0.10, all of it rounding, which is the evidence that moving the alpha
 * from the element into the colour changes no pixel:
 *   0.30  10.15 / 5.89     0.50   5.85 / 5.00
 *   0.75   7.47 / 9.75     1.00  16.97 / 16.97
 *   no spend  5.13 / 5.72
 *
 * `text-foreground/60` rather than `text-muted-foreground` for the zero cell:
 * `text-muted-foreground` on `bg-muted` is shadcn's own default pair and
 * measured 4.40:1 in light mode — under the bar. The alpha keeps the cell
 * visibly quieter than a spending day without inheriting that ratio.
 *
 * SHARED WITH THE MONTH PICKER, deliberately — its twelve chips are painted
 * from the same stops, so the 0.50 failure hit three of them too. One
 * function, or the picker silently keeps whichever rule the day cells left
 * behind.
 *
 * Desktop cells carry no text at all, so none of this applies there.
 */
function chipTextClass(
  total: number,
  count: number,
  opacity: number,
  upcoming = false,
): string {
  // An upcoming cell paints no chip, so its number sits on the CARD, not on a
  // stop: measured 4.83 light / 5.49 dark. (The test table said 5.29 for the
  // dark figure — a transcription slip; 5.49 is what both measurement passes
  // produced, and the table now agrees.)
  if (upcoming) return 'text-muted-foreground';
  if (count === 0) return 'text-foreground/60';
  // A day with rows that netted <= 0 sits on `bg-muted` like an empty day, so
  // its number is the same colour problem — but it is NOT an empty day, and
  // the full-strength foreground is what separates the two at a glance
  // (the ring below is the deliberate signal; this reinforces it). Strictly
  // more contrast than the 5.13 / 5.72 the alpha'd version measures.
  if (total <= 0) return 'text-foreground';
  if (opacity >= OPACITY_STOPS[2]) return 'text-primary-foreground';
  if (opacity >= OPACITY_STOPS[1])
    return 'text-foreground dark:text-primary-foreground';
  return 'text-foreground';
}

/** One day's accumulated money and rows — see the note where it is built. */
interface DayEntry {
  total: number;
  count: number;
}

/**
 * The chip's fill, for EVERY state a cell can be in. One function because the
 * states are the point: there are five of them, three surfaces paint them
 * (year cell, month cell, month picker), and the defect this closes was
 * exactly a value falling between two gates.
 *
 *   upcoming              nothing at all — it cannot be read as a real zero
 *   no rows               `bg-muted`, the "nothing happened" grey
 *   spent                 the ranked intensity chip
 *   net <= 0 with rows    `bg-muted` PLUS a primary ring
 *
 * The last one is the new state and the ring is why it is not simply painted
 * pale: a refunded day spent nothing NET, so a fill on the spending scale
 * would rank it against days that did — but it is not an empty day either,
 * and the household has to be able to see that there is something to tap. An
 * outline says "there is data here" without claiming an intensity. It is a
 * ring on the CHIP, which is free: the today marker and the selected month
 * both spend theirs on the BUTTON.
 */
function chipPaint(
  total: number,
  count: number,
  opacity: number,
  upcoming: boolean,
): { className: string; style?: CSSProperties } {
  if (upcoming) return { className: '' };
  if (count === 0) return { className: 'bg-muted' };
  if (total > 0) return { className: '', style: { backgroundColor: chipFill(opacity) } };
  return { className: 'bg-muted ring-1 ring-inset ring-primary/50' };
}

/** Which month the phone opens on: the current one, or January for a past year. */
function defaultMonth(year: number, today: Date): number {
  return year === today.getFullYear() ? today.getMonth() : 0;
}

/**
 * Walk from (row, col) in the given direction until a real date is found.
 *
 * Padding cells are stepped OVER rather than landed on — they carry no button
 * and no label, so focus would simply vanish. Returns null at the grid edge,
 * which is what the APG grid pattern specifies ("if focus is on the right-most
 * cell in the row, focus does not move").
 */
function step(
  rows: (string | null)[][],
  row: number,
  col: number,
  dRow: number,
  dCol: number,
): string | null {
  let r = row;
  let c = col;
  for (;;) {
    r += dRow;
    c += dCol;
    if (r < 0 || r >= rows.length) return null;
    if (c < 0 || c >= rows[r].length) return null;
    const date = rows[r][c];
    if (date !== null) return date;
  }
}

/** First (`dir` 1) or last (`dir` -1) real date in one row. */
function rowEdge(rows: (string | null)[][], row: number, dir: 1 | -1): string | null {
  const cells = rows[row];
  if (cells === undefined) return null;
  const ordered = dir === 1 ? cells : [...cells].reverse();
  return ordered.find((c): c is string => c !== null) ?? null;
}

/**
 * First (`dir` 1) or last (`dir` -1) real date in the whole grid, in DOM order.
 *
 * DOM ORDER IS THE VISUAL ORDER, NOT THE CHRONOLOGICAL ONE, and on the desktop
 * year grid those differ: rows are weekdays, so the last cell in DOM order is
 * the last SUNDAY of the year (2026-12-27), not December 31st. That is what
 * Ctrl+End should do — the APG grid pattern defines it as the last cell of the
 * last row, a spatial move — but it surprises anyone who reads it as "jump to
 * the end of the year", so it is worth stating rather than rediscovering.
 * On the phone, rows are weeks, so the two orders coincide.
 */
function gridEdge(rows: (string | null)[][], dir: 1 | -1): string | null {
  const flat = rows.flat();
  const ordered = dir === 1 ? flat : [...flat].reverse();
  return ordered.find((c): c is string => c !== null) ?? null;
}

export function SpendingHeatmap({ data, year, format }: SpendingHeatmapProps) {
  // NOT `useIsMobileViewport`. The year grid needs a pointer that can hover
  // and hit a sub-24px target, and no width implies that — see
  // `useFinePointerDesktop` for why this is a second hook rather than a wider
  // one. `isMobile` here means "give this device the calendar", which is now
  // true of a 1152px tablet in landscape as well as a 360px phone.
  const isMobile = !useFinePointerDesktop();
  const today = new Date();
  const todayIso = formatYYYYMMDD(today);

  // Adjusting state when a prop changes, the pattern React documents and
  // TransactionEditSheet already uses: switching year has to reset the month
  // (December 2019 is not a sensible landing spot when the user picks 2026)
  // and an effect would paint the stale month for a frame first.
  const [monthFor, setMonthFor] = useState(() => ({
    year,
    month: defaultMonth(year, today),
  }));
  if (monthFor.year !== year) {
    setMonthFor({ year, month: defaultMonth(year, today) });
  }
  const month = monthFor.month;

  const { lookup, dayTotals, dayScale } = useMemo<{
    lookup: Map<string, DayEntry>;
    dayTotals: Map<string, number>;
    dayScale: (total: number) => number;
  }>(() => {
    // ACCUMULATE PER DAY, never overwrite. `SumExpensesByDay` groups by
    // `t.date` — the raw timestamp column, not `date(t.date)` — so a day
    // holding rows with different times of day arrives as SEVERAL entries, and
    // the handler formats every one of them to the same "YYYY-MM-DD" string
    // (`row.Date.Format("2006-01-02")` in reports_new_handlers.go). Building
    // this with `new Map(data.map(...))` was last-write-wins: the cell painted
    // and announced one group's subtotal while the sheet listed the whole day,
    // and `monthTotals` below — which already accumulates — disagreed with its
    // own day cells. Non-midnight timestamps are reachable through the import
    // bulk-confirm path, which stores `parseImportDate(row.Date)` as-is and so
    // preserves a posting time from a bank export.
    //
    // FOLLOW-UP, deliberately NOT in this branch: the real fix is
    // `GROUP BY date(t.date)` in queries.sql. That changes the sqlc row type
    // off `time.Time`, and this repo's sqlc codegen is hand-maintained, so it
    // is a lockstep hand-edit that belongs in its own PR. Accumulating here is
    // correct either way, and stays correct after that lands.
    //
    // THE COUNT ACCUMULATES WITH THE MONEY, for the same reason and by the
    // same rule: the wire's `txn_count` is per group, so a day arriving as
    // several groups holds its rows across all of them. Everything downstream
    // asks "does this day have rows" rather than "is its total positive".
    const byDay = new Map<string, DayEntry>();
    for (const entry of data) {
      const prev = byDay.get(entry.date);
      byDay.set(entry.date, {
        total: (prev?.total ?? 0) + entry.total,
        count: (prev?.count ?? 0) + entry.txn_count,
      });
    }
    // PERCENTILES ARE COMPUTED OVER THE WHOLE YEAR, never over the month the
    // phone happens to be showing. Two reasons, and the second is the one
    // that bites: months stay comparable to each other, and a sparse month
    // (a holiday, a partial import, a first month of use) would otherwise
    // rank three or four days against each other and paint noise.
    //
    // Ranked over the ACCUMULATED day totals, so `n` and every rank describe
    // days — over `data` directly they would describe per-timestamp groups,
    // which is a different population with a different size.
    const totals = new Map<string, number>();
    for (const [date, entry] of byDay) totals.set(date, entry.total);
    return {
      lookup: byDay,
      // `heaviestDay` asks only about money, so it gets only the money —
      // derived here rather than inside it so the two cannot disagree about
      // which days were accumulated.
      dayTotals: totals,
      dayScale: buildIntensityScale([...totals.values()]),
    };
  }, [data]);

  const months = useMemo(() => {
    const totals = new Array<number>(12).fill(0);
    const counts = new Array<number>(12).fill(0);
    for (const entry of data) {
      if (!entry.date.startsWith(`${year}-`)) continue;
      const m = Number(entry.date.slice(5, 7)) - 1;
      if (m >= 0 && m < 12) {
        totals[m] += entry.total;
        counts[m] += entry.txn_count;
      }
    }
    return { totals, counts };
  }, [data, year]);

  // No `.filter(t => t > 0)` here any more: `buildIntensityScale` owns the
  // population rule now, so the picker and the day grid cannot end up ranking
  // over differently-filtered sets.
  const monthScale = useMemo(
    () => buildIntensityScale(months.totals),
    [months],
  );

  const cells = useMemo(
    () =>
      isMobile ? generateMonthDates(year, month) : generateYearDates(year),
    [isMobile, year, month],
  );
  const rows = useMemo(
    () => (isMobile ? toWeekRows(cells) : toWeekdayRows(cells)),
    [isMobile, cells],
  );
  // Scoped to the cells ON SCREEN: the displayed month on the phone, the whole
  // year on desktop. Both read as "the heaviest day in what you are looking
  // at", which is the only reading that stays true when the month picker
  // moves — a year-wide figure under a March grid would name a day that is
  // not on it.
  const heaviest = useMemo(
    () => heaviestDay(cells, dayTotals),
    [cells, dayTotals],
  );

  const monthLabels = useMemo(
    () => (isMobile ? [] : monthLabelColumns(year, cells)),
    [isMobile, year, cells],
  );

  // --- Roving tabindex -----------------------------------------------------
  // ONE tab stop for the whole grid. 366 tab stops to leave a card is exactly
  // what `role="grid"` exists to prevent. `activeDate` is derived rather than
  // synced by an effect, so changing year or month falls back to the default
  // anchor automatically instead of leaving a stale, unmounted date holding
  // the only tabIndex 0 — which would leave the grid unreachable by Tab.
  const dates = useMemo(
    () => new Set(cells.filter((c): c is string => c !== null)),
    [cells],
  );
  const defaultFocusDate = useMemo(() => {
    if (dates.has(todayIso)) return todayIso;
    return cells.find((c): c is string => c !== null) ?? null;
  }, [cells, dates, todayIso]);
  const [focusedDate, setFocusedDate] = useState<string | null>(null);
  const activeDate =
    focusedDate !== null && dates.has(focusedDate)
      ? focusedDate
      : defaultFocusDate;

  const gridRef = useRef<HTMLTableElement>(null);
  const focusCell = useCallback((date: string) => {
    gridRef.current
      ?.querySelector<HTMLButtonElement>(`[data-heatmap-date="${date}"]`)
      ?.focus();
  }, []);

  // Arrow semantics follow the VISUAL axes, which is why one implementation
  // produces two different key maps: the DOM is row-major in both grids, but
  // a desktop row is a weekday (so Left/Right is +/- a week) and a phone row
  // is a week (so Left/Right is +/- a day). Transposing the data rather than
  // the key handler is what keeps that from being two code paths.
  const handleKeyDown = useCallback(
    (e: KeyboardEvent<HTMLButtonElement>, row: number, col: number) => {
      let target: string | null = null;
      switch (e.key) {
        case 'ArrowRight':
          target = step(rows, row, col, 0, 1);
          break;
        case 'ArrowLeft':
          target = step(rows, row, col, 0, -1);
          break;
        case 'ArrowDown':
          target = step(rows, row, col, 1, 0);
          break;
        case 'ArrowUp':
          target = step(rows, row, col, -1, 0);
          break;
        case 'Home':
          target = e.ctrlKey ? gridEdge(rows, 1) : rowEdge(rows, row, 1);
          break;
        case 'End':
          target = e.ctrlKey ? gridEdge(rows, -1) : rowEdge(rows, row, -1);
          break;
        default:
          return;
      }
      e.preventDefault();
      if (target === null) return;
      setFocusedDate(target);
      focusCell(target);
    },
    [rows, focusCell],
  );

  // --- Day sheet -----------------------------------------------------------
  const [openDay, setOpenDay] = useState<HeatmapDay | null>(null);
  // The cell the sheet was opened from, so focus can go back there. Held in a
  // ref rather than read off `openDay` because `openDay` is already null by
  // the time Radix runs its close-autofocus.
  const openedFromRef = useRef<string | null>(null);

  const openDaySheet = useCallback(
    (date: string, total: number, count: number, upcoming: boolean) => {
      openedFromRef.current = date;
      setOpenDay({ date, total, count, upcoming });
    },
    [],
  );

  const cellProps = (dateStr: string, row: number, col: number) => {
    const entry = lookup.get(dateStr);
    const total = entry?.total ?? 0;
    const count = entry?.count ?? 0;
    const opacity = dayScale(total);
    const upcoming = isUpcoming(dateStr, todayIso, count > 0);
    return {
      dateStr,
      total,
      count,
      opacity,
      label: heatmapCellLabel(dateStr, total, count, format, todayIso),
      isToday: dateStr === todayIso,
      isUpcoming: upcoming,
      isActive: dateStr === activeDate,
      onFocus: () => setFocusedDate(dateStr),
      onKeyDown: (e: KeyboardEvent<HTMLButtonElement>) =>
        handleKeyDown(e, row, col),
      onActivate: () => openDaySheet(dateStr, total, count, upcoming),
    };
  };

  return (
    <>
      {isMobile ? (
        <div className="flex flex-col gap-4">
          {/* The card's own `p-6` leaves 278px at a 360px viewport, which is
              39.7px per column — under the 44px touch floor. Cancelling 16px
              of it per side buys the columns back to a measured 44.28px; the
              grid keeps an 8px gutter inside the card rather than running to
              the border. */}
          <div className="-mx-4">
            <div className="mx-auto flex w-full max-w-[420px] flex-col gap-3">
              <MonthPicker
                year={year}
                month={month}
                monthTotals={months.totals}
                monthCounts={months.counts}
                scale={monthScale}
                format={format}
                onSelect={(m) => setMonthFor({ year, month: m })}
              />
              <table
                ref={gridRef}
                role="grid"
                aria-label={`Spending heatmap for ${MONTH_NAMES_FULL[month]} ${year}`}
                className="w-full table-fixed border-collapse"
              >
                <thead>
                  <tr role="row">
                    {WEEKDAYS.map((d, i) => (
                      <th
                        key={i}
                        role="columnheader"
                        scope="col"
                        className="p-0 pb-1 text-xs font-normal text-muted-foreground"
                      >
                        <span aria-hidden="true">{d.short}</span>
                        <span className="sr-only">{d.long}</span>
                      </th>
                    ))}
                  </tr>
                </thead>
                <tbody>
                  {rows.map((row, r) => (
                    <tr role="row" key={r}>
                      {row.map((dateStr, c) => (
                        <td
                          role="gridcell"
                          key={c}
                          className="p-0 align-top"
                        >
                          {dateStr !== null && (
                            <MonthCell {...cellProps(dateStr, r, c)} />
                          )}
                        </td>
                      ))}
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </div>
          <HeatmapFooter heaviest={heaviest} format={format} />
        </div>
      ) : (
        <TooltipProvider>
          <div className="flex flex-col gap-3">
            <table
              ref={gridRef}
              role="grid"
              aria-label={`Spending heatmap for ${year}`}
              className="w-full table-fixed border-collapse"
            >
              {/* aria-hidden on purpose. A label sits over the column its
                  month STARTS in, and that column's first day can be up to
                  six days into the previous month — so as a `columnheader` it
                  would be announced with cells it does not describe. Every
                  cell already carries its own full date in `aria-label`; this
                  row is visual scaffolding for sighted users only. */}
              <thead aria-hidden="true">
                <tr>
                  {monthLabels.map((label, c) => (
                    <th key={c} className="p-0 font-normal">
                      {/* The label is wider than a ~20px column, so it is
                          taken out of flow and allowed to paint across its
                          neighbours. Consecutive month starts are never
                          closer than four columns in any year of
                          [1900, 2100], so they cannot collide. The wrapper
                          reserves the row's height; the span itself has
                          none. */}
                      <div className="relative h-4">
                        {label !== null && (
                          <span className="absolute left-0 top-0 whitespace-nowrap text-xs leading-4 text-muted-foreground">
                            {label}
                          </span>
                        )}
                      </div>
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {rows.map((row, r) => (
                  <tr role="row" key={r}>
                    {row.map((dateStr, c) => (
                      // Padding cells stay in the DOM as EMPTY gridcells:
                      // dropping them would shift every following cell's
                      // weekday, and `aria-hidden` on one would corrupt the
                      // row's column count. No button inside means no tab
                      // stop to forget.
                      <td role="gridcell" key={c} className="p-0 align-top">
                        {dateStr !== null && (
                          <YearCell {...cellProps(dateStr, r, c)} />
                        )}
                      </td>
                    ))}
                  </tr>
                ))}
              </tbody>
            </table>
            <HeatmapFooter heaviest={heaviest} format={format} />
          </div>
        </TooltipProvider>
      )}
      <HeatmapDaySheet
        day={openDay}
        format={format}
        onClose={() => setOpenDay(null)}
        onCloseFocus={() => {
          const date = openedFromRef.current;
          if (date !== null) focusCell(date);
        }}
      />
    </>
  );
}

interface CellProps {
  dateStr: string;
  /** The day's NET expense total — signed once amounts can be refunds. */
  total: number;
  /** Live expense rows on the day. The paint and the label key on THIS. */
  count: number;
  opacity: number;
  label: string;
  isToday: boolean;
  /** A date that has not happened yet — absent data, not a zero. */
  isUpcoming: boolean;
  isActive: boolean;
  onFocus: () => void;
  onKeyDown: (e: KeyboardEvent<HTMLButtonElement>) => void;
  onActivate: () => void;
}

/**
 * One day of the desktop year grid.
 *
 * The button is the full cell and the painted chip is inset inside it, rather
 * than the button BEING the chip with a gap around it. Same lever as the
 * sheet close button's `-m-3.5 p-3.5`: the visible square keeps the density
 * the year grid needs while the tap/click target grows to the whole column
 * pitch, which is what the old `gap-[3px]` was giving away.
 */
function YearCell({
  dateStr,
  total,
  count,
  opacity,
  label,
  isToday,
  isUpcoming,
  isActive,
  onFocus,
  onKeyDown,
  onActivate,
}: CellProps) {
  const paint = chipPaint(total, count, opacity, isUpcoming);
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <button
          type="button"
          data-heatmap-date={dateStr}
          aria-label={label}
          tabIndex={isActive ? 0 : -1}
          onFocus={onFocus}
          onKeyDown={onKeyDown}
          onClick={onActivate}
          className={cn(
            'relative block aspect-square w-full rounded-sm',
            FOCUS_RING,
            // ON THE BUTTON, NOT ON THE CHIP — and D1 did not make this
            // redundant, which is the trap. It was MOVED here for a reason
            // that has now gone: the chip carried a CSS `opacity`, which fades
            // an element's own ring, so the ring was invisible on every day
            // that had spending and visible only on days that had none —
            // marking today exactly when there was nothing to mark. `chipFill`
            // ends that. It is KEPT here for a SECOND reason that was never
            // written down and does not go away: `--foreground` and
            // `--primary` are near-identical at both ends of both themes, so a
            // ring drawn on the chip has almost nothing to contrast against.
            //
            // Measured in Chrome, ring composited over what it would sit on,
            // against a 3:1 non-text bar (light / dark):
            //   on the CHIP   0.30  3.15 / 2.77    0.50  2.60 / 1.85
            //                 0.75  1.74 / 1.26    1.00  1.06 / 1.05
            //   on the BUTTON  every stop  3.74 / 4.71
            // Seven of those eight chip figures fail, and the two that pass
            // are the pale end — the same inversion as the original defect,
            // arrived at by a different route. On the button the ring lands in
            // the 1.5px card-coloured gutter, where the ratio is a constant
            // that does not depend on the day's total at all.
            isToday && 'ring-1 ring-inset ring-foreground/50',
          )}
        >
          {/* Still two elements here, and for a reason that has nothing to do
              with alpha: the button is the whole column pitch so the click
              target is not the 20px square, and the chip is inset inside it. */}
          <span
            aria-hidden="true"
            className={cn(
              'absolute inset-[1.5px] rounded-[2px]',
              paint.className,
            )}
            style={paint.style}
          />
        </button>
      </TooltipTrigger>
      {/* Mouse-hover sugar, and — now that the trigger is a real button —
          keyboard-focus sugar, which it never was as a bare div. It is NOT
          where the information lives: Radix tooltips cannot open on touch at
          all. See `heatmapCellLabel`.
          
          The SAME string the cell announces, not a second rendering of the
          same facts: this used to print the raw ISO `2026-03-02` beside an
          `aria-label` reading "Monday, March 2nd, 2026", so a mouse user and a
          screen-reader user were told the same thing two different ways — and
          only one of them said "no spending" or "upcoming". */}
      <TooltipContent>
        <p>{label}</p>
      </TooltipContent>
    </Tooltip>
  );
}

/** One day of the phone month grid: the day number, on an intensity chip. */
function MonthCell({
  dateStr,
  total,
  count,
  opacity,
  label,
  isToday,
  isUpcoming,
  isActive,
  onFocus,
  onKeyDown,
  onActivate,
}: CellProps) {
  const day = Number(dateStr.slice(8, 10));
  const paint = chipPaint(total, count, opacity, isUpcoming);
  return (
    <button
      type="button"
      data-heatmap-date={dateStr}
      aria-label={label}
      tabIndex={isActive ? 0 : -1}
      onFocus={onFocus}
      onKeyDown={onKeyDown}
      onClick={onActivate}
      className={cn(
        // `aspect-square` keeps the calendar square once the column is wide
        // enough; `min-h-11` is the floor for when it is not. At 360px the
        // column measures 44.28px — a shade OVER, so the square wins and the
        // floor is inert. It binds at 358px and below: a 320px iPhone SE gives
        // a 38.6px column, and `min-h-11` is what keeps that cell 44px tall.
        'relative block aspect-square min-h-11 w-full rounded-md',
        FOCUS_RING,
        isToday && 'ring-1 ring-inset ring-foreground/50',
      )}
    >
      {/* ONE layer. This was a painted chip with a separate number span sitting
          over it, and the only thing separating them was that CSS `opacity`
          would have faded the number along with its own background — making the
          darkest days the hardest to read. `chipFill` puts the alpha in the
          colour, which does not touch text, so the chip can hold its own
          number.

          Still inset from the button rather than being it: the button is the
          44px touch target and the 2px it gives up is the gutter between
          adjacent days. The today ring stays on the BUTTON — see YearCell for
          the reason that outlived this one. */}
      <span
        className={cn(
          'absolute inset-[2px] flex items-center justify-center rounded-md text-xs font-medium',
          // Every fill state — including the upcoming day that paints NOTHING,
          // so it cannot be read as the `bg-muted` of a day that really did
          // cost nothing (the month opens on today, so that is most of the
          // default view) — is decided in `chipPaint`.
          paint.className,
          chipTextClass(total, count, opacity, isUpcoming),
        )}
        style={paint.style}
      >
        {day}
      </span>
    </button>
  );
}

/**
 * The twelve months of the displayed year as intensity chips that double as
 * the month picker.
 *
 * Six columns rather than one twelve-wide strip because twelve columns at a
 * 360px viewport is a 24px target — the shape this whole rebuild exists to
 * remove. Its own scale, over the twelve monthly totals, so the picker
 * compares months to months; the day grid keeps the year-wide day scale.
 */
function MonthPicker({
  year,
  month,
  monthTotals,
  monthCounts,
  scale,
  format,
  onSelect,
}: {
  year: number;
  month: number;
  monthTotals: number[];
  monthCounts: number[];
  scale: (total: number) => number;
  format: (amount: number) => string;
  onSelect: (month: number) => void;
}) {
  return (
    <div
      role="group"
      aria-label="Heatmap month"
      // gap-1.5, not gap-1: the selected month's ring is drawn 4px outside its
      // button (see below) and needs somewhere to land. Six columns of that
      // still leaves ~47px per button at a 360px viewport.
      className="grid grid-cols-6 gap-1.5"
    >
      {MONTH_NAMES_SHORT.map((name, m) => {
        const total = monthTotals[m] ?? 0;
        const count = monthCounts[m] ?? 0;
        const opacity = scale(total);
        const paint = chipPaint(total, count, opacity, false);
        return (
          <button
            key={name}
            type="button"
            aria-pressed={m === month}
            // The same phrase the day cells announce, from the same function:
            // a month whose refunds outweighed its spending must not report
            // "no spending" while its days say otherwise. Never `upcoming` —
            // a month is offered for selection whether or not it has begun.
            aria-label={`${MONTH_NAMES_FULL[m]} ${year}: ${spendingSummaryPhrase(
              total,
              count,
              format,
            )}`}
            onClick={() => onSelect(m)}
            className={cn(
              // Same 44px floor as the day cells, and the same single painted
              // chip: the month name rides on its own background now that the
              // alpha is in the colour rather than on the element.
              'relative block min-h-11 w-full rounded-md',
              FOCUS_RING,
              // OFFSET, not inset. `--ring` is near-white in dark mode and
              // near-black in light, and so is a full-opacity chip — an inset
              // ring on the busiest month of the year is the same colour as
              // the chip it is drawn on and vanishes. Measured on the selected
              // month at the 1.00 stop, ring against the chip: 1.00:1 in both
              // themes. Against the 2px card-coloured offset band it is drawn
              // on: 17.72 light / 17.31 dark. That band is the whole fix, and
              // it is the same reason shadcn's own focus ring has one.
              m === month && 'ring-2 ring-ring ring-offset-2 ring-offset-card',
            )}
          >
            <span
              className={cn(
                'absolute inset-0 flex items-center justify-center rounded-md text-xs font-medium',
                paint.className,
                chipTextClass(total, count, opacity),
              )}
              style={paint.style}
            >
              {name}
            </span>
          </button>
        );
      })}
    </div>
  );
}

/**
 * The summary line and the legend share a row.
 *
 * The line is ORDINARY TEXT, not an `sr-only` aside: rank bucketing cannot
 * identify a maximum for anyone, so a sighted user staring at a dozen
 * top-quartile cells needs this exactly as much as a screen-reader user does.
 */
function HeatmapFooter({
  heaviest,
  format,
}: {
  heaviest: { date: string; total: number; tiedWith: number } | null;
  format: (amount: number) => string;
}) {
  return (
    <div className="flex flex-wrap items-center gap-x-4 gap-y-1">
      {heaviest !== null && (
        <p className="text-xs text-muted-foreground">
          {/* No year: the grid already names it, and both scopes sit inside
              one year. `FORMAT_DATE_SHORT` rather than a new token. */}
          Heaviest day:{' '}
          <span className="font-medium text-foreground">
            {formatDate(parseISO(heaviest.date), FORMAT_DATE_SHORT)}
          </span>{' '}
          — {format(heaviest.total)}
          {heaviest.tiedWith > 0 &&
            ` (tied with ${heaviest.tiedWith} other ${
              heaviest.tiedWith === 1 ? 'day' : 'days'
            })`}
        </p>
      )}
      <HeatmapLegend />
    </div>
  );
}

/**
 * LABELLED "Daily", and that word is the whole point of it.
 *
 * The month picker's twelve chips and the grid's day cells paint from the SAME
 * four stops of the SAME token through `chipTextClass` — but they are ranked
 * over different populations: `dayScale` over the year's DAY totals,
 * `monthScale` over the twelve MONTH totals. An identically dark chip therefore
 * means different things in the two blocks. One unqualified legend sitting
 * below both read as governing both, and it is paired in that row with
 * "Heaviest day:", which is also day-scoped and reinforced the wrong reading.
 *
 * Naming the scale is the cheap fix and the right scope for this branch.
 * Redesigning the picker to share the day scale, or giving it its own legend,
 * is a larger change than a labelling defect warrants.
 */
function HeatmapLegend() {
  return (
    <div
      role="group"
      aria-label="Daily spending intensity legend"
      className="ml-auto flex flex-wrap items-center justify-end gap-x-4 gap-y-1 text-xs text-muted-foreground"
    >
      {/* LEADS the legend rather than sitting between its two groups, so it
          qualifies the whole scale — "No spend" is day-scoped too. Mid-string
          it read as labelling only the gradient. */}
      <span className="font-medium text-foreground">Daily spending</span>
      <div className="flex items-center gap-1.5">
        <div className="h-3 w-3 rounded-sm bg-muted" aria-hidden="true" />
        <span>No spend</span>
      </div>
      <div className="flex items-center gap-1.5">
        <span>Less</span>
        <div className="flex items-center gap-1">
          {OPACITY_STOPS.map((opacity, i) => (
            <div
              key={i}
              className="h-3 w-3 rounded-sm"
              // Through `chipFill`, not a second copy of the expression: a
              // legend that paints its swatches by its own rule is a legend
              // that can drift off the cells it claims to explain.
              style={{ backgroundColor: chipFill(opacity) }}
              aria-hidden="true"
            />
          ))}
        </div>
        <span>More</span>
      </div>
    </div>
  );
}
