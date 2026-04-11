import { TrendingUp, TrendingDown } from 'lucide-react';
import {
  Card,
  CardHeader,
  CardDescription,
  CardContent,
  CardFooter,
} from '@/components/ui/card';
import { Badge } from '@/components/ui/badge';

export interface KpiDelta {
  percent: number;
  direction: 'up' | 'down' | 'flat';
}

interface KpiCardProps {
  label: string;
  dollars: string;
  cents: string;
  delta?: KpiDelta | null;
  footnote?: string;
}

export function KpiCard({
  label,
  dollars,
  cents,
  delta,
  footnote,
}: KpiCardProps) {
  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
        <CardDescription className="text-sm font-medium">
          {label}
        </CardDescription>
        {delta && (
          <Badge variant="outline" className="gap-1 text-xs">
            {delta.direction === 'up' && (
              <TrendingUp className="h-3 w-3" aria-hidden="true" />
            )}
            {delta.direction === 'down' && (
              <TrendingDown className="h-3 w-3" aria-hidden="true" />
            )}
            {delta.direction === 'up' ? '+' : delta.direction === 'down' ? '-' : ''}
            {Math.abs(delta.percent).toFixed(1)}%
          </Badge>
        )}
      </CardHeader>
      <CardContent className="pb-2">
        <div className="font-mono text-2xl font-semibold tabular-nums">
          {dollars}{cents}
        </div>
      </CardContent>
      {footnote && (
        <CardFooter>
          <p className="text-xs text-muted-foreground">{footnote}</p>
        </CardFooter>
      )}
    </Card>
  );
}
