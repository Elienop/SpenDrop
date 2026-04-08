import type { CSSProperties } from 'react';
import styles from '../styles/ChartTooltip.module.css';

interface TooltipPayloadItem {
  name: string;
  value: number;
  color: string;
  fill?: string;
}

interface ChartTooltipProps {
  active?: boolean;
  payload?: TooltipPayloadItem[];
  label?: string;
  /** Map of SVG fill values (e.g. "url(#cf-stripe)") to CSS legend-dot styles */
  patternStyles?: Record<string, CSSProperties>;
}

function formatCurrency(value: number): string {
  return '$' + Math.abs(value).toLocaleString('en-US', {
    minimumFractionDigits: 0,
    maximumFractionDigits: 0,
  });
}

export function ChartTooltip({ active, payload, label, patternStyles }: ChartTooltipProps) {
  if (!active || !payload?.length) return null;

  return (
    <div className={styles.tooltip}>
      <div className={styles.label}>{label}</div>
      {payload.map((entry, i) => {
        const fill = entry.color || entry.fill || '';
        const isPattern = fill.startsWith('url(');
        const dotStyle: CSSProperties = patternStyles?.[fill]
          ?? (isPattern ? { background: 'var(--text-tertiary)' } : { background: fill });
        return (
          <div key={i} className={styles.row}>
            <div className={styles.dot} style={dotStyle} />
            <span className={styles.name}>{entry.name}</span>
            <span className={styles.value}>{formatCurrency(entry.value)}</span>
          </div>
        );
      })}
    </div>
  );
}
