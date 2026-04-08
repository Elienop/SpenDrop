import { useState, useEffect } from 'react';
import {
  ResponsiveContainer,
  BarChart,
  Bar,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip,
  ReferenceLine,
  PieChart,
  Pie,
  Cell,
} from 'recharts';
import { useDashboard } from '../hooks/useDashboard';
import { useChartTheme } from '../hooks/useChartTheme';
import { useChartPatterns, ChartPatternDefs } from '../hooks/useChartPatterns';
import { useAuth } from '../hooks/useAuth';
import { ChartTooltip } from '../components/ChartTooltip';
import { api } from '../api/client';
import type { Transaction, PaginatedResponse } from '../api/types';
import styles from '../styles/Dashboard.module.css';

function formatCurrency(amount: number): string {
  return amount.toLocaleString('en-US', {
    style: 'currency',
    currency: 'USD',
  });
}

function formatCompact(amount: number): string {
  return '$' + Math.abs(amount).toLocaleString('en-US', {
    minimumFractionDigits: 0,
    maximumFractionDigits: 0,
  });
}

function pctChange(current: number, previous: number): number | null {
  if (previous === 0) return null;
  return ((current - previous) / Math.abs(previous)) * 100;
}

const MONTHS = [
  'January', 'February', 'March', 'April', 'May', 'June',
  'July', 'August', 'September', 'October', 'November', 'December',
];

const SHORT_MONTHS = [
  'Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun',
  'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec',
];

type CashFlowView = 'monthly' | 'yearly';

export function Dashboard() {
  const { user } = useAuth();
  const now = new Date();
  const [selectedYear, setSelectedYear] = useState(now.getFullYear());
  const [selectedMonth, setSelectedMonth] = useState(now.getMonth() + 1);
  const [cashFlowView, setCashFlowView] = useState<CashFlowView>('monthly');
  const { summary, trend, categories, loading, error } = useDashboard(
    selectedYear,
    selectedMonth,
  );
  const chartTheme = useChartTheme();
  const patterns = useChartPatterns();
  const [recentTransactions, setRecentTransactions] = useState<Transaction[]>([]);

  useEffect(() => {
    api
      .get<PaginatedResponse<Transaction>>('transactions?per_page=6')
      .then((data) => setRecentTransactions(data.transactions))
      .catch(() => { /* silent — non-critical */ });
  }, []);

  const currentYear = now.getFullYear();
  const yearOptions = Array.from({ length: 5 }, (_, i) => currentYear - i);

  // --- Derived data ---

  const totalIncome = summary?.total_income ?? 0;
  const totalExpense = summary?.total_spent ?? 0;
  const totalBalance = totalIncome - totalExpense;

  // Cash flow chart data
  const monthlyChartData = [...trend].reverse().map((item) => ({
    name: SHORT_MONTHS[item.month - 1],
    income: item.total_income,
    expense: -item.total_spent,
  }));

  const yearlyChartData = (() => {
    const byYear = new Map<number, { income: number; expense: number }>();
    for (const item of trend) {
      const existing = byYear.get(item.year) || { income: 0, expense: 0 };
      existing.income += item.total_income;
      existing.expense += item.total_spent;
      byYear.set(item.year, existing);
    }
    return Array.from(byYear.entries())
      .sort(([a], [b]) => a - b)
      .slice(-5)
      .map(([year, data]) => ({
        name: String(year),
        income: data.income,
        expense: -data.expense,
      }));
  })();

  const chartData = cashFlowView === 'monthly' ? monthlyChartData : yearlyChartData;

  // Categories donut data
  const pieData = categories.slice(0, 6).map((cat) => ({
    name: cat.name,
    value: cat.total,
    color: cat.color,
  }));

  const totalCategorySpent = categories.reduce((sum, cat) => sum + cat.total, 0);

  // Pattern configs for charts
  const categoryPatterns = pieData.map((cat, i) => patterns.getCategoryPattern(i, cat.color));
  const categoryDefs = patterns.getCategoryDefs(pieData);

  const cfPatternStyles = patterns.buildStyleMap([
    patterns.cashFlow.income,
    patterns.cashFlow.expense,
  ]);

  const catPatternStyles = patterns.buildStyleMap(categoryPatterns);

  // KPI computations
  const savingsRate = totalIncome > 0
    ? ((totalIncome - totalExpense) / totalIncome * 100)
    : 0;

  const budgetUsage = summary && summary.budget > 0
    ? (totalExpense / summary.budget * 100)
    : 0;

  // Delta from previous month (find in trend data)
  const prevMonthTrend = (() => {
    const prevM = selectedMonth === 1 ? 12 : selectedMonth - 1;
    const prevY = selectedMonth === 1 ? selectedYear - 1 : selectedYear;
    return trend.find(t => t.year === prevY && t.month === prevM);
  })();

  const balanceDelta = prevMonthTrend
    ? pctChange(totalBalance, prevMonthTrend.total_income - prevMonthTrend.total_spent)
    : null;

  const incomeDelta = prevMonthTrend
    ? pctChange(totalIncome, prevMonthTrend.total_income)
    : null;

  // --- Loading state ---
  if (loading) {
    return (
      <div className={styles.page}>
        <div className={styles.header}>
          <div>
            <div className={styles.skeletonHeading} />
            <div className={styles.skeletonText} style={{ marginTop: 8, width: '40%' }} />
          </div>
        </div>
        <div className={styles.kpiRow}>
          {[1, 2, 3, 4].map((n) => (
            <div key={n} className={styles.kpiCard}>
              <div className={styles.skeletonText} style={{ width: '50%' }} />
              <div className={styles.skeletonHeading} />
              <div className={styles.skeletonText} style={{ width: '70%' }} />
            </div>
          ))}
        </div>
        <div className={styles.contentGrid}>
          <div className={styles.card}>
            <div className={styles.skeletonText} style={{ width: '30%' }} />
            <div className={styles.skeleton} style={{ height: 200, marginTop: 16 }} />
          </div>
          <div className={styles.card}>
            <div className={styles.skeletonText} style={{ width: '40%' }} />
            <div className={styles.skeleton} style={{ height: 200, marginTop: 16 }} />
          </div>
        </div>
      </div>
    );
  }

  // --- Error state ---
  if (error) {
    return (
      <div className={styles.page}>
        <div className={styles.header}>
          <h1>Dashboard</h1>
        </div>
        <div className={styles.errorState} role="alert">
          <p>{error}</p>
          <button
            className={styles.retryButton}
            onClick={() => window.location.reload()}
          >
            Retry
          </button>
        </div>
      </div>
    );
  }

  return (
    <div className={styles.page}>
      {/* Header */}
      <div className={styles.header}>
        <div>
          <h1>Welcome back, {user?.display_name ?? 'there'}</h1>
          <p className={styles.subtitle}>
            Here's what's happening with your finances.
          </p>
        </div>
        <div className={styles.selectors}>
          <label htmlFor="dash-month" className="sr-only">Month</label>
          <select
            id="dash-month"
            aria-label="Month"
            className={styles.select}
            value={selectedMonth}
            onChange={(e) => setSelectedMonth(Number(e.target.value))}
          >
            {MONTHS.map((m, i) => (
              <option key={m} value={i + 1}>{m}</option>
            ))}
          </select>

          <label htmlFor="dash-year" className="sr-only">Year</label>
          <select
            id="dash-year"
            aria-label="Year"
            className={styles.select}
            value={selectedYear}
            onChange={(e) => setSelectedYear(Number(e.target.value))}
          >
            {yearOptions.map((y) => (
              <option key={y} value={y}>{y}</option>
            ))}
          </select>
        </div>
      </div>

      {/* ===== KPI Row ===== */}
      {summary && (
        <div className={styles.kpiRow}>
          {/* Total Balance — featured accent card */}
          <div className={`${styles.kpiCard} ${styles.featured}`}>
            <span className={styles.kpiLabel}>Total Balance</span>
            <span className={styles.kpiValue}>{formatCurrency(totalBalance)}</span>
            {balanceDelta != null && (
              <span className={balanceDelta >= 0 ? styles.kpiBadgePositive : styles.kpiBadgeNegative}>
                {balanceDelta >= 0 ? '\u2191' : '\u2193'}
                {Math.abs(balanceDelta).toFixed(1)}% vs last month
              </span>
            )}
          </div>

          {/* Income */}
          <div className={styles.kpiCard}>
            <span className={styles.kpiLabel}>Income</span>
            <span className={styles.kpiValue}>{formatCurrency(totalIncome)}</span>
            {incomeDelta != null && (
              <span className={incomeDelta >= 0 ? styles.kpiBadgePositive : styles.kpiBadgeNegative}>
                {incomeDelta >= 0 ? '\u2191' : '\u2193'}
                {Math.abs(incomeDelta).toFixed(1)}% vs last month
              </span>
            )}
          </div>

          {/* Expenses — with budget progress bar */}
          <div className={styles.kpiCard}>
            <span className={styles.kpiLabel}>Expenses</span>
            <span className={styles.kpiValue}>{formatCurrency(totalExpense)}</span>
            {summary.budget > 0 && (
              <>
                <span className={styles.kpiSub}>
                  {formatCompact(totalExpense)} of {formatCompact(summary.budget)} budget
                </span>
                <div className={styles.kpiProgress}>
                  <div
                    className={styles.kpiProgressFill}
                    style={{
                      width: `${Math.min(100, budgetUsage)}%`,
                      background: budgetUsage > 100
                        ? 'var(--color-expense)'
                        : budgetUsage > 85
                          ? 'var(--color-warning)'
                          : 'var(--color-primary)',
                    }}
                  />
                </div>
              </>
            )}
          </div>

          {/* Savings Rate */}
          <div className={styles.kpiCard}>
            <span className={styles.kpiLabel}>Savings Rate</span>
            <span className={styles.kpiValue}>
              {savingsRate.toFixed(1)}<span className={styles.kpiUnit}>%</span>
            </span>
            <span className={styles.kpiSub}>
              {formatCompact(summary.savings_ytd)} saved this year
            </span>
          </div>
        </div>
      )}

      {/* ===== Content Grid: Cash Flow + Categories ===== */}
      <div className={styles.contentGrid}>
        {/* Cash Flow */}
        <div className={styles.card}>
          <div className={styles.cardHeader}>
            <span className={styles.cardTitle}>Cash Flow</span>
            <div className={styles.cfControls}>
              <div className={styles.cfLegend}>
                <div className={styles.legendItem}>
                  <div className={styles.legendDot} style={patterns.cashFlow.income.legendStyle} />
                  <span>Income</span>
                </div>
                <div className={styles.legendItem}>
                  <div className={styles.legendDot} style={patterns.cashFlow.expense.legendStyle} />
                  <span>Expenses</span>
                </div>
              </div>
              <div className={styles.cfToggle}>
                <button
                  className={`${styles.cfToggleBtn} ${cashFlowView === 'monthly' ? styles.cfToggleBtnActive : ''}`}
                  onClick={() => setCashFlowView('monthly')}
                >
                  6M
                </button>
                <button
                  className={`${styles.cfToggleBtn} ${cashFlowView === 'yearly' ? styles.cfToggleBtnActive : ''}`}
                  onClick={() => setCashFlowView('yearly')}
                >
                  1Y
                </button>
              </div>
            </div>
          </div>

          <div className={styles.cfChartWrap}>
            <ResponsiveContainer width="100%" height="100%">
              <BarChart data={chartData} margin={{ top: 10, right: 10, bottom: 0, left: 0 }}>
                <ChartPatternDefs patterns={patterns.cashFlowDefs} />
                <CartesianGrid
                  strokeDasharray="3 3"
                  stroke={chartTheme.gridStroke}
                  strokeOpacity={0.4}
                  vertical={false}
                />
                <XAxis
                  dataKey="name"
                  stroke={chartTheme.axisStroke}
                  tick={{ fill: chartTheme.axisStroke, fontFamily: 'Inter Variable', fontSize: 12 }}
                  tickLine={false}
                  axisLine={false}
                />
                <YAxis
                  stroke={chartTheme.axisStroke}
                  tick={{ fill: chartTheme.axisStroke, fontFamily: 'Inter Variable', fontSize: 12 }}
                  tickLine={false}
                  axisLine={false}
                  tickFormatter={(v: number) => `$${Math.abs(v / 1000).toFixed(0)}k`}
                />
                <Tooltip
                  content={<ChartTooltip patternStyles={cfPatternStyles} />}
                  cursor={{ fill: chartTheme.hoverBg }}
                />
                <ReferenceLine y={0} stroke={chartTheme.axisStroke} />
                <Bar
                  dataKey="income"
                  fill={patterns.cashFlow.income.fill}
                  radius={[4, 4, 0, 0]}
                  name="Income"
                />
                <Bar
                  dataKey="expense"
                  fill={patterns.cashFlow.expense.fill}
                  stroke={patterns.cashFlow.expense.stroke}
                  strokeWidth={patterns.cashFlow.expense.strokeWidth}
                  radius={[4, 4, 0, 0]}
                  name="Expenses"
                />
              </BarChart>
            </ResponsiveContainer>
          </div>
        </div>

        {/* Spending by Category */}
        <div className={styles.card}>
          <div className={styles.cardHeader}>
            <span className={styles.cardTitle}>Spending by Category</span>
            <a href="/categories" className={styles.cardLink}>View all &rarr;</a>
          </div>
          <div className={styles.catLayout}>
            <div className={styles.donutWrap}>
              <ResponsiveContainer width="100%" height="100%">
                <PieChart>
                  <ChartPatternDefs patterns={categoryDefs} />
                  <Pie
                    data={pieData}
                    dataKey="value"
                    nameKey="name"
                    cx="50%"
                    cy="50%"
                    innerRadius={55}
                    outerRadius={70}
                    cornerRadius={3}
                    paddingAngle={2}
                    stroke="none"
                  >
                    {pieData.map((_, i) => (
                      <Cell
                        key={i}
                        fill={categoryPatterns[i].fill}
                        stroke={categoryPatterns[i].stroke ?? 'none'}
                        strokeWidth={categoryPatterns[i].strokeWidth ?? 0}
                      />
                    ))}
                  </Pie>
                  <Tooltip
                    content={<ChartTooltip patternStyles={catPatternStyles} />}
                    cursor={{ fill: chartTheme.hoverBg }}
                  />
                </PieChart>
              </ResponsiveContainer>
              <div className={styles.donutCenter}>
                <span className={styles.donutTotal}>{formatCompact(totalCategorySpent)}</span>
                <span className={styles.donutSub}>Total Spent</span>
              </div>
            </div>
            <div className={styles.catList}>
              {categories.slice(0, 6).map((cat, i) => (
                <div key={cat.id} className={styles.catItem}>
                  <div
                    className={styles.catDot}
                    style={categoryPatterns[i]?.legendStyle ?? { background: cat.color }}
                  />
                  <span className={styles.catName}>{cat.name}</span>
                  <span className={styles.catPct}>
                    {totalCategorySpent > 0 ? Math.round((cat.total / totalCategorySpent) * 100) : 0}%
                  </span>
                  <div className={styles.catBar}>
                    <div
                      className={styles.catBarFill}
                      style={{
                        width: totalCategorySpent > 0
                          ? `${(cat.total / totalCategorySpent) * 100}%`
                          : '0%',
                        background: cat.color,
                      }}
                    />
                  </div>
                  <span className={styles.catAmount}>{formatCompact(cat.total)}</span>
                </div>
              ))}
            </div>
          </div>
        </div>
      </div>

      {/* ===== Recent Transactions ===== */}
      <div className={styles.card}>
        <div className={styles.cardHeader}>
          <span className={styles.cardTitle}>Recent Transactions</span>
          <a href="/transactions" className={styles.cardLink}>View all &rarr;</a>
        </div>
        {recentTransactions.length === 0 ? (
          <p className={styles.emptyState}>No recent transactions</p>
        ) : (
          <div className={styles.txList}>
            {recentTransactions.slice(0, 6).map((tx) => (
              <div key={tx.id} className={styles.txItem}>
                <div
                  className={styles.txIcon}
                  style={{
                    background: `color-mix(in srgb, ${tx.category_color} 15%, transparent)`,
                    color: tx.category_color,
                  }}
                >
                  {tx.category_name.charAt(0)}
                </div>
                <div className={styles.txInfo}>
                  <div className={styles.txName}>{tx.description}</div>
                  <div className={styles.txCategory}>{tx.category_name}</div>
                </div>
                <div className={styles.txRight}>
                  <div
                    className={`${styles.txAmount} ${
                      tx.category_type === 'expense' ? styles.expense : styles.income
                    }`}
                  >
                    {tx.category_type === 'expense' ? '-' : '+'}
                    {formatCurrency(tx.amount)}
                  </div>
                  <div className={styles.txDate}>
                    {new Date(tx.date).toLocaleDateString('en-US', {
                      month: 'short',
                      day: 'numeric',
                      year: 'numeric',
                    })}
                  </div>
                </div>
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}
