import { useState, useEffect } from 'react';
import {
  ResponsiveContainer,
  BarChart,
  Bar,
  XAxis,
  YAxis,
  Tooltip,
  CartesianGrid,
  PieChart,
  Pie,
  Cell,
  Legend,
} from 'recharts';
import { format } from 'date-fns';
import { useDashboard } from '../hooks/useDashboard';
import { KPICard } from '../components/KPICard';
import { api } from '../api/client';
import type { Transaction, PaginatedResponse } from '../api/types';
import styles from '../styles/Dashboard.module.css';

function formatCurrency(amount: number): string {
  return amount.toLocaleString('en-US', {
    style: 'currency',
    currency: 'USD',
  });
}

const MONTHS = [
  'January', 'February', 'March', 'April', 'May', 'June',
  'July', 'August', 'September', 'October', 'November', 'December',
];

export function Dashboard() {
  const now = new Date();
  const [selectedYear, setSelectedYear] = useState(now.getFullYear());
  const [selectedMonth, setSelectedMonth] = useState(now.getMonth() + 1);
  const { summary, trend, categories, loading, error } = useDashboard(
    selectedYear,
    selectedMonth,
  );
  const [recentTransactions, setRecentTransactions] = useState<Transaction[]>(
    [],
  );

  useEffect(() => {
    api
      .get<PaginatedResponse<Transaction>>('transactions?per_page=5')
      .then((data) => setRecentTransactions(data.transactions))
      .catch(() => {
        /* silent — non-critical */
      });
  }, []);

  const trendData = trend.map((item) => ({
    name: `${MONTHS[item.month - 1]?.slice(0, 3)} ${item.year}`,
    spent: item.total_spent,
    income: item.total_income,
  }));

  const pieData = categories.slice(0, 6).map((cat) => ({
    name: cat.name,
    value: cat.total,
    color: cat.color,
  }));

  const currentYear = now.getFullYear();
  const yearOptions = Array.from({ length: 5 }, (_, i) => currentYear - i);

  return (
    <div className={styles.page}>
      <div className={styles.header}>
        <h1>Dashboard</h1>
        <div className={styles.selectors}>
          <label htmlFor="dash-month" className="sr-only">
            Month
          </label>
          <select
            id="dash-month"
            aria-label="Month"
            className={styles.select}
            value={selectedMonth}
            onChange={(e) => setSelectedMonth(Number(e.target.value))}
          >
            {MONTHS.map((m, i) => (
              <option key={m} value={i + 1}>
                {m}
              </option>
            ))}
          </select>

          <label htmlFor="dash-year" className="sr-only">
            Year
          </label>
          <select
            id="dash-year"
            aria-label="Year"
            className={styles.select}
            value={selectedYear}
            onChange={(e) => setSelectedYear(Number(e.target.value))}
          >
            {yearOptions.map((y) => (
              <option key={y} value={y}>
                {y}
              </option>
            ))}
          </select>
        </div>
      </div>

      {loading && <div className={styles.loading}>Loading dashboard...</div>}

      {error && (
        <div className={styles.error} role="alert">
          {error}
        </div>
      )}

      {summary && (
        <>
          <div className={styles.kpiRow}>
            <KPICard
              label="Budget"
              value={formatCurrency(summary.budget)}
              color="var(--color-info)"
            />
            <KPICard
              label="Spent"
              value={formatCurrency(summary.total_spent)}
              color="var(--color-danger)"
            />
            <KPICard
              label="Remaining"
              value={formatCurrency(summary.remaining)}
              color={
                summary.remaining >= 0
                  ? 'var(--color-primary)'
                  : 'var(--color-danger)'
              }
            />
            <KPICard
              label="Savings Progress"
              value={`${summary.savings_goal_progress.toFixed(1)}%`}
              color="var(--color-warning)"
            />
          </div>

          <div className={styles.chartsRow}>
            <div className={styles.chartCard}>
              <h2 className={styles.chartTitle}>Spending Trend</h2>
              <div className={styles.chartContainer}>
                <ResponsiveContainer width="100%" height="100%">
                  <BarChart data={trendData}>
                    <CartesianGrid
                      strokeDasharray="3 3"
                      stroke="var(--border-color)"
                    />
                    <XAxis dataKey="name" stroke="var(--color-text-secondary)" />
                    <YAxis stroke="var(--color-text-secondary)" />
                    <Tooltip />
                    <Legend />
                    <Bar dataKey="spent" fill="var(--color-danger)" name="Spent" />
                    <Bar
                      dataKey="income"
                      fill="var(--color-primary)"
                      name="Income"
                    />
                  </BarChart>
                </ResponsiveContainer>
              </div>
            </div>

            <div className={styles.chartCard}>
              <h2 className={styles.chartTitle}>Category Breakdown</h2>
              <div className={styles.chartContainer}>
                <ResponsiveContainer width="100%" height="100%">
                  <PieChart>
                    <Pie
                      data={pieData}
                      dataKey="value"
                      nameKey="name"
                      cx="50%"
                      cy="50%"
                      outerRadius={100}
                    >
                      {pieData.map((entry) => (
                        <Cell key={entry.name} fill={entry.color} />
                      ))}
                    </Pie>
                    <Tooltip />
                    <Legend />
                  </PieChart>
                </ResponsiveContainer>
              </div>
            </div>
          </div>
        </>
      )}

      <div className={styles.recentSection}>
        <h2 className={styles.recentTitle}>Recent Transactions</h2>
        {recentTransactions.length === 0 ? (
          <p className={styles.emptyState}>No recent transactions</p>
        ) : (
          <div className={styles.recentList}>
            {recentTransactions.map((tx) => (
              <div key={tx.id} className={styles.recentItem}>
                <div className={styles.recentItemLeft}>
                  <span className={styles.recentItemDesc}>
                    {tx.description}
                  </span>
                  <span className={styles.recentItemDate}>
                    {format(new Date(tx.date), 'MMM d, yyyy')}
                  </span>
                </div>
                <span
                  className={`${styles.recentItemAmount} ${
                    tx.category_type === 'expense'
                      ? styles.expense
                      : styles.income
                  }`}
                >
                  {tx.category_type === 'expense' ? '-' : '+'}
                  {formatCurrency(tx.amount)}
                </span>
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}
