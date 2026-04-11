import { useMemo, useState } from 'react';
import {
  LineChart,
  Line,
  PieChart,
  Pie,
  Cell,
  XAxis,
  CartesianGrid,
  Label,
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
import {
  useCategoryBreakdown,
  useCategoryTrends,
  useTopMerchants,
  useExpenseVelocity,
} from '@/hooks/useReports';
import { getCategoryColorVar } from '@/lib/chart-colors';
import { MONTH_NAMES, MONTH_FULL_NAMES, formatCurrency, yearOptions } from './utils';
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

  const years = yearOptions();

  // Category breakdown donut data
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

  // Category trends - top 6 expense categories
  const expenseCategories = useMemo(
    () =>
      catTrends.data
        .filter((c) => c.type === 'expense')
        .map((c) => ({
          ...c,
          totalSum: c.data.reduce((sum, d) => sum + d.total, 0),
        }))
        .sort((a, b) => b.totalSum - a.totalSum)
        .slice(0, 6),
    [catTrends.data],
  );

  const catTrendData = useMemo<Record<string, string | number>[]>(() => {
    if (expenseCategories.length === 0) return [];
    const monthSet = new Set<string>();
    const byCat = new Map<number, Map<string, number>>();
    for (const cat of expenseCategories) {
      const inner = new Map<string, number>();
      for (const d of cat.data) {
        const key = `${d.year}-${d.month}`;
        inner.set(key, d.total);
        monthSet.add(key);
      }
      byCat.set(cat.id, inner);
    }
    const sortedMonths = Array.from(monthSet).sort();
    return sortedMonths.map((ym) => {
      const [y, m] = ym.split('-').map(Number);
      const point: Record<string, string | number> = {
        name: `${MONTH_NAMES[m - 1]} ${y}`,
      };
      for (const cat of expenseCategories) {
        point[`cat-${cat.id}`] = byCat.get(cat.id)?.get(ym) ?? 0;
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
      {/* Category Breakdown (donut) */}
      <Card aria-labelledby="category-breakdown-heading">
        <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
          <CardTitle
            id="category-breakdown-heading"
            className="text-base font-semibold"
          >
            Category Breakdown
          </CardTitle>
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
                {MONTH_FULL_NAMES.map((m, i) => (
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
                <PieChart>
                  <ChartTooltip content={<ChartTooltipContent />} />
                  <Pie
                    data={catBreakdown.data.map((c) => ({
                      ...c,
                      configKey: `cat-${c.id}`,
                    }))}
                    dataKey="total"
                    nameKey="configKey"
                    innerRadius={60}
                    outerRadius={80}
                  >
                    {catBreakdown.data.map((entry) => (
                      <Cell
                        key={entry.id}
                        fill={getCategoryColorVar({ id: entry.id })}
                      />
                    ))}
                    <Label
                      content={({ viewBox }) => {
                        if (
                          viewBox &&
                          'cx' in viewBox &&
                          'cy' in viewBox
                        ) {
                          return (
                            <text
                              x={viewBox.cx}
                              y={viewBox.cy}
                              textAnchor="middle"
                              dominantBaseline="middle"
                            >
                              <tspan
                                x={viewBox.cx}
                                y={viewBox.cy}
                                className="fill-foreground text-xl font-bold"
                              >
                                {formatCurrency(breakdownTotal)}
                              </tspan>
                              <tspan
                                x={viewBox.cx}
                                y={(viewBox.cy ?? 0) + 20}
                                className="fill-muted-foreground text-xs"
                              >
                                Total
                              </tspan>
                            </text>
                          );
                        }
                      }}
                    />
                  </Pie>
                </PieChart>
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
      <Card aria-labelledby="category-trends-heading">
        <CardHeader className="pb-2">
          <CardTitle
            id="category-trends-heading"
            className="text-base font-semibold"
          >
            Category Trends
          </CardTitle>
        </CardHeader>
        <CardContent>
          {catTrends.loading && <Skeleton className="h-[300px] w-full" />}
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
                'h-[300px] w-full transition-opacity duration-200',
                catTrends.fetching && !catTrends.loading && 'opacity-60',
              )}
            >
              <LineChart data={catTrendData}>
                <CartesianGrid vertical={false} />
                <XAxis
                  dataKey="name"
                  tickLine={false}
                  axisLine={false}
                  tickMargin={10}
                />
                <ChartTooltip content={<ChartTooltipContent />} />
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
        <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
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
              <ul
                className={cn(
                  'divide-border divide-y transition-opacity duration-200',
                  merchants.fetching && !merchants.loading && 'opacity-60',
                )}
              >
                {merchants.data.map((m, i) => (
                  <li key={m.description} className="flex items-center gap-3 py-2">
                    <span className="w-6 text-sm font-mono tabular-nums text-muted-foreground">
                      {i + 1}
                    </span>
                    <span className="flex-1 truncate text-sm">
                      {m.description}
                    </span>
                    <span className="text-xs text-muted-foreground">
                      {m.tx_count} transaction{m.tx_count !== 1 ? 's' : ''}
                    </span>
                    <span className="font-mono text-sm tabular-nums">
                      {formatCurrency(m.total)}
                    </span>
                  </li>
                ))}
              </ul>
            )}
        </CardContent>
      </Card>

      {/* Expense Velocity */}
      <Card aria-labelledby="expense-velocity-heading">
        <CardHeader className="pb-2">
          <CardTitle
            id="expense-velocity-heading"
            className="text-base font-semibold"
          >
            Expense Velocity
          </CardTitle>
        </CardHeader>
        <CardContent>
          {velocity.loading && <Skeleton className="h-[300px] w-full" />}
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
                'h-[300px] w-full transition-opacity duration-200',
                velocity.fetching && !velocity.loading && 'opacity-60',
              )}
            >
              <LineChart data={velocityData}>
                <CartesianGrid vertical={false} />
                <XAxis
                  dataKey="day"
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
