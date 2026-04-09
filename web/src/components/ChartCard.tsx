import type { ReactNode } from 'react';
import { Card, CardHeader, CardDescription, CardContent } from '@/components/ui/card';
import { Skeleton } from '@/components/ui/skeleton';
import { cn } from '@/lib/utils';

interface ChartCardProps {
  title: string;
  subtitle?: string;
  action?: ReactNode;
  loading?: boolean;
  className?: string;
  children: ReactNode;
}

export function ChartCard({
  title,
  subtitle,
  action,
  loading = false,
  className,
  children,
}: ChartCardProps) {
  return (
    <Card className={cn('flex flex-col', className)}>
      <CardHeader className="flex flex-row items-start justify-between gap-4 space-y-0 pb-4">
        <div className="flex flex-col gap-0.5">
          {/*
            Use a semantic <h2> (not shadcn's default CardTitle <div>) to keep
            the heading hierarchy consistent under the Dashboard <h1>. Classes
            mirror CardTitle's shadcn defaults so visual output is unchanged.
          */}
          <h2 className="text-base font-semibold leading-none tracking-tight">
            {title}
          </h2>
          {subtitle && (
            <CardDescription className="text-xs text-muted-foreground">
              {subtitle}
            </CardDescription>
          )}
        </div>
        {action && <div className="shrink-0">{action}</div>}
      </CardHeader>
      <CardContent className="flex flex-1 flex-col pt-0">
        {loading ? (
          <Skeleton data-testid="chart-loading" className="h-64 w-full" />
        ) : (
          children
        )}
      </CardContent>
    </Card>
  );
}
