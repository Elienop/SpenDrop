import { describe, test, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { TransactionToolbar } from './TransactionToolbar';

const defaultProps = {
  search: '',
  onSearchChange: vi.fn(),
  type: '',
  onTypeChange: vi.fn(),
  activeFilterCount: 0,
  showFilters: false,
  onToggleFilters: vi.fn(),
  showEntry: false,
  onToggleEntry: vi.fn(),
};

describe('TransactionToolbar', () => {
  test('renders type filter buttons inside a ButtonGroup', () => {
    render(<TransactionToolbar {...defaultProps} />);
    const group = screen.getByRole('group');
    expect(group).toBeInTheDocument();
    expect(group).toContainElement(screen.getByRole('button', { name: 'All' }));
    expect(group).toContainElement(screen.getByRole('button', { name: 'Expenses' }));
    expect(group).toContainElement(screen.getByRole('button', { name: 'Income' }));
  });

  test('active type button uses secondary variant', () => {
    render(<TransactionToolbar {...defaultProps} type="expense" />);
    const expenseBtn = screen.getByRole('button', { name: 'Expenses' });
    // secondary variant applies bg-secondary class
    expect(expenseBtn.className).toContain('bg-secondary');
  });

  test('inactive type buttons use outline variant', () => {
    render(<TransactionToolbar {...defaultProps} type="expense" />);
    const allBtn = screen.getByRole('button', { name: 'All' });
    // outline variant applies border and bg-background
    expect(allBtn.className).toContain('bg-background');
  });

  test('calls onTypeChange when a type button is clicked', async () => {
    const onTypeChange = vi.fn();
    const user = userEvent.setup();
    render(<TransactionToolbar {...defaultProps} onTypeChange={onTypeChange} />);
    await user.click(screen.getByRole('button', { name: 'Income' }));
    expect(onTypeChange).toHaveBeenCalledWith('income');
  });

  test('renders search input', () => {
    render(<TransactionToolbar {...defaultProps} />);
    expect(screen.getByLabelText(/search transactions/i)).toBeInTheDocument();
  });

  test('search input uses h-9 to match toolbar button height', () => {
    render(<TransactionToolbar {...defaultProps} />);
    const input = screen.getByLabelText(/search transactions/i);
    expect(input.className).toContain('h-9');
  });

  test('ButtonGroup buttons do not use custom h-7 height override', () => {
    render(<TransactionToolbar {...defaultProps} />);
    const allBtn = screen.getByRole('button', { name: 'All' });
    const expensesBtn = screen.getByRole('button', { name: 'Expenses' });
    const incomeBtn = screen.getByRole('button', { name: 'Income' });
    expect(allBtn.className).not.toContain('h-7');
    expect(expensesBtn.className).not.toContain('h-7');
    expect(incomeBtn.className).not.toContain('h-7');
  });
});
