import { useMemo, useState } from 'react';
import {
  BarChart,
  Bar,
  Cell,
  LabelList,
  LineChart,
  Line,
  XAxis,
  YAxis,
  CartesianGrid,
  ReferenceLine,
} from 'recharts';
import {
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
  ChartLegend,
  ChartLegendContent,
  type ChartConfig,
} from '@/components/ui/chart';
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from '@/components/ui/card';
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
import { Skeleton } from '@/components/ui/skeleton';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { AlertCircle } from 'lucide-react';
import {
  useCategoryBreakdown,
  useCategoryTrends,
  useTopMerchants,
  useExpenseVelocity,
} from '@/hooks/useReports';
import { getCategoryColorVar } from '@/lib/chart-colors';
import {
  MONTH_NAMES_FULL,
  yearOptionsFrom,
  formatMonthTick,
  formatMonthLabel,
} from '@/lib/dates';
import { formatCurrency } from '@/lib/format';
import { useBaseCurrency } from '@/hooks/useBaseCurrency';
import { useReportYears } from '@/hooks/useReportYears';
import { TYPE_EXPENSE } from '@/lib/transaction-types';
import { cn } from '@/lib/utils';
import {
  categoryChartHeightPx,
  shortenCategoryLabel,
  monthAxisInterval,
  CLAMPED_TABLE_TEXT,
  MONTH_TICK,
  MONTH_TICK_ANGLE,
  PHONE_TABLE_DENSITY,
  ROTATED_MONTH_TICK_PADDING,
  TABLE_TEXT_CELL_WIDTH,
} from './utils';
import type { ExpenseVelocityData } from '@/api/types';

interface VelocityPoint {
  day: number;
  current: number;
  previous: number;
  pace: number | undefined;
}

function buildVelocityData(
  source: ExpenseVelocityData | null,
): VelocityPoint[] {
  if (!source) return [];
  const { days_in_month: days, budget } = source;
  const currentEntries = source.current;
  const previousEntries = source.previous;
  const currentMap = new Map(
    currentEntries.map((d) => [d.day, d.daily_total]),
  );
  const previousMap = new Map(
    previousEntries.map((d) => [d.day, d.daily_total]),
  );

  const result: VelocityPoint[] = [];
  let cumCurrent = 0;
  let cumPrevious = 0;

  for (let i = 0; i < days; i++) {
    const day = i + 1;
    cumCurrent += currentMap.get(day) ?? 0;
    cumPrevious += previousMap.get(day) ?? 0;
    result.push({
      day,
      current: cumCurrent,
      previous: cumPrevious,
      pace: budget > 0 ? (budget / days) * day : undefined,
    });
  }
  return result;
}

const VELOCITY_CONFIG = {
  current: { label: 'Current Month', color: 'hsl(var(--primary))' },
  previous: { label: 'Previous Month', color: 'hsl(var(--primary) / 0.35)' },
  pace: { label: 'Budget Pace', color: 'hsl(var(--chart-3))' },
} satisfies ChartConfig;

export function SpendingTab() {
  const now = new Date();
  const [year, setYear] = useState(now.getFullYear());
  const [month, setMonth] = useState(now.getMonth() + 1);
  const catBreakdown = useCategoryBreakdown(year, month);
  const catTrends = useCategoryTrends(12);
  const merchants = useTopMerchants(year, month);
  const velocity = useExpenseVelocity(year, month);

  const baseCurrency = useBaseCurrency();
  const fmt = (amount: number) => formatCurrency(amount, baseCurrency);

  // Exactly the years the ledger holds, so an imported 1984 statement is
  // selectable and the empty years between are not offered. `year` is unioned
  // in so a refetch that drops it cannot leave the Select holding a value with
  // no matching item — that renders a blank trigger, silently, with no error.
  const { years: ledgerYears } = useReportYears();
  const years = yearOptionsFrom(ledgerYears, year);

  // Category breakdown bar chart data
  const breakdownTotal = useMemo(
    () => catBreakdown.data.reduce((s, c) => s + c.total, 0),
    [catBreakdown.data],
  );
  const breakdownConfig = useMemo<ChartConfig>(
    () =>
      catBreakdown.data.reduce<ChartConfig>((acc, cat) => {
        acc[`cat-${cat.id}`] = {
          label: cat.name,
          color: getCategoryColorVar({ id: cat.id }),
        };
        return acc;
      }, {}),
    [catBreakdown.data],
  );
  const breakdownSorted = useMemo(
    () =>
      [...catBreakdown.data]
        .sort((a, b) => b.total - a.total)
        .map((c) => ({ ...c, configKey: `cat-${c.id}` })),
    [catBreakdown.data],
  );

  // Category trends - top 6 expense categories
  const expenseCategories = useMemo(
    () =>
      catTrends.data
        .filter((c) => c.type === TYPE_EXPENSE)
        .map((c) => ({
          ...c,
          totalSum: c.data.reduce((sum, d) => sum + d.total, 0),
        }))
        .sort((a, b) => b.totalSum - a.totalSum)
        .slice(0, 6),
    [catTrends.data],
  );

  // Category Trends shares the ISO-8601 `"YYYY-MM-01"` X-axis convention
  // with OverviewTab and Dashboard so `formatMonthTick` / `formatMonthLabel`
  // from `lib/dates` can parse it. Previously we pre-baked `"Jun '25"` into
  // the `name` field, which worked but duplicated the formatter logic and
  // couldn't share tooltip rendering with other charts.
  const catTrendData = useMemo<Record<string, string | number>[]>(() => {
    if (expenseCategories.length === 0) return [];
    const monthSet = new Set<string>();
    const byCat = new Map<number, Map<string, number>>();
    for (const cat of expenseCategories) {
      const inner = new Map<string, number>();
      for (const d of cat.data) {
        const key = `${d.year}-${String(d.month).padStart(2, '0')}-01`;
        inner.set(key, d.total);
        monthSet.add(key);
      }
      byCat.set(cat.id, inner);
    }
    const sortedMonths = Array.from(monthSet).sort();
    return sortedMonths.map((date) => {
      const point: Record<string, string | number> = { date };
      for (const cat of expenseCategories) {
        point[`cat-${cat.id}`] = byCat.get(cat.id)?.get(date) ?? 0;
      }
      return point;
    });
  }, [expenseCategories]);

  const catTrendConfig = useMemo<ChartConfig>(
    () =>
      expenseCategories.reduce<ChartConfig>((acc, cat) => {
        acc[`cat-${cat.id}`] = {
          label: cat.name,
          color: getCategoryColorVar({ id: cat.id }),
        };
        return acc;
      }, {}),
    [expenseCategories],
  );

  // Expense velocity - cumulative lines
  const velocityData = useMemo(() => buildVelocityData(velocity.data), [velocity.data]);

  return (
    // Every card in this grid carries `min-w-0` — without it this tab measured
    // 3530px wide at a 390px viewport. A grid item defaults to min-width:auto,
    // so the Recharts SVG's width becomes the track's min-content, and
    // ResponsiveContainer then measures the widened track and grows to match.
    // See the longer note in OverviewTab.
    <div className="grid gap-6 md:grid-cols-2">
      {/* Category Breakdown (bar chart) */}
      <Card aria-labelledby="category-breakdown-heading" className="min-w-0">
        <CardHeader className="flex flex-row flex-wrap items-center justify-between gap-3 space-y-0 pb-4">
          <div className="flex flex-col gap-0.5">
            <CardTitle
              id="category-breakdown-heading"
              className="text-base font-semibold"
            >
              Category Breakdown
            </CardTitle>
            <CardDescription className="text-xs">{fmt(breakdownTotal)} total</CardDescription>
          </div>
          <div className="flex gap-2">
            <Select
              value={String(month)}
              onValueChange={(v) => setMonth(Number(v))}
            >
              <SelectTrigger
                aria-label="Breakdown Month"
                className="h-9 w-[140px]"
              >
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {MONTH_NAMES_FULL.map((m, i) => (
                  <SelectItem key={m} value={String(i + 1)}>
                    {m}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <Select
              value={String(year)}
              onValueChange={(v) => setYear(Number(v))}
            >
              <SelectTrigger
                aria-label="Breakdown Year"
                className="h-9 w-[100px]"
              >
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {years.map((y) => (
                  <SelectItem key={y.value} value={y.value}>
                    {y.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
        </CardHeader>
        <CardContent>
          {catBreakdown.loading && <Skeleton className="h-[300px] w-full" />}
          {catBreakdown.error && (
            <Alert variant="destructive">
              <AlertCircle className="h-4 w-4" />
              <AlertDescription>{catBreakdown.error}</AlertDescription>
            </Alert>
          )}
          {!catBreakdown.loading &&
            !catBreakdown.error &&
            catBreakdown.data.length > 0 && (
              <ChartContainer
                config={breakdownConfig}
                className={cn(
                  // `aspect-auto` neutralizes ChartContainer's base
                  // `aspect-video` so the explicit height below is the sole
                  // sizing input — the same pairing every other data-driven
                  // chart here uses. It renders correctly without it only
                  // because CSS ignores `aspect-ratio` when both axes are
                  // already definite; the class is what keeps that true if the
                  // width ever stops being.
                  'aspect-auto w-full transition-opacity duration-200',
                  catBreakdown.fetching &&
                    !catBreakdown.loading &&
                    'opacity-60',
                )}
                // Inline because the height is DATA, not a design token — one
                // row per category, like Patterns' tag chart. A Tailwind class
                // cannot express it and an arbitrary `h-[Npx]` would not be
                // generated for a value only known at runtime.
                style={{ height: categoryChartHeightPx(breakdownSorted.length) }}
              >
                <BarChart
                  accessibilityLayer
                  data={breakdownSorted}
                  layout="vertical"
                  margin={{ left: 0, right: 80 }}
                >
                  <CartesianGrid horizontal={false} />
                  <XAxis type="number" tickLine={false} axisLine={false} hide />
                  {/* THE ZERO REFERENCE, and the only one this chart can have:
                      its numeric axis is `hide`, and a category whose refunds
                      outweigh its spending nets negative and draws LEFTWARD
                      (recharts 3 extends the default [0, 'auto'] domain to
                      include negative data). Without the line the bar simply
                      starts somewhere else with nothing to start from.

                      The `LabelList` below stays `position="right"`, which for
                      a leftward bar puts the figure just right of this line
                      rather than at the bar's tip. Deliberate: each row holds
                      one bar, so that space is empty, and the alternative —
                      flipping the label to the outer tip — puts it against the
                      100px category axis with no left margin to clear, where
                      the longest negative value clips. The label carries its
                      own minus, and this line is what it is measured from. */}
                  <ReferenceLine x={0} stroke="hsl(var(--border))" />
                  <YAxis
                    dataKey="name"
                    type="category"
                    tickLine={false}
                    axisLine={false}
                    width={100}
                    className="text-xs"
                    // Every category gets its label. Without this the axis is on
                    // recharts' default collision strategy over an UNCAPPED list,
                    // so it silently drops names — and the height above is what
                    // makes rendering them all safe.
                    interval={0}
                    // ...but only once each label is ONE line: recharts wraps a
                    // category label that overruns `width`, and the extra lines
                    // grow into the next row. See `shortenCategoryLabel`.
                    tickFormatter={shortenCategoryLabel}
                  />
                  <Bar dataKey="total" radius={4}>
                    <LabelList
                      dataKey="total"
                      position="right"
                      className="fill-muted-foreground text-xs"
                      // recharts 3 types LabelList's formatter as
                      // `LabelFormatter`, whose argument is `RenderableText`
                      // (string | number | boolean | null | undefined), so the
                      // old `(value: number)` annotation no longer applies.
                      // Only numbers are currency here; anything else renders
                      // empty rather than being coerced into a bogus amount.
                      formatter={(value) =>
                        typeof value === 'number' ? fmt(value) : ''
                      }
                    />
                    {breakdownSorted.map((entry) => (
                      <Cell
                        key={entry.id}
                        fill={`var(--color-cat-${entry.id})`}
                      />
                    ))}
                  </Bar>
                </BarChart>
              </ChartContainer>
            )}
          {!catBreakdown.loading &&
            !catBreakdown.error &&
            catBreakdown.data.length === 0 && (
              <p className="text-sm text-muted-foreground">
                No expense data for this period
              </p>
            )}
        </CardContent>
      </Card>

      {/* Category Trends */}
      <Card
        aria-labelledby="category-trends-heading"
        className="flex min-w-0 flex-col"
      >
        <CardHeader className="pb-4">
          <CardTitle
            id="category-trends-heading"
            className="text-base font-semibold"
          >
            Category Trends
          </CardTitle>
        </CardHeader>
        <CardContent className="flex flex-1 flex-col">
          {catTrends.loading && <Skeleton className="min-h-[300px] w-full flex-1" />}
          {catTrends.error && (
            <Alert variant="destructive">
              <AlertCircle className="h-4 w-4" />
              <AlertDescription>{catTrends.error}</AlertDescription>
            </Alert>
          )}
          {!catTrends.loading && !catTrends.error && (
            <ChartContainer
              config={catTrendConfig}
              className={cn(
                'aspect-auto min-h-[300px] w-full flex-1 transition-opacity duration-200',
                catTrends.fetching && !catTrends.loading && 'opacity-60',
              )}
            >
              <LineChart accessibilityLayer data={catTrendData}>
                <CartesianGrid vertical={false} />
                <XAxis
                  dataKey="date"
                  tickLine={false}
                  axisLine={false}
                  tickMargin={8}
                  // Was a hardcoded `0`, which forces every bucket regardless of
                  // whether they fit. Same rotated-axis rule as the two Overview
                  // charts — see the long note on Income vs Expenses. The dev
                  // ledger only reached back six months when this was written,
                  // so the collision was invisible locally and would have
                  // arrived with the twelfth month of data.
                  interval={monthAxisInterval(catTrendData.length)}
                  angle={MONTH_TICK_ANGLE}
                  textAnchor="end"
                  height={48}
                  tick={MONTH_TICK}
                  // Was `{ left: 20, right: 20 }`, which is not enough for a
                  // rotated month label: `Mar'26` still started at -9.8px, at
                  // 390 AND at 1440. The shared constant is sized from the
                  // measured overhang instead of guessed.
                  padding={ROTATED_MONTH_TICK_PADDING}
                  tickFormatter={formatMonthTick}
                />
                <ChartTooltip
                  content={
                    <ChartTooltipContent labelFormatter={formatMonthLabel} />
                  }
                />
                {/*
                  recharts 3 added `itemSorter` to Legend, defaulting to
                  'value' and applied in its own selector, so it reaches CUSTOM
                  legend content too; recharts 2 never sorted at all. Here the
                  series are declared in descending-spend order (see
                  `expenseCategories`, sorted above), and the dataKeys are
                  `cat-<id>` — so the default sorter would re-order the chips
                  lexicographically by id string (cat-11 before cat-2 before
                  cat-4), destroying the spend ranking. `null` restores the v2
                  behaviour: recharts' docs state a null sorter leaves the
                  payload unsorted.
                */}
                <ChartLegend
                  content={<ChartLegendContent />}
                  itemSorter={null}
                />
                {expenseCategories.map((cat) => (
                  <Line
                    key={cat.id}
                    type="monotone"
                    dataKey={`cat-${cat.id}`}
                    stroke={getCategoryColorVar({ id: cat.id })}
                    strokeWidth={2}
                    dot={false}
                  />
                ))}
              </LineChart>
            </ChartContainer>
          )}
        </CardContent>
      </Card>

      {/* Top Merchants */}
      <Card aria-labelledby="top-merchants-heading" className="min-w-0">
        <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-4">
          <CardTitle
            id="top-merchants-heading"
            className="text-base font-semibold"
          >
            Top Merchants
          </CardTitle>
        </CardHeader>
        <CardContent>
          {merchants.loading && <Skeleton className="h-[200px] w-full" />}
          {merchants.error && (
            <Alert variant="destructive">
              <AlertCircle className="h-4 w-4" />
              <AlertDescription>{merchants.error}</AlertDescription>
            </Alert>
          )}
          {!merchants.loading &&
            !merchants.error &&
            merchants.data.length === 0 && (
              <p className="text-sm text-muted-foreground">
                No transactions for this period
              </p>
            )}
          {!merchants.loading &&
            !merchants.error &&
            merchants.data.length > 0 && (
              <div
                className={cn(
                  'transition-opacity duration-200',
                  merchants.fetching && !merchants.loading && 'opacity-60',
                )}
              >
                <Table
                    // Phone-width column density. The shared `TableCell` is `p-4` and
                    // `TableHead` `px-4`, so four columns spend 128px on
                    // padding alone — measured, this table wanted 318.5px inside
                    // a 293px card and the amount column was clipped. Halving the
                    // HORIZONTAL padding below `sm` gives 64px back, which is more
                    // than the overflow; vertical padding is untouched, so rows
                    // keep their tap height.
                    //
                    // Scoped to this table rather than to `ui/table.tsx`: the
                    // shared component is used by the ledger, Trash, the import
                    // preview and Settings, and none of those asked for it. A
                    // descendant selector rather than a class on every cell so a
                    // new column cannot be added without it.
                    className={PHONE_TABLE_DENSITY}
                >
                  <TableHeader>
                    <TableRow>
                      <TableHead className="w-10">#</TableHead>
                      <TableHead>Description</TableHead>
                      <TableHead className="text-right">Count</TableHead>
                      <TableHead className="text-right">Amount</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {merchants.data.map((m, i) => (
                      <TableRow key={m.description}>
                        <TableCell className="font-mono tabular-nums text-muted-foreground">
                          {i + 1}
                        </TableCell>
                        {/* BOUNDED IN BOTH DIRECTIONS, and each class is doing
                            a different job — none of them is decoration.

                            WIDTH: `overflow-wrap:anywhere`. Tailwind's
                            `break-words` (`overflow-wrap:break-word`) breaks a
                            long token for PAINTING but leaves the element's
                            min-content contribution at the token's full width,
                            so a table cell keeps sizing itself to it;
                            `anywhere` counts the break when computing
                            min-content, which is what actually collapses the
                            column. Measured before: one row with an unbroken
                            token drove this table to 3464px inside a 293px
                            card, widest cell 3269px. After: 310px, widest cell
                            107px. `max-w-*` then keeps an ordinary long
                            description from eating the amount column on a wide
                            screen.

                            HEIGHT: `line-clamp-3`, and it is not cosmetic.
                            Wrapping alone trades a horizontal overflow for a
                            vertical one — measured, a 500-character token
                            wrapped into a 932px-tall row at 390px. 500 is the
                            per-row edit ceiling, but IMPORT does not enforce
                            it (the body cap is 256 KiB), so the unclamped
                            height is bounded by nothing a user cannot reach
                            with a spreadsheet.

                            Three lines rather than `truncate`'s one because
                            this surface has no way back to the full string on
                            the primary platform: no row expansion, and `title`
                            is dead on touch. Three lines is ~27 characters at
                            phone width and ~150 at desktop, against ~14 for a
                            single truncated line. `title` is still set, for
                            the pointer case where it does work. */}
                        <TableCell className={cn(TABLE_TEXT_CELL_WIDTH, 'font-medium')}>
                          <div
                            className={CLAMPED_TABLE_TEXT}
                            title={m.description}
                          >
                            {m.description}
                          </div>
                        </TableCell>
                        <TableCell className="text-right text-muted-foreground">
                          {m.tx_count}
                        </TableCell>
                        <TableCell className="text-right font-mono tabular-nums">
                          {fmt(m.total)}
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </div>
            )}
        </CardContent>
      </Card>

      {/* Expense Velocity */}
      <Card
        aria-labelledby="expense-velocity-heading"
        className="flex min-w-0 flex-col"
      >
        <CardHeader className="pb-4">
          <CardTitle
            id="expense-velocity-heading"
            className="text-base font-semibold"
          >
            Expense Velocity
          </CardTitle>
          <CardDescription className="text-xs">Current vs previous month cumulative spending</CardDescription>
        </CardHeader>
        <CardContent className="flex flex-1 flex-col">
          {velocity.loading && <Skeleton className="min-h-[250px] w-full flex-1" />}
          {velocity.error && (
            <Alert variant="destructive">
              <AlertCircle className="h-4 w-4" />
              <AlertDescription>{velocity.error}</AlertDescription>
            </Alert>
          )}
          {velocity.data && (
            <ChartContainer
              config={VELOCITY_CONFIG}
              className={cn(
                'aspect-auto min-h-[250px] w-full flex-1 transition-opacity duration-200',
                velocity.fetching && !velocity.loading && 'opacity-60',
              )}
            >
              <LineChart accessibilityLayer data={velocityData}>
                <CartesianGrid vertical={false} />
                <XAxis
                  dataKey="day"
                  type="number"
                  domain={[1, 'dataMax']}
                  ticks={[1, 5, 10, 15, 20, 25, 30]}
                  tickLine={false}
                  axisLine={false}
                  tickMargin={10}
                />
                <ChartTooltip content={<ChartTooltipContent />} />
                {/*
                  See the note on the Category Trends legend above. This is the
                  chart where the regression was observed live: declared
                  current / previous / pace rendered as "Current Month, Budget
                  Pace, Previous Month" under recharts 3's default sorter.
                */}
                <ChartLegend
                  content={<ChartLegendContent />}
                  itemSorter={null}
                />
                <Line
                  type="monotone"
                  dataKey="current"
                  stroke="var(--color-current)"
                  strokeWidth={2}
                  dot={false}
                />
                <Line
                  type="monotone"
                  dataKey="previous"
                  stroke="var(--color-previous)"
                  strokeWidth={2}
                  strokeDasharray="2 2"
                  dot={false}
                />
                <Line
                  type="monotone"
                  dataKey="pace"
                  stroke="var(--color-pace)"
                  strokeWidth={1}
                  strokeDasharray="5 5"
                  dot={false}
                  connectNulls={false}
                />
              </LineChart>
            </ChartContainer>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
