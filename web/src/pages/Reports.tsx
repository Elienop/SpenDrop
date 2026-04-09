import { useState } from 'react';
import {
  BarChart,
  Bar,
  LineChart,
  Line,
  XAxis,
  YAxis,
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
import {
  useYearOverYear,
  useCategoryTrends,
  useIncomeExpenses,
  useTopMerchants,
} from '../hooks/useReports';
import { getCategoryColorVar } from '@/lib/chart-colors';

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

// TODO (spec §8 commit 10): DateRangePicker and Period Tabs (This Month /
// Last Month / YTD / Custom) are deferred. Current Reports keeps its
// four-section-at-once layout. Introducing the period-tabs + range-picker
// control requires new backend endpoints and a redesign of the hook data
// shapes — out of scope for the visual-system rewrite.

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
  // IMPORTANT: use hyphen-free, space-free keys (`currentExpenses` /
  // `previousExpenses`) for the ChartConfig. shadcn's `ChartContainer`
  // emits a CSS variable per config key as `--color-<key>`; any non-ident
  // characters in the key (spaces, slashes) break `var(--color-...)`
  // resolution. The human-facing year labels live in `config[key].label`
  // and are what the shadcn `ChartLegendContent` renders to the user.
  const yoyData = yoy.data
    ? yoy.data.current.map((cur, i) => ({
        name: MONTH_NAMES[i],
        currentExpenses: cur.expenses,
        previousExpenses: yoy.data!.previous[i].expenses,
      }))
    : [];

  const yoyConfig: ChartConfig = yoy.data
    ? {
        currentExpenses: {
          label: `${yoy.data.current_year}`,
          color: 'hsl(var(--chart-10))',
        },
        previousExpenses: {
          label: `${yoy.data.previous_year}`,
          color: 'hsl(var(--chart-3))',
        },
      }
    : {};

  // --- Income vs Expenses chart data ---
  const incExpData = incExp.data.map((entry) => ({
    name: `${MONTH_NAMES[entry.month - 1]} ${entry.year}`,
    income: entry.income,
    expenses: entry.expenses,
    net: entry.net,
  }));

  const incExpConfig = {
    income: { label: 'Income', color: 'hsl(var(--chart-6))' },
    expenses: { label: 'Expenses', color: 'hsl(var(--chart-10))' },
  } satisfies ChartConfig;

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

  const catTrendConfig = expenseCategories.reduce<ChartConfig>((acc, cat) => {
    acc[cat.name] = {
      label: cat.name,
      color: getCategoryColorVar({ id: cat.id }),
    };
    return acc;
  }, {});

  return (
    <div className="space-y-6 p-6">
      <h1 className="text-2xl font-semibold tracking-tight">Reports</h1>

      {/* Year-over-Year */}
      <Card aria-labelledby="yoy-heading">
        <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
          <CardTitle id="yoy-heading" className="text-base font-medium">
            Year-over-Year Comparison
          </CardTitle>
          <Select
            value={String(yoyYear)}
            onValueChange={(v) => setYoyYear(Number(v))}
          >
            <SelectTrigger
              aria-label="Year-over-Year Year"
              className="w-[180px]"
            >
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {yearOptions.map((y) => (
                <SelectItem key={y} value={String(y)}>
                  {y} vs {y - 1}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </CardHeader>
        <CardContent>
          {yoy.loading && (
            <div className="text-muted-foreground text-sm">Loading...</div>
          )}
          {yoy.error && (
            <div className="text-destructive text-sm" role="alert">
              {yoy.error}
            </div>
          )}
          {yoy.data && (
            <ChartContainer config={yoyConfig} className="h-[300px] w-full">
              <BarChart data={yoyData}>
                <CartesianGrid
                  strokeDasharray="3 3"
                  stroke="hsl(var(--border))"
                  strokeOpacity={0.4}
                  vertical={false}
                />
                <XAxis
                  dataKey="name"
                  fontSize={11}
                  stroke="hsl(var(--muted-foreground))"
                  tickLine={false}
                  axisLine={{ stroke: 'hsl(var(--border))' }}
                />
                <YAxis
                  fontSize={11}
                  stroke="hsl(var(--muted-foreground))"
                  tickLine={false}
                  axisLine={false}
                  tickFormatter={(v: number) => formatCurrency(v)}
                />
                <ChartTooltip content={<ChartTooltipContent />} />
                <ChartLegend content={<ChartLegendContent />} />
                <Bar
                  dataKey="currentExpenses"
                  fill="var(--color-currentExpenses)"
                  radius={[4, 4, 0, 0]}
                />
                <Bar
                  dataKey="previousExpenses"
                  fill="var(--color-previousExpenses)"
                  radius={[4, 4, 0, 0]}
                />
              </BarChart>
            </ChartContainer>
          )}
        </CardContent>
      </Card>

      {/* Income vs Expenses + Category Trends side by side */}
      <div className="grid gap-6 md:grid-cols-2">
        {/* Income vs Expenses */}
        <Card aria-labelledby="incexp-heading">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle id="incexp-heading" className="text-base font-medium">
              Income vs Expenses
            </CardTitle>
            <Select
              value={String(trendMonths)}
              onValueChange={(v) => setTrendMonths(Number(v))}
            >
              <SelectTrigger aria-label="Time Period" className="w-[140px]">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="6">6 months</SelectItem>
                <SelectItem value="12">12 months</SelectItem>
                <SelectItem value="24">24 months</SelectItem>
              </SelectContent>
            </Select>
          </CardHeader>
          <CardContent>
            {incExp.loading && (
              <div className="text-muted-foreground text-sm">Loading...</div>
            )}
            {incExp.error && (
              <div className="text-destructive text-sm" role="alert">
                {incExp.error}
              </div>
            )}
            {!incExp.loading && !incExp.error && (
              <ChartContainer
                config={incExpConfig}
                className="h-[300px] w-full"
              >
                <BarChart data={incExpData}>
                  <CartesianGrid
                    strokeDasharray="3 3"
                    stroke="hsl(var(--border))"
                    strokeOpacity={0.4}
                    vertical={false}
                  />
                  <XAxis
                    dataKey="name"
                    fontSize={11}
                    stroke="hsl(var(--muted-foreground))"
                    tickLine={false}
                    axisLine={{ stroke: 'hsl(var(--border))' }}
                  />
                  <YAxis
                    fontSize={11}
                    stroke="hsl(var(--muted-foreground))"
                    tickLine={false}
                    axisLine={false}
                    tickFormatter={(v: number) => formatCurrency(v)}
                  />
                  <ChartTooltip content={<ChartTooltipContent />} />
                  <ChartLegend content={<ChartLegendContent />} />
                  <Bar
                    dataKey="income"
                    fill="var(--color-income)"
                    radius={[4, 4, 0, 0]}
                  />
                  <Bar
                    dataKey="expenses"
                    fill="var(--color-expenses)"
                    radius={[4, 4, 0, 0]}
                  />
                </BarChart>
              </ChartContainer>
            )}
          </CardContent>
        </Card>

        {/* Category Trends */}
        <Card aria-labelledby="cattrend-heading">
          <CardHeader className="pb-2">
            <CardTitle id="cattrend-heading" className="text-base font-medium">
              Category Trends
            </CardTitle>
          </CardHeader>
          <CardContent>
            {catTrends.loading && (
              <div className="text-muted-foreground text-sm">Loading...</div>
            )}
            {catTrends.error && (
              <div className="text-destructive text-sm" role="alert">
                {catTrends.error}
              </div>
            )}
            {!catTrends.loading && !catTrends.error && (
              <ChartContainer
                config={catTrendConfig}
                className="h-[300px] w-full"
              >
                <LineChart data={catTrendData}>
                  <CartesianGrid
                    strokeDasharray="3 3"
                    stroke="hsl(var(--border))"
                    strokeOpacity={0.4}
                    vertical={false}
                  />
                  <XAxis
                    dataKey="name"
                    fontSize={11}
                    stroke="hsl(var(--muted-foreground))"
                    tickLine={false}
                    axisLine={{ stroke: 'hsl(var(--border))' }}
                  />
                  <YAxis
                    fontSize={11}
                    stroke="hsl(var(--muted-foreground))"
                    tickLine={false}
                    axisLine={false}
                    tickFormatter={(v: number) => formatCurrency(v)}
                  />
                  <ChartTooltip content={<ChartTooltipContent />} />
                  <ChartLegend content={<ChartLegendContent />} />
                  {expenseCategories.map((cat) => (
                    <Line
                      key={cat.id}
                      type="monotone"
                      dataKey={cat.name}
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
      </div>

      {/* Top Merchants */}
      <Card aria-labelledby="merchants-heading">
        <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
          <CardTitle id="merchants-heading" className="text-base font-medium">
            Top Merchants
          </CardTitle>
          <div className="flex gap-2">
            <Select
              value={String(merchantMonth)}
              onValueChange={(v) => setMerchantMonth(Number(v))}
            >
              <SelectTrigger
                aria-label="Merchant Month"
                className="w-[140px]"
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
              value={String(merchantYear)}
              onValueChange={(v) => setMerchantYear(Number(v))}
            >
              <SelectTrigger
                aria-label="Merchant Year"
                className="w-[120px]"
              >
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {yearOptions.map((y) => (
                  <SelectItem key={y} value={String(y)}>
                    {y}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
        </CardHeader>
        <CardContent>
          {merchants.loading && (
            <div className="text-muted-foreground text-sm">Loading...</div>
          )}
          {merchants.error && (
            <div className="text-destructive text-sm" role="alert">
              {merchants.error}
            </div>
          )}
          {!merchants.loading &&
            !merchants.error &&
            merchants.data.length === 0 && (
              <p className="text-muted-foreground text-sm">
                No transactions for this period
              </p>
            )}
          {!merchants.loading &&
            !merchants.error &&
            merchants.data.length > 0 && (
              <ul className="divide-border divide-y">
                {merchants.data.map((m, i) => (
                  <li
                    key={m.description}
                    className="flex items-center gap-3 py-2"
                  >
                    <span className="text-muted-foreground w-6 text-sm font-mono tabular-nums">
                      {i + 1}
                    </span>
                    <span className="flex-1 truncate text-sm">
                      {m.description}
                    </span>
                    <span className="text-muted-foreground text-xs">
                      {m.tx_count} tx{m.tx_count !== 1 ? 's' : ''}
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
    </div>
  );
}
