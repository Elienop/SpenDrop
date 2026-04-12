import { useMemo } from 'react';
import type { HeatmapEntry } from '@/api/types';
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from '@/components/ui/tooltip';
import { cn } from '@/lib/utils';

interface SpendingHeatmapProps {
  data: HeatmapEntry[];
  year: number;
  /**
   * Currency formatter bound to the household's base currency. Injected
   * by the parent (PatternsTab) so the heatmap stays agnostic about
   * which currency the household uses.
   */
  format: (amount: number) => string;
}

function generateYearDates(year: number): (string | null)[] {
  const cells: (string | null)[] = [];
  const jan1 = new Date(Date.UTC(year, 0, 1));
  const startDow = (jan1.getUTCDay() + 6) % 7; // Monday = 0

  // Pad empty cells for days before Jan 1
  for (let i = 0; i < startDow; i++) cells.push(null);

  // Fill all days of the year (UTC to avoid timezone offset issues)
  const d = new Date(Date.UTC(year, 0, 1));
  while (d.getUTCFullYear() === year) {
    const iso = d.toISOString().slice(0, 10);
    cells.push(iso);
    d.setUTCDate(d.getUTCDate() + 1);
  }

  // Pad end to fill final column (total cells = multiple of 7)
  while (cells.length % 7 !== 0) cells.push(null);
  return cells;
}

/**
 * Percentile → opacity stops for the primary color. Shared by
 * `getOpacity` (cell rendering) and `HeatmapLegend` (scale swatches)
 * so the rendered scale can never drift.
 */
const OPACITY_STOPS = [0.3, 0.5, 0.75, 1] as const;

function getOpacity(
  total: number,
  p25: number,
  p50: number,
  p75: number,
): number {
  if (total <= p25) return OPACITY_STOPS[0];
  if (total <= p50) return OPACITY_STOPS[1];
  if (total <= p75) return OPACITY_STOPS[2];
  return OPACITY_STOPS[3];
}

export function SpendingHeatmap({ data, year, format }: SpendingHeatmapProps) {
  const cells = useMemo(() => generateYearDates(year), [year]);

  const { lookup, p25, p50, p75 } = useMemo(() => {
    const map = new Map(data.map((d) => [d.date, d.total]));
    const sorted = data.map((d) => d.total).sort((a, b) => a - b);
    const pct = (p: number) => sorted[Math.floor(sorted.length * p)] ?? 0;
    return { lookup: map, p25: pct(0.25), p50: pct(0.5), p75: pct(0.75) };
  }, [data]);

  return (
    <TooltipProvider>
      <div className="flex flex-col gap-3">
        {/* 53 columns x 7 rows, column-first flow: Mon-Sun top-to-bottom, weeks left-to-right like GitHub */}
        <div
          className="grid gap-[3px]"
          style={{
            gridTemplateColumns: 'repeat(53, 1fr)',
            gridTemplateRows: 'repeat(7, 1fr)',
            gridAutoFlow: 'column',
          }}
        >
          {cells.map((dateStr, i) =>
            dateStr === null ? (
              <div key={`pad-${i}`} />
            ) : (
              <HeatmapCell
                key={dateStr}
                dateStr={dateStr}
                total={lookup.get(dateStr) ?? 0}
                p25={p25}
                p50={p50}
                p75={p75}
                format={format}
              />
            ),
          )}
        </div>
        <HeatmapLegend />
      </div>
    </TooltipProvider>
  );
}

function HeatmapCell({
  dateStr,
  total,
  p25,
  p50,
  p75,
  format,
}: {
  dateStr: string;
  total: number;
  p25: number;
  p50: number;
  p75: number;
  format: (amount: number) => string;
}) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <div
          className={cn('aspect-square rounded-sm', total === 0 && 'bg-muted')}
          style={
            total > 0
              ? {
                  backgroundColor: 'hsl(var(--primary))',
                  opacity: getOpacity(total, p25, p50, p75),
                }
              : undefined
          }
        />
      </TooltipTrigger>
      <TooltipContent>
        <p>
          {dateStr}: {format(total)}
        </p>
      </TooltipContent>
    </Tooltip>
  );
}

function HeatmapLegend() {
  return (
    <div
      role="group"
      aria-label="Spending intensity legend"
      className="flex flex-wrap items-center justify-end gap-x-4 gap-y-1 text-xs text-muted-foreground"
    >
      <div className="flex items-center gap-1.5">
        <div className="h-3 w-3 rounded-sm bg-muted" aria-hidden />
        <span>No spend</span>
      </div>
      <div className="flex items-center gap-1.5">
        <span>Less</span>
        <div className="flex items-center gap-[3px]">
          {OPACITY_STOPS.map((opacity, i) => (
            <div
              key={i}
              className="h-3 w-3 rounded-sm"
              style={{
                backgroundColor: 'hsl(var(--primary))',
                opacity,
              }}
              aria-hidden
            />
          ))}
        </div>
        <span>More</span>
      </div>
    </div>
  );
}
