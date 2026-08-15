import { useId, useMemo, useState, useEffect } from 'react';
import {
  RadialBarChart,
  RadialBar,
  AreaChart,
  Area,
  BarChart,
  Bar,
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
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
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
import { useYearOverYear, useIncomeExpenses } from '@/hooks/useReports';
import { MONTH_NAMES_SHORT, yearOptionsFrom } from '@/lib/dates';
import { formatCurrency } from '@/lib/format';
import { useBaseCurrency } from '@/hooks/useBaseCurrency';
import { useReportYears } from '@/hooks/useReportYears';
import { cn } from '@/lib/utils';
import {
  monthsToCoverYear,
  monthAxisInterval,
  MONTH_TICK,
  MONTH_TICK_ANGLE,
  ROTATED_MONTH_TICK_PADDING_INSIDE_YAXIS,
} from './utils';
import type { SavingsGoal, YoYResponse } from '@/api/types';
import { api } from '@/api/client';

const SAVINGS_CONFIG = {
  savings: { label: 'Cumulative Savings', color: 'hsl(var(--primary))' },
} satisfies ChartConfig;

/** Extract YoY chart data outside the component to avoid React Compiler .current ref issue.
 *  Plots monthly *net* savings (income - expenses) so deficit months render
 *  as negative bars beneath the zero line — the whole point of the Savings
 *  tab is showing whether you saved or overspent, not raw outflow. */
function buildYoYData(data: YoYResponse | null) {
  if (!data) return [];
  const currentYear = data.current;
  const previousYear = data.previous;
  return currentYear.map((cur, i) => {
    const prev = previousYear[i];
    return {
      name: MONTH_NAMES_SHORT[i],
      currentNet: cur.income - cur.expenses,
      previousNet: prev ? prev.income - prev.expenses : 0,
    };
  });
}

export function SavingsTab() {
  const [year, setYear] = useState(new Date().getFullYear());
  // The window must reach back to January of the SELECTED year, not a fixed
  // 24 months. The year Select now reaches back to the ledger-derived floor
  // (it was a hard-coded 2024 when this was written), so a hardcoded 24
  // silently truncated any year more than two back: in mid-2026
  // the window began in August 2024, and picking 2024 rendered five months as
  // though they were the whole year — the cumulative savings curve started
  // mid-year and the goal progress was computed against a partial total.
  // monthsToCoverYear clamps at MAX_REPORT_MONTHS (./utils.ts), which is kept
  // in step with the backend's MaxTrendMonths — see the comment there for why
  // that cap must stay out of reach of the year Select.
  const months = useMemo(
    () => monthsToCoverYear(year, new Date().getFullYear()),
    [year],
  );
  const incExp = useIncomeExpenses(months);
  const yoy = useYearOverYear(year);
  const gradientId = useId();

  const baseCurrency = useBaseCurrency();
  const fmt = (amount: number) => formatCurrency(amount, baseCurrency);

  const [goals, setGoals] = useState<SavingsGoal[]>([]);
  const [goalsLoading, setGoalsLoading] = useState(true);
  const [goalsError, setGoalsError] = useState('');
  useEffect(() => {
    api
      .get<SavingsGoal[]>('savings-goals')
      .then(setGoals)
      .catch((err) =>
        setGoalsError(
          err instanceof Error ? err.message : 'Failed to load savings goals',
        ),
      )
      .finally(() => setGoalsLoading(false));
  }, []);

  const goal = goals.find((g) => g.year === year);

  const savingsData = useMemo(() => {
    // Cumulative running total of monthly net (income - expenses). We no
    // longer clamp to zero: a losing month must pull the curve down, so
    // the year-end figure reflects reality (e.g. Excel shows $17,216 when
    // two earlier months were in the red — clamping would have inflated
    // the total to $25,528 by silently dropping the losses).
    //
    // Built with an explicit loop rather than `.reduce()`: the accumulator was
    // only ever the running total, the reduce's RETURN VALUE was discarded and
    // the array came from a `push` into an outer `const` (Sonar
    // typescript:S2201). `running += e.net` is the identical addition in the
    // identical order, so the curve — negative months included — is unchanged.
    const result: { name: string; savings: number }[] = [];
    let running = 0;
    for (const e of incExp.data.filter((e) => e.year === year)) {
      running += e.net;
      result.push({
        name: MONTH_NAMES_SHORT[e.month - 1],
        savings: running,
      });
    }
    return result;
  }, [incExp.data, year]);

  const currentSavings = savingsData.at(-1)?.savings ?? 0;
  // Progress is shown two ways: the label text uses the raw signed value
  // (so an overspent year correctly reads "-12%"), while the radial arc
  // clamps to [0, 100] — the ring geometry has no meaningful way to
  // render negative or >100% arcs. Guard against `target_amount <= 0`:
  // the backend only rejects negative targets, so a soft-deleted goal can
  // persist with `target_amount === 0`, which would divide to Infinity.
  const rawProgress =
    goal && goal.target_amount > 0
      ? Math.round((currentSavings / goal.target_amount) * 100)
      : 0;
  const ringProgress = Math.max(0, Math.min(100, rawProgress));

  const radialData = [{ progress: ringProgress, fill: 'hsl(var(--primary))' }];

  // YoY chart data
  const yoyData = useMemo(() => buildYoYData(yoy.data), [yoy.data]);

  const yoyConfig = useMemo<ChartConfig>(() => {
    if (!yoy.data) return {} as ChartConfig;
    return {
      currentNet: {
        label: `${yoy.data.current_year} net`,
        color: 'hsl(var(--primary))',
      },
      previousNet: {
        label: `${yoy.data.previous_year} net`,
        color: 'hsl(var(--primary) / 0.35)',
      },
    };
  }, [yoy.data]);

  // Exactly the years the ledger holds, so an imported 1984 statement is
  // selectable and the empty years between are not offered. Both Selects on
  // this tab drive the same `year`, which is unioned in so a refetch that
  // drops it cannot leave either Select holding a value with no matching item
  // — that renders a blank trigger, silently, with no error.
  const { years: ledgerYears } = useReportYears();
  const years = yearOptionsFrom(ledgerYears, year);

  return (
    <div className="flex flex-col gap-6">
      {/* Savings Goal Progress */}
      <Card aria-labelledby="savings-goal-heading">
        <CardHeader className="flex flex-row flex-wrap items-center justify-between gap-3 space-y-0 pb-4">
          <CardTitle
            id="savings-goal-heading"
            className="text-base font-semibold"
          >
            Savings Goal Progress
          </CardTitle>
          <Select
            value={String(year)}
            onValueChange={(v) => setYear(Number(v))}
          >
            <SelectTrigger aria-label="Savings Year" className="h-9 w-[120px]">
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
          {(incExp.loading || goalsLoading) && (
            <Skeleton className="h-[300px] w-full" />
          )}
          {(incExp.error || goalsError) && (
            <Alert variant="destructive">
              <AlertCircle className="h-4 w-4" />
              <AlertDescription>{incExp.error || goalsError}</AlertDescription>
            </Alert>
          )}
          {!incExp.loading && !goalsLoading && !incExp.error && !goalsError && (
            <div
              className={cn(
                'transition-opacity duration-200',
                incExp.fetching && !incExp.loading && 'opacity-60',
              )}
            >
              {goal ? (
                /* STACKED below `sm`, side by side above it. At 360px the ring
                   is a fixed 200px square, so the figures beside it were left
                   with ~90px and "Remaining: $17,151.23" wrapped onto three
                   cramped lines. The owner asked for the percentage on one line
                   and the goal/saved figures on another; a column does that and
                   gives the figures the full card width. `items-center` only
                   applies once they are side by side — a centred column would
                   leave the three figures floating away from the card's left
                   edge, out of line with every other block on the tab. */
                <div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:gap-6">
                  <ChartContainer
                    config={SAVINGS_CONFIG}
                    className="h-[200px] w-[200px]"
                  >
                    {/*
                      recharts 3 flipped `accessibilityLayer` to default TRUE.
                      Every other chart in the app opts in explicitly; this ring
                      never did, so the bump would silently have made it a
                      keyboard tab stop with `role="application"` and no
                      accessible name — a decorative single-value ring with no
                      data series to navigate. Opting out keeps the pre-bump
                      behaviour, so this dependency change alters no UX. The
                      figure it displays is already read from the text beside it.
                    */}
                    <RadialBarChart
                      accessibilityLayer={false}
                      innerRadius={70}
                      outerRadius={90}
                      data={radialData}
                      startAngle={90}
                      endAngle={90 - (ringProgress / 100) * 360}
                    >
                      <RadialBar
                        dataKey="progress"
                        background
                        cornerRadius={10}
                      />
                      <text
                        x="50%"
                        y="50%"
                        textAnchor="middle"
                        dominantBaseline="middle"
                      >
                        <tspan
                          x="50%"
                          dy="-8"
                          className="fill-foreground text-2xl font-semibold"
                        >
                          {rawProgress}%
                        </tspan>
                        <tspan
                          x="50%"
                          dy="20"
                          className="fill-muted-foreground text-xs"
                        >
                          {fmt(currentSavings)}
                        </tspan>
                      </text>
                    </RadialBarChart>
                  </ChartContainer>
                  <div className="text-sm text-muted-foreground">
                    <p>Goal: {fmt(goal.target_amount)}</p>
                    <p>Saved: {fmt(currentSavings)}</p>
                    <p>
                      Remaining:{' '}
                      {fmt(goal.target_amount - currentSavings)}
                    </p>
                  </div>
                </div>
              ) : (
                <p className="text-sm text-muted-foreground">
                  No savings goal set for {year}. Set one in Settings.
                </p>
              )}
              {/* Always render cumulative savings area chart */}
              <ChartContainer
                config={SAVINGS_CONFIG}
                className="mt-4 h-[200px] w-full"
              >
                <AreaChart accessibilityLayer data={savingsData}>
                  <defs>
                    <linearGradient
                      id={`${gradientId}-savings`}
                      x1="0"
                      y1="0"
                      x2="0"
                      y2="1"
                    >
                      <stop
                        offset="5%"
                        stopColor="var(--color-savings)"
                        stopOpacity={0.8}
                      />
                      <stop
                        offset="95%"
                        stopColor="var(--color-savings)"
                        stopOpacity={0.05}
                      />
                    </linearGradient>
                  </defs>
                  <CartesianGrid vertical={false} />
                  {/* The tightest of the three twelve-month axes in Reports:
                      an 80px YAxis leaves the narrowest plot on the page. It
                      still renders EVERY month, because rotating to -45° and
                      dropping the tick font to 9px cut what twelve labels need
                      from 23.7px of horizontal width to 11.7px of perpendicular
                      spacing — the geometry and the measurements are on
                      `MONTH_TICK_ANGLE` and `monthAxisInterval` in
                      `reports/utils.ts`, and are deliberately not restated here.

                      A partial year renders fewer than twelve — eight months in
                      August, twelve every December — so December is the case to
                      re-measure, not today's. */}
                  <XAxis
                    dataKey="name"
                    tickLine={false}
                    axisLine={false}
                    tickMargin={10}
                    tick={MONTH_TICK}
                    angle={MONTH_TICK_ANGLE}
                    textAnchor="end"
                    height={48}
                    interval={monthAxisInterval(savingsData.length)}
                    padding={ROTATED_MONTH_TICK_PADDING_INSIDE_YAXIS}
                  />
                  <YAxis
                    tickLine={false}
                    axisLine={false}
                    tickMargin={8}
                    width={80}
                    tickFormatter={(v: number) => fmt(v)}
                  />
                  <ReferenceLine y={0} stroke="hsl(var(--border))" />
                  <ChartTooltip content={<ChartTooltipContent />} />
                  <Area
                    type="monotone"
                    dataKey="savings"
                    fill={`url(#${gradientId}-savings)`}
                    stroke="var(--color-savings)"
                    strokeWidth={2}
                  />
                </AreaChart>
              </ChartContainer>
            </div>
          )}
        </CardContent>
      </Card>

      {/* Year-over-Year */}
      <Card aria-labelledby="yoy-heading">
        <CardHeader className="flex flex-row flex-wrap items-center justify-between gap-3 space-y-0 pb-4">
          <CardTitle id="yoy-heading" className="text-base font-semibold">
            Year-over-Year Net Savings
          </CardTitle>
          <Select
            value={String(year)}
            onValueChange={(v) => setYear(Number(v))}
          >
            <SelectTrigger
              aria-label="Year-over-Year Year"
              className="h-9 w-[180px]"
            >
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {years.map((y) => (
                <SelectItem key={y.value} value={y.value}>
                  {y.label} vs {Number(y.value) - 1}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </CardHeader>
        <CardContent>
          {yoy.loading && <Skeleton className="h-[300px] w-full" />}
          {yoy.error && (
            <Alert variant="destructive">
              <AlertCircle className="h-4 w-4" />
              <AlertDescription>{yoy.error}</AlertDescription>
            </Alert>
          )}
          {yoy.data && (
            <ChartContainer
              config={yoyConfig}
              className={cn(
                'h-[300px] w-full transition-opacity duration-200',
                yoy.fetching && !yoy.loading && 'opacity-60',
              )}
            >
              <BarChart accessibilityLayer data={yoyData}>
                <defs>
                  <linearGradient
                    id={`${gradientId}-yoyCurrent`}
                    x1="0"
                    y1="0"
                    x2="0"
                    y2="1"
                  >
                    <stop
                      offset="5%"
                      stopColor="var(--color-currentNet)"
                      stopOpacity={0.8}
                    />
                    <stop
                      offset="95%"
                      stopColor="var(--color-currentNet)"
                      stopOpacity={0.1}
                    />
                  </linearGradient>
                  <linearGradient
                    id={`${gradientId}-yoyPrevious`}
                    x1="0"
                    y1="0"
                    x2="0"
                    y2="1"
                  >
                    <stop
                      offset="5%"
                      stopColor="var(--color-previousNet)"
                      stopOpacity={0.8}
                    />
                    <stop
                      offset="95%"
                      stopColor="var(--color-previousNet)"
                      stopOpacity={0.1}
                    />
                  </linearGradient>
                </defs>
                <CartesianGrid vertical={false} />
                {/* Recharts' default `preserveEnd` walk drops whichever labels
                    its collision pass reaches, so the stride is not uniform and
                    changes with the container width. `monthAxisInterval` is
                    what replaces it; its docblock holds the numbers.

                    THIS is the chart to re-measure first if any of the shared
                    geometry moves. At 360px it has the least tick spacing on
                    the page — 13.1px against the 11.7px twelve rotated 9px
                    labels need, so +1.8px, where its siblings have +3.0 and
                    +3.7. An earlier revision THINNED this axis instead, on the
                    strength of the same twelve labels needing 23.7px of
                    horizontal width at the old flat 12px; steepening and
                    shrinking the font is what made forcing all twelve fit, and
                    keeping the months was the owner's actual complaint.

                    The three twelve-month axes agreeing is a COINCIDENCE OF THE
                    DEVICE FLEET, not one rule: each was decided against its own
                    plot width. A future reader unifying them has to re-derive
                    all three, not copy one. */}
                <XAxis
                  dataKey="name"
                  tickLine={false}
                  axisLine={false}
                  tickMargin={10}
                  tick={MONTH_TICK}
                  angle={MONTH_TICK_ANGLE}
                  textAnchor="end"
                  height={48}
                  interval={monthAxisInterval(yoyData.length)}
                  padding={ROTATED_MONTH_TICK_PADDING_INSIDE_YAXIS}
                />
                <YAxis
                  tickLine={false}
                  axisLine={false}
                  tickMargin={8}
                  width={80}
                  tickFormatter={(v: number) => fmt(v)}
                />
                <ReferenceLine y={0} stroke="hsl(var(--border))" />
                <ChartTooltip
                  content={<ChartTooltipContent hideIndicator />}
                />
                {/*
                  recharts 3's Legend sorts by `itemSorter` (default 'value')
                  in its own selector, reaching custom content; recharts 2 never
                  sorted. This legend's dataKeys (currentNet, previousNet)
                  happen to sort into their declared order, so it does not move
                  today — pinned anyway, because that is a coincidence and a
                  future rename would silently reverse the two years.
                */}
                <ChartLegend
                  content={<ChartLegendContent />}
                  itemSorter={null}
                />
                {/* Symmetric radius (not [4, 4, 0, 0]) so negative-net bars,
                    which grow downward from the zero line, still render with
                    rounded outer corners — the array form rounds only the
                    visual top and leaves negative bars glued to the axis. */}
                <Bar
                  dataKey="currentNet"
                  fill={`url(#${gradientId}-yoyCurrent)`}
                  stroke="var(--color-currentNet)"
                  strokeOpacity={0.3}
                  radius={4}
                />
                <Bar
                  dataKey="previousNet"
                  fill={`url(#${gradientId}-yoyPrevious)`}
                  stroke="var(--color-previousNet)"
                  strokeOpacity={0.3}
                  radius={4}
                />
              </BarChart>
            </ChartContainer>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
