import { useState } from 'react';
import { BarChart, Bar, XAxis, YAxis, CartesianGrid, Cell } from 'recharts';
import {
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
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
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { AlertCircle, X } from 'lucide-react';
import {
  useSpendingHeatmap,
  useRecurring,
  useTagBreakdown,
  dismissRecurring,
} from '@/hooks/useReports';
import { SpendingHeatmap } from './SpendingHeatmap';
import { MONTH_FULL_NAMES, formatCurrency, yearOptions } from './utils';
import { cn } from '@/lib/utils';

const TAG_CONFIG = {
  total: { label: 'Total', color: 'hsl(var(--primary))' },
} satisfies ChartConfig;

export function PatternsTab() {
  const now = new Date();
  const [year, setYear] = useState(now.getFullYear());
  const [tagYear, setTagYear] = useState(now.getFullYear());
  const [tagMonth, setTagMonth] = useState(0);
  const heatmap = useSpendingHeatmap(year);
  const recurring = useRecurring(year);
  const tags = useTagBreakdown(tagYear, tagMonth);

  const tagYears = yearOptions();

  return (
    <div className="flex flex-col gap-6">
      {/* Spending Heatmap */}
      <Card aria-labelledby="spending-heatmap-heading">
        <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
          <CardTitle
            id="spending-heatmap-heading"
            className="text-base font-semibold"
          >
            Spending Heatmap
          </CardTitle>
          <Select
            value={String(year)}
            onValueChange={(v) => setYear(Number(v))}
          >
            <SelectTrigger aria-label="Heatmap Year" className="h-9 w-[120px]">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {tagYears.map((y) => (
                <SelectItem key={y.value} value={y.value}>
                  {y.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </CardHeader>
        <CardContent>
          {heatmap.loading && <Skeleton className="h-[120px] w-full" />}
          {heatmap.error && (
            <Alert variant="destructive">
              <AlertCircle className="h-4 w-4" />
              <AlertDescription>{heatmap.error}</AlertDescription>
            </Alert>
          )}
          {!heatmap.loading && !heatmap.error && (
            <div
              className={cn(
                'transition-opacity duration-200',
                heatmap.fetching && !heatmap.loading && 'opacity-60',
              )}
            >
              <SpendingHeatmap data={heatmap.data} year={year} />
            </div>
          )}
        </CardContent>
      </Card>

      {/* Recurring Expenses */}
      <Card aria-labelledby="recurring-heading">
        <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
          <CardTitle id="recurring-heading" className="text-base font-semibold">
            Recurring Expenses
          </CardTitle>
        </CardHeader>
        <CardContent>
          {recurring.loading && <Skeleton className="h-[200px] w-full" />}
          {recurring.error && (
            <Alert variant="destructive">
              <AlertCircle className="h-4 w-4" />
              <AlertDescription>{recurring.error}</AlertDescription>
            </Alert>
          )}
          {!recurring.loading &&
            !recurring.error &&
            recurring.data.length === 0 && (
              <p className="text-sm text-muted-foreground">
                No recurring expenses detected yet
              </p>
            )}
          {!recurring.loading &&
            !recurring.error &&
            recurring.data.length > 0 && (
              <div
                className={cn(
                  'transition-opacity duration-200',
                  recurring.fetching && !recurring.loading && 'opacity-60',
                )}
              >
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>Description</TableHead>
                      <TableHead className="text-right">Monthly Avg</TableHead>
                      <TableHead className="text-right">Frequency</TableHead>
                      <TableHead className="text-right">
                        Annual Total
                      </TableHead>
                      <TableHead className="w-[50px]" />
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {recurring.data.map((entry) => (
                      <TableRow key={entry.description}>
                        <TableCell>{entry.description}</TableCell>
                        <TableCell className="text-right font-mono tabular-nums">
                          {formatCurrency(entry.monthly_avg)}
                        </TableCell>
                        <TableCell className="text-right">
                          {entry.month_count}/12 months
                        </TableCell>
                        <TableCell className="text-right font-mono tabular-nums">
                          {formatCurrency(entry.annual_total)}
                        </TableCell>
                        <TableCell>
                          <Button
                            variant="ghost"
                            size="icon"
                            onClick={async () => {
                              try {
                                await dismissRecurring(
                                  year,
                                  entry.description,
                                );
                                recurring.refetch();
                              } catch (err) {
                                console.error('Failed to dismiss:', err);
                              }
                            }}
                          >
                            <X className="h-4 w-4" />
                          </Button>
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </div>
            )}
        </CardContent>
      </Card>

      {/* Tag Analysis */}
      <Card aria-labelledby="tag-analysis-heading">
        <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
          <CardTitle
            id="tag-analysis-heading"
            className="text-base font-semibold"
          >
            Tag Analysis
          </CardTitle>
          <div className="flex gap-2">
            <Select
              value={String(tagMonth)}
              onValueChange={(v) => setTagMonth(Number(v))}
            >
              <SelectTrigger aria-label="Tag Month" className="h-9 w-[140px]">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="0">Year to Date</SelectItem>
                {MONTH_FULL_NAMES.map((m, i) => (
                  <SelectItem key={m} value={String(i + 1)}>
                    {m}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <Select
              value={String(tagYear)}
              onValueChange={(v) => setTagYear(Number(v))}
            >
              <SelectTrigger aria-label="Tag Year" className="h-9 w-[100px]">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {tagYears.map((y) => (
                  <SelectItem key={y.value} value={y.value}>
                    {y.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
        </CardHeader>
        <CardContent>
          {tags.loading && <Skeleton className="h-[300px] w-full" />}
          {tags.error && (
            <Alert variant="destructive">
              <AlertCircle className="h-4 w-4" />
              <AlertDescription>{tags.error}</AlertDescription>
            </Alert>
          )}
          {!tags.loading && !tags.error && tags.data.length === 0 && (
            <p className="text-sm text-muted-foreground">
              No tagged transactions yet. Add tags to your transactions to see
              breakdowns here.
            </p>
          )}
          {!tags.loading && !tags.error && tags.data.length > 0 && (
            <ChartContainer
              config={TAG_CONFIG}
              className={cn(
                'h-[300px] w-full transition-opacity duration-200',
                tags.fetching && !tags.loading && 'opacity-60',
              )}
            >
              <BarChart data={tags.data} layout="vertical">
                <CartesianGrid horizontal={false} />
                <XAxis type="number" tickLine={false} axisLine={false} />
                <YAxis
                  dataKey="tag"
                  type="category"
                  tickLine={false}
                  axisLine={false}
                  width={120}
                />
                <ChartTooltip content={<ChartTooltipContent />} />
                <Bar dataKey="total" radius={[0, 4, 4, 0]}>
                  {tags.data.map((_, i) => (
                    <Cell
                      key={i}
                      fill={`hsl(var(--chart-${(i % 11) + 1}))`}
                    />
                  ))}
                </Bar>
              </BarChart>
            </ChartContainer>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
