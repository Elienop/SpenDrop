import { useId, useMemo, useState, useEffect } from 'react';
import {
  RadialBarChart,
  RadialBar,
  AreaChart,
  Area,
  BarChart,
  Bar,
  XAxis,
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
import { MONTH_NAMES, formatCurrency, yearOptions } from './utils';
import { cn } from '@/lib/utils';
import type { SavingsGoal, YoYResponse } from '@/api/types';
import { api } from '@/api/client';

const SAVINGS_CONFIG = {
  savings: { label: 'Cumulative Savings', color: 'hsl(var(--primary))' },
} satisfies ChartConfig;

/** Extract YoY chart data outside the component to avoid React Compiler .current ref issue */
function buildYoYData(data: YoYResponse | null) {
  if (!data) return [];
  const currentYear = data.current;
  const previousYear = data.previous;
  return currentYear.map((cur, i) => ({
    name: MONTH_NAMES[i],
    currentExpenses: cur.expenses,
    previousExpenses: previousYear[i]?.expenses ?? 0,
  }));
}

export function SavingsTab() {
  const [year, setYear] = useState(new Date().getFullYear());
  const incExp = useIncomeExpenses(24);
  const yoy = useYearOverYear(year);
  const gradientId = useId();

  const [goals, setGoals] = useState<SavingsGoal[]>([]);
  const [goalsLoading, setGoalsLoading] = useState(true);
  useEffect(() => {
    api
      .get<SavingsGoal[]>('savings-goals')
      .then(setGoals)
      .catch((err) => console.error('Failed to load savings goals:', err))
      .finally(() => setGoalsLoading(false));
  }, []);

  const goal = goals.find((g) => g.year === year);

  const savingsData = useMemo(() => {
    const result: { name: string; savings: number }[] = [];
    incExp.data
      .filter((e) => e.year === year)
      .reduce((acc, e) => {
        const total = acc + e.net;
        result.push({
          name: MONTH_NAMES[e.month - 1],
          savings: Math.max(0, total),
        });
        return total;
      }, 0);
    return result;
  }, [incExp.data, year]);

  const currentSavings = savingsData.at(-1)?.savings ?? 0;
  const progress = goal
    ? Math.max(0, Math.round((currentSavings / goal.target_amount) * 100))
    : 0;

  const radialData = [{ progress, fill: 'hsl(var(--primary))' }];

  // YoY chart data
  const yoyData = useMemo(() => buildYoYData(yoy.data), [yoy.data]);

  const yoyConfig = useMemo<ChartConfig>(() => {
    if (!yoy.data) return {} as ChartConfig;
    return {
      currentExpenses: {
        label: `${yoy.data.current_year}`,
        color: 'hsl(var(--primary))',
      },
      previousExpenses: {
        label: `${yoy.data.previous_year}`,
        color: 'hsl(var(--primary) / 0.35)',
      },
    };
  }, [yoy.data]);

  const years = yearOptions();

  return (
    <div className="flex flex-col gap-6">
      {/* Savings Goal Progress */}
      <Card aria-labelledby="savings-goal-heading">
        <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-4">
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
          {incExp.error && (
            <Alert variant="destructive">
              <AlertCircle className="h-4 w-4" />
              <AlertDescription>{incExp.error}</AlertDescription>
            </Alert>
          )}
          {!incExp.loading && !goalsLoading && !incExp.error && (
            <div
              className={cn(
                'transition-opacity duration-200',
                incExp.fetching && !incExp.loading && 'opacity-60',
              )}
            >
              {goal ? (
                <div className="flex items-center gap-6">
                  <ChartContainer
                    config={SAVINGS_CONFIG}
                    className="h-[200px] w-[200px]"
                  >
                    <RadialBarChart
                      innerRadius={70}
                      outerRadius={90}
                      data={radialData}
                      startAngle={90}
                      endAngle={90 - (progress / 100) * 360}
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
                          className="fill-foreground text-2xl font-bold"
                        >
                          {progress}%
                        </tspan>
                        <tspan
                          x="50%"
                          dy="20"
                          className="fill-muted-foreground text-xs"
                        >
                          {formatCurrency(currentSavings)}
                        </tspan>
                      </text>
                    </RadialBarChart>
                  </ChartContainer>
                  <div className="text-sm text-muted-foreground">
                    <p>Goal: {formatCurrency(goal.target_amount)}</p>
                    <p>Saved: {formatCurrency(currentSavings)}</p>
                    <p>
                      Remaining:{' '}
                      {formatCurrency(
                        Math.max(0, goal.target_amount - currentSavings),
                      )}
                    </p>
                  </div>
                </div>
              ) : (
                <p className="text-sm text-muted-foreground mb-4">
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
                  <XAxis
                    dataKey="name"
                    tickLine={false}
                    axisLine={false}
                    tickMargin={10}
                    interval={0}
                    padding={{ left: 20, right: 20 }}
                  />
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
        <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-4">
          <CardTitle id="yoy-heading" className="text-base font-semibold">
            Year-over-Year Comparison
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
                      stopColor="var(--color-currentExpenses)"
                      stopOpacity={0.8}
                    />
                    <stop
                      offset="95%"
                      stopColor="var(--color-currentExpenses)"
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
                      stopColor="var(--color-previousExpenses)"
                      stopOpacity={0.8}
                    />
                    <stop
                      offset="95%"
                      stopColor="var(--color-previousExpenses)"
                      stopOpacity={0.1}
                    />
                  </linearGradient>
                </defs>
                <CartesianGrid vertical={false} />
                <XAxis
                  dataKey="name"
                  tickLine={false}
                  axisLine={false}
                  tickMargin={10}
                />
                <ChartTooltip
                  content={<ChartTooltipContent hideIndicator />}
                />
                <ChartLegend content={<ChartLegendContent />} />
                <Bar
                  dataKey="currentExpenses"
                  fill={`url(#${gradientId}-yoyCurrent)`}
                  stroke="var(--color-currentExpenses)"
                  strokeOpacity={0.3}
                  radius={[4, 4, 0, 0]}
                />
                <Bar
                  dataKey="previousExpenses"
                  fill={`url(#${gradientId}-yoyPrevious)`}
                  stroke="var(--color-previousExpenses)"
                  strokeOpacity={0.3}
                  radius={[4, 4, 0, 0]}
                />
              </BarChart>
            </ChartContainer>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
