import { useState } from 'react';
import {
  ResponsiveContainer,
  BarChart,
  Bar,
  LineChart,
  Line,
  XAxis,
  YAxis,
  Tooltip,
  CartesianGrid,
  Legend,
} from 'recharts';
import {
  useYearOverYear,
  useCategoryTrends,
  useIncomeExpenses,
  useTopMerchants,
} from '../hooks/useReports';
import styles from '../styles/Reports.module.css';

const MONTH_NAMES = [
  'Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun',
  'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec',
];

const MONTH_FULL_NAMES = [
  'January', 'February', 'March', 'April', 'May', 'June',
  'July', 'August', 'September', 'October', 'November', 'December',
];

function formatCurrency(amount: number): string {
  return amount.toLocaleString('en-US', {
    style: 'currency',
    currency: 'USD',
  });
}

export function Reports() {
  const now = new Date();
  const thisYear = now.getFullYear();
  const [yoyYear, setYoyYear] = useState(thisYear);
  const [trendMonths, setTrendMonths] = useState(12);
  const [merchantYear, setMerchantYear] = useState(thisYear);
  const [merchantMonth, setMerchantMonth] = useState(now.getMonth() + 1);

  const yearOptions = Array.from({ length: 5 }, (_, i) => thisYear - i);

  const yoy = useYearOverYear(yoyYear);
  const catTrends = useCategoryTrends(trendMonths);
  const incExp = useIncomeExpenses(trendMonths);
  const merchants = useTopMerchants(merchantYear, merchantMonth);

  // --- Year-over-Year chart data ---
  const yoyData = yoy.data
    ? yoy.data.current.map((cur, i) => ({
        name: MONTH_NAMES[i],
        [`${yoy.data!.current_year} Expenses`]: cur.expenses,
        [`${yoy.data!.previous_year} Expenses`]: yoy.data!.previous[i].expenses,
      }))
    : [];

  // --- Income vs Expenses chart data ---
  const incExpData = incExp.data.map((entry) => ({
    name: `${MONTH_NAMES[entry.month - 1]} ${entry.year}`,
    income: entry.income,
    expenses: entry.expenses,
    net: entry.net,
  }));

  // --- Category Trends: top 6 expense categories by total ---
  const expenseCategories = catTrends.data
    .filter((c) => c.type === 'expense')
    .map((c) => ({
      ...c,
      totalSum: c.data.reduce((sum, d) => sum + d.total, 0),
    }))
    .sort((a, b) => b.totalSum - a.totalSum)
    .slice(0, 6);

  // Build category trend line data: one point per month
  const catTrendData: Record<string, unknown>[] = [];
  if (expenseCategories.length > 0) {
    const monthSet = new Set<string>();
    for (const cat of expenseCategories) {
      for (const d of cat.data) {
        monthSet.add(`${d.year}-${d.month}`);
      }
    }
    const sortedMonths = Array.from(monthSet).sort();
    for (const ym of sortedMonths) {
      const [y, m] = ym.split('-').map(Number);
      const point: Record<string, unknown> = {
        name: `${MONTH_NAMES[m - 1]} ${y}`,
      };
      for (const cat of expenseCategories) {
        const found = cat.data.find((d) => d.year === y && d.month === m);
        point[cat.name] = found ? found.total : 0;
      }
      catTrendData.push(point);
    }
  }

  return (
    <div className={styles.page}>
      <h1>Reports</h1>

      {/* Year-over-Year */}
      <section className={styles.section} aria-labelledby="yoy-heading">
        <div className={styles.sectionHeader}>
          <h2 id="yoy-heading">Year-over-Year Comparison</h2>
          <div className={styles.controls}>
            <label htmlFor="yoy-year" className="sr-only">Year</label>
            <select
              id="yoy-year"
              aria-label="Year"
              className={styles.select}
              value={yoyYear}
              onChange={(e) => setYoyYear(Number(e.target.value))}
            >
              {yearOptions.map((y) => (
                <option key={y} value={y}>{y} vs {y - 1}</option>
              ))}
            </select>
          </div>
        </div>
        {yoy.loading && <div className={styles.loading}>Loading...</div>}
        {yoy.error && <div className={styles.error} role="alert">{yoy.error}</div>}
        {yoy.data && (
          <div className={styles.chartContainer}>
            <ResponsiveContainer width="100%" height="100%">
              <BarChart data={yoyData}>
                <CartesianGrid strokeDasharray="3 3" stroke="var(--border-color)" />
                <XAxis dataKey="name" stroke="var(--color-text-secondary)" />
                <YAxis stroke="var(--color-text-secondary)" />
                <Tooltip />
                <Legend />
                <Bar
                  dataKey={`${yoy.data.current_year} Expenses`}
                  fill="var(--color-danger)"
                />
                <Bar
                  dataKey={`${yoy.data.previous_year} Expenses`}
                  fill="var(--color-info)"
                />
              </BarChart>
            </ResponsiveContainer>
          </div>
        )}
      </section>

      {/* Income vs Expenses + Category Trends side by side */}
      <div className={styles.chartsGrid}>
        {/* Income vs Expenses */}
        <section className={styles.section} aria-labelledby="incexp-heading">
          <div className={styles.sectionHeader}>
            <h2 id="incexp-heading">Income vs Expenses</h2>
            <div className={styles.controls}>
              <label htmlFor="ie-months" className="sr-only">Time period</label>
              <select
                id="ie-months"
                aria-label="Time period"
                className={styles.select}
                value={trendMonths}
                onChange={(e) => setTrendMonths(Number(e.target.value))}
              >
                <option value={6}>6 months</option>
                <option value={12}>12 months</option>
                <option value={24}>24 months</option>
              </select>
            </div>
          </div>
          {incExp.loading && <div className={styles.loading}>Loading...</div>}
          {incExp.error && <div className={styles.error} role="alert">{incExp.error}</div>}
          {!incExp.loading && !incExp.error && (
            <div className={styles.chartContainer}>
              <ResponsiveContainer width="100%" height="100%">
                <BarChart data={incExpData}>
                  <CartesianGrid strokeDasharray="3 3" stroke="var(--border-color)" />
                  <XAxis dataKey="name" stroke="var(--color-text-secondary)" />
                  <YAxis stroke="var(--color-text-secondary)" />
                  <Tooltip />
                  <Legend />
                  <Bar dataKey="income" fill="var(--color-primary)" name="Income" />
                  <Bar dataKey="expenses" fill="var(--color-danger)" name="Expenses" />
                </BarChart>
              </ResponsiveContainer>
            </div>
          )}
        </section>

        {/* Category Trends */}
        <section className={styles.section} aria-labelledby="cattrend-heading">
          <div className={styles.sectionHeader}>
            <h2 id="cattrend-heading">Category Trends</h2>
          </div>
          {catTrends.loading && <div className={styles.loading}>Loading...</div>}
          {catTrends.error && <div className={styles.error} role="alert">{catTrends.error}</div>}
          {!catTrends.loading && !catTrends.error && (
            <div className={styles.chartContainer}>
              <ResponsiveContainer width="100%" height="100%">
                <LineChart data={catTrendData}>
                  <CartesianGrid strokeDasharray="3 3" stroke="var(--border-color)" />
                  <XAxis dataKey="name" stroke="var(--color-text-secondary)" />
                  <YAxis stroke="var(--color-text-secondary)" />
                  <Tooltip />
                  <Legend />
                  {expenseCategories.map((cat) => (
                    <Line
                      key={cat.id}
                      type="monotone"
                      dataKey={cat.name}
                      stroke={cat.color || 'var(--color-text-secondary)'}
                      strokeWidth={2}
                      dot={false}
                    />
                  ))}
                </LineChart>
              </ResponsiveContainer>
            </div>
          )}
        </section>
      </div>

      {/* Top Merchants */}
      <section className={styles.section} aria-labelledby="merchants-heading">
        <div className={styles.sectionHeader}>
          <h2 id="merchants-heading">Top Merchants</h2>
          <div className={styles.controls}>
            <label htmlFor="merch-month" className="sr-only">Month</label>
            <select
              id="merch-month"
              aria-label="Month"
              className={styles.select}
              value={merchantMonth}
              onChange={(e) => setMerchantMonth(Number(e.target.value))}
            >
              {MONTH_FULL_NAMES.map((m, i) => (
                <option key={m} value={i + 1}>{m}</option>
              ))}
            </select>
            <label htmlFor="merch-year" className="sr-only">Merchant year</label>
            <select
              id="merch-year"
              aria-label="Merchant year"
              className={styles.select}
              value={merchantYear}
              onChange={(e) => setMerchantYear(Number(e.target.value))}
            >
              {yearOptions.map((y) => (
                <option key={y} value={y}>{y}</option>
              ))}
            </select>
          </div>
        </div>
        {merchants.loading && <div className={styles.loading}>Loading...</div>}
        {merchants.error && <div className={styles.error} role="alert">{merchants.error}</div>}
        {!merchants.loading && !merchants.error && merchants.data.length === 0 && (
          <p className={styles.emptyState}>No transactions for this period</p>
        )}
        {!merchants.loading && !merchants.error && merchants.data.length > 0 && (
          <div className={styles.merchantList}>
            {merchants.data.map((m, i) => (
              <div key={m.description} className={styles.merchantItem}>
                <span className={styles.merchantRank}>{i + 1}</span>
                <span className={styles.merchantName}>{m.description}</span>
                <div className={styles.merchantMeta}>
                  <span className={styles.merchantCount}>
                    {m.tx_count} tx{m.tx_count !== 1 ? 's' : ''}
                  </span>
                  <span className={styles.merchantTotal}>
                    {formatCurrency(m.total)}
                  </span>
                </div>
              </div>
            ))}
          </div>
        )}
      </section>
    </div>
  );
}
