import { useState, useEffect, useMemo } from 'react';
import {
  BarChart,
  Bar,
  XAxis,
  CartesianGrid,
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
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Skeleton } from '@/components/ui/skeleton';
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { Button } from '@/components/ui/button';
import { Card, CardHeader, CardTitle, CardDescription, CardContent } from '@/components/ui/card';
import {
  Table,
  TableBody,
  TableRow,
  TableCell,
} from '@/components/ui/table';
import { ButtonGroup } from '@/components/ui/button-group';
import { cn } from '@/lib/utils';
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
  expense: { label: 'Expense', color: 'hsl(var(--primary) / 0.35)' },
};

/** Number of categories shown in the collapsed "Spending by Category" card. */
const CATEGORY_COLLAPSED_LIMIT = 6;

/* ── Component ── */

export function Dashboard() {
  const { user } = useAuth();
  const now = new Date();
  const [selectedYear, setSelectedYear] = useState(() => {
    const stored = localStorage.getItem('spendrop-dash-year');
    return stored ? Number(stored) : now.getFullYear();
  });
  const [selectedMonth, setSelectedMonth] = useState(() => {
    const stored = localStorage.getItem('spendrop-dash-month');
    return stored ? Number(stored) : now.getMonth() + 1;
  });
  useEffect(() => { localStorage.setItem('spendrop-dash-year', String(selectedYear)); }, [selectedYear]);
  useEffect(() => { localStorage.setItem('spendrop-dash-month', String(selectedMonth)); }, [selectedMonth]);

  const [cashFlowView, setCashFlowView] = useState<CashFlowView>('6m');
  const { summary, trend, categories, loading, fetching, error } = useDashboard(
    selectedYear,
    selectedMonth,
  );
  const [recentTransactions, setRecentTransactions] = useState<Transaction[]>([]);
  const [showLatest, setShowLatest] = useState(false);
  const [categoriesExpanded, setCategoriesExpanded] = useState(false);

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

  const hasMoreCategories = categories.length > CATEGORY_COLLAPSED_LIMIT;

  const gaugeData = useMemo(() => {
    const visibleCats = categoriesExpanded
      ? categories
      : categories.slice(0, CATEGORY_COLLAPSED_LIMIT);
    return visibleCats.map((cat) => ({
      id: cat.id,
      name: cat.name,
      value: cat.total,
      color: getCategoryColorVar({ id: cat.id }),
    }));
  }, [categories, categoriesExpanded]);

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
        <Card className="p-6">
          <Skeleton className="mb-4 h-4 w-1/4" />
          <Skeleton className="h-64 w-full" />
        </Card>
        <Card className="p-6">
          <Skeleton className="mb-4 h-4 w-1/3" />
          <Skeleton className="h-48 w-full" />
        </Card>
      </div>
    );
  }

  /* ── Error state ── */
  if (error) {
    return (
      <div className="flex flex-col gap-6">
        <h1 className="text-2xl font-semibold tracking-tight">Dashboard</h1>
        <Alert variant="destructive">
          <AlertDescription className="flex items-center justify-between">
            {error}
            <Button variant="outline" size="sm" onClick={() => window.location.reload()}>Retry</Button>
          </AlertDescription>
        </Alert>
      </div>
    );
  }

  /* ── Render ── */
  const refetching = fetching && !loading;

  return (
    <div className={cn('flex flex-col gap-6 transition-opacity duration-200', refetching && 'opacity-60')}>
      {/* Header */}
      <div className="flex flex-col gap-3 md:flex-row md:items-center md:justify-between">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">
            Welcome back, {user?.display_name ?? 'there'}
          </h1>
          <p className="mt-0.5 text-sm text-muted-foreground">
            Here's what's happening with your finances.
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Select value={String(selectedMonth)} onValueChange={(v) => setSelectedMonth(Number(v))}>
            <SelectTrigger className="h-9 w-[140px]" aria-label="Month">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectGroup>
                {MONTHS.map((m, i) => (
                  <SelectItem key={m} value={String(i + 1)}>{m}</SelectItem>
                ))}
              </SelectGroup>
            </SelectContent>
          </Select>
          <Select value={String(selectedYear)} onValueChange={(v) => setSelectedYear(Number(v))}>
            <SelectTrigger className="h-9 w-[100px]" aria-label="Year">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectGroup>
                {yearOptions.map((y) => (
                  <SelectItem key={y} value={String(y)}>{y}</SelectItem>
                ))}
              </SelectGroup>
            </SelectContent>
          </Select>
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
          <ButtonGroup>
            <Button
              variant={cashFlowView === '6m' ? 'secondary' : 'outline'}
              size="sm"
              className="h-8 px-3 text-xs"
              onClick={() => setCashFlowView('6m')}
            >
              6M
            </Button>
            <Button
              variant={cashFlowView === '12m' ? 'secondary' : 'outline'}
              size="sm"
              className="h-8 px-3 text-xs"
              onClick={() => setCashFlowView('12m')}
            >
              12M
            </Button>
          </ButtonGroup>
        }
      >
        <ChartContainer config={cashFlowConfig} className="aspect-auto h-64 w-full">
          <BarChart accessibilityLayer data={chartData} barGap={4}>
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
            <CartesianGrid vertical={false} />
            <XAxis
              dataKey="name"
              tickLine={false}
              axisLine={false}
              tickMargin={10}
            />
            <ChartTooltip content={<ChartTooltipContent hideIndicator />} />
            <Bar dataKey="income" fill="url(#fillIncome)" stroke="var(--color-income)" strokeOpacity={0.3} radius={[4, 4, 0, 0]} />
            <Bar dataKey="expense" fill="url(#fillExpense)" stroke="var(--color-expense)" strokeOpacity={0.3} radius={[4, 4, 0, 0]} />
          </BarChart>
        </ChartContainer>
      </ChartCard>

      {/* Category breakdown + Recent Transactions — side by side */}
      <div className="grid grid-cols-1 gap-3 lg:grid-cols-2">
        <Card>
          <CardHeader className="flex flex-row items-center justify-between space-y-0">
            <div>
              <CardTitle className="text-base font-semibold">Spending by Category</CardTitle>
              <CardDescription className="text-xs text-muted-foreground">
                {MONTHS[selectedMonth - 1]} {selectedYear}
              </CardDescription>
            </div>
            <span className="font-mono text-lg font-semibold tabular-nums">
              {formatFull(totalCategorySpent)}
            </span>
          </CardHeader>
          <CardContent>
            {gaugeData.length === 0 ? (
              <div className="flex items-center justify-center p-8 text-sm text-muted-foreground">
                No spending yet this month.
              </div>
            ) : (
              <div className="flex flex-1 flex-col justify-between gap-4">
                {gaugeData.map((slice) => {
                  const pct = totalCategorySpent > 0
                    ? (slice.value / totalCategorySpent) * 100
                    : 0;
                  return (
                    <div key={slice.id} className="flex flex-col gap-2">
                      <div className="flex items-center justify-between text-sm">
                        <span className="font-medium">{slice.name}</span>
                        <span className="font-mono tabular-nums text-muted-foreground">
                          {formatFull(slice.value)}
                        </span>
                      </div>
                      <div className="h-5 w-full rounded-full bg-muted">
                        <div
                          className="h-full rounded-full transition-all"
                          style={{
                            width: `${Math.max(pct, 2)}%`,
                            backgroundColor: slice.color,
                          }}
                        />
                      </div>
                    </div>
                  );
                })}
                {hasMoreCategories && (
                  <Button
                    variant="ghost"
                    size="sm"
                    className="w-full"
                    onClick={() => setCategoriesExpanded((prev) => !prev)}
                  >
                    {categoriesExpanded ? 'Show less' : 'Show more'}
                  </Button>
                )}
              </div>
            )}
          </CardContent>
        </Card>

        <Card>
          <CardHeader className="flex flex-row items-center justify-between space-y-0">
            <CardTitle className="text-base font-semibold">Recent Transactions</CardTitle>
            <div className="flex items-center gap-2">
              <ButtonGroup>
                <Button
                  variant={!showLatest ? 'secondary' : 'outline'}
                  size="sm"
                  className="h-8 px-3 text-xs"
                  onClick={() => setShowLatest(false)}
                >
                  {SHORT_MONTHS[selectedMonth - 1]}
                </Button>
                <Button
                  variant={showLatest ? 'secondary' : 'outline'}
                  size="sm"
                  className="h-8 px-3 text-xs"
                  onClick={() => setShowLatest(true)}
                >
                  Latest
                </Button>
              </ButtonGroup>
              <Button variant="outline" size="sm" asChild>
                <Link to="/transactions">View All</Link>
              </Button>
            </div>
          </CardHeader>
          <CardContent>
            {recentTransactions.length === 0 ? (
              <div className="flex items-center justify-center p-8 text-sm text-muted-foreground">
                No transactions yet.
              </div>
            ) : (
              <Table>
                <TableBody>
                  {recentTransactions.map((tx) => {
                    const color = getCategoryColorVar({ id: tx.category_id });
                    return (
                      <TableRow key={tx.id}>
                        <TableCell className="w-10">
                          <div
                            className="flex size-10 items-center justify-center rounded-lg"
                            style={{
                              backgroundColor: `color-mix(in srgb, ${color} 15%, transparent)`,
                            }}
                          >
                            <span className="text-xs font-medium" style={{ color }}>
                              {tx.category_name?.[0]?.toUpperCase() ?? '?'}
                            </span>
                          </div>
                        </TableCell>
                        <TableCell>
                          <div className="flex flex-col">
                            <span className="font-medium">{tx.description}</span>
                            <span className="text-sm text-muted-foreground">{tx.category_name}</span>
                          </div>
                        </TableCell>
                        <TableCell className="text-sm text-muted-foreground">
                          {tx.date}
                        </TableCell>
                        <TableCell className="text-right">
                          <span
                            className={cn(
                              'text-sm font-semibold tabular-nums',
                              tx.category_type === 'income' && 'text-emerald-500',
                            )}
                          >
                            {tx.category_type === 'income' ? '+' : '-'}
                            {formatFull(tx.amount)}
                          </span>
                        </TableCell>
                      </TableRow>
                    );
                  })}
                </TableBody>
              </Table>
            )}
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
