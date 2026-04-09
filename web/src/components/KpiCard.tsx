import type { LucideIcon } from 'lucide-react';
import { ArrowUpRight, ArrowDownRight } from 'lucide-react';
import { Card } from '@/components/ui/card';
import { cn } from '@/lib/utils';

export interface KpiDelta {
  percent: number;
  direction: 'up' | 'down' | 'flat';
}

interface KpiCardProps {
  label: string;
  icon?: LucideIcon;
  dollars: string;
  cents: string;
  delta?: KpiDelta | null;
  featured?: boolean;
}

export function KpiCard({
  label,
  icon: Icon,
  dollars,
  cents,
  delta,
  featured = false,
}: KpiCardProps) {
  return (
    <Card
      data-featured={featured ? 'true' : 'false'}
      className={cn(
        'flex flex-col gap-3.5 p-6 transition-shadow hover:shadow-md',
        featured && 'border-transparent bg-primary text-primary-foreground',
      )}
    >
      <div className="flex items-center justify-between">
        <span
          className={cn(
            'text-xs font-medium uppercase tracking-wide',
            featured ? 'text-primary-foreground/80' : 'text-muted-foreground',
          )}
        >
          {label}
        </span>
        {Icon && (
          <div
            className={cn(
              'grid h-9 w-9 shrink-0 place-items-center rounded-lg',
              featured
                ? 'bg-primary-foreground/15 text-primary-foreground'
                : 'bg-muted text-muted-foreground',
            )}
          >
            <Icon className="h-[18px] w-[18px]" strokeWidth={1.5} aria-hidden="true" />
          </div>
        )}
      </div>
      <div className="font-mono text-3xl font-bold leading-none tracking-tight tabular-nums">
        {dollars}
        <span
          className={cn(
            'text-lg font-medium',
            featured ? 'text-primary-foreground/70' : 'text-muted-foreground',
          )}
        >
          {cents}
        </span>
      </div>
      {delta && (
        <div className="flex items-center gap-2 text-xs">
          <span
            className={cn(
              'inline-flex items-center gap-1 rounded-md px-2 py-0.5 font-semibold',
              featured && 'bg-primary-foreground/15 text-primary-foreground',
              !featured && delta.direction === 'up' && 'bg-emerald-500/10 text-emerald-500',
              !featured && delta.direction === 'down' && 'bg-rose-500/10 text-rose-500',
              !featured && delta.direction === 'flat' && 'bg-muted text-muted-foreground',
            )}
          >
            {delta.direction === 'up' && (
              <ArrowUpRight className="h-3 w-3" strokeWidth={3} aria-hidden="true" />
            )}
            {delta.direction === 'down' && (
              <ArrowDownRight className="h-3 w-3" strokeWidth={3} aria-hidden="true" />
            )}
            {/* Defensive abs: direction already encodes sign, the label
                should always be positive even if a future caller forgets
                to route through toDelta. */}
            {Math.abs(delta.percent).toFixed(1)}%
          </span>
          <span
            className={cn(
              featured ? 'text-primary-foreground/60' : 'text-muted-foreground',
            )}
          >
            vs last month
          </span>
        </div>
      )}
    </Card>
  );
}
