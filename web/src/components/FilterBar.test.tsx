import { describe, test, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { FilterBar } from './FilterBar';
import type { Category, SavedFilter } from '../api/types';

const sampleCategories: Category[] = [
  { id: 1, name: 'Groceries', type: 'expense', color: '#e94560', icon: null, sort_order: 0, is_active: true, created_at: '' },
  { id: 2, name: 'Rent', type: 'expense', color: '#3366cc', icon: null, sort_order: 1, is_active: true, created_at: '' },
  { id: 3, name: 'Salary', type: 'income', color: '#4ecc99', icon: null, sort_order: 2, is_active: true, created_at: '' },
];

const sampleSavedFilters: SavedFilter[] = [
  { id: 10, user_id: 1, name: 'Big expenses', filter_json: '{"amountMin":"100"}', created_at: '', updated_at: '' },
  { id: 11, user_id: 1, name: 'Food only', filter_json: '{"categoryIds":"1"}', created_at: '', updated_at: '' },
];

describe('FilterBar', () => {
  const mockSetFilter = vi.fn();
  const mockOnClear = vi.fn();
  const mockOnSaveFilter = vi.fn();
  const mockOnLoadFilter = vi.fn();
  const mockOnDeleteFilter = vi.fn();

  const defaultFilters = {
    dateFrom: '',
    dateTo: '',
    categoryId: '',
    categoryIds: '',
    amountMin: '',
    amountMax: '',
    tags: '',
    type: '',
    search: '',
  };

  function renderFilterBar(overrides: Record<string, unknown> = {}) {
    return render(
      <FilterBar
        filters={defaultFilters}
        setFilter={mockSetFilter}
        categories={sampleCategories}
        onClear={mockOnClear}
        savedFilters={sampleSavedFilters}
        onSaveFilter={mockOnSaveFilter}
        onLoadFilter={mockOnLoadFilter}
        onDeleteFilter={mockOnDeleteFilter}
        {...overrides}
      />,
    );
  }

  beforeEach(() => {
    vi.clearAllMocks();
  });

  test('renders date presets', () => {
    renderFilterBar();
    expect(screen.getByRole('button', { name: /this month/i })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /last month/i })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /this year/i })).toBeInTheDocument();
  });

  test('renders type toggle buttons', () => {
    renderFilterBar();
    expect(screen.getByRole('button', { name: /^all$/i })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /^expenses$/i })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /^income$/i })).toBeInTheDocument();
  });

  test('renders search input', () => {
    renderFilterBar();
    expect(screen.getByPlaceholderText(/search/i)).toBeInTheDocument();
  });

  test('calls setFilter on type button click', async () => {
    const user = userEvent.setup();
    renderFilterBar();

    await user.click(screen.getByRole('button', { name: /^expenses$/i }));
    expect(mockSetFilter).toHaveBeenCalledWith('type', 'expense');
  });

  test('renders clear filters button', () => {
    renderFilterBar();
    expect(screen.getByRole('button', { name: /clear/i })).toBeInTheDocument();
  });

  // --- Multi-category chips ---

  test('renders category chips for each category', () => {
    renderFilterBar();
    expect(screen.getByRole('button', { name: 'Groceries' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Rent' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Salary' })).toBeInTheDocument();
  });

  test('category chip toggles selection on click', async () => {
    const user = userEvent.setup();
    renderFilterBar();

    await user.click(screen.getByRole('button', { name: 'Groceries' }));
    expect(mockSetFilter).toHaveBeenCalledWith('categoryIds', '1');
  });

  test('category chip deselects when already selected', async () => {
    const user = userEvent.setup();
    renderFilterBar({ filters: { ...defaultFilters, categoryIds: '1,2' } });

    await user.click(screen.getByRole('button', { name: 'Groceries' }));
    expect(mockSetFilter).toHaveBeenCalledWith('categoryIds', '2');
  });

  test('category chip adds to existing selection', async () => {
    const user = userEvent.setup();
    renderFilterBar({ filters: { ...defaultFilters, categoryIds: '1' } });

    await user.click(screen.getByRole('button', { name: 'Rent' }));
    expect(mockSetFilter).toHaveBeenCalledWith('categoryIds', '1,2');
  });

  test('active category chip gets background color from category', () => {
    renderFilterBar({ filters: { ...defaultFilters, categoryIds: '1' } });
    const chip = screen.getByRole('button', { name: 'Groceries' });
    // happy-dom may return hex or rgb depending on version
    const bg = chip.style.backgroundColor;
    expect(bg === 'rgb(233, 69, 96)' || bg === '#e94560').toBe(true);
  });

  // --- Amount range inputs ---

  test('renders amount min and max inputs', () => {
    renderFilterBar();
    expect(screen.getByPlaceholderText(/min/i)).toBeInTheDocument();
    expect(screen.getByPlaceholderText(/max/i)).toBeInTheDocument();
  });

  test('amount min input calls setFilter on change', async () => {
    const user = userEvent.setup();
    renderFilterBar();

    const minInput = screen.getByPlaceholderText(/min/i);
    await user.type(minInput, '50');
    expect(mockSetFilter).toHaveBeenCalledWith('amountMin', expect.any(String));
  });

  test('amount max input calls setFilter on change', async () => {
    const user = userEvent.setup();
    renderFilterBar();

    const maxInput = screen.getByPlaceholderText(/max/i);
    await user.type(maxInput, '200');
    expect(mockSetFilter).toHaveBeenCalledWith('amountMax', expect.any(String));
  });

  // --- Tag filter input ---

  test('renders tag filter input', () => {
    renderFilterBar();
    expect(screen.getByPlaceholderText(/tags/i)).toBeInTheDocument();
  });

  test('tag filter input calls setFilter on change', async () => {
    const user = userEvent.setup();
    renderFilterBar();

    const tagInput = screen.getByPlaceholderText(/tags/i);
    await user.type(tagInput, 'food');
    expect(mockSetFilter).toHaveBeenCalledWith('tags', expect.any(String));
  });

  // --- Saved filter chips ---

  test('renders saved filter chips', () => {
    renderFilterBar();
    expect(screen.getByRole('button', { name: 'Big expenses' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Food only' })).toBeInTheDocument();
  });

  test('clicking saved filter chip calls onLoadFilter', async () => {
    const user = userEvent.setup();
    renderFilterBar();

    await user.click(screen.getByRole('button', { name: 'Big expenses' }));
    expect(mockOnLoadFilter).toHaveBeenCalledWith(sampleSavedFilters[0]);
  });

  test('clicking delete on saved filter calls onDeleteFilter', async () => {
    const user = userEvent.setup();
    renderFilterBar();

    // Each saved filter chip has a delete button with aria-label
    const deleteButtons = screen.getAllByRole('button', { name: /delete saved filter/i });
    await user.click(deleteButtons[0]);
    expect(mockOnDeleteFilter).toHaveBeenCalledWith(10);
  });

  test('renders save filter button', () => {
    renderFilterBar();
    expect(screen.getByRole('button', { name: /save filter/i })).toBeInTheDocument();
  });

  test('save filter button prompts for name and calls onSaveFilter', async () => {
    const user = userEvent.setup();
    // Mock window.prompt to return a name
    const originalPrompt = window.prompt;
    window.prompt = vi.fn().mockReturnValue('My filter');
    renderFilterBar();

    await user.click(screen.getByRole('button', { name: /save filter/i }));
    expect(mockOnSaveFilter).toHaveBeenCalledWith('My filter');

    window.prompt = originalPrompt;
  });

  test('save filter does nothing when prompt is cancelled', async () => {
    const user = userEvent.setup();
    const originalPrompt = window.prompt;
    window.prompt = vi.fn().mockReturnValue(null);
    renderFilterBar();

    await user.click(screen.getByRole('button', { name: /save filter/i }));
    expect(mockOnSaveFilter).not.toHaveBeenCalled();

    window.prompt = originalPrompt;
  });

  // --- Clear resets all fields ---

  test('clear button resets all filter fields including new ones', async () => {
    const user = userEvent.setup();
    renderFilterBar({
      filters: {
        ...defaultFilters,
        categoryIds: '1,2',
        amountMin: '50',
        amountMax: '200',
        tags: 'food',
        search: 'test',
      },
    });

    await user.click(screen.getByRole('button', { name: /clear/i }));
    expect(mockSetFilter).toHaveBeenCalledWith('categoryIds', '');
    expect(mockSetFilter).toHaveBeenCalledWith('amountMin', '');
    expect(mockSetFilter).toHaveBeenCalledWith('amountMax', '');
    expect(mockSetFilter).toHaveBeenCalledWith('tags', '');
    expect(mockOnClear).toHaveBeenCalled();
  });
});
