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
  yearOptions,
  formatMonthTick,
  formatMonthLabel,
} from '@/lib/dates';
import { formatCurrency } from '@/lib/format';
import { useBaseCurrency } from '@/hooks/useBaseCurrency';
import { TYPE_EXPENSE } from '@/lib/transaction-types';
import { cn } from '@/lib/utils';
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

  const years = yearOptions();

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
    <div className="grid gap-6 md:grid-cols-2">
      {/* Category Breakdown (bar chart) */}
      <Card aria-labelledby="category-breakdown-heading">
        <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-4">
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
                  'h-[300px] w-full transition-opacity duration-200',
                  catBreakdown.fetching &&
                    !catBreakdown.loading &&
                    'opacity-60',
                )}
              >
                <BarChart
                  accessibilityLayer
                  data={breakdownSorted}
                  layout="vertical"
                  margin={{ left: 0, right: 80 }}
                >
                  <CartesianGrid horizontal={false} />
                  <XAxis type="number" tickLine={false} axisLine={false} hide />
                  <YAxis
                    dataKey="name"
                    type="category"
                    tickLine={false}
                    axisLine={false}
                    width={100}
                    className="text-xs"
                  />
                  <Bar dataKey="total" radius={4}>
                    <LabelList
                      dataKey="total"
                      position="right"
                      className="fill-muted-foreground text-xs"
                      formatter={(value: number) => fmt(value)}
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
      <Card aria-labelledby="category-trends-heading" className="flex flex-col">
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
                  interval={0}
                  angle={-30}
                  textAnchor="end"
                  height={48}
                  padding={{ left: 20, right: 20 }}
                  tickFormatter={formatMonthTick}
                />
                <ChartTooltip
                  content={
                    <ChartTooltipContent labelFormatter={formatMonthLabel} />
                  }
                />
                <ChartLegend content={<ChartLegendContent />} />
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
      <Card aria-labelledby="top-merchants-heading">
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
                <Table>
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
                        <TableCell className="font-medium">
                          {m.description}
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
      <Card aria-labelledby="expense-velocity-heading" className="flex flex-col">
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
                <ChartLegend content={<ChartLegendContent />} />
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
