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
import { Badge } from '@/components/ui/badge';
import { AlertTriangle } from 'lucide-react';
import { cn } from '@/lib/utils';
import type { Transaction, PaginatedResponse } from '../api/types';
import { formatCurrency, DEFAULT_LOCALE } from '@/lib/format';
import {
  MONTH_NAMES_SHORT,
  MONTH_NAMES_FULL,
  formatMonthTick,
  formatMonthLabel,
  yearOptionsFrom,
} from '@/lib/dates';
import { useReportYears } from '@/hooks/useReportYears';
import { STORAGE_KEYS } from '@/lib/storage-keys';
import {
  DASHBOARD_RECENT_TX_LIMIT,
  DASHBOARD_CATEGORY_COLLAPSED_LIMIT,
} from '@/lib/constants';
import { TYPE_INCOME } from '@/lib/transaction-types';
import { useBaseCurrency } from '@/hooks/useBaseCurrency';
import { useIsMobileViewport } from '@/hooks/useIsMobileViewport';
import { RecentTransactionCard } from '@/components/RecentTransactionCard';
import { transactionLabel } from '@/lib/transaction-label';
import {
  monthAxisInterval,
  MONTH_TICK,
  MONTH_TICK_ANGLE,
  ROTATED_MONTH_TICK_PADDING,
} from '@/components/reports/utils';

/* ── Pure helpers ── */

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

type CashFlowView = '6m' | '12m';

const cashFlowConfig: ChartConfig = {
  income: { label: 'Income', color: 'hsl(var(--primary))' },
  expense: { label: 'Expense', color: 'hsl(var(--primary) / 0.35)' },
};

/* ── Component ── */

export function Dashboard() {
  const { user } = useAuth();
  const baseCurrency = useBaseCurrency();
  // Below `md` the recent-transactions table becomes a card list. Same gate and
  // same one-presentation-mounted rule as the Transactions and Trash lists —
  // this was the last table left panning on a phone.
  const isMobile = useIsMobileViewport();
  const now = new Date();
  const [selectedYear, setSelectedYear] = useState(() => {
    const stored = localStorage.getItem(STORAGE_KEYS.dashboardYear);
    return stored ? Number(stored) : now.getFullYear();
  });
  const [selectedMonth, setSelectedMonth] = useState(() => {
    const stored = localStorage.getItem(STORAGE_KEYS.dashboardMonth);
    return stored ? Number(stored) : now.getMonth() + 1;
  });
  useEffect(() => {
    localStorage.setItem(STORAGE_KEYS.dashboardYear, String(selectedYear));
  }, [selectedYear]);
  useEffect(() => {
    localStorage.setItem(STORAGE_KEYS.dashboardMonth, String(selectedMonth));
  }, [selectedMonth]);

  // Formatters bound to the household's base currency. `formatFull` returns
  // the full currency-formatted string; `splitCurrency` splits it into
  // integer and fractional parts (plus the currency symbol) for the KPI
  // card's typography. The sign is preserved — negative amounts propagate
  // through `formatToParts` so the `minusSign` part lands in `dollars` and
  // `KpiCard` renders `{dollars}{cents}` verbatim (e.g. `-$1,598` + `.90`).
  const formatFull = (amount: number): string =>
    formatCurrency(amount, baseCurrency);
  const splitCurrency = (
    amount: number,
  ): { dollars: string; cents: string } => {
    const parts = new Intl.NumberFormat(DEFAULT_LOCALE, {
      style: 'currency',
      currency: baseCurrency,
      minimumFractionDigits: 2,
    }).formatToParts(amount);
    let dollars = '';
    let cents = '';
    let seenDecimal = false;
    for (const part of parts) {
      if (part.type === 'decimal') {
        seenDecimal = true;
        cents += part.value;
        continue;
      }
      if (part.type === 'fraction') {
        cents += part.value;
        continue;
      }
      if (seenDecimal) {
        // Trailing symbols (e.g. some locales place currency after amount)
        cents += part.value;
      } else {
        dollars += part.value;
      }
    }
    return { dollars, cents };
  };

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
    let url = `transactions?per_page=${DASHBOARD_RECENT_TX_LIMIT}`;
    if (!showLatest) {
      const mm = String(selectedMonth).padStart(2, '0');
      const lastDay = new Date(selectedYear, selectedMonth, 0).getDate();
      const dd = String(lastDay).padStart(2, '0');
      url += `&date_from=${selectedYear}-${mm}-01&date_to=${selectedYear}-${mm}-${dd}`;
    } else {
      // The "Latest" tab is "what I just typed" — order by entry time
      // (created_at) so a just-added but earlier-dated row surfaces at the
      // top. The month tab stays date-scoped and date-sorted above.
      url += `&sort_by=created_at&sort_dir=desc`;
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

  // Exactly the years the ledger holds — the same source as the Reports
  // pickers, so the two cannot disagree about which years exist.
  //
  // This replaced a rolling five-year window (`currentYear - i`), which was
  // not merely cosmetic here: `/api/dashboard/summary` is where an old row
  // first surfaces as a NUMBER, so a household that imported a 1984 statement
  // could see the total and never select the year that produced it. The trend
  // request is hardcoded `months=12`, so a wider picker costs nothing on the
  // wire.
  //
  // `selectedYear` is unioned in because it is PERSISTED in localStorage and
  // can therefore be stale before the first render — restored from a backup,
  // or simply the year whose last row was just deleted. A Select holding a
  // value with no matching item renders a blank trigger, silently.
  const { years: ledgerYears } = useReportYears();
  const yearOptions = yearOptionsFrom(ledgerYears, selectedYear);

  /* ── Derived ── */

  const totalIncome = summary?.total_income ?? 0;
  const totalExpense = summary?.total_spent ?? 0;
  const totalBalance = totalIncome - totalExpense;

  // The backend returns `trend` newest-first (dashboard_handlers.go walks
  // backwards from the anchor with `AddDate(0, -i, 0)`), so reverse to
  // ascending order before slicing — the bar chart renders left-to-right
  // chronologically with the most recent month on the right edge.
  //
  // Date is stored as an ISO-8601 `"YYYY-MM-01"` string so `formatMonthTick`
  // / `formatMonthLabel` from `lib/dates` can parse it. Bare month
  // abbreviations like `"Jan"` collapsed same-month buckets from different
  // years into one category on 12M views that crossed a year boundary.
  const chartData = useMemo(() => {
    const sorted = [...trend].reverse();
    const sliced = cashFlowView === '6m' ? sorted.slice(-6) : sorted;
    return sliced.map((item) => ({
      date: `${item.year}-${String(item.month).padStart(2, '0')}-01`,
      income: item.total_income,
      expense: item.total_spent,
    }));
  }, [trend, cashFlowView]);

  const totalCategorySpent = categories.reduce((sum, cat) => sum + cat.total, 0);

  const overBudgetCount = categories.filter((cat) => cat.over).length;

  const hasMoreCategories =
    categories.length > DASHBOARD_CATEGORY_COLLAPSED_LIMIT;

  const gaugeData = useMemo(() => {
    const visibleCats = categoriesExpanded
      ? categories
      : categories.slice(0, DASHBOARD_CATEGORY_COLLAPSED_LIMIT);
    return visibleCats.map((cat) => ({
      id: cat.id,
      name: cat.name,
      value: cat.total,
      color: getCategoryColorVar({ id: cat.id }),
      limit: cat.limit,
      over: cat.over,
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
        <div className="flex flex-wrap items-center gap-2">
          <Button
            variant="outline"
            size="sm"
            className="h-9"
            onClick={() => {
              // Rebuild `now` on click so a tab open past midnight jumps
              // to the *actual* current month, not the one captured at
              // initial render.
              const today = new Date();
              setSelectedYear(today.getFullYear());
              setSelectedMonth(today.getMonth() + 1);
            }}
            disabled={
              selectedYear === now.getFullYear() &&
              selectedMonth === now.getMonth() + 1
            }
          >
            Today
          </Button>
          <Select value={String(selectedMonth)} onValueChange={(v) => setSelectedMonth(Number(v))}>
            <SelectTrigger className="h-9 w-[140px]" aria-label="Month">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectGroup>
                {MONTH_NAMES_FULL.map((m, i) => (
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
                  <SelectItem key={y.value} value={y.value}>{y.label}</SelectItem>
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
            // Not negated. KpiCard renders the arrow and the signed magnitude
            // with no good/bad colouring, so flipping the sign here did not
            // convey "spending more is bad" — it just reported a 15% rise in
            // spending as "↓ -15.0% vs last month".
            delta={toDelta(expenseDelta)}
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
              dataKey="date"
              tickLine={false}
              axisLine={false}
              tickMargin={8}
              // Was a hardcoded `0`, and this call RETURNS 0 for every input
              // this chart can produce — the trend request is hardcoded
              // `months=12` and the toggle only narrows it to 6, both at or
              // under `MAX_UNTHINNED_MONTH_TICKS`. So it changes nothing today
              // and is not what made twelve labels fit a 208px plot at phone
              // width; the rotation and the 9px tick did that.
              //
              // Kept deliberately: it is the seam that stops a hardcoded `0`
              // being wrong the day the window grows past twelve, and it makes
              // this axis answerable by the same rule as the six in Reports
              // rather than by a local constant nobody re-derives.
              interval={monthAxisInterval(chartData.length)}
              angle={MONTH_TICK_ANGLE}
              textAnchor="end"
              height={48}
              tick={MONTH_TICK}
              padding={ROTATED_MONTH_TICK_PADDING}
              tickFormatter={formatMonthTick}
            />
            <ChartTooltip
              content={
                <ChartTooltipContent
                  hideIndicator
                  labelFormatter={formatMonthLabel}
                />
              }
            />
            <Bar dataKey="income" fill="url(#fillIncome)" stroke="var(--color-income)" strokeOpacity={0.3} radius={[4, 4, 0, 0]} />
            <Bar dataKey="expense" fill="url(#fillExpense)" stroke="var(--color-expense)" strokeOpacity={0.3} radius={[4, 4, 0, 0]} />
          </BarChart>
        </ChartContainer>
      </ChartCard>

      {/* Category breakdown + Recent Transactions — side by side.
          Both cards carry `min-w-0`: a grid item defaults to min-width:auto, so
          anything wide inside (the transactions table, a future chart) becomes
          the track's min-content and widens the whole page. This grid happened
          to measure clean at 390px with current data, but the report tabs with
          the same shape did not — the class is what makes it not depend on the
          data. See the note in OverviewTab. */}
      <div className="grid grid-cols-1 gap-3 lg:grid-cols-2">
        <Card className="min-w-0">
          <CardHeader className="flex flex-row flex-wrap items-center justify-between gap-3 space-y-0">
            <div>
              <CardTitle className="text-base font-semibold">Spending by Category</CardTitle>
              <CardDescription className="text-xs text-muted-foreground">
                {MONTH_NAMES_FULL[selectedMonth - 1]} {selectedYear}
              </CardDescription>
            </div>
            <div className="flex items-center gap-2">
              {overBudgetCount > 0 && (
                <Badge variant="warning" className="gap-1">
                  <AlertTriangle className="h-3.5 w-3.5" aria-hidden="true" />
                  {overBudgetCount} over budget
                </Badge>
              )}
              <span className="font-mono text-lg font-semibold tabular-nums">
                {formatFull(totalCategorySpent)}
              </span>
            </div>
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
                  const budgetPct = slice.limit != null && slice.limit > 0
                    ? Math.round((slice.value / slice.limit) * 100)
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
                      {slice.limit != null && (
                        <div className="flex flex-col gap-1">
                          <div className="flex items-center justify-between text-xs text-muted-foreground">
                            <span>
                              {formatFull(slice.value)} / {formatFull(slice.limit)} · {budgetPct}%
                            </span>
                            {slice.over && (
                              <span className="flex items-center gap-1 font-medium text-amber-600 dark:text-amber-500">
                                <AlertTriangle className="h-3.5 w-3.5" aria-hidden="true" />
                                over {formatFull(slice.value - slice.limit)}
                              </span>
                            )}
                          </div>
                          <div className="h-1.5 w-full rounded-full bg-muted">
                            <div
                              className={cn(
                                'h-full rounded-full transition-all',
                                slice.over ? 'bg-amber-500' : 'bg-primary',
                              )}
                              style={{ width: `${Math.min(budgetPct, 100)}%` }}
                            />
                          </div>
                        </div>
                      )}
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

        <Card className="min-w-0">
          <CardHeader className="flex flex-row flex-wrap items-center justify-between gap-3 space-y-0">
            <CardTitle className="text-base font-semibold">Recent Transactions</CardTitle>
            <div className="flex items-center gap-2">
              <ButtonGroup>
                <Button
                  variant={!showLatest ? 'secondary' : 'outline'}
                  size="sm"
                  className="h-8 px-3 text-xs"
                  onClick={() => setShowLatest(false)}
                >
                  {MONTH_NAMES_SHORT[selectedMonth - 1]}
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
            ) : isMobile ? (
              /*
                A four-column table in a 293px box is the pattern the research
                says nobody ships, and this is the first screen the owner lands
                on. `role="list"` is explicit because Tailwind's preflight
                strips the marker and Safari/VoiceOver drop the role with it.
              */
              <ul
                role="list"
                aria-label="Recent transactions"
                className="flex flex-col divide-y divide-border"
              >
                {recentTransactions.map((tx) => (
                  <RecentTransactionCard
                    key={tx.id}
                    transaction={tx}
                    baseCode={baseCurrency}
                  />
                ))}
              </ul>
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
                        {/* The phone branch above renders
                            `TransactionDescription`; this desktop branch was
                            left rendering a bare span, so one imported row with
                            an unbroken token still stretched the table.

                            NOT that component: its mechanism is `min-w-0` +
                            `truncate`, which works in a FLEX row (the
                            component's own docblock says so) and not in a
                            table, because a `white-space: nowrap` cell
                            contributes its full text width as the column's
                            min-content. Measured with that idiom here,
                            `max-w-md` became the column's FLOOR at 448px inside
                            a card 384.5px wide at 1024 — 302px of overflow,
                            worse than the bug.

                            AND NO `max-w-*` HERE, unlike the two Reports tables
                            — re-measured rather than reasoned about, because
                            the obvious argument is wrong. `overflow-wrap:
                            anywhere` drops MIN-content to one character, so a
                            cap "cannot floor anything"; measured, it still
                            does. Chrome's auto table layout sizes this column
                            from its max-content capped by `max-width`, and a
                            long token makes that the cap itself: adding
                            `max-w-md` back pinned the column at exactly 448px
                            and the table overflowed its card by 302px at 1024,
                            174px at 1280, 87px at 1440. Without it the same
                            table measures 0px of overflow at 768, 900, 1024,
                            1150, 1280, 1440 and 1920, with the column adapting
                            145px -> 465px.

                            The Reports cells keep their caps because theirs
                            never bind — 12rem at phone width against a 107px
                            column, and 28rem at desktop against 383px. This
                            card is the narrow one (384.5px at 1024), which is
                            the whole difference.

                            `line-clamp-2` bounds the height that wrapping would
                            otherwise trade the width problem for, and
                            `transactionLabel` is the shared empty-description
                            fallback this branch never had. */}
                        <TableCell>
                          <div className="flex flex-col">
                            <div
                              className="line-clamp-2 font-medium [overflow-wrap:anywhere]"
                              title={transactionLabel(tx)}
                            >
                              {transactionLabel(tx)}
                            </div>
                            {/* The CATEGORY name needs the same bound as the
                                description above it, and this was found by
                                measurement rather than reasoning: with the
                                description bounded, the column still pinned at
                                473.7px and the table overflowed its card by
                                119px at 1440. The cause was this line — a
                                category name is user-supplied and the server
                                caps it at 100 characters
                                (`internal/api/limits.go`), so one long one
                                sets the column's min-content on its own and
                                the fix one line up does nothing. One clamped
                                line, because a category name is an
                                identifier rather than a sentence. */}
                            <span className="line-clamp-1 text-sm text-muted-foreground [overflow-wrap:anywhere]">
                              {tx.category_name}
                            </span>
                          </div>
                        </TableCell>
                        <TableCell className="text-sm text-muted-foreground">
                          {tx.date}
                        </TableCell>
                        <TableCell className="text-right">
                          <span
                            className={cn(
                              'text-sm font-semibold tabular-nums',
                              tx.category_type === TYPE_INCOME &&
                                'text-emerald-500',
                            )}
                          >
                            {tx.category_type === TYPE_INCOME ? '+' : '-'}
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
