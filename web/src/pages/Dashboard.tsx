import { useState, useEffect } from 'react';
import {
  ResponsiveContainer,
  BarChart,
  Bar,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip,
  PieChart,
  Pie,
  Cell,
} from 'recharts';
import {
  Wallet,
  TrendingUp,
  TrendingDown,
  Database,
  ArrowUpRight,
  ArrowDownRight,
} from 'lucide-react';
import { useDashboard } from '../hooks/useDashboard';
import { useChartTheme } from '../hooks/useChartTheme';
import { useChartPatterns, ChartPatternDefs } from '../hooks/useChartPatterns';
import { useAuth } from '../hooks/useAuth';
import { ChartTooltip } from '../components/ChartTooltip';
import { api } from '../api/client';
import type { Transaction, PaginatedResponse } from '../api/types';
import styles from '../styles/Dashboard.module.css';

/* ── Formatters ── */

function formatCompact(amount: number): string {
  return '$' + Math.abs(amount).toLocaleString('en-US', {
    minimumFractionDigits: 0,
    maximumFractionDigits: 0,
  });
}

function splitCurrency(amount: number): { dollars: string; cents: string } {
  const abs = Math.abs(amount);
  const dollars = Math.floor(abs).toLocaleString('en-US');
  const cents = (abs % 1).toFixed(2).slice(1); // ".52"
  return { dollars: `$${dollars}`, cents };
}

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

/* ── Budget bar gradient helpers ── */

function budgetGradient(pct: number): string {
  if (pct >= 100) return 'linear-gradient(90deg, #EF8B6E, #E07050)';
  if (pct >= 85) return 'linear-gradient(90deg, #F0C84D, #E0B83D)';
  return 'linear-gradient(90deg, #16C8C7, #12B0AF)';
}

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

  /* ── Derived data ── */

  const totalIncome = summary?.total_income ?? 0;
  const totalExpense = summary?.total_spent ?? 0;
  const totalBalance = totalIncome - totalExpense;

  // Cash flow chart data
  const chartData = (() => {
    const sorted = [...trend].reverse();
    const sliced = cashFlowView === '6m' ? sorted.slice(-6) : sorted;
    return sliced.map((item) => ({
      name: SHORT_MONTHS[item.month - 1],
      income: item.total_income,
      expense: -item.total_spent,
    }));
  })();

  // Categories — top 5 for donut + list
  const topCats = categories.slice(0, 5);
  const otherTotal = categories.slice(5).reduce((sum, cat) => sum + cat.total, 0);
  const totalCategorySpent = categories.reduce((sum, cat) => sum + cat.total, 0);

  // Half-gauge pie data (top 5 + "Other")
  const gaugeData = [
    ...topCats.map((cat) => ({
      name: cat.name,
      value: cat.total,
      color: cat.color,
    })),
    ...(otherTotal > 0
      ? [{ name: 'Other', value: otherTotal, color: '#B8BCC8' }]
      : []),
  ];

  // Pattern configs for charts
  const categoryPatterns = gaugeData.map((cat, i) => patterns.getCategoryPattern(i, cat.color));
  const categoryDefs = patterns.getCategoryDefs(gaugeData);

  const cfPatternStyles = patterns.buildStyleMap([
    patterns.cashFlow.income,
    patterns.cashFlow.expense,
  ]);

  const catPatternStyles = patterns.buildStyleMap(categoryPatterns);

  // KPI computations
  const savingsRate = totalIncome > 0
    ? ((totalIncome - totalExpense) / totalIncome * 100)
    : 0;

  const budgetTotal = summary?.budget ?? 0;

  // Delta from previous month
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

  const expenseDelta = prevMonthTrend
    ? pctChange(totalExpense, prevMonthTrend.total_spent)
    : null;

  const savingsRatePrev = prevMonthTrend && prevMonthTrend.total_income > 0
    ? ((prevMonthTrend.total_income - prevMonthTrend.total_spent) / prevMonthTrend.total_income * 100)
    : null;

  const savingsDelta = savingsRatePrev !== null
    ? savingsRate - savingsRatePrev
    : null;

  // Split values for decimal formatting
  const balanceSplit = splitCurrency(totalBalance);
  const incomeSplit = splitCurrency(totalIncome);
  const expenseSplit = splitCurrency(totalExpense);

  /* ── Loading state ── */
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
            <div className={styles.skeleton} style={{ height: 260, marginTop: 16 }} />
          </div>
          <div className={styles.card}>
            <div className={styles.skeletonText} style={{ width: '40%' }} />
            <div className={styles.skeleton} style={{ height: 200, marginTop: 16 }} />
          </div>
        </div>
      </div>
    );
  }

  /* ── Error state ── */
  if (error) {
    return (
      <div className={styles.page}>
        <div className={styles.header}>
          <h1 className={styles.headerTitle}>Dashboard</h1>
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

  /* ── Render ── */
  return (
    <div className={styles.page}>
      {/* ===== Header ===== */}
      <div className={styles.header}>
        <div>
          <h1 className={styles.headerTitle}>
            Welcome back, {user?.display_name ?? 'there'}
          </h1>
          <p className={styles.subtitle}>
            Here's what's happening with your finances.
          </p>
        </div>
        <div className={styles.selectors}>
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
            <div className={styles.kpiTop}>
              <span className={styles.kpiLabel}>Total Balance</span>
              <div className={`${styles.kpiIcon} ${styles.kpiIconPurple}`}>
                <Wallet size={18} strokeWidth={1.5} />
              </div>
            </div>
            <div className={styles.kpiValue}>
              {balanceSplit.dollars}
              <span className={styles.kpiDecimal}>{balanceSplit.cents}</span>
            </div>
            <div className={styles.kpiFooter}>
              {balanceDelta != null && (
                <>
                  <span className={balanceDelta >= 0 ? styles.kpiBadgePositive : styles.kpiBadgeNegative}>
                    {balanceDelta >= 0
                      ? <ArrowUpRight size={12} strokeWidth={3} />
                      : <ArrowDownRight size={12} strokeWidth={3} />}
                    {Math.abs(balanceDelta).toFixed(1)}%
                  </span>
                  <span className={styles.kpiVs}>vs last month</span>
                </>
              )}
            </div>
          </div>

          {/* Income */}
          <div className={styles.kpiCard}>
            <div className={styles.kpiTop}>
              <span className={styles.kpiLabel}>Income</span>
              <div className={`${styles.kpiIcon} ${styles.kpiIconTeal}`}>
                <TrendingUp size={18} strokeWidth={1.5} />
              </div>
            </div>
            <div className={styles.kpiValue}>
              {incomeSplit.dollars}
              <span className={styles.kpiDecimal}>{incomeSplit.cents}</span>
            </div>
            <div className={styles.kpiFooter}>
              {incomeDelta != null && (
                <>
                  <span className={incomeDelta >= 0 ? styles.kpiBadgePositive : styles.kpiBadgeNegative}>
                    {incomeDelta >= 0
                      ? <ArrowUpRight size={12} strokeWidth={3} />
                      : <ArrowDownRight size={12} strokeWidth={3} />}
                    {Math.abs(incomeDelta).toFixed(1)}%
                  </span>
                  <span className={styles.kpiVs}>vs last month</span>
                </>
              )}
            </div>
          </div>

          {/* Expenses */}
          <div className={styles.kpiCard}>
            <div className={styles.kpiTop}>
              <span className={styles.kpiLabel}>Expenses</span>
              <div className={`${styles.kpiIcon} ${styles.kpiIconRed}`}>
                <TrendingDown size={18} strokeWidth={1.5} />
              </div>
            </div>
            <div className={styles.kpiValue}>
              {expenseSplit.dollars}
              <span className={styles.kpiDecimal}>{expenseSplit.cents}</span>
            </div>
            <div className={styles.kpiFooter}>
              {expenseDelta != null && (
                <>
                  <span className={expenseDelta >= 0 ? styles.kpiBadgeNegative : styles.kpiBadgePositive}>
                    {expenseDelta >= 0
                      ? <ArrowUpRight size={12} strokeWidth={3} />
                      : <ArrowDownRight size={12} strokeWidth={3} />}
                    {Math.abs(expenseDelta).toFixed(1)}%
                  </span>
                  <span className={styles.kpiVs}>vs last month</span>
                </>
              )}
            </div>
          </div>

          {/* Savings Rate */}
          <div className={styles.kpiCard}>
            <div className={styles.kpiTop}>
              <span className={styles.kpiLabel}>Savings Rate</span>
              <div className={`${styles.kpiIcon} ${styles.kpiIconYellow}`}>
                <Database size={18} strokeWidth={1.5} />
              </div>
            </div>
            <div className={styles.kpiValue}>
              {savingsRate.toFixed(1)}
              <span className={styles.kpiDecimal}>%</span>
            </div>
            <div className={styles.kpiFooter}>
              {savingsDelta != null && (
                <>
                  <span className={savingsDelta >= 0 ? styles.kpiBadgePositive : styles.kpiBadgeNegative}>
                    {savingsDelta >= 0
                      ? <ArrowUpRight size={12} strokeWidth={3} />
                      : <ArrowDownRight size={12} strokeWidth={3} />}
                    {Math.abs(savingsDelta).toFixed(1)}%
                  </span>
                  <span className={styles.kpiVs}>vs last month</span>
                </>
              )}
            </div>
          </div>
        </div>
      )}

      {/* ===== Content Grid ===== */}
      <div className={styles.contentGrid}>
        {/* ── Cash Flow Chart ── */}
        <div className={styles.card}>
          <div className={styles.cardHeader}>
            <div>
              <div className={styles.cardTitle}>Cash Flow</div>
              <div className={styles.cardSubtitle}>Income vs expenses over time</div>
            </div>
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
                  className={`${styles.cfToggleBtn} ${cashFlowView === '6m' ? styles.cfToggleBtnActive : ''}`}
                  onClick={() => setCashFlowView('6m')}
                >
                  6M
                </button>
                <button
                  className={`${styles.cfToggleBtn} ${cashFlowView === '12m' ? styles.cfToggleBtnActive : ''}`}
                  onClick={() => setCashFlowView('12m')}
                >
                  12M
                </button>
              </div>
            </div>
          </div>

          <div className={styles.cfChartWrap}>
            <ResponsiveContainer width="100%" height="100%">
              <BarChart data={chartData} margin={{ top: 4, right: 0, bottom: 0, left: 0 }} barGap={4}>
                <ChartPatternDefs patterns={patterns.cashFlowDefs} />
                <CartesianGrid
                  strokeDasharray="3 3"
                  stroke={chartTheme.gridStroke}
                  vertical={false}
                />
                <XAxis
                  dataKey="name"
                  tickLine={false}
                  axisLine={{ stroke: chartTheme.gridStroke, strokeWidth: 1 }}
                  tick={{ fill: chartTheme.axisStroke, fontFamily: 'Inter Variable', fontSize: 12 }}
                  dy={4}
                />
                <YAxis
                  tickLine={false}
                  axisLine={false}
                  width={48}
                  tick={{ fill: chartTheme.axisStroke, fontFamily: 'Inter Variable', fontSize: 12 }}
                  tickFormatter={(v: number) => `$${Math.abs(v / 1000).toFixed(0)}k`}
                />
                <Tooltip
                  content={<ChartTooltip patternStyles={cfPatternStyles} />}
                  cursor={{ fill: chartTheme.hoverBg }}
                />
                <Bar
                  dataKey="income"
                  fill={patterns.cashFlow.income.fill}
                  radius={[6, 6, 6, 6]}
                  name="Income"
                  barSize={36}
                />
                <Bar
                  dataKey="expense"
                  fill={patterns.cashFlow.expense.fill}
                  stroke={patterns.cashFlow.expense.stroke}
                  strokeWidth={patterns.cashFlow.expense.strokeWidth}
                  radius={[6, 6, 6, 6]}
                  name="Expenses"
                  barSize={36}
                />
              </BarChart>
            </ResponsiveContainer>
          </div>
        </div>

        {/* ── Spending by Category (Half-Gauge) ── */}
        <div className={styles.card}>
          <div className={styles.cardHeader}>
            <div>
              <div className={styles.cardTitle}>Spending by Category</div>
              <div className={styles.cardSubtitle}>
                {MONTHS[selectedMonth - 1]} {selectedYear}
              </div>
            </div>
            <a href="/categories" className={styles.cardLink}>View all &rarr;</a>
          </div>
          <div className={styles.spendGrid}>
            <div className={styles.gaugeWrap}>
              <ResponsiveContainer width="100%" height="100%">
                <PieChart>
                  <ChartPatternDefs patterns={categoryDefs} />
                  <Pie
                    data={gaugeData}
                    dataKey="value"
                    nameKey="name"
                    startAngle={180}
                    endAngle={0}
                    cx="50%"
                    cy="95%"
                    innerRadius={130}
                    outerRadius={175}
                    cornerRadius={4}
                    paddingAngle={1.5}
                    stroke="none"
                  >
                    {gaugeData.map((_, i) => (
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
                    offset={10}
                  />
                </PieChart>
              </ResponsiveContainer>
              <div className={styles.gaugeCenter}>
                <span className={styles.gaugeTotal}>
                  {formatCompact(totalCategorySpent)}
                </span>
                <span className={styles.gaugeSub}>Total Spent</span>
              </div>
            </div>

            <div className={styles.catList}>
              {gaugeData.map((cat, i) => {
                const pct = totalCategorySpent > 0
                  ? Math.round((cat.value / totalCategorySpent) * 100)
                  : 0;
                const barPct = totalCategorySpent > 0
                  ? (cat.value / gaugeData[0].value) * 100
                  : 0;
                return (
                  <div key={cat.name} className={styles.catRow}>
                    <div
                      className={styles.catDot}
                      style={categoryPatterns[i]?.legendStyle ?? { background: cat.color }}
                    />
                    <span className={styles.catName}>{cat.name}</span>
                    <div className={styles.catBar}>
                      <div
                        className={styles.catBarFill}
                        style={{
                          width: `${Math.min(100, barPct)}%`,
                          background: cat.color,
                          opacity: 0.7,
                        }}
                      />
                    </div>
                    <span className={styles.catPct}>{pct}%</span>
                    <span className={styles.catAmount}>{formatCompact(cat.value)}</span>
                  </div>
                );
              })}
            </div>
          </div>
        </div>

        {/* ── Monthly Budget ── */}
        <div className={styles.card}>
          <div className={styles.cardHeader}>
            <div>
              <div className={styles.cardTitle}>Monthly Budget</div>
              <div className={styles.cardSubtitle}>
                {budgetTotal > 0
                  ? `${formatCompact(totalExpense)} of ${formatCompact(budgetTotal)} used`
                  : 'No budget set'}
              </div>
            </div>
            <a href="/settings" className={styles.cardLink}>Details &rarr;</a>
          </div>
          {budgetTotal > 0 ? (
            <div className={styles.budgetList}>
              {topCats.map((cat) => {
                // Per-category share of total budget proportional to spending
                const catPct = budgetTotal > 0
                  ? (cat.total / budgetTotal) * 100
                  : 0;
                return (
                  <div key={cat.id} className={styles.budgetItem}>
                    <div className={styles.budgetInfo}>
                      <span className={styles.budgetName}>{cat.name}</span>
                      <span className={styles.budgetAmounts}>
                        {formatCompact(cat.total)}
                        <span className={catPct > 25 ? styles.budgetLimitOver : styles.budgetLimit}>
                          {' '}/ {formatCompact(budgetTotal)}
                        </span>
                      </span>
                    </div>
                    <div className={styles.budgetBarRow}>
                      <div className={styles.budgetTrack}>
                        <div
                          className={styles.budgetFill}
                          style={{
                            width: `${Math.min(100, catPct)}%`,
                            background: budgetGradient(catPct > 20 ? catPct * 4 : catPct * 3),
                          }}
                        />
                      </div>
                      <span className={catPct > 25 ? styles.budgetPctOver : styles.budgetPct}>
                        {Math.round(catPct)}%
                      </span>
                    </div>
                  </div>
                );
              })}
            </div>
          ) : (
            <p className={styles.emptyState}>
              Set a monthly budget in Settings to track progress
            </p>
          )}
        </div>

        {/* ── Recent Transactions ── */}
        <div className={styles.card}>
          <div className={styles.cardHeader}>
            <div>
              <div className={styles.cardTitle}>Recent Transactions</div>
              <div className={styles.cardSubtitle}>Latest activity across all accounts</div>
            </div>
            <a href="/transactions" className={styles.cardLink}>View all &rarr;</a>
          </div>
          {recentTransactions.length === 0 ? (
            <p className={styles.emptyState}>No recent transactions</p>
          ) : (
            <div className={styles.txList}>
              {recentTransactions.slice(0, 6).map((tx) => (
                <div key={tx.id} className={styles.txRow}>
                  <div
                    className={styles.txIcon}
                    style={{
                      background: `color-mix(in srgb, ${tx.category_color} 15%, transparent)`,
                      color: tx.category_color,
                    }}
                  >
                    <span style={{ fontSize: 14, fontWeight: 600 }}>
                      {tx.category_name.charAt(0)}
                    </span>
                  </div>
                  <div className={styles.txInfo}>
                    <div className={styles.txName}>{tx.description}</div>
                    <div className={styles.txMeta}>{tx.category_name}</div>
                  </div>
                  <div className={styles.txRight}>
                    <div
                      className={`${styles.txAmount} ${
                        tx.category_type === 'expense' ? styles.expense : styles.income
                      }`}
                    >
                      {tx.category_type === 'expense' ? '-' : '+'}
                      {formatFull(tx.amount)}
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
    </div>
  );
}
