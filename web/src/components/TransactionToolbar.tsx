import styles from '../styles/Transactions.module.css';

interface TransactionToolbarProps {
  search: string;
  onSearchChange: (value: string) => void;
  type: string;
  onTypeChange: (value: string) => void;
  activeFilterCount: number;
  showFilters: boolean;
  onToggleFilters: () => void;
  showEntry: boolean;
  onToggleEntry: () => void;
}

export function TransactionToolbar({
  search,
  onSearchChange,
  type,
  onTypeChange,
  activeFilterCount,
  showFilters,
  onToggleFilters,
  showEntry,
  onToggleEntry,
}: TransactionToolbarProps) {
  return (
    <div className={styles.toolbar}>
      {/* Search */}
      <div className={styles.toolbarSearch}>
        <span className={styles.toolbarSearchIcon} aria-hidden="true">
          &#128269;
        </span>
        <input
          type="text"
          className={styles.toolbarSearchInput}
          placeholder="Search transactions..."
          value={search}
          onChange={(e) => onSearchChange(e.target.value)}
          aria-label="Search transactions"
        />
      </div>

      {/* Divider */}
      <div className={styles.toolbarDivider} />

      {/* Type Toggle */}
      <div className={styles.typeToggle}>
        <button
          type="button"
          className={`${styles.typeButton} ${type === '' ? styles.typeActive : ''}`}
          onClick={() => onTypeChange('')}
        >
          All
        </button>
        <button
          type="button"
          className={`${styles.typeButton} ${type === 'expense' ? styles.typeActive : ''}`}
          onClick={() => onTypeChange('expense')}
        >
          Expenses
        </button>
        <button
          type="button"
          className={`${styles.typeButton} ${type === 'income' ? styles.typeActive : ''}`}
          onClick={() => onTypeChange('income')}
        >
          Income
        </button>
      </div>

      {/* Divider */}
      <div className={styles.toolbarDivider} />

      {/* Filters Button */}
      <button
        type="button"
        className={styles.filterButton}
        onClick={onToggleFilters}
        aria-expanded={showFilters}
      >
        {activeFilterCount > 0 && (
          <span className={styles.filterDot} aria-hidden="true" />
        )}
        Filters{activeFilterCount > 0 ? ` (${activeFilterCount})` : ''}
      </button>

      {/* + Add / Cancel Button */}
      <button
        type="button"
        className={styles.addButtonToolbar}
        onClick={onToggleEntry}
        aria-expanded={showEntry}
      >
        {showEntry ? 'Cancel' : '+ Add'}
      </button>
    </div>
  );
}
