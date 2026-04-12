import { useId, useMemo, useState } from 'react';
import {
  BarChart,
  Bar,
  AreaChart,
  Area,
  XAxis,
  YAxis,
  CartesianGrid,
  ReferenceLine,
} from 'recharts';
import { formatCurrency } from '@/lib/format';
import { useBaseCurrency } from '@/hooks/useBaseCurrency';
import {
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
  type ChartConfig,
} from '@/components/ui/chart';
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from '@/components/ui/card';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { Skeleton } from '@/components/ui/skeleton';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { AlertCircle } from 'lucide-react';
import { useIncomeExpenses, useBudgetVsActual } from '@/hooks/useReports';
import { MONTH_NAMES_SHORT, yearOptions } from '@/lib/dates';
import { INCEXP_CONFIG } from './utils';
import { cn } from '@/lib/utils';

const NET_FLOW_CONFIG = {
  cumulative: { label: 'Net Cash Flow', color: 'hsl(var(--primary))' },
} satisfies ChartConfig;

const BVA_CONFIG = {
  budget: { label: 'Budget', color: 'hsl(var(--primary) / 0.35)' },
  actual: { label: 'Actual', color: 'hsl(var(--primary))' },
} satisfies ChartConfig;

/** Axis tick formatter for month-timeseries dataKeys of the form "YYYY-MM".
 *  Returns "Jan '26" on January and on the leading tick (to anchor the year),
 *  and just "Jan", "Feb", ... on other months. Pairs with `interval={0}` so
 *  no tick ever gets decimated — the user saw the leftmost label disappear
 *  at 12- and 24-month ranges because Recharts' default `preserveEnd`
 *  strategy drops left-edge ticks when labels collide. */
function formatMonthTick(value: string, index: number): string {
  const [yearStr, monthStr] = value.split('-');
  const year = Number(yearStr);
  const month = Number(monthStr);
  const showYear = month === 1 || index === 0;
  return showYear
    ? `${MONTH_NAMES_SHORT[month - 1]} '${String(year).slice(2)}`
    : MONTH_NAMES_SHORT[month - 1];
}

/** Tooltip label formatter: "2026-01" → "Jan 2026". Full four-digit year
 *  on hover keeps the axis compact while detail lives in the tooltip. */
function formatMonthLabel(value: unknown): string {
  if (typeof value !== 'string') return '';
  const [yearStr, monthStr] = value.split('-');
  return `${MONTH_NAMES_SHORT[Number(monthStr) - 1]} ${yearStr}`;
}

export function OverviewTab() {
  const [months, setMonths] = useState(12);
  const [bvaYear, setBvaYear] = useState(new Date().getFullYear());
  const incExp = useIncomeExpenses(months);
  const bva = useBudgetVsActual(bvaYear);
  const gradientId = useId();
  const baseCurrency = useBaseCurrency();
  const fmt = (amount: number) => formatCurrency(amount, baseCurrency);

  // Store a stable "YYYY-MM" key as the XAxis dataKey so the formatter can
  // derive month/year without a closure, and so 24-month ranges don't
  // collapse two same-month buckets into one category.
  const incExpData = useMemo(
    () =>
      incExp.data.map((entry) => ({
        key: `${entry.year}-${String(entry.month).padStart(2, '0')}`,
        income: entry.income,
        expenses: entry.expenses,
      })),
    [incExp.data],
  );

  const cashFlowData = useMemo(() => {
    const result: { key: string; cumulative: number }[] = [];
    incExp.data.reduce((acc, entry) => {
      const total = acc + entry.net;
      result.push({
        key: `${entry.year}-${String(entry.month).padStart(2, '0')}`,
        cumulative: total,
      });
      return total;
    }, 0);
    return result;
  }, [incExp.data]);

  const bvaData = useMemo(
    () =>
      bva.data.map((e) => ({
        name: MONTH_NAMES_SHORT[e.month - 1],
        budget: e.budget,
        actual: e.actual,
      })),
    [bva.data],
  );

  const years = yearOptions();

  return (
    <div className="grid gap-6 md:grid-cols-2">
      {/* Income vs Expenses */}
      <Card aria-labelledby="income-expenses-heading">
        <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-4">
          <div className="flex flex-col gap-0.5">
            <CardTitle
              id="income-expenses-heading"
              className="text-base font-semibold"
            >
              Income vs Expenses
            </CardTitle>
            <CardDescription className="text-xs">Monthly income and spending comparison</CardDescription>
          </div>
          <Select
            value={String(months)}
            onValueChange={(v) => setMonths(Number(v))}
          >
            <SelectTrigger aria-label="Time Period" className="h-9 w-[140px]">
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
          {incExp.loading && <Skeleton className="h-[300px] w-full" />}
          {incExp.error && (
            <Alert variant="destructive">
              <AlertCircle className="h-4 w-4" />
              <AlertDescription>{incExp.error}</AlertDescription>
            </Alert>
          )}
          {!incExp.loading && !incExp.error && (
            <ChartContainer
              config={INCEXP_CONFIG}
              className={cn(
                'h-[300px] w-full transition-opacity duration-200',
                incExp.fetching && !incExp.loading && 'opacity-60',
              )}
            >
              <BarChart accessibilityLayer data={incExpData}>
                <defs>
                  <linearGradient
                    id={`${gradientId}-income`}
                    x1="0"
                    y1="0"
                    x2="0"
                    y2="1"
                  >
                    <stop
                      offset="5%"
                      stopColor="var(--color-income)"
                      stopOpacity={0.8}
                    />
                    <stop
                      offset="95%"
                      stopColor="var(--color-income)"
                      stopOpacity={0.1}
                    />
                  </linearGradient>
                  <linearGradient
                    id={`${gradientId}-expenses`}
                    x1="0"
                    y1="0"
                    x2="0"
                    y2="1"
                  >
                    <stop
                      offset="5%"
                      stopColor="var(--color-expenses)"
                      stopOpacity={0.8}
                    />
                    <stop
                      offset="95%"
                      stopColor="var(--color-expenses)"
                      stopOpacity={0.1}
                    />
                  </linearGradient>
                </defs>
                <CartesianGrid vertical={false} />
                <XAxis
                  dataKey="key"
                  tickLine={false}
                  axisLine={false}
                  tickMargin={10}
                  interval={0}
                  minTickGap={-1}
                  padding={{ left: 20, right: 20 }}
                  tick={{ fontSize: 11 }}
                  tickFormatter={formatMonthTick}
                />
                <ChartTooltip
                  content={<ChartTooltipContent labelFormatter={formatMonthLabel} />}
                />
                <Bar
                  dataKey="income"
                  fill={`url(#${gradientId}-income)`}
                  stroke="var(--color-income)"
                  strokeOpacity={0.3}
                  radius={[4, 4, 0, 0]}
                />
                <Bar
                  dataKey="expenses"
                  fill={`url(#${gradientId}-expenses)`}
                  stroke="var(--color-expenses)"
                  strokeOpacity={0.3}
                  radius={[4, 4, 0, 0]}
                />
              </BarChart>
            </ChartContainer>
          )}
        </CardContent>
      </Card>

      {/* Net Cash Flow */}
      <Card aria-labelledby="net-cash-flow-heading">
        <CardHeader className="pb-4">
          <CardTitle
            id="net-cash-flow-heading"
            className="text-base font-semibold"
          >
            Net Cash Flow
          </CardTitle>
          <CardDescription className="text-xs">Cumulative net balance over time</CardDescription>
        </CardHeader>
        <CardContent>
          {incExp.loading && <Skeleton className="h-[300px] w-full" />}
          {incExp.error && (
            <Alert variant="destructive">
              <AlertCircle className="h-4 w-4" />
              <AlertDescription>{incExp.error}</AlertDescription>
            </Alert>
          )}
          {!incExp.loading && !incExp.error && (
            <ChartContainer
              config={NET_FLOW_CONFIG}
              className={cn(
                'h-[300px] w-full transition-opacity duration-200',
                incExp.fetching && !incExp.loading && 'opacity-60',
              )}
            >
              <AreaChart accessibilityLayer data={cashFlowData}>
                <defs>
                  <linearGradient
                    id={`${gradientId}-cashflow`}
                    x1="0"
                    y1="0"
                    x2="0"
                    y2="1"
                  >
                    <stop
                      offset="5%"
                      stopColor="var(--color-cumulative)"
                      stopOpacity={0.8}
                    />
                    <stop
                      offset="95%"
                      stopColor="var(--color-cumulative)"
                      stopOpacity={0.05}
                    />
                  </linearGradient>
                </defs>
                <CartesianGrid vertical={false} />
                <XAxis
                  dataKey="key"
                  tickLine={false}
                  axisLine={false}
                  tickMargin={10}
                  interval={0}
                  minTickGap={-1}
                  padding={{ left: 20, right: 20 }}
                  tick={{ fontSize: 11 }}
                  tickFormatter={formatMonthTick}
                />
                <YAxis
                  tickLine={false}
                  axisLine={false}
                  tickMargin={8}
                  width={80}
                  tickFormatter={(v: number) => fmt(v)}
                />
                <ReferenceLine y={0} stroke="hsl(var(--border))" />
                <ChartTooltip content={<ChartTooltipContent labelFormatter={formatMonthLabel} />} />
                <Area
                  type="monotone"
                  dataKey="cumulative"
                  fill={`url(#${gradientId}-cashflow)`}
                  stroke="var(--color-cumulative)"
                  strokeWidth={2}
                />
              </AreaChart>
            </ChartContainer>
          )}
        </CardContent>
      </Card>

      {/* Budget vs Actual — full width */}
      <Card aria-labelledby="budget-vs-actual-heading" className="md:col-span-2">
        <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-4">
          <div className="flex flex-col gap-0.5">
            <CardTitle
              id="budget-vs-actual-heading"
              className="text-base font-semibold"
            >
              Budget vs Actual
            </CardTitle>
            <CardDescription className="text-xs">Monthly budget targets vs actual spending</CardDescription>
          </div>
          <Select
            value={String(bvaYear)}
            onValueChange={(v) => setBvaYear(Number(v))}
          >
            <SelectTrigger aria-label="Budget Year" className="h-9 w-[120px]">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {years.map((y) => (
                <SelectItem key={y.value} value={y.value}>
                  {y.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </CardHeader>
        <CardContent>
          {bva.loading && <Skeleton className="h-[300px] w-full" />}
          {bva.error && (
            <Alert variant="destructive">
              <AlertCircle className="h-4 w-4" />
              <AlertDescription>{bva.error}</AlertDescription>
            </Alert>
          )}
          {!bva.loading && !bva.error && bva.data.length > 0 && (
            <ChartContainer
              config={BVA_CONFIG}
              className={cn(
                'h-[300px] w-full transition-opacity duration-200',
                bva.fetching && !bva.loading && 'opacity-60',
              )}
            >
              <BarChart accessibilityLayer data={bvaData}>
                <defs>
                  <linearGradient
                    id={`${gradientId}-budget`}
                    x1="0"
                    y1="0"
                    x2="0"
                    y2="1"
                  >
                    <stop
                      offset="5%"
                      stopColor="var(--color-budget)"
                      stopOpacity={0.8}
                    />
                    <stop
                      offset="95%"
                      stopColor="var(--color-budget)"
                      stopOpacity={0.1}
                    />
                  </linearGradient>
                  <linearGradient
                    id={`${gradientId}-actual`}
                    x1="0"
                    y1="0"
                    x2="0"
                    y2="1"
                  >
                    <stop
                      offset="5%"
                      stopColor="var(--color-actual)"
                      stopOpacity={0.8}
                    />
                    <stop
                      offset="95%"
                      stopColor="var(--color-actual)"
                      stopOpacity={0.1}
                    />
                  </linearGradient>
                </defs>
                <CartesianGrid vertical={false} />
                <XAxis
                  dataKey="name"
                  tickLine={false}
                  axisLine={false}
                  tickMargin={10}
                />
                <ChartTooltip content={<ChartTooltipContent />} />
                <Bar
                  dataKey="budget"
                  fill={`url(#${gradientId}-budget)`}
                  stroke="var(--color-budget)"
                  strokeOpacity={0.3}
                  radius={[4, 4, 0, 0]}
                />
                <Bar
                  dataKey="actual"
                  fill={`url(#${gradientId}-actual)`}
                  stroke="var(--color-actual)"
                  strokeOpacity={0.3}
                  radius={[4, 4, 0, 0]}
                />
              </BarChart>
            </ChartContainer>
          )}
          {!bva.loading &&
            !bva.error &&
            bva.data.length === 0 && (
              <p className="text-sm text-muted-foreground">
                No budget data for this year
              </p>
            )}
        </CardContent>
      </Card>
    </div>
  );
}
