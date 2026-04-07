import styles from '../styles/Transactions.module.css';

interface CategoryBadgeProps {
  name: string;
  color: string;
}

export function CategoryBadge({ name, color }: CategoryBadgeProps) {
  return (
    <span
      className={styles.badge}
      style={{ backgroundColor: color }}
    >
      {name}
    </span>
  );
}
