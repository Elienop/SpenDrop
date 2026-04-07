import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, it, expect, vi, beforeEach, type Mock } from 'vitest';
import { TransactionEntry } from './TransactionEntry';
import type { Category } from '../api/types';

const mockCategories: Category[] = [
  {
    id: 1,
    name: 'Groceries',
    type: 'expense',
    color: '#e94560',
    icon: null,
    sort_order: 1,
    is_active: true,
    created_at: '2026-01-01',
  },
];

describe('TransactionEntry tags integration', () => {
  let onSubmit: Mock;

  beforeEach(() => {
    onSubmit = vi.fn().mockResolvedValue(undefined);
  });

  it('renders a TagInput field with label "Tags"', () => {
    render(
      <TransactionEntry categories={mockCategories} onSubmit={onSubmit} />,
    );
    expect(screen.getByText('Tags')).toBeInTheDocument();
  });

  it('submits tags as comma-separated string', async () => {
    const user = userEvent.setup();
    render(
      <TransactionEntry categories={mockCategories} onSubmit={onSubmit} />,
    );

    // Fill required fields
    await user.clear(screen.getByLabelText('Amount'));
    await user.type(screen.getByLabelText('Amount'), '12.50');
    await user.type(screen.getByLabelText('Description'), 'Test purchase');
    await user.selectOptions(screen.getByLabelText('Category'), '1');

    // Add a tag via the tag input
    const tagInput = screen.getByPlaceholderText('Add tags...');
    await user.type(tagInput, 'groceries{Enter}');

    // Submit via fireEvent (happy-dom doesn't fire submit from button click)
    const form = screen.getByRole('button', { name: /add/i }).closest('form')!;
    fireEvent.submit(form);

    await waitFor(() => {
      expect(onSubmit).toHaveBeenCalledWith(
        expect.objectContaining({
          tags: 'groceries',
        }),
      );
    });
  });

  it('resets tags after successful submit', async () => {
    const user = userEvent.setup();
    render(
      <TransactionEntry categories={mockCategories} onSubmit={onSubmit} />,
    );

    // Fill required fields
    await user.clear(screen.getByLabelText('Amount'));
    await user.type(screen.getByLabelText('Amount'), '10');
    await user.type(screen.getByLabelText('Description'), 'Test');
    await user.selectOptions(screen.getByLabelText('Category'), '1');

    // Add a tag
    const tagInput = screen.getByPlaceholderText('Add tags...');
    await user.type(tagInput, 'food{Enter}');

    // Verify the tag pill is present before submit
    expect(screen.getByText('food')).toBeInTheDocument();

    // Submit via fireEvent (happy-dom doesn't fire submit from button click)
    const form = screen.getByRole('button', { name: /add/i }).closest('form')!;
    fireEvent.submit(form);

    // After submit, the tag pill should be gone
    await waitFor(() => {
      expect(screen.queryByText('food')).not.toBeInTheDocument();
    });
  });

  it('sends empty tags when no tags added', async () => {
    const user = userEvent.setup();
    render(
      <TransactionEntry categories={mockCategories} onSubmit={onSubmit} />,
    );

    await user.clear(screen.getByLabelText('Amount'));
    await user.type(screen.getByLabelText('Amount'), '5');
    await user.type(screen.getByLabelText('Description'), 'No tags test');
    await user.selectOptions(screen.getByLabelText('Category'), '1');

    // Submit via fireEvent (happy-dom doesn't fire submit from button click)
    const form = screen.getByRole('button', { name: /add/i }).closest('form')!;
    fireEvent.submit(form);

    await waitFor(() => {
      expect(onSubmit).toHaveBeenCalledWith(
        expect.objectContaining({
          tags: '',
        }),
      );
    });
  });
});
