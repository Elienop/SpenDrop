import styles from '../styles/ChartTooltip.module.css';

interface TooltipPayloadItem {
  name: string;
  value: number;
  color: string;
}

interface ChartTooltipProps {
  active?: boolean;
  payload?: TooltipPayloadItem[];
  label?: string;
}

function formatCurrency(value: number): string {
  return '$' + Math.abs(value).toLocaleString('en-US', {
    minimumFractionDigits: 0,
    maximumFractionDigits: 0,
  });
}

export function ChartTooltip({ active, payload, label }: ChartTooltipProps) {
  if (!active || !payload?.length) return null;

  return (
    <div className={styles.tooltip}>
      <div className={styles.label}>{label}</div>
      {payload.map((entry, i) => (
        <div key={i} className={styles.row}>
          <div className={styles.dot} style={{ background: entry.color }} />
          <span className={styles.name}>{entry.name}</span>
          <span className={styles.value}>{formatCurrency(entry.value)}</span>
        </div>
      ))}
    </div>
  );
}
