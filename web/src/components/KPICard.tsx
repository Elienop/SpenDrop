import styles from '../styles/Dashboard.module.css';

interface KPICardProps {
  label: string;
  value: string;
  color?: string;
}

export function KPICard({ label, value, color }: KPICardProps) {
  return (
    <div className={styles.kpiCard}>
      <span className={styles.kpiLabel}>{label}</span>
      <span
        className={styles.kpiValue}
        style={color ? { color } : undefined}
      >
        {value}
      </span>
    </div>
  );
}
