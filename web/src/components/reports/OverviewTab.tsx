import { useId, useMemo, useState } from 'react';
import { BarChart, Bar, AreaChart, Area, XAxis, CartesianGrid, Cell } from 'recharts';
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
import { Skeleton } from '@/components/ui/skeleton';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { AlertCircle } from 'lucide-react';
import { useIncomeExpenses, useBudgetVsActual } from '@/hooks/useReports';
import { MONTH_NAMES, INCEXP_CONFIG, yearOptions } from './utils';
import { cn } from '@/lib/utils';

const NET_FLOW_CONFIG = {
  cumulative: { label: 'Net Cash Flow', color: 'hsl(var(--primary))' },
} satisfies ChartConfig;

const BVA_CONFIG = {
  budget: { label: 'Budget', color: 'hsl(var(--chart-1))' },
  actual: { label: 'Actual', color: 'hsl(var(--chart-8))' },
} satisfies ChartConfig;

export function OverviewTab() {
  const [months, setMonths] = useState(12);
  const [bvaYear, setBvaYear] = useState(new Date().getFullYear());
  const incExp = useIncomeExpenses(months);
  const bva = useBudgetVsActual(bvaYear);
  const gradientId = useId();

  const incExpData = useMemo(
    () =>
      incExp.data.map((entry) => ({
        name: `${MONTH_NAMES[entry.month - 1]} ${entry.year}`,
        income: entry.income,
        expenses: entry.expenses,
      })),
    [incExp.data],
  );

  const cashFlowData = useMemo(() => {
    const result: { name: string; cumulative: number }[] = [];
    incExp.data.reduce((acc, entry) => {
      const total = acc + entry.net;
      result.push({
        name: `${MONTH_NAMES[entry.month - 1]} ${entry.year}`,
        cumulative: total,
      });
      return total;
    }, 0);
    return result;
  }, [incExp.data]);

  const bvaData = useMemo(
    () =>
      bva.data.map((e) => ({
        name: MONTH_NAMES[e.month - 1],
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
        <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
          <CardTitle
            id="income-expenses-heading"
            className="text-base font-semibold"
          >
            Income vs Expenses
          </CardTitle>
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
              <BarChart data={incExpData}>
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
                  dataKey="name"
                  tickLine={false}
                  axisLine={false}
                  tickMargin={10}
                />
                <ChartTooltip
                  content={<ChartTooltipContent hideIndicator />}
                />
                <ChartLegend content={<ChartLegendContent />} />
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
        <CardHeader className="pb-2">
          <CardTitle
            id="net-cash-flow-heading"
            className="text-base font-semibold"
          >
            Net Cash Flow
          </CardTitle>
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
              <AreaChart data={cashFlowData}>
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
                  dataKey="name"
                  tickLine={false}
                  axisLine={false}
                  tickMargin={10}
                />
                <ChartTooltip content={<ChartTooltipContent />} />
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
        <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
          <CardTitle
            id="budget-vs-actual-heading"
            className="text-base font-semibold"
          >
            Budget vs Actual
          </CardTitle>
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
              <BarChart data={bvaData}>
                <CartesianGrid vertical={false} />
                <XAxis
                  dataKey="name"
                  tickLine={false}
                  axisLine={false}
                  tickMargin={10}
                />
                <ChartTooltip content={<ChartTooltipContent />} />
                <ChartLegend content={<ChartLegendContent />} />
                <Bar
                  dataKey="budget"
                  fill="var(--color-budget)"
                  radius={[4, 4, 0, 0]}
                />
                <Bar dataKey="actual" radius={[4, 4, 0, 0]}>
                  {bvaData.map((entry, i) => (
                    <Cell
                      key={i}
                      fill={
                        entry.actual <= entry.budget
                          ? 'hsl(var(--chart-8))'
                          : 'hsl(var(--chart-10))'
                      }
                    />
                  ))}
                </Bar>
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
