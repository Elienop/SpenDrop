import { useState, useEffect, useMemo } from 'react';
import {
  ResponsiveContainer,
  BarChart,
  Bar,
  XAxis,
  YAxis,
  CartesianGrid,
  RadialBarChart,
  RadialBar,
  PolarGrid,
  PolarRadiusAxis,
  Label as RechartsLabel,
} from 'recharts';
import { Link } from 'react-router-dom';
import { useDashboard } from '../hooks/useDashboard';
import { useAuth } from '../hooks/useAuth';
import { api } from '../api/client';
import { getCategoryColorVar } from '@/lib/chart-colors';
import { KpiCard, type KpiDelta } from '@/components/KpiCard';
import { ChartCard } from '@/components/ChartCard';
import {
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
  type ChartConfig,
} from '@/components/ui/chart';
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { Skeleton } from '@/components/ui/skeleton';
import { Label } from '@/components/ui/label';
import { Button } from '@/components/ui/button';
import { Card } from '@/components/ui/card';
import type { Transaction, PaginatedResponse } from '../api/types';

/* ── Formatters ── */

function splitCurrency(amount: number): { dollars: string; cents: string } {
  const abs = Math.abs(amount);
  const dollars = Math.floor(abs).toLocaleString('en-US');
  const cents = (abs % 1).toFixed(2).slice(1); // ".52"
  return { dollars: `$${dollars}`, cents };
}

// Intentionally unsigned: callers supply the sign glyph (+/-) from
// category_type or from a dedicated delta. Do not rely on this formatter
// to preserve negative amounts.
function formatFull(amount: number): string {
  return '$' + Math.abs(amount).toLocaleString('en-US', {
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  });
}

function pctChange(current: number, previous: number): number | null {
  if (previous === 0) return null;
  return ((current - previous) / Math.abs(previous)) * 100;
}

function toDelta(value: number | null): KpiDelta | null {
  if (value == null) return null;
  return {
    percent: Math.abs(value),
    direction: value > 0 ? 'up' : value < 0 ? 'down' : 'flat',
  };
}

/* ── Constants ── */

const MONTHS = [
  'January', 'February', 'March', 'April', 'May', 'June',
  'July', 'August', 'September', 'October', 'November', 'December',
];

const SHORT_MONTHS = [
  'Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun',
  'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec',
];

type CashFlowView = '6m' | '12m';

const cashFlowConfig: ChartConfig = {
  income: { label: 'Income', color: 'hsl(var(--primary))' },
  expense: { label: 'Expense', color: 'hsl(var(--muted-foreground))' },
};

const savingsConfig: ChartConfig = {
  savings: { label: 'Progress', color: 'hsl(var(--primary))' },
};

/* ── Component ── */

export function Dashboard() {
  const { user } = useAuth();
  const now = new Date();
  const [selectedYear, setSelectedYear] = useState(now.getFullYear());
  const [selectedMonth, setSelectedMonth] = useState(now.getMonth() + 1);
  const [cashFlowView, setCashFlowView] = useState<CashFlowView>('6m');
  const { summary, trend, categories, loading, error } = useDashboard(
    selectedYear,
    selectedMonth,
  );
  const [recentTransactions, setRecentTransactions] = useState<Transaction[]>([]);
  const [showLatest, setShowLatest] = useState(false);

  useEffect(() => {
    let cancelled = false;
    let url = 'transactions?per_page=6';
    if (!showLatest) {
      const mm = String(selectedMonth).padStart(2, '0');
      const lastDay = new Date(selectedYear, selectedMonth, 0).getDate();
      const dd = String(lastDay).padStart(2, '0');
      url += `&date_from=${selectedYear}-${mm}-01&date_to=${selectedYear}-${mm}-${dd}`;
    }
    api
      .get<PaginatedResponse<Transaction>>(url)
      .then((data) => { if (!cancelled) setRecentTransactions(data.transactions); })
      .catch(() => {
        // Clear stale data on failure so the empty state renders under the
        // new month header instead of showing the previous month's rows.
        if (!cancelled) setRecentTransactions([]);
      });
    return () => { cancelled = true; };
  }, [selectedYear, selectedMonth, showLatest]);

  const currentYear = now.getFullYear();
  const yearOptions = Array.from({ length: 5 }, (_, i) => currentYear - i);

  /* ── Derived ── */

  const totalIncome = summary?.total_income ?? 0;
  const totalExpense = summary?.total_spent ?? 0;
  const totalBalance = totalIncome - totalExpense;

  // The backend returns `trend` newest-first (dashboard_handlers.go walks
  // backwards from the anchor with `AddDate(0, -i, 0)`), so reverse to
  // ascending order before slicing — the bar chart renders left-to-right
  // chronologically with the most recent month on the right edge.
  const chartData = useMemo(() => {
    const sorted = [...trend].reverse();
    const sliced = cashFlowView === '6m' ? sorted.slice(-6) : sorted;
    return sliced.map((item) => ({
      name: SHORT_MONTHS[item.month - 1],
      income: item.total_income,
      expense: item.total_spent,
    }));
  }, [trend, cashFlowView]);

  const totalCategorySpent = categories.reduce((sum, cat) => sum + cat.total, 0);

  const gaugeData = useMemo(() => {
    const topCats = categories.slice(0, 5);
    const otherTotal = categories
      .slice(5)
      .reduce((sum, cat) => sum + cat.total, 0);
    return [
      ...topCats.map((cat) => ({
        id: cat.id,
        name: cat.name,
        value: cat.total,
        color: getCategoryColorVar({ id: cat.id }),
      })),
      ...(otherTotal > 0
        ? [{
            id: -1,
            name: 'Other',
            value: otherTotal,
            color: 'hsl(var(--muted-foreground))',
          }]
        : []),
    ];
  }, [categories]);

  // Key by a stable slug (`cat-${id}`), not by name. shadcn's ChartStyle
  // turns every ChartConfig key into a `--color-${key}` CSS custom property,
  // so keys must be valid CSS identifiers — user-editable names like
  // "Food & Drink" would produce malformed CSS and break tooltip color
  // lookup silently. The id-derived slug also prevents duplicate-name
  // categories from clobbering each other in the config map.
  const categoryChartConfig = useMemo<ChartConfig>(() => {
    return gaugeData.reduce<ChartConfig>((acc, slice) => {
      acc[`cat-${slice.id}`] = { label: slice.name, color: slice.color };
      return acc;
    }, {});
  }, [gaugeData]);

  const radialCategoryData = useMemo(() => {
    const point: Record<string, number> = {};
    for (const slice of gaugeData) {
      point[`cat-${slice.id}`] = slice.value;
    }
    return [point];
  }, [gaugeData]);

  const savingsRate = totalIncome > 0
    ? ((totalIncome - totalExpense) / totalIncome * 100)
    : 0;

  const prevMonthTrend = useMemo(() => {
    const prevM = selectedMonth === 1 ? 12 : selectedMonth - 1;
    const prevY = selectedMonth === 1 ? selectedYear - 1 : selectedYear;
    return trend.find(t => t.year === prevY && t.month === prevM);
  }, [trend, selectedMonth, selectedYear]);

  const balanceDelta = prevMonthTrend
    ? pctChange(totalBalance, prevMonthTrend.total_income - prevMonthTrend.total_spent)
    : null;
  const incomeDelta = prevMonthTrend
    ? pctChange(totalIncome, prevMonthTrend.total_income)
    : null;
  const expenseDelta = prevMonthTrend
    ? pctChange(totalExpense, prevMonthTrend.total_spent)
    : null;
  const savingsRatePrev = prevMonthTrend && prevMonthTrend.total_income > 0
    ? ((prevMonthTrend.total_income - prevMonthTrend.total_spent) / prevMonthTrend.total_income * 100)
    : null;
  const savingsDelta = savingsRatePrev !== null ? savingsRate - savingsRatePrev : null;

  const balanceSplit = splitCurrency(totalBalance);
  const incomeSplit = splitCurrency(totalIncome);
  const expenseSplit = splitCurrency(totalExpense);

  const savingsGoalPct = summary?.savings_goal_progress ?? 0;

  /* ── Loading state ── */
  if (loading) {
    return (
      <div className="flex flex-col gap-6">
        <div className="flex items-center justify-between">
          <Skeleton className="h-8 w-64" />
          <Skeleton className="h-9 w-48" />
        </div>
        <div className="grid grid-cols-1 gap-3 md:grid-cols-2 xl:grid-cols-4">
          {[1, 2, 3, 4].map((n) => (
            <Card key={n} className="p-6">
              <Skeleton className="mb-3 h-3 w-1/2" />
              <Skeleton className="mb-3 h-8 w-3/4" />
              <Skeleton className="h-3 w-2/3" />
            </Card>
          ))}
        </div>
        <div className="grid grid-cols-1 gap-3 lg:grid-cols-5">
          <Card className="p-6 lg:col-span-3">
            <Skeleton className="mb-4 h-4 w-1/4" />
            <Skeleton className="h-64 w-full" />
          </Card>
          <Card className="p-6 lg:col-span-2">
            <Skeleton className="mb-4 h-4 w-1/3" />
            <Skeleton className="h-48 w-full" />
          </Card>
        </div>
      </div>
    );
  }

  /* ── Error state ── */
  if (error) {
    return (
      <div className="flex flex-col gap-6">
        <h1 className="text-2xl font-bold tracking-tight">Dashboard</h1>
        <Card className="flex flex-col items-center gap-4 p-12 text-center" role="alert">
          <p className="text-muted-foreground">{error}</p>
          <Button onClick={() => window.location.reload()}>Retry</Button>
        </Card>
      </div>
    );
  }

  /* ── Render ── */
  return (
    <div className="flex flex-col gap-6">
      {/* Header */}
      <div className="flex flex-col gap-3 md:flex-row md:items-center md:justify-between">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">
            Welcome back, {user?.display_name ?? 'there'}
          </h1>
          <p className="mt-0.5 text-sm text-muted-foreground">
            Here's what's happening with your finances.
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Label htmlFor="dash-month" className="sr-only">Month</Label>
          <select
            id="dash-month"
            aria-label="Month"
            className="h-9 rounded-md border border-input bg-background px-3 text-sm font-medium"
            value={selectedMonth}
            onChange={(e) => setSelectedMonth(Number(e.target.value))}
          >
            {MONTHS.map((m, i) => (
              <option key={m} value={i + 1}>{m}</option>
            ))}
          </select>
          <Label htmlFor="dash-year" className="sr-only">Year</Label>
          <select
            id="dash-year"
            aria-label="Year"
            className="h-9 rounded-md border border-input bg-background px-3 text-sm font-medium"
            value={selectedYear}
            onChange={(e) => setSelectedYear(Number(e.target.value))}
          >
            {yearOptions.map((y) => (
              <option key={y} value={y}>{y}</option>
            ))}
          </select>
        </div>
      </div>

      {/* KPI row */}
      {summary && (
        <div className="grid grid-cols-1 gap-3 md:grid-cols-2 xl:grid-cols-4">
          <KpiCard
            label="Total Balance"
            dollars={balanceSplit.dollars}
            cents={balanceSplit.cents}
            delta={toDelta(balanceDelta)}
            footnote="vs last month"
          />
          <KpiCard
            label="Income"
            dollars={incomeSplit.dollars}
            cents={incomeSplit.cents}
            delta={toDelta(incomeDelta)}
            footnote="vs last month"
          />
          <KpiCard
            label="Expenses"
            dollars={expenseSplit.dollars}
            cents={expenseSplit.cents}
            delta={toDelta(expenseDelta == null ? null : -expenseDelta)}
            footnote="vs last month"
          />
          <KpiCard
            label="Savings Rate"
            dollars={savingsRate.toFixed(1)}
            cents="%"
            delta={toDelta(savingsDelta)}
            footnote="vs last month"
          />
        </div>
      )}

      {/* Cash flow — full width */}
      <ChartCard
        title="Cash Flow"
        subtitle="Income vs expenses over time"
        action={
          <Tabs
            value={cashFlowView}
            onValueChange={(v) => setCashFlowView(v as CashFlowView)}
          >
            <TabsList className="h-8">
              <TabsTrigger value="6m" className="h-6 px-3 text-xs">6M</TabsTrigger>
              <TabsTrigger value="12m" className="h-6 px-3 text-xs">12M</TabsTrigger>
            </TabsList>
          </Tabs>
        }
      >
        <ChartContainer config={cashFlowConfig} className="h-64 w-full">
          <ResponsiveContainer width="100%" height="100%">
            <BarChart data={chartData} margin={{ top: 4, right: 0, bottom: 0, left: 0 }} barGap={4}>
              <defs>
                <linearGradient id="fillIncome" x1="0" y1="0" x2="0" y2="1">
                  <stop offset="5%" stopColor="var(--color-income)" stopOpacity={0.8} />
                  <stop offset="95%" stopColor="var(--color-income)" stopOpacity={0.1} />
                </linearGradient>
                <linearGradient id="fillExpense" x1="0" y1="0" x2="0" y2="1">
                  <stop offset="5%" stopColor="var(--color-expense)" stopOpacity={0.8} />
                  <stop offset="95%" stopColor="var(--color-expense)" stopOpacity={0.1} />
                </linearGradient>
              </defs>
              <CartesianGrid
                strokeDasharray="3 3"
                stroke="hsl(var(--border))"
                vertical={false}
              />
              <XAxis
                dataKey="name"
                tickLine={false}
                axisLine={false}
                className="text-xs"
                stroke="hsl(var(--muted-foreground))"
              />
              <YAxis
                tickLine={false}
                axisLine={false}
                className="text-xs"
                stroke="hsl(var(--muted-foreground))"
              />
              <ChartTooltip content={<ChartTooltipContent />} />
              <Bar dataKey="income" fill="url(#fillIncome)" stroke="var(--color-income)" strokeOpacity={0.3} radius={[4, 4, 0, 0]} />
              <Bar dataKey="expense" fill="url(#fillExpense)" stroke="var(--color-expense)" strokeOpacity={0.3} radius={[4, 4, 0, 0]} />
            </BarChart>
          </ResponsiveContainer>
        </ChartContainer>
      </ChartCard>

      {/* Bottom grid */}
      <div className="grid grid-cols-1 gap-3 lg:grid-cols-5">
        {/* Spending by Category */}
        <ChartCard
          title="Spending by Category"
          subtitle={formatFull(totalCategorySpent)}
          className="lg:col-span-3"
        >
          {gaugeData.length === 0 ? (
            <div className="flex flex-1 items-center justify-center p-8 text-sm text-muted-foreground">
              No spending yet this month.
            </div>
          ) : (
            <div className="flex flex-1 flex-col gap-4 md:flex-row md:items-center">
              <ChartContainer
                config={categoryChartConfig}
                className="mx-auto aspect-square w-full max-w-[250px] md:w-1/2"
              >
                <RadialBarChart
                  data={radialCategoryData}
                  endAngle={180}
                  innerRadius={80}
                  outerRadius={130}
                >
                  <ChartTooltip
                    cursor={false}
                    content={<ChartTooltipContent hideLabel />}
                  />
                  {gaugeData.map((slice) => (
                    <RadialBar
                      key={slice.id}
                      dataKey={`cat-${slice.id}`}
                      stackId="a"
                      fill={slice.color}
                      cornerRadius={5}
                      className="stroke-transparent stroke-2"
                    />
                  ))}
                  <PolarRadiusAxis tick={false} tickLine={false} axisLine={false}>
                    <RechartsLabel
                      content={({ viewBox }) => {
                        if (viewBox && 'cx' in viewBox && 'cy' in viewBox) {
                          return (
                            <text x={viewBox.cx} y={viewBox.cy} textAnchor="middle">
                              <tspan
                                x={viewBox.cx}
                                y={(viewBox.cy || 0) - 16}
                                className="fill-foreground text-2xl font-bold"
                              >
                                {formatFull(totalCategorySpent)}
                              </tspan>
                              <tspan
                                x={viewBox.cx}
                                y={(viewBox.cy || 0) + 4}
                                className="fill-muted-foreground text-sm"
                              >
                                Total Spent
                              </tspan>
                            </text>
                          );
                        }
                      }}
                    />
                  </PolarRadiusAxis>
                </RadialBarChart>
              </ChartContainer>
              <ul className="flex flex-1 flex-col gap-2 md:w-1/2">
                {gaugeData.map((slice) => {
                  const pct = totalCategorySpent > 0
                    ? (slice.value / totalCategorySpent) * 100
                    : 0;
                  return (
                    <li key={slice.id} className="flex items-center gap-3 text-sm">
                      <span
                        className="h-2.5 w-2.5 shrink-0 rounded-sm"
                        style={{ backgroundColor: slice.color }}
                        aria-hidden="true"
                      />
                      <span className="flex-1 truncate font-medium">{slice.name}</span>
                      <span className="font-mono tabular-nums text-muted-foreground">
                        {pct.toFixed(0)}%
                      </span>
                      <span className="min-w-20 text-right font-mono font-semibold tabular-nums">
                        {formatFull(slice.value)}
                      </span>
                    </li>
                  );
                })}
              </ul>
            </div>
          )}
        </ChartCard>

        {/* Recent Transactions */}
        <Card className="flex flex-col p-6 lg:col-span-2">
          <div className="mb-4 flex items-center justify-between">
            <div>
              <h2 className="text-base font-semibold tracking-tight">Recent Transactions</h2>
              <button
                type="button"
                aria-pressed={showLatest}
                className="mt-0.5 text-xs font-medium text-primary hover:underline"
                onClick={() => setShowLatest((v) => !v)}
              >
                {showLatest ? 'Show this month' : 'Show latest'}
              </button>
            </div>
            <Link
              to="/transactions"
              className="text-xs font-medium text-primary hover:underline"
            >
              View all →
            </Link>
          </div>
          {recentTransactions.length === 0 ? (
            <div className="flex flex-1 items-center justify-center p-8 text-sm text-muted-foreground">
              No transactions yet.
            </div>
          ) : (
            <ul className="flex flex-col divide-y divide-border">
              {recentTransactions.map((tx) => {
                const color = getCategoryColorVar({ id: tx.category_id });
                return (
                  <li key={tx.id} className="flex items-center gap-3 py-2.5">
                    <span
                      className="h-9 w-9 shrink-0 rounded-full"
                      style={{
                        backgroundColor: `color-mix(in srgb, ${color} 15%, transparent)`,
                      }}
                      aria-hidden="true"
                    />
                    <div className="min-w-0 flex-1">
                      <div className="truncate text-sm font-medium">{tx.description}</div>
                      <div className="text-xs text-muted-foreground">{tx.category_name}</div>
                    </div>
                    <div className="text-right">
                      <div
                        className={
                          tx.category_type === 'income'
                            ? 'font-mono text-sm font-semibold tabular-nums text-emerald-500'
                            : 'font-mono text-sm font-semibold tabular-nums'
                        }
                      >
                        {tx.category_type === 'income' ? '+' : '-'}
                        {formatFull(tx.amount)}
                      </div>
                      <div className="text-xs text-muted-foreground">{tx.date}</div>
                    </div>
                  </li>
                );
              })}
            </ul>
          )}
        </Card>
      </div>

      {/* Savings Progress — full width */}
      <ChartCard
        title="Savings Progress"
        subtitle={`${savingsGoalPct.toFixed(0)}% of goal`}
      >
        <div className="flex flex-col items-center gap-6 md:flex-row md:justify-around">
          <ChartContainer config={savingsConfig} className="h-48 w-48">
            <RadialBarChart
              data={[{ name: 'savings', value: savingsGoalPct, fill: 'var(--color-savings)' }]}
              endAngle={(Math.min(savingsGoalPct, 100) / 100) * 360}
              innerRadius={65}
              outerRadius={95}
            >
              <PolarGrid
                gridType="circle"
                radialLines={false}
                stroke="none"
                className="first:fill-muted last:fill-background"
                polarRadius={[86, 74]}
              />
              <RadialBar dataKey="value" cornerRadius={10} />
              <PolarRadiusAxis tick={false} tickLine={false} axisLine={false}>
                <RechartsLabel
                  content={({ viewBox }) => {
                    if (viewBox && 'cx' in viewBox && 'cy' in viewBox) {
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
                            className="fill-foreground text-4xl font-bold"
                          >
                            {savingsGoalPct.toFixed(0)}%
                          </tspan>
                          <tspan
                            x={viewBox.cx}
                            y={(viewBox.cy || 0) + 24}
                            className="fill-muted-foreground"
                          >
                            of goal
                          </tspan>
                        </text>
                      );
                    }
                  }}
                />
              </PolarRadiusAxis>
            </RadialBarChart>
          </ChartContainer>
          <div className="flex gap-8">
            <div className="text-center">
              <div className="font-mono text-base font-semibold tabular-nums">
                {formatFull(summary?.savings_ytd ?? 0)}
              </div>
              <div className="mt-0.5 text-xs text-muted-foreground">Saved YTD</div>
            </div>
            <div className="text-center">
              <div className="font-mono text-base font-semibold tabular-nums">
                {formatFull(summary?.savings_goal ?? 0)}
              </div>
              <div className="mt-0.5 text-xs text-muted-foreground">Annual Goal</div>
            </div>
          </div>
        </div>
      </ChartCard>
    </div>
  );
}
