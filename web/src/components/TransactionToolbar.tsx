import { Search } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { ButtonGroup } from '@/components/ui/button-group';
import { Input } from '@/components/ui/input';
import { Badge } from '@/components/ui/badge';

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
    <div className="flex flex-wrap items-center gap-2 rounded-lg border bg-card p-2">
      {/* 240px is the min width where the "Search transactions..." placeholder fits without truncation */}
      <div className="relative min-w-[240px] flex-1">
        <Search
          aria-hidden="true"
          className="pointer-events-none absolute left-2.5 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground"
        />
        <Input
          type="text"
          value={search}
          onChange={(e) => onSearchChange(e.target.value)}
          placeholder="Search transactions..."
          aria-label="Search transactions"
          className="h-9 pl-8"
        />
      </div>

      <div className="hidden h-6 w-px bg-border sm:block" aria-hidden="true" />

      <ButtonGroup>
        {([
          { value: '', label: 'All' },
          { value: 'expense', label: 'Expenses' },
          { value: 'income', label: 'Income' },
        ] as const).map((opt) => (
          <Button
            key={opt.value || 'all'}
            type="button"
            variant={type === opt.value ? 'secondary' : 'outline'}
            size="sm"
            className="text-xs"
            onClick={() => onTypeChange(opt.value)}
          >
            {opt.label}
          </Button>
        ))}
      </ButtonGroup>

      <div className="hidden h-6 w-px bg-border sm:block" aria-hidden="true" />

      <Button
        type="button"
        variant="outline"
        size="sm"
        onClick={onToggleFilters}
        aria-expanded={showFilters}
      >
        Filters
        {activeFilterCount > 0 && (
          <Badge variant="secondary" className="ml-2 h-5 px-1.5 text-[10px]">
            {activeFilterCount}
          </Badge>
        )}
      </Button>

      <Button
        type="button"
        size="sm"
        onClick={onToggleEntry}
        aria-expanded={showEntry}
      >
        {showEntry ? 'Cancel' : '+ Add'}
      </Button>
    </div>
  );
}
