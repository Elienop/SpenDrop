import { useState } from 'react';
import {
  BarChart,
  Bar,
  XAxis,
  YAxis,
  CartesianGrid,
  Cell,
  ReferenceLine,
} from 'recharts';
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
import { toast } from 'sonner';
import {
  useSpendingHeatmap,
  useRecurring,
  useTagBreakdown,
  dismissRecurring,
} from '@/hooks/useReports';
import { SpendingHeatmap } from './SpendingHeatmap';
import { tagChartHeightPx } from './tagChart';
import {
  CLAMPED_TABLE_TEXT,
  PHONE_TABLE_DENSITY,
  TABLE_TEXT_CELL_WIDTH,
} from './utils';
import { MONTH_NAMES_FULL, yearOptionsFrom } from '@/lib/dates';
import { formatCurrency } from '@/lib/format';
import { useBaseCurrency } from '@/hooks/useBaseCurrency';
import { useFinePointerDesktop } from '@/hooks/useFinePointerDesktop';
import { useReportYears } from '@/hooks/useReportYears';
import { cn } from '@/lib/utils';

const TAG_CONFIG = {
  total: { label: 'Total', color: 'hsl(var(--primary))' },
} satisfies ChartConfig;

export function PatternsTab() {
  const now = new Date();
  const [year, setYear] = useState(now.getFullYear());
  const [tagYear, setTagYear] = useState(now.getFullYear());
  const [tagMonth, setTagMonth] = useState(0);
  // Mirrors the heatmap's own gate exactly — the placeholder has to reserve
  // room for the branch that is about to render, and that branch is now chosen
  // on pointer capability, not width.
  const isMobile = !useFinePointerDesktop();
  const heatmap = useSpendingHeatmap(year);
  const recurring = useRecurring(year);
  const tags = useTagBreakdown(tagYear, tagMonth);

  const baseCurrency = useBaseCurrency();
  const fmt = (amount: number) => formatCurrency(amount, baseCurrency);

  // Exactly the years the ledger holds, so an imported 1984 statement is
  // selectable and the empty years between are not offered. One list feeds
  // BOTH Selects on this tab (heatmap `year` and tag `tagYear`), so BOTH
  // selections are unioned in — dropping either from the list after a refetch
  // would leave that Select holding a value with no matching item, which
  // renders a blank trigger, silently, with no error.
  const { years: ledgerYears } = useReportYears();
  const tagYears = yearOptionsFrom(ledgerYears, year, tagYear);

  return (
    <div className="flex flex-col gap-6">
      {/* Spending Heatmap */}
      {/* `role="region"` is what makes the `aria-labelledby` beneath it do
          anything. `Card` renders a bare <div>, whose implicit `generic` role
          is on ARIA's name-prohibited list — so the association was silently
          discarded and the card read as unnamed. */}
      <Card role="region" aria-labelledby="spending-heatmap-heading">
        <CardHeader className="flex flex-row flex-wrap items-center justify-between gap-3 space-y-0 pb-4">
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
          {/* Sized per BRANCH, because the two presentations are nowhere near
              the same height and one number cannot be right for both. The old
              flat 120px under-reserved even the desktop grid (7 rows of
              ~21px cells plus the legend is closer to 200px), so the card
              grew when data landed; a phone month grid plus the month picker
              is roughly twice that again. */}
          {heatmap.loading && (
            <Skeleton
              className={cn('w-full', isMobile ? 'h-[420px]' : 'h-[200px]')}
            />
          )}
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
              <SpendingHeatmap
                data={heatmap.data}
                year={year}
                format={fmt}
              />
            </div>
          )}
        </CardContent>
      </Card>

      {/* Recurring Expenses */}
      {/* Named for the same reason as the heatmap card above: `Card` is a
          bare <div>, whose implicit `generic` role is name-prohibited, so the
          `aria-labelledby` was discarded. All THREE carry it — one landmark
          among three identical cards would put a single arbitrary card in a
          landmark list and hide its siblings. */}
      <Card role="region" aria-labelledby="recurring-heading">
        <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-4">
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
                {/* Same phone-width density as Top Merchants in SpendingTab —
                    see the note there. Five columns here, so 160px of padding
                    before any content. */}
                <Table className={PHONE_TABLE_DENSITY}>
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
                        {/* Same exposure and same treatment as Top Merchants
                            in SpendingTab — see the note there for why
                            `overflow-wrap:anywhere` (not `break-words`) is the
                            class that bounds a table cell's WIDTH, why
                            `line-clamp-3` is needed to bound its HEIGHT, and
                            why three clamped lines beat one truncated one on a
                            surface with no row expansion. */}
                        <TableCell className={TABLE_TEXT_CELL_WIDTH}>
                          <div
                            className={CLAMPED_TABLE_TEXT}
                            title={entry.description}
                          >
                            {entry.description}
                          </div>
                        </TableCell>
                        <TableCell className="text-right font-mono tabular-nums">
                          {fmt(entry.monthly_avg)}
                        </TableCell>
                        <TableCell className="text-right">
                          {entry.month_count}/12 months
                        </TableCell>
                        <TableCell className="text-right font-mono tabular-nums">
                          {fmt(entry.annual_total)}
                        </TableCell>
                        <TableCell>
                          <Button
                            variant="ghost"
                            size="icon"
                            aria-label="Dismiss recurring expense"
                            onClick={async () => {
                              try {
                                await dismissRecurring(
                                  year,
                                  entry.description,
                                );
                                recurring.refetch();
                              } catch {
                                toast.error('Failed to dismiss recurring expense');
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
      <Card role="region" aria-labelledby="tag-analysis-heading">
        <CardHeader className="flex flex-row flex-wrap items-center justify-between gap-3 space-y-0 pb-4">
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
                {MONTH_NAMES_FULL.map((m, i) => (
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
              style={{ height: tagChartHeightPx(tags.data.length) }}
              className={cn(
                // `aspect-auto` neutralizes ChartContainer's base `aspect-video`
                // so our explicit data-driven height is the sole sizing input —
                // matches the variable-height charts in SpendingTab.
                'aspect-auto w-full transition-opacity duration-200',
                tags.fetching && !tags.loading && 'opacity-60',
              )}
            >
              <BarChart accessibilityLayer data={tags.data} layout="vertical">
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
                {/* A tag whose refunds outweigh its spending nets negative and
                    draws LEFTWARD — recharts 3 extends the default
                    [0, 'auto'] domain to include negative data rather than
                    clipping it. The axis above is visible, but its zero tick
                    is one label among several; the line is what makes the
                    direction of a bar readable at a glance. */}
                <ReferenceLine x={0} stroke="hsl(var(--border))" />
                {/* Symmetric radius, not [0, 4, 4, 0]: the array form rounds
                    the RIGHT end only, so a leftward bar came out rounded at
                    the zero line and square at its own tip. */}
                <Bar dataKey="total" radius={4}>
                  {tags.data.map((_, i) => (
                    <Cell
                      key={i}
                      fill={`hsl(var(--chart-${(i % 20) + 1}))`}
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
