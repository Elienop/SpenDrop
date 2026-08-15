import { useRef, useState } from 'react';
import {
  BarChart,
  Bar,
  XAxis,
  YAxis,
  CartesianGrid,
  ReferenceLine,
  Rectangle,
  type BarShapeProps,
} from 'recharts';
import {
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
  type ChartConfig,
} from '@/components/ui/chart';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { AlertCircle, X } from 'lucide-react';
import { toast } from 'sonner';
import {
  useSpendingHeatmap,
  useRecurring,
  useTagBreakdown,
  dismissRecurring,
} from '@/hooks/useReports';
import { SpendingHeatmap } from './SpendingHeatmap';
import { tagChartHeightPx } from './tagChart';
import {
  CLAMPED_TABLE_TEXT,
  PHONE_TABLE_DENSITY,
  TABLE_TEXT_CELL_WIDTH,
} from './utils';
import { MONTH_NAMES_FULL, yearOptionsFrom } from '@/lib/dates';
import { formatCurrency } from '@/lib/format';
import { useBaseCurrency } from '@/hooks/useBaseCurrency';
import { useFinePointerDesktop } from '@/hooks/useFinePointerDesktop';
import { useIsMobileViewport } from '@/hooks/useIsMobileViewport';
import { useReportYears } from '@/hooks/useReportYears';
import { cn } from '@/lib/utils';
import type { RecurringEntry } from '@/api/types';

const TAG_CONFIG = {
  total: { label: 'Total', color: 'hsl(var(--primary))' },
} satisfies ChartConfig;

/**
 * The three column headers of the Recurring Expenses table, reused verbatim as
 * the card's own labels.
 *
 * A table row is labelled by its column headers; a card is not, so the labels
 * have to travel with the values or the phone reads out three bare numbers.
 * Shared so the two presentations cannot end up calling the same figure two
 * different things — which is also what lets a test compare the card's labels
 * against the table's `<th>`s rather than hard-coding either.
 */
const RECURRING_FIELDS = ['Monthly Avg', 'Frequency', 'Annual Total'] as const;

/**
 * How much of a description an accessible name may carry.
 *
 * Descriptions are user text and import bypasses the 500-character ceiling the
 * per-row PATCH route enforces, so `Dismiss <description>` was an accessible
 * name that a screen reader would read out in full — hundreds of characters
 * before the user learns what the button does. The visible text is already
 * clamped to three lines; this is the same bound for the spoken one.
 *
 * 60 characters is roughly one spoken breath and comfortably longer than any
 * real merchant string ("Spotify Family", "AUB Loan Repayment — Standing Order").
 *
 * WHAT THIS GIVES UP: two entries whose descriptions agree for their first 59
 * characters would end up with the same accessible name, and voice control
 * could not tell them apart. That needs two recurring patterns — three or more
 * months each, same category — differing only past character 59, which no real
 * statement produces; the alternative (an index in the name, "Dismiss row 2")
 * is worse for every real case. If it ever happens, the fix is to disambiguate
 * with something other than more characters, not to raise the cap.
 */
const MAX_DISMISS_LABEL_CHARS = 60;

/**
 * The dismiss button's accessible name, for BOTH presentations.
 *
 * Shared so a rename or a re-cap cannot land on one surface only — the table
 * and the card offer the same action and must offer it under the same name.
 * Same shortening shape as `shortenCategoryLabel` in `./utils`: trim before the
 * ellipsis so a break at a space does not read as "Spotify …".
 */
function dismissLabel(description: string): string {
  const name =
    description.length > MAX_DISMISS_LABEL_CHARS
      ? `${description.slice(0, MAX_DISMISS_LABEL_CHARS - 1).trimEnd()}…`
      : description;
  return `Dismiss ${name}`;
}

/**
 * One detected recurring expense as a stacked card — the below-`md`
 * presentation of the row the table renders.
 *
 * Five columns do not survive a 360px viewport. Measured on the built container
 * at 360px: the tab panned 102px sideways, and what sat out past the right edge
 * was the dismiss control — the row's only action — plus the annual total it is
 * dismissed on the strength of.
 *
 * ANATOMY: the same identity-column-plus-trailing-action shape the Categories
 * card uses, with the three figures as a `<dl>` under the description rather
 * than beside it. Stacking is what buys the width back — three labelled
 * figures side by side are what the table was already failing to fit.
 *
 * The budget, derived rather than measured, so treat it as the sizing argument
 * and not as an observation: Top Merchants next door measured 263px of content
 * box inside this same card at an emulated 360px viewport, and this list keeps
 * `CardContent`'s gutters, so 263 − 8 (`px-1`) − 44 (dismiss) − 12 (gap) ≈ 199px
 * for the identity column. A `<dl>` row is one label plus one right-aligned
 * value, which is comfortably inside that; a row of all three would not be.
 * The real device is ~15px WIDER than the emulated figure (headless Chrome
 * reserves a classic scrollbar gutter — see the header of `./utils`), so the
 * estimate errs pessimistic.
 *
 * The values are right-aligned in a `1fr` track so the two currency figures
 * line up under each other the way the table's `text-right` columns do —
 * `tabular-nums` only aligns digits within a string, not strings with each
 * other.
 */
function RecurringCard({
  entry,
  format,
  onDismiss,
}: {
  entry: RecurringEntry;
  format: (amount: number) => string;
  onDismiss: (entry: RecurringEntry) => void;
}) {
  return (
    <li className="flex items-start gap-3 px-1 py-3">
      {/* `min-w-0` is what lets the clamp bind — a flex item's automatic
          minimum is `min-content`, so without it the column keeps sizing itself
          to the longest unbroken token in the description. */}
      <div className="flex min-w-0 flex-1 flex-col gap-2">
        <span
          className={cn('font-medium', CLAMPED_TABLE_TEXT)}
          title={entry.description}
        >
          {entry.description}
        </span>
        {/* `items-baseline` because the rows are no longer one type size: a
            14px figure and its 12px label sit on a shared baseline instead of
            both hugging the top of a 20px row. */}
        <dl className="grid grid-cols-[auto_1fr] items-baseline gap-x-3 gap-y-1 text-xs">
          <dt className="text-muted-foreground">{RECURRING_FIELDS[0]}</dt>
          {/* The money keeps the app's base type size while the labels stay at
              12px. The desktop table renders these at 14px, every other
              money-bearing card in the app does too, and the annual total is
              the figure the dismiss decision actually rests on — a 12px total
              under a 12px label reads as metadata about the description rather
              than as the payload. Re-checked against the width budget above:
              "$11,110.50" in 14px mono next to a 12px "Annual Total" is ~162px
              of the ~199px column. */}
          <dd className="text-right font-mono text-sm tabular-nums">
            {format(entry.monthly_avg)}
          </dd>
          <dt className="text-muted-foreground">{RECURRING_FIELDS[1]}</dt>
          {/* Frequency is deliberately NOT promoted: it is a count that
              qualifies the two figures around it, and giving all three the same
              weight is what made the card read as a flat list of numbers. */}
          <dd className="text-right">{entry.month_count}/12 months</dd>
          <dt className="text-muted-foreground">{RECURRING_FIELDS[2]}</dt>
          <dd className="text-right font-mono text-sm tabular-nums">
            {format(entry.annual_total)}
          </dd>
        </dl>
      </div>
      {/* 44px for a mouse too, not only for a coarse pointer: this subtree only
          exists below `md`, the same call the Categories and Trash cards make.
          `min-*` is a different tailwind-merge group from the icon variant's
          `h-10 w-10`, so it composes with the primitive instead of racing it. */}
      <Button
        variant="ghost"
        size="icon"
        className="min-h-11 min-w-11 shrink-0"
        aria-label={dismissLabel(entry.description)}
        onClick={() => onDismiss(entry)}
      >
        <X className="h-4 w-4" />
      </Button>
    </li>
  );
}

export function PatternsTab() {
  const now = new Date();
  // Where focus goes when a dismiss removes the row it was sitting on — see
  // `handleDismiss`.
  const recurringTitleRef = useRef<HTMLDivElement>(null);
  const [year, setYear] = useState(now.getFullYear());
  const [tagYear, setTagYear] = useState(now.getFullYear());
  const [tagMonth, setTagMonth] = useState(0);
  // TWO gates, deliberately, because the two cards below ask different
  // questions and one hook cannot answer both.
  //
  // The heatmap's placeholder mirrors the heatmap's OWN gate exactly — it has
  // to reserve room for the branch that is about to render, and that branch is
  // chosen on pointer capability: a year grid of sub-24px cells whose only
  // readout is a hover tooltip is unusable on a touch device at any width.
  //
  // The recurring table is a room question, so it forks on width alone, the
  // same `useIsMobileViewport` the other five card lists in this app use. The
  // household's ~1130px landscape tablet keeps the five-column table there and
  // that is correct: the width exists, and its dismiss button already takes the
  // Button primitive's own coarse-pointer floor.
  const heatmapIsCalendar = !useFinePointerDesktop();
  const isMobile = useIsMobileViewport();
  const heatmap = useSpendingHeatmap(year);
  const recurring = useRecurring(year);
  const tags = useTagBreakdown(tagYear, tagMonth);

  const baseCurrency = useBaseCurrency();
  const fmt = (amount: number) => formatCurrency(amount, baseCurrency);

  // Exactly the years the ledger holds, so an imported 1984 statement is
  // selectable and the empty years between are not offered. One list feeds
  // BOTH Selects on this tab (heatmap `year` and tag `tagYear`), so BOTH
  // selections are unioned in — dropping either from the list after a refetch
  // would leave that Select holding a value with no matching item, which
  // renders a blank trigger, silently, with no error.
  const { years: ledgerYears } = useReportYears();
  const tagYears = yearOptionsFrom(ledgerYears, year, tagYear);

  // Lifted out of the table cell it used to live in so both presentations
  // dismiss through ONE implementation. A card with its own copy is how the
  // two surfaces end up posting different years, or one of them forgetting to
  // refetch, with nothing failing.
  async function handleDismiss(entry: RecurringEntry) {
    try {
      await dismissRecurring(year, entry.description);
      // ORDER IS LOAD-BEARING, and all three lines are about the same moment.
      //
      // Focus first, while the DOM is still standing. The button that was just
      // pressed is inside the row about to be dropped, so once the refetch
      // lands its focused element unmounts and focus falls to <body> — no
      // context, and a keyboard user starts again from the top of the page.
      // Moving focus BEFORE the row goes means nothing is ever taken out from
      // under it. The section title is the anchor rather than the list, because
      // dismissing the LAST entry unmounts the list too and the title is the
      // one node that survives every outcome.
      recurringTitleRef.current?.focus();
      // The toast is the whole of the success feedback: the row simply vanishes
      // otherwise, which reads the same as a tap that missed. It is also the
      // announcement — sonner renders into a live region, and the focus move
      // above is silent.
      //
      // No Undo action, and that is a backend fact, not an oversight: the API
      // has POST /reports/recurring/dismiss and no inverse (router.go), so an
      // Undo button here would have nothing to call. If un-dismissing is ever
      // wanted, it is an endpoint first and a toast action second.
      toast.success('Recurring expense dismissed');
      recurring.refetch();
    } catch {
      toast.error('Failed to dismiss recurring expense');
    }
  }

  return (
    <div className="flex flex-col gap-6">
      {/* Spending Heatmap */}
      {/* `role="region"` is what makes the `aria-labelledby` beneath it do
          anything. `Card` renders a bare <div>, whose implicit `generic` role
          is on ARIA's name-prohibited list — so the association was silently
          discarded and the card read as unnamed. */}
      <Card role="region" aria-labelledby="spending-heatmap-heading">
        <CardHeader className="flex flex-row flex-wrap items-center justify-between gap-3 space-y-0 pb-4">
          <CardTitle
            id="spending-heatmap-heading"
            className="text-base font-semibold"
          >
            Spending Heatmap
          </CardTitle>
          <Select
            value={String(year)}
            onValueChange={(v) => setYear(Number(v))}
          >
            <SelectTrigger aria-label="Heatmap Year" className="h-9 w-[120px]">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {tagYears.map((y) => (
                <SelectItem key={y.value} value={y.value}>
                  {y.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </CardHeader>
        <CardContent>
          {/* Sized per BRANCH, because the two presentations are nowhere near
              the same height and one number cannot be right for both. The old
              flat 120px under-reserved even the desktop grid (7 rows of
              ~21px cells plus the legend is closer to 200px), so the card
              grew when data landed; a phone month grid plus the month picker
              is roughly twice that again. */}
          {heatmap.loading && (
            <Skeleton
              className={cn(
                'w-full',
                heatmapIsCalendar ? 'h-[420px]' : 'h-[200px]',
              )}
            />
          )}
          {heatmap.error && (
            <Alert variant="destructive">
              <AlertCircle className="h-4 w-4" />
              <AlertDescription>{heatmap.error}</AlertDescription>
            </Alert>
          )}
          {!heatmap.loading && !heatmap.error && (
            <div
              className={cn(
                'transition-opacity duration-200',
                heatmap.fetching && !heatmap.loading && 'opacity-60',
              )}
            >
              <SpendingHeatmap
                data={heatmap.data}
                year={year}
                format={fmt}
              />
            </div>
          )}
        </CardContent>
      </Card>

      {/* Recurring Expenses */}
      {/* Named for the same reason as the heatmap card above: `Card` is a
          bare <div>, whose implicit `generic` role is name-prohibited, so the
          `aria-labelledby` was discarded. All THREE carry it — one landmark
          among three identical cards would put a single arbitrary card in a
          landmark list and hide its siblings. */}
      <Card role="region" aria-labelledby="recurring-heading">
        <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-4">
          {/* `tabIndex={-1}` makes this reachable programmatically without
              putting it in the tab order, and `outline-none` keeps the landing
              silent — the same anchor treatment the Categories page heading
              takes after a confirmed delete. It is a `<div>`, not a heading:
              `CardTitle` always has been, and it is the region's
              `aria-labelledby` target, so focusing it announces the section. */}
          <CardTitle
            ref={recurringTitleRef}
            id="recurring-heading"
            tabIndex={-1}
            className="text-base font-semibold outline-none"
          >
            Recurring Expenses
          </CardTitle>
        </CardHeader>
        <CardContent>
          {recurring.loading && <Skeleton className="h-[200px] w-full" />}
          {recurring.error && (
            <Alert variant="destructive">
              <AlertCircle className="h-4 w-4" />
              <AlertDescription>{recurring.error}</AlertDescription>
            </Alert>
          )}
          {!recurring.loading &&
            !recurring.error &&
            recurring.data.length === 0 && (
              <p className="text-sm text-muted-foreground">
                No recurring expenses detected yet
              </p>
            )}
          {!recurring.loading &&
            !recurring.error &&
            recurring.data.length > 0 && (
              <div
                className={cn(
                  'transition-opacity duration-200',
                  recurring.fetching && !recurring.loading && 'opacity-60',
                )}
              >
                {isMobile ? (
                  /*
                    --- The presentation fork ----------------------------------
                    Below `md` the table is replaced wholesale by stacked cards,
                    not hidden with `md:hidden` — mounting both trees would put
                    every entry's dismiss button in the document twice, and
                    `display:none` removes the loser from the a11y tree but
                    never from React. See `useIsMobileViewport`.

                    Everything above this fork is shared and already
                    width-agnostic: the heading, the loading placeholder, the
                    error banner, the empty line and the refetch fade.

                    `role="list"` is not redundant: Tailwind's preflight sets
                    `list-style: none`, and Safari/VoiceOver drop the list role
                    along with the marker.

                    Kept inside `CardContent` rather than pulled out to reclaim
                    its 48px of gutters, unlike the Categories list: the four
                    states above share that one wrapper here, so pulling the
                    list out would mean forking the wrapper too, and the width
                    budget on <RecurringCard> says the gutters are affordable.
                    If the browser pass at 360px disagrees, THIS is the knob —
                    `px-0` on the wrapper, not a denser card.
                  */
                  <ul
                    role="list"
                    aria-label="Recurring expenses"
                    className="flex flex-col divide-y divide-border"
                  >
                    {recurring.data.map((entry) => (
                      <RecurringCard
                        key={entry.description}
                        entry={entry}
                        format={fmt}
                        onDismiss={(e) => void handleDismiss(e)}
                      />
                    ))}
                  </ul>
                ) : (
                  /* Same phone-width density as Top Merchants in SpendingTab —
                     see the note there. Five columns here, so 160px of padding
                     before any content.

                     Its narrow half is now INERT on this table and kept anyway:
                     the fork above means this branch only mounts at 768px and
                     up, which is already past the `sm:` (640px) restore, so the
                     px-4 arm always wins. It stays because the constant is the
                     shared Reports recipe — Top Merchants still reaches the
                     narrow arm, and a table that spells out its own density is
                     how the two drift apart the next time one of them moves.
                     Do not read its presence as evidence this table renders on
                     a phone; it does not any more. */
                  <Table className={PHONE_TABLE_DENSITY}>
                    <TableHeader>
                      <TableRow>
                        <TableHead>Description</TableHead>
                        {RECURRING_FIELDS.map((field) => (
                          <TableHead key={field} className="text-right">
                            {field}
                          </TableHead>
                        ))}
                        <TableHead className="w-[50px]" />
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {recurring.data.map((entry) => (
                        <TableRow key={entry.description}>
                          {/* Same exposure and same treatment as Top Merchants
                              in SpendingTab — see the note there for why
                              `overflow-wrap:anywhere` (not `break-words`) is
                              the class that bounds a table cell's WIDTH, why
                              `line-clamp-3` is needed to bound its HEIGHT, and
                              why three clamped lines beat one truncated one on
                              a surface with no row expansion. */}
                          <TableCell className={TABLE_TEXT_CELL_WIDTH}>
                            <div
                              className={CLAMPED_TABLE_TEXT}
                              title={entry.description}
                            >
                              {entry.description}
                            </div>
                          </TableCell>
                          <TableCell className="text-right font-mono tabular-nums">
                            {fmt(entry.monthly_avg)}
                          </TableCell>
                          <TableCell className="text-right">
                            {entry.month_count}/12 months
                          </TableCell>
                          <TableCell className="text-right font-mono tabular-nums">
                            {fmt(entry.annual_total)}
                          </TableCell>
                          <TableCell>
                            {/* Named per entry, as the card's is. Every row
                                used to carry the same "Dismiss recurring
                                expense", so a screen reader or voice control
                                offered N identical buttons with no way to say
                                which one dismisses which. */}
                            <Button
                              variant="ghost"
                              size="icon"
                              aria-label={dismissLabel(entry.description)}
                              onClick={() => void handleDismiss(entry)}
                            >
                              <X className="h-4 w-4" />
                            </Button>
                          </TableCell>
                        </TableRow>
                      ))}
                    </TableBody>
                  </Table>
                )}
              </div>
            )}
        </CardContent>
      </Card>

      {/* Tag Analysis */}
      <Card role="region" aria-labelledby="tag-analysis-heading">
        <CardHeader className="flex flex-row flex-wrap items-center justify-between gap-3 space-y-0 pb-4">
          <CardTitle
            id="tag-analysis-heading"
            className="text-base font-semibold"
          >
            Tag Analysis
          </CardTitle>
          <div className="flex gap-2">
            <Select
              value={String(tagMonth)}
              onValueChange={(v) => setTagMonth(Number(v))}
            >
              <SelectTrigger aria-label="Tag Month" className="h-9 w-[140px]">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="0">Year to Date</SelectItem>
                {MONTH_NAMES_FULL.map((m, i) => (
                  <SelectItem key={m} value={String(i + 1)}>
                    {m}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <Select
              value={String(tagYear)}
              onValueChange={(v) => setTagYear(Number(v))}
            >
              <SelectTrigger aria-label="Tag Year" className="h-9 w-[100px]">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {tagYears.map((y) => (
                  <SelectItem key={y.value} value={y.value}>
                    {y.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
        </CardHeader>
        <CardContent>
          {tags.loading && <Skeleton className="h-[300px] w-full" />}
          {tags.error && (
            <Alert variant="destructive">
              <AlertCircle className="h-4 w-4" />
              <AlertDescription>{tags.error}</AlertDescription>
            </Alert>
          )}
          {!tags.loading && !tags.error && tags.data.length === 0 && (
            <p className="text-sm text-muted-foreground">
              No tagged transactions yet. Add tags to your transactions to see
              breakdowns here.
            </p>
          )}
          {!tags.loading && !tags.error && tags.data.length > 0 && (
            <ChartContainer
              config={TAG_CONFIG}
              style={{ height: tagChartHeightPx(tags.data.length) }}
              className={cn(
                // `aspect-auto` neutralizes ChartContainer's base `aspect-video`
                // so our explicit data-driven height is the sole sizing input —
                // matches the variable-height charts in SpendingTab.
                'aspect-auto w-full transition-opacity duration-200',
                tags.fetching && !tags.loading && 'opacity-60',
              )}
            >
              <BarChart accessibilityLayer data={tags.data} layout="vertical">
                <CartesianGrid horizontal={false} />
                <XAxis type="number" tickLine={false} axisLine={false} />
                <YAxis
                  dataKey="tag"
                  type="category"
                  tickLine={false}
                  axisLine={false}
                  width={120}
                />
                <ChartTooltip content={<ChartTooltipContent />} />
                {/* A tag whose refunds outweigh its spending nets negative and
                    draws LEFTWARD — recharts 3 extends the default
                    [0, 'auto'] domain to include negative data rather than
                    clipping it. The axis above is visible, but its zero tick
                    is one label among several; the line is what makes the
                    direction of a bar readable at a glance. */}
                <ReferenceLine x={0} stroke="hsl(var(--border))" />
                {/* Symmetric radius, not [0, 4, 4, 0]: the array form rounds
                    the RIGHT end only, so a leftward bar came out rounded at
                    the zero line and square at its own tip. */}
                {/* One colour per tag, cycling the 20 chart slots. This was a
                    `<Cell>` child per bar until recharts deprecated `Cell` in
                    3.10 (removed in 4.0); `shape` is the replacement recharts
                    documents. `{...props}` forwards the whole rect — geometry,
                    `radius`, active state and the animation frame props — so
                    only the fill is ours.

                    `originalDataIndex`, NOT `index`: `index` is the position
                    among the rects that survived filtering, while
                    `originalDataIndex` is the row's position in `tags.data` —
                    the same index the `<Cell>` children were mapped over, so
                    every tag keeps the colour it had.

                    SpendingTab's category chart deliberately does NOT use this
                    mechanism; see the note on `breakdownSorted` there for the
                    LabelList reason. */}
                <Bar
                  dataKey="total"
                  radius={4}
                  shape={(props: BarShapeProps) => (
                    <Rectangle
                      {...props}
                      fill={`hsl(var(--chart-${(props.originalDataIndex % 20) + 1}))`}
                    />
                  )}
                />
              </BarChart>
            </ChartContainer>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
