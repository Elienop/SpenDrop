import { useMemo, useState } from 'react';
import { AlertCircle } from 'lucide-react';
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
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Skeleton } from '@/components/ui/skeleton';
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

// Income vs Expenses chart config is a static literal — hoisting it out of
// the component avoids recreating the object on every render and keeps it
// out of the useMemo dependency graph below.
const INCEXP_CONFIG = {
  income: { label: 'Income', color: 'hsl(var(--primary))' },
  expenses: { label: 'Expenses', color: 'hsl(var(--muted-foreground))' },
} satisfies ChartConfig;

// TODO (spec §8 commit 10): DateRangePicker and Period Tabs (This Month /
// Last Month / YTD / Custom) are deferred. Current Reports keeps its
// four-section-at-once layout. Introducing the period-tabs + range-picker
// control requires new backend endpoints and a redesign of the hook data
// shapes — out of scope for the visual-system rewrite.

export function Reports() {
  const [yoyYear, setYoyYear] = useState<number>(() => new Date().getFullYear());
  const [trendMonths, setTrendMonths] = useState<number>(12);
  const [merchantYear, setMerchantYear] = useState<number>(() => new Date().getFullYear());
  const [merchantMonth, setMerchantMonth] = useState<number>(() => new Date().getMonth() + 1);

  // `new Date()` on each render is cheap compared to useMemo bookkeeping,
  // but we still memoize `yearOptions` so its array identity is stable
  // across renders that don't cross a year boundary.
  const thisYear = new Date().getFullYear();
  const yearOptions = useMemo(
    () => Array.from({ length: 5 }, (_, i) => thisYear - i),
    [thisYear],
  );

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
  const yoyData = useMemo(() => {
    const data = yoy.data;
    if (!data) return [];
    return data.current.map((cur, i) => ({
      name: MONTH_NAMES[i],
      currentExpenses: cur.expenses,
      previousExpenses: data.previous[i].expenses,
    }));
  }, [yoy.data]);

  const yoyConfig = useMemo<ChartConfig>(() => {
    const data = yoy.data;
    if (!data) return {} as ChartConfig;
    return {
      currentExpenses: {
        label: `${data.current_year}`,
        color: 'hsl(var(--primary))',
      },
      previousExpenses: {
        label: `${data.previous_year}`,
        color: 'hsl(var(--muted-foreground))',
      },
    };
  }, [yoy.data]);

  // --- Income vs Expenses chart data ---
  const incExpData = useMemo(
    () =>
      incExp.data.map((entry) => ({
        name: `${MONTH_NAMES[entry.month - 1]} ${entry.year}`,
        income: entry.income,
        expenses: entry.expenses,
        net: entry.net,
      })),
    [incExp.data],
  );

  // --- Category Trends: top 6 expense categories by total ---
  const expenseCategories = useMemo(
    () =>
      catTrends.data
        .filter((c) => c.type === 'expense')
        .map((c) => ({
          ...c,
          totalSum: c.data.reduce((sum, d) => sum + d.total, 0),
        }))
        .sort((a, b) => b.totalSum - a.totalSum)
        .slice(0, 6),
    [catTrends.data],
  );

  // Build category trend line data: one point per month.
  // Pre-build a per-category lookup of "year-month" -> total to avoid the
  // nested O(data-length) find during the outer month loop. Also collects
  // the union of months across all categories in one pass.
  const catTrendData = useMemo<Record<string, string | number>[]>(() => {
    if (expenseCategories.length === 0) return [];

    const monthSet = new Set<string>();
    const byCat = new Map<number, Map<string, number>>();
    for (const cat of expenseCategories) {
      const inner = new Map<string, number>();
      for (const d of cat.data) {
        const key = `${d.year}-${d.month}`;
        inner.set(key, d.total);
        monthSet.add(key);
      }
      byCat.set(cat.id, inner);
    }

    const sortedMonths = Array.from(monthSet).sort();
    return sortedMonths.map((ym) => {
      const [y, m] = ym.split('-').map(Number);
      const point: Record<string, string | number> = {
        name: `${MONTH_NAMES[m - 1]} ${y}`,
      };
      for (const cat of expenseCategories) {
        point[cat.name] = byCat.get(cat.id)?.get(ym) ?? 0;
      }
      return point;
    });
  }, [expenseCategories]);

  const catTrendConfig = useMemo<ChartConfig>(
    () =>
      expenseCategories.reduce<ChartConfig>((acc, cat) => {
        acc[cat.name] = {
          label: cat.name,
          color: getCategoryColorVar({ id: cat.id }),
        };
        return acc;
      }, {}),
    [expenseCategories],
  );

  return (
    <div className="space-y-6 p-6">
      <h1 className="text-2xl font-semibold tracking-tight">Reports</h1>

      {/* Year-over-Year */}
      <Card aria-labelledby="yoy-heading">
        <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
          <CardTitle id="yoy-heading" className="text-base font-semibold">
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
            <Skeleton className="h-[300px] w-full" />
          )}
          {yoy.error && (
            <Alert variant="destructive">
              <AlertCircle className="h-4 w-4" />
              <AlertDescription>{yoy.error}</AlertDescription>
            </Alert>
          )}
          {yoy.data && (
            <ChartContainer config={yoyConfig} className="h-[300px] w-full">
              <BarChart data={yoyData}>
                <defs>
                  <linearGradient id="fillYoyCurrent" x1="0" y1="0" x2="0" y2="1">
                    <stop offset="5%" stopColor="var(--color-currentExpenses)" stopOpacity={0.8} />
                    <stop offset="95%" stopColor="var(--color-currentExpenses)" stopOpacity={0.1} />
                  </linearGradient>
                  <linearGradient id="fillYoyPrevious" x1="0" y1="0" x2="0" y2="1">
                    <stop offset="5%" stopColor="var(--color-previousExpenses)" stopOpacity={0.8} />
                    <stop offset="95%" stopColor="var(--color-previousExpenses)" stopOpacity={0.1} />
                  </linearGradient>
                </defs>
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
                  fill="url(#fillYoyCurrent)"
                  stroke="var(--color-currentExpenses)"
                  strokeOpacity={0.3}
                  radius={[4, 4, 0, 0]}
                />
                <Bar
                  dataKey="previousExpenses"
                  fill="url(#fillYoyPrevious)"
                  stroke="var(--color-previousExpenses)"
                  strokeOpacity={0.3}
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
            <CardTitle id="incexp-heading" className="text-base font-semibold">
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
              <Skeleton className="h-[300px] w-full" />
            )}
            {incExp.error && (
              <Alert variant="destructive">
                <AlertCircle className="h-4 w-4" />
                <AlertDescription>{incExp.error}</AlertDescription>
              </Alert>
            )}
            {!incExp.loading && !incExp.error && (
              <ChartContainer
                config={INCEXP_CONFIG}
                className="h-[300px] w-full"
              >
                <BarChart data={incExpData}>
                  <defs>
                    <linearGradient id="fillIncExpIncome" x1="0" y1="0" x2="0" y2="1">
                      <stop offset="5%" stopColor="var(--color-income)" stopOpacity={0.8} />
                      <stop offset="95%" stopColor="var(--color-income)" stopOpacity={0.1} />
                    </linearGradient>
                    <linearGradient id="fillIncExpExpenses" x1="0" y1="0" x2="0" y2="1">
                      <stop offset="5%" stopColor="var(--color-expenses)" stopOpacity={0.8} />
                      <stop offset="95%" stopColor="var(--color-expenses)" stopOpacity={0.1} />
                    </linearGradient>
                  </defs>
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
                    fill="url(#fillIncExpIncome)"
                    stroke="var(--color-income)"
                    strokeOpacity={0.3}
                    radius={[4, 4, 0, 0]}
                  />
                  <Bar
                    dataKey="expenses"
                    fill="url(#fillIncExpExpenses)"
                    stroke="var(--color-expenses)"
                    strokeOpacity={0.3}
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
            <CardTitle id="cattrend-heading" className="text-base font-semibold">
              Category Trends
            </CardTitle>
          </CardHeader>
          <CardContent>
            {catTrends.loading && (
              <Skeleton className="h-[300px] w-full" />
            )}
            {catTrends.error && (
              <Alert variant="destructive">
                <AlertCircle className="h-4 w-4" />
                <AlertDescription>{catTrends.error}</AlertDescription>
              </Alert>
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
          <CardTitle id="merchants-heading" className="text-base font-semibold">
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
            <Skeleton className="h-[200px] w-full" />
          )}
          {merchants.error && (
            <Alert variant="destructive">
              <AlertCircle className="h-4 w-4" />
              <AlertDescription>{merchants.error}</AlertDescription>
            </Alert>
          )}
          {!merchants.loading &&
            !merchants.error &&
            merchants.data.length === 0 && (
              <p className="text-sm text-muted-foreground">
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
                    <span className="text-xs text-muted-foreground">
                      {m.tx_count} transaction{m.tx_count !== 1 ? 's' : ''}
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
