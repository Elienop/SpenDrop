import { useMemo } from 'react';
import type { HeatmapEntry } from '@/api/types';
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from '@/components/ui/tooltip';
import { cn } from '@/lib/utils';
import { formatCurrency } from './utils';

interface SpendingHeatmapProps {
  data: HeatmapEntry[];
  year: number;
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

function getOpacity(
  total: number,
  p25: number,
  p50: number,
  p75: number,
): number {
  if (total <= p25) return 0.3;
  if (total <= p50) return 0.5;
  if (total <= p75) return 0.75;
  return 1;
}

export function SpendingHeatmap({ data, year }: SpendingHeatmapProps) {
  const cells = useMemo(() => generateYearDates(year), [year]);

  const { lookup, p25, p50, p75 } = useMemo(() => {
    const map = new Map(data.map((d) => [d.date, d.total]));
    const sorted = data.map((d) => d.total).sort((a, b) => a - b);
    const pct = (p: number) => sorted[Math.floor(sorted.length * p)] ?? 0;
    return { lookup: map, p25: pct(0.25), p50: pct(0.5), p75: pct(0.75) };
  }, [data]);

  return (
    <TooltipProvider>
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
            />
          ),
        )}
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
}: {
  dateStr: string;
  total: number;
  p25: number;
  p50: number;
  p75: number;
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
          {dateStr}: {formatCurrency(total)}
        </p>
      </TooltipContent>
    </Tooltip>
  );
}
