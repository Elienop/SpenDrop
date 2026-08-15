import { useId, useMemo, useState } from 'react';
import {
  BarChart,
  Bar,
  AreaChart,
  Area,
  XAxis,
  YAxis,
  CartesianGrid,
  ReferenceLine,
} from 'recharts';
import { formatCurrency } from '@/lib/format';
import { useBaseCurrency } from '@/hooks/useBaseCurrency';
import { useReportYears } from '@/hooks/useReportYears';
import {
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
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
import { Skeleton } from '@/components/ui/skeleton';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { AlertCircle } from 'lucide-react';
import { useIncomeExpenses, useBudgetVsActual } from '@/hooks/useReports';
import {
  MONTH_NAMES_SHORT,
  yearOptionsFrom,
  formatMonthTick,
  formatMonthLabel,
} from '@/lib/dates';
import {
  INCEXP_CONFIG,
  monthsToCoverYear,
  monthAxisInterval,
  MONTH_TICK,
  MONTH_TICK_ANGLE,
  ROTATED_MONTH_TICK_PADDING,
  ROTATED_MONTH_TICK_PADDING_INSIDE_YAXIS,
} from './utils';
import { cn } from '@/lib/utils';

const NET_FLOW_CONFIG = {
  cumulative: { label: 'Net Cash Flow', color: 'hsl(var(--primary))' },
} satisfies ChartConfig;

const BVA_CONFIG = {
  budget: { label: 'Budget', color: 'hsl(var(--primary) / 0.35)' },
  actual: { label: 'Actual', color: 'hsl(var(--primary))' },
} satisfies ChartConfig;

/**
 * The Time Period control's value. `'all'` is a DISCRIMINANT, not a month
 * count, and storing it that way is load-bearing — see `months` below.
 */
type Period = '6' | '12' | '24' | 'all';

export function OverviewTab() {
  const [period, setPeriod] = useState<Period>('12');
  const [bvaYear, setBvaYear] = useState(new Date().getFullYear());

  // Exactly the years the ledger holds, so an imported 1984 statement is
  // selectable and the empty years between are not offered. `bvaYear` is
  // unioned in so a refetch that drops it (its last row was just deleted)
  // cannot leave the Select holding a value with no matching item — that
  // renders a blank trigger, silently, with no error.
  const { years: ledgerYears, currentYear } = useReportYears();
  const years = yearOptionsFrom(ledgerYears, bvaYear);

  // WHY THE SELECT STORES `period` AND NOT THIS NUMBER. `months` changes
  // identity when the years response lands: on first paint `ledgerYears` is
  // the current-year fallback, so 'all' resolves to 24, and moments later the
  // real list makes it 516. A <Select value={String(months)}> would then hold
  // "24" with no matching <SelectItem> and render a blank trigger, silently.
  // The discriminant is stable across that refetch; the derived count is not.
  //
  // `monthsToCoverYear` is reused rather than open-coded because it floors at
  // 24 and caps at MAX_REPORT_MONTHS, so no clock skew or empty ledger can
  // produce 0, a negative, or NaN here — `?months=NaN` fails the backend's
  // strconv.Atoi and 400s the request.
  //
  // All time is bounded by the LEDGER, not by the cap: `years` is filtered to
  // the data window and held at the current year, so the widest real request
  // is ~516 buckets for a 1984 household, never the 2412-month worst case.
  const months =
    period === 'all'
      ? monthsToCoverYear(Math.min(...ledgerYears), currentYear)
      : Number(period);

  const incExp = useIncomeExpenses(months);
  const bva = useBudgetVsActual(bvaYear);
  const gradientId = useId();
  const baseCurrency = useBaseCurrency();
  const fmt = (amount: number) => formatCurrency(amount, baseCurrency);

  // Store an ISO-8601 `"YYYY-MM-01"` date as the XAxis dataKey — matches
  // shadcn's `chart-bar-interactive` convention so `new Date(value)` parses
  // reliably in the tick/tooltip formatters. Using the first-of-month keeps
  // 24-month ranges from collapsing two same-month buckets into one
  // category (which would happen with bare `"Jan"` labels).
  const incExpData = useMemo(
    () =>
      incExp.data.map((entry) => ({
        date: `${entry.year}-${String(entry.month).padStart(2, '0')}-01`,
        income: entry.income,
        expenses: entry.expenses,
      })),
    [incExp.data],
  );

  // A running cumulative, built with an explicit loop rather than `.reduce()`.
  // The reduce here was a loop in disguise: its accumulator carried the running
  // total but its RETURN VALUE was thrown away, the array being built by a
  // `push` into an outer `const` (Sonar typescript:S2201). Same elements, same
  // order, same arithmetic — `running += entry.net` is the identical addition
  // in the identical sequence, so the plotted series is unchanged.
  const cashFlowData = useMemo(() => {
    const result: { date: string; cumulative: number }[] = [];
    let running = 0;
    for (const entry of incExp.data) {
      running += entry.net;
      result.push({
        date: `${entry.year}-${String(entry.month).padStart(2, '0')}-01`,
        cumulative: running,
      });
    }
    return result;
  }, [incExp.data]);

  const bvaData = useMemo(
    () =>
      bva.data.map((e) => ({
        name: MONTH_NAMES_SHORT[e.month - 1],
        budget: e.budget,
        actual: e.actual,
      })),
    [bva.data],
  );

  return (
    // Every card in this grid carries `min-w-0`. A grid item defaults to
    // min-width:auto, so the width of the Recharts SVG inside propagates up as
    // the track's min-content and widens the page — measured live at a 390px
    // viewport: this tab rendered 599px, Spending 3530px. It also latches,
    // because ResponsiveContainer then measures the widened track and grows to
    // match. A `min-width:0` on the chart itself does NOT break the loop; it
    // has to sit on the grid item.
    <div className="grid gap-6 md:grid-cols-2">
      {/* Income vs Expenses */}
      <Card aria-labelledby="income-expenses-heading" className="min-w-0">
        <CardHeader className="flex flex-row flex-wrap items-center justify-between gap-3 space-y-0 pb-4">
          <div className="flex flex-col gap-0.5">
            <CardTitle
              id="income-expenses-heading"
              className="text-base font-semibold"
            >
              Income vs Expenses
            </CardTitle>
            <CardDescription className="text-xs">Monthly income and spending comparison</CardDescription>
          </div>
          <Select
            value={period}
            onValueChange={(v) => setPeriod(v as Period)}
          >
            <SelectTrigger aria-label="Time Period" className="h-9 w-[140px]">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="6">6 months</SelectItem>
              <SelectItem value="12">12 months</SelectItem>
              <SelectItem value="24">24 months</SelectItem>
              {/* Shown unconditionally. These charts are driven by the months
                  control, not by the year picker, so All time is the ONLY
                  control that can reach an old year here — hiding it until
                  the ledger looks old enough would make the fix invisible in
                  precisely the case it exists for. */}
              <SelectItem value="all">All time</SelectItem>
            </SelectContent>
          </Select>
        </CardHeader>
        <CardContent>
          {incExp.loading && <Skeleton className="h-[300px] w-full" />}
          {incExp.error && (
            <Alert variant="destructive">
              <AlertCircle className="h-4 w-4" />
              <AlertDescription>{incExp.error}</AlertDescription>
            </Alert>
          )}
          {!incExp.loading && !incExp.error && (
            <ChartContainer
              config={INCEXP_CONFIG}
              className={cn(
                'h-[300px] w-full transition-opacity duration-200',
                incExp.fetching && !incExp.loading && 'opacity-60',
              )}
            >
              <BarChart accessibilityLayer data={incExpData}>
                <defs>
                  <linearGradient
                    id={`${gradientId}-income`}
                    x1="0"
                    y1="0"
                    x2="0"
                    y2="1"
                  >
                    <stop
                      offset="5%"
                      stopColor="var(--color-income)"
                      stopOpacity={0.8}
                    />
                    <stop
                      offset="95%"
                      stopColor="var(--color-income)"
                      stopOpacity={0.1}
                    />
                  </linearGradient>
                  <linearGradient
                    id={`${gradientId}-expenses`}
                    x1="0"
                    y1="0"
                    x2="0"
                    y2="1"
                  >
                    <stop
                      offset="5%"
                      stopColor="var(--color-expenses)"
                      stopOpacity={0.8}
                    />
                    <stop
                      offset="95%"
                      stopColor="var(--color-expenses)"
                      stopOpacity={0.1}
                    />
                  </linearGradient>
                </defs>
                <CartesianGrid vertical={false} />
                {/* One of four month axes that share a rule. The rule, the
                    geometry and every measured number behind them live on
                    `MONTH_TICK_ANGLE`, `MONTH_TICK`, the two padding constants
                    and `monthAxisInterval` in `reports/utils.ts` — read them
                    there, and do not restate them here. That is not tidiness:
                    this comment used to carry its own copy, the design went
                    through a second attempt, and the copy kept asserting the
                    REJECTED mechanism (`equidistantPreserveStart`, -30°, 12px)
                    long after the code shipped the accepted one. Four labels
                    quoting one rule is four chances to describe a design that
                    no longer exists.

                    What is local to THIS chart: it is a band scale (Bar) with
                    no `<YAxis>`, so it takes the standalone padding constant,
                    and it is the loosest of the four — 18.1px of tick spacing
                    at 360px against the 11.7px twelve rotated 9px labels need.
                    Net Cash Flow below is the tight one. */}
                <XAxis
                  dataKey="date"
                  tickLine={false}
                  axisLine={false}
                  tickMargin={8}
                  interval={monthAxisInterval(incExpData.length)}
                  angle={MONTH_TICK_ANGLE}
                  textAnchor="end"
                  height={48}
                  tick={MONTH_TICK}
                  padding={ROTATED_MONTH_TICK_PADDING}
                  tickFormatter={formatMonthTick}
                />
                <ChartTooltip
                  content={<ChartTooltipContent labelFormatter={formatMonthLabel} />}
                />
                {/* Zero reference. Either series can go negative once amounts
                    are signed — a month of refunds against a small expense
                    total, or an income reversal — and recharts 3 EXTENDS its
                    default [0, 'auto'] domain to include negative data rather
                    than clipping it, so the bar draws below the axis. This
                    chart has no YAxis, so without the line nothing on screen
                    marks where zero was. Net Cash Flow below and SavingsTab's
                    YoY chart are the same pattern. */}
                <ReferenceLine y={0} stroke="hsl(var(--border))" />
                {/* Symmetric radius: [4, 4, 0, 0] rounds the visual top only,
                    leaving a downward bar glued to the axis. */}
                <Bar
                  dataKey="income"
                  fill={`url(#${gradientId}-income)`}
                  stroke="var(--color-income)"
                  strokeOpacity={0.3}
                  radius={4}
                />
                <Bar
                  dataKey="expenses"
                  fill={`url(#${gradientId}-expenses)`}
                  stroke="var(--color-expenses)"
                  strokeOpacity={0.3}
                  radius={4}
                />
              </BarChart>
            </ChartContainer>
          )}
        </CardContent>
      </Card>

      {/* Net Cash Flow */}
      <Card aria-labelledby="net-cash-flow-heading" className="min-w-0">
        <CardHeader className="pb-4">
          <CardTitle
            id="net-cash-flow-heading"
            className="text-base font-semibold"
          >
            Net Cash Flow
          </CardTitle>
          <CardDescription className="text-xs">Cumulative net balance over time</CardDescription>
        </CardHeader>
        <CardContent>
          {incExp.loading && <Skeleton className="h-[300px] w-full" />}
          {incExp.error && (
            <Alert variant="destructive">
              <AlertCircle className="h-4 w-4" />
              <AlertDescription>{incExp.error}</AlertDescription>
            </Alert>
          )}
          {!incExp.loading && !incExp.error && (
            <ChartContainer
              config={NET_FLOW_CONFIG}
              className={cn(
                'h-[300px] w-full transition-opacity duration-200',
                incExp.fetching && !incExp.loading && 'opacity-60',
              )}
            >
              <AreaChart accessibilityLayer data={cashFlowData}>
                <defs>
                  <linearGradient
                    id={`${gradientId}-cashflow`}
                    x1="0"
                    y1="0"
                    x2="0"
                    y2="1"
                  >
                    <stop
                      offset="5%"
                      stopColor="var(--color-cumulative)"
                      stopOpacity={0.8}
                    />
                    <stop
                      offset="95%"
                      stopColor="var(--color-cumulative)"
                      stopOpacity={0.05}
                    />
                  </linearGradient>
                </defs>
                <CartesianGrid vertical={false} />
                {/* Same rotated-axis rule as Income vs Expenses above; the
                    numbers live on the constants in `reports/utils.ts`.

                    Local to THIS chart: a POINT scale (Area) that also renders
                    an 80px currency `<YAxis>`, so it takes the inside-YAxis
                    padding constant — left 0, because the YAxis already IS the
                    left gutter. It is the chart the end-anchored strategy
                    collapsed to a single label on, and `monthAxisInterval`
                    documents why: a point scale's last tick sits ON the plot's
                    right bound, so the strategy only survives if the RIGHT
                    padding covers half recharts' modelled footprint. That is
                    the 16 in both padding constants — shrinking it is what
                    breaks this axis, not the strategy.

                    `preserveStartEnd` remains wrong here for the reason it
                    always was: it pins both ends and thins between them, so on
                    a point scale the last tick's neighbour loses the minTickGap
                    contest and vanishes (measured: 11 of 12, May'26 -> Jul'26). */}
                <XAxis
                  dataKey="date"
                  tickLine={false}
                  axisLine={false}
                  tickMargin={8}
                  interval={monthAxisInterval(cashFlowData.length)}
                  angle={MONTH_TICK_ANGLE}
                  textAnchor="end"
                  height={48}
                  tick={MONTH_TICK}
                  padding={ROTATED_MONTH_TICK_PADDING_INSIDE_YAXIS}
                  tickFormatter={formatMonthTick}
                />
                <YAxis
                  tickLine={false}
                  axisLine={false}
                  tickMargin={8}
                  width={80}
                  tickFormatter={(v: number) => fmt(v)}
                />
                <ReferenceLine y={0} stroke="hsl(var(--border))" />
                <ChartTooltip content={<ChartTooltipContent labelFormatter={formatMonthLabel} />} />
                <Area
                  type="monotone"
                  dataKey="cumulative"
                  fill={`url(#${gradientId}-cashflow)`}
                  stroke="var(--color-cumulative)"
                  strokeWidth={2}
                />
              </AreaChart>
            </ChartContainer>
          )}
        </CardContent>
      </Card>

      {/* Budget vs Actual — full width */}
      <Card
        aria-labelledby="budget-vs-actual-heading"
        className="min-w-0 md:col-span-2"
      >
        <CardHeader className="flex flex-row flex-wrap items-center justify-between gap-3 space-y-0 pb-4">
          <div className="flex flex-col gap-0.5">
            <CardTitle
              id="budget-vs-actual-heading"
              className="text-base font-semibold"
            >
              Budget vs Actual
            </CardTitle>
            <CardDescription className="text-xs">Monthly budget targets vs actual spending</CardDescription>
          </div>
          <Select
            value={String(bvaYear)}
            onValueChange={(v) => setBvaYear(Number(v))}
          >
            <SelectTrigger aria-label="Budget Year" className="h-9 w-[120px]">
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
        </CardHeader>
        <CardContent>
          {bva.loading && <Skeleton className="h-[300px] w-full" />}
          {bva.error && (
            <Alert variant="destructive">
              <AlertCircle className="h-4 w-4" />
              <AlertDescription>{bva.error}</AlertDescription>
            </Alert>
          )}
          {!bva.loading && !bva.error && bva.data.length > 0 && (
            <ChartContainer
              config={BVA_CONFIG}
              className={cn(
                'h-[300px] w-full transition-opacity duration-200',
                bva.fetching && !bva.loading && 'opacity-60',
              )}
            >
              <BarChart accessibilityLayer data={bvaData}>
                <defs>
                  <linearGradient
                    id={`${gradientId}-budget`}
                    x1="0"
                    y1="0"
                    x2="0"
                    y2="1"
                  >
                    <stop
                      offset="5%"
                      stopColor="var(--color-budget)"
                      stopOpacity={0.8}
                    />
                    <stop
                      offset="95%"
                      stopColor="var(--color-budget)"
                      stopOpacity={0.1}
                    />
                  </linearGradient>
                  <linearGradient
                    id={`${gradientId}-actual`}
                    x1="0"
                    y1="0"
                    x2="0"
                    y2="1"
                  >
                    <stop
                      offset="5%"
                      stopColor="var(--color-actual)"
                      stopOpacity={0.8}
                    />
                    <stop
                      offset="95%"
                      stopColor="var(--color-actual)"
                      stopOpacity={0.1}
                    />
                  </linearGradient>
                </defs>
                <CartesianGrid vertical={false} />
                {/* Without an explicit `interval` recharts falls back to
                    `preserveEnd`, which drops whichever labels its collision
                    walk happens to reach — and the result is not evenly
                    spaced. Measured on this chart, same data, three widths:
                    1440 → all 12; 420 → NINE, `Jan Feb Mar Apr Jun Jul Sep Oct
                    Dec` (May, Aug and Nov gone); 390 → six, `Feb Apr Jun Aug
                    Oct Dec`. The 420 reading is the damaging one: the cadence
                    changes mid-axis, so counting bars off a label lands on the
                    wrong month with nothing on screen saying so.

                    `monthAxisInterval` answers it. At twelve buckets it returns
                    `0` — render EVERY month — and beyond that a stride-picking
                    strategy rather than one that drops individuals. Its
                    docblock carries the measurements; this one deliberately
                    does not repeat them.

                    DERIVED HERE, not copied from the rotated charts above, and
                    that is the part worth keeping: this axis is FLAT, so its
                    labels clear on horizontal width rather than perpendicular
                    spacing, and it needs 17.4px where a rotated 9px label needs
                    11.7px. It measures 21.1px at 360px, so twelve fit with
                    +3.7px in hand. Three twelve-month axes on this page, three
                    different plots, three separate derivations that happen to
                    land on the same answer — anyone unifying them has to
                    re-derive all three. */}
                <XAxis
                  dataKey="name"
                  tickLine={false}
                  axisLine={false}
                  tickMargin={10}
                  tick={MONTH_TICK}
                  interval={monthAxisInterval(bvaData.length)}
                />
                <ChartTooltip content={<ChartTooltipContent />} />
                {/* Same zero reference as Income vs Expenses. It is the
                    ACTUAL series that goes negative here: a month whose
                    refunds outweighed its spending in a budgeted category
                    draws a downward bar against a positive budget bar beside
                    it, which is unreadable without a zero line. */}
                <ReferenceLine y={0} stroke="hsl(var(--border))" />
                <Bar
                  dataKey="budget"
                  fill={`url(#${gradientId}-budget)`}
                  stroke="var(--color-budget)"
                  strokeOpacity={0.3}
                  radius={4}
                />
                <Bar
                  dataKey="actual"
                  fill={`url(#${gradientId}-actual)`}
                  stroke="var(--color-actual)"
                  strokeOpacity={0.3}
                  radius={4}
                />
              </BarChart>
            </ChartContainer>
          )}
          {!bva.loading &&
            !bva.error &&
            bva.data.length === 0 && (
              <p className="text-sm text-muted-foreground">
                No budget data for this year
              </p>
            )}
        </CardContent>
      </Card>
    </div>
  );
}
