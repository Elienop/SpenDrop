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
  const now = new Date();
  const [selectedYear, setSelectedYear] = useState(now.getFullYear());
  const [selectedMonth, setSelectedMonth] = useState(now.getMonth() + 1);
  const [cashFlowView, setCashFlowView] = useState<CashFlowView>('monthly');
  const { summary, trend, categories, loading, error } = useDashboard(
    selectedYear,
    selectedMonth,
  );
  const chartTheme = useChartTheme();
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

  const totalBalance = summary
    ? summary.total_income - summary.total_spent
    : 0;

  const deltaFromLastMonth = summary ? summary.remaining : 0;

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

  const totalIncome = summary?.total_income ?? 0;
  const totalExpense = summary?.total_spent ?? 0;

  // Categories donut data
  const pieData = categories.slice(0, 6).map((cat) => ({
    name: cat.name,
    value: cat.total,
    color: cat.color,
  }));

  const totalCategorySpent = categories.reduce((sum, cat) => sum + cat.total, 0);

  // Savings goal helpers
  const savingsProgress = summary?.savings_goal_progress ?? 0;
  const savingsColor =
    savingsProgress >= 75
      ? 'var(--color-income)'
      : savingsProgress >= 25
        ? 'var(--color-warning)'
        : 'var(--color-expense)';

  const savingsRemaining = summary
    ? Math.max(0, summary.savings_goal - summary.savings_ytd)
    : 0;

  // --- Loading state ---
  if (loading) {
    return (
      <div className={styles.page}>
        <div className={styles.header}>
          <h1>Dashboard</h1>
        </div>
        <div className={styles.heroRow}>
          <div className={styles.heroCard}>
            <div className={styles.skeletonText} />
            <div className={styles.skeletonHeading} />
            <div className={styles.skeletonText} />
          </div>
          <div className={styles.heroCard}>
            <div className={styles.skeletonText} />
            <div className={styles.skeletonHeading} />
            <div className={styles.skeletonText} />
          </div>
        </div>
        <div className={styles.cashFlow}>
          <div className={styles.skeletonText} style={{ width: '30%' }} />
          <div className={styles.skeleton} style={{ height: 200, marginTop: 16 }} />
        </div>
        <div className={styles.bottomGrid}>
          <div className={styles.card}>
            <div className={styles.skeletonText} />
            <div className={styles.skeleton} style={{ height: 160, marginTop: 16 }} />
          </div>
          <div className={styles.card}>
            <div className={styles.skeletonText} />
            <div className={styles.skeleton} style={{ height: 160, marginTop: 16 }} />
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
      {/* Header with month/year selectors */}
      <div className={styles.header}>
        <h1>Dashboard</h1>
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

      {/* ===== Hero Row ===== */}
      {summary && (
        <div className={styles.heroRow}>
          {/* Total Balance */}
          <div className={styles.heroCard}>
            <span className={styles.heroLabel}>Total Balance</span>
            <span className={styles.heroValue}>{formatCurrency(totalBalance)}</span>
            <span
              className={styles.heroDelta}
              style={{
                color: deltaFromLastMonth >= 0
                  ? 'var(--color-income)'
                  : 'var(--color-expense)',
              }}
            >
              {deltaFromLastMonth >= 0 ? '+' : '-'}
              {formatCompact(deltaFromLastMonth)} from last month
            </span>
          </div>

          {/* Savings Goal */}
          <div className={styles.heroCard}>
            <span className={styles.heroLabel}>Savings Goal</span>
            <div className={styles.savingsRow}>
              <span className={styles.savingsPct} style={{ color: savingsColor }}>
                {savingsProgress.toFixed(1)}%
              </span>
              <span className={styles.savingsAmounts}>
                {formatCompact(summary.savings_ytd)} / {formatCompact(summary.savings_goal)}
              </span>
            </div>
            <div className={styles.savingsBar}>
              <div
                className={styles.savingsBarFill}
                style={{
                  width: `${Math.min(100, savingsProgress)}%`,
                  background: savingsColor,
                }}
              />
            </div>
            <span className={styles.savingsDetail}>
              {formatCompact(savingsRemaining)} remaining to reach goal
            </span>
          </div>
        </div>
      )}

      {/* ===== Cash Flow Section ===== */}
      <div className={styles.cashFlow}>
        <div className={styles.cfTopBar}>
          <span className={styles.cfTitle}>Cash Flow</span>
          <div className={styles.cfTopRight}>
            <div className={styles.cfStatsInline}>
              <div className={styles.cfStatInline}>
                <span
                  className={styles.cfStatDot}
                  style={{ background: chartTheme.incomeColor }}
                />
                <span className={styles.cfStatLabel}>Income</span>
                <span className={styles.cfStatVal}>{formatCompact(totalIncome)}</span>
              </div>
              <div className={styles.cfStatInline}>
                <span
                  className={styles.cfStatDot}
                  style={{ background: chartTheme.expenseColor }}
                />
                <span className={styles.cfStatLabel}>Expenses</span>
                <span className={styles.cfStatVal}>{formatCompact(totalExpense)}</span>
              </div>
            </div>
            <div className={styles.cfToggle}>
              <button
                className={`${styles.cfToggleBtn} ${cashFlowView === 'monthly' ? styles.cfToggleBtnActive : ''}`}
                onClick={() => setCashFlowView('monthly')}
              >
                Monthly
              </button>
              <button
                className={`${styles.cfToggleBtn} ${cashFlowView === 'yearly' ? styles.cfToggleBtnActive : ''}`}
                onClick={() => setCashFlowView('yearly')}
              >
                Yearly
              </button>
            </div>
          </div>
        </div>

        <div className={styles.cfChartWrap}>
          <ResponsiveContainer width="100%" height="100%">
            <BarChart data={chartData} margin={{ top: 10, right: 10, bottom: 0, left: 0 }}>
              <CartesianGrid
                strokeDasharray="3 3"
                stroke={chartTheme.gridStroke}
                vertical={false}
              />
              <XAxis
                dataKey="name"
                stroke={chartTheme.axisStroke}
                tick={{ fontFamily: 'Inter Variable', fontSize: 12 }}
                tickLine={false}
                axisLine={false}
              />
              <YAxis
                stroke={chartTheme.axisStroke}
                tick={{ fontFamily: 'Inter Variable', fontSize: 12 }}
                tickLine={false}
                axisLine={false}
                tickFormatter={(v: number) => `$${Math.abs(v / 1000).toFixed(0)}k`}
              />
              <Tooltip content={<ChartTooltip />} />
              <ReferenceLine y={0} stroke={chartTheme.axisStroke} />
              <Bar
                dataKey="income"
                fill={chartTheme.incomeColor}
                radius={[4, 4, 0, 0]}
                name="Income"
              />
              <Bar
                dataKey="expense"
                fill={chartTheme.expenseColor}
                radius={[0, 0, 4, 4]}
                name="Expenses"
              />
            </BarChart>
          </ResponsiveContainer>
        </div>
      </div>

      {/* ===== Bottom Grid ===== */}
      <div className={styles.bottomGrid}>
        {/* Categories Donut */}
        <div className={styles.card}>
          <div className={styles.cardHeader}>
            <span className={styles.cardTitle}>Categories</span>
            <a href="/categories" className={styles.cardLink}>All &rarr;</a>
          </div>
          <div className={styles.catLayout}>
            <div className={styles.donutWrap}>
              <ResponsiveContainer width="100%" height="100%">
                <PieChart>
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
                    {pieData.map((entry) => (
                      <Cell key={entry.name} fill={entry.color} />
                    ))}
                  </Pie>
                  <Tooltip content={<ChartTooltip />} />
                </PieChart>
              </ResponsiveContainer>
              <div className={styles.donutCenter}>
                <span className={styles.donutTotal}>{formatCompact(totalCategorySpent)}</span>
                <span className={styles.donutSub}>total</span>
              </div>
            </div>
            <div className={styles.catList}>
              {categories.slice(0, 6).map((cat) => (
                <div key={cat.id} className={styles.catItem}>
                  <span className={styles.catDot} style={{ background: cat.color }} />
                  <span className={styles.catName}>{cat.name}</span>
                  <span className={styles.catAmount}>{formatCompact(cat.total)}</span>
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
                </div>
              ))}
            </div>
          </div>
        </div>

        {/* Recent Transactions */}
        <div className={styles.card}>
          <div className={styles.cardHeader}>
            <span className={styles.cardTitle}>Recent Transactions</span>
            <a href="/transactions" className={styles.cardLink}>View All &rarr;</a>
          </div>
          {recentTransactions.length === 0 ? (
            <p className={styles.emptyState}>No recent transactions</p>
          ) : (
            <table className={styles.txTable}>
              <thead>
                <tr>
                  <th>Name</th>
                  <th>Date</th>
                  <th>Category</th>
                  <th style={{ textAlign: 'right' }}>Amount</th>
                </tr>
              </thead>
              <tbody>
                {recentTransactions.slice(0, 6).map((tx) => (
                  <tr key={tx.id}>
                    <td className={styles.txName}>{tx.description}</td>
                    <td className={styles.txDate}>
                      {new Date(tx.date).toLocaleDateString('en-US', {
                        month: 'short',
                        day: 'numeric',
                        year: 'numeric',
                      })}
                    </td>
                    <td>
                      <span
                        className={styles.txBadge}
                        style={{
                          background: `color-mix(in srgb, ${tx.category_color} 15%, transparent)`,
                          color: tx.category_color,
                        }}
                      >
                        {tx.category_name}
                      </span>
                    </td>
                    <td
                      className={`${styles.txAmount} ${
                        tx.category_type === 'expense' ? styles.expense : styles.income
                      }`}
                    >
                      {tx.category_type === 'expense' ? '-' : '+'}
                      {formatCurrency(tx.amount)}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      </div>
    </div>
  );
}
