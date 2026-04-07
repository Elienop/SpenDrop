import styles from '../styles/Tabs.module.css';

interface Tab {
  key: string;
  label: string;
}

interface TabsProps {
  tabs: Tab[];
  activeKey: string;
  onTabChange: (key: string) => void;
}

export function Tabs({ tabs, activeKey, onTabChange }: TabsProps) {
  return (
    <div className={styles.tabRow} role="tablist">
      {tabs.map(tab => (
        <button
          key={tab.key}
          role="tab"
          aria-selected={tab.key === activeKey}
          className={`${styles.tab}${tab.key === activeKey ? ` ${styles.tabActive}` : ''}`}
          onClick={() => onTabChange(tab.key)}
        >
          {tab.label}
        </button>
      ))}
    </div>
  );
}
