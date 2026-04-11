import { describe, it, expect, vi } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';

// Mock all tab components
vi.mock('@/components/reports/OverviewTab', () => ({
  OverviewTab: () => <div data-testid="overview-tab">Overview content</div>,
}));
vi.mock('@/components/reports/SpendingTab', () => ({
  SpendingTab: () => <div data-testid="spending-tab">Spending content</div>,
}));
vi.mock('@/components/reports/SavingsTab', () => ({
  SavingsTab: () => <div data-testid="savings-tab">Savings content</div>,
}));
vi.mock('@/components/reports/PatternsTab', () => ({
  PatternsTab: () => <div data-testid="patterns-tab">Patterns content</div>,
}));

import { Reports } from './Reports';

function renderReports() {
  return render(
    <MemoryRouter>
      <Reports />
    </MemoryRouter>,
  );
}

describe('Reports', () => {
  it('renders the page heading', () => {
    renderReports();
    expect(
      screen.getByRole('heading', { level: 1, name: 'Reports' }),
    ).toBeInTheDocument();
  });

  it('renders all four tab triggers', () => {
    renderReports();
    expect(screen.getByRole('tab', { name: 'Overview' })).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: 'Spending' })).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: 'Savings' })).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: 'Patterns' })).toBeInTheDocument();
  });

  it('shows Overview tab by default', () => {
    renderReports();
    expect(screen.getByTestId('overview-tab')).toBeInTheDocument();
  });

  it('does not render other tabs initially (lazy loading)', () => {
    renderReports();
    expect(screen.queryByTestId('spending-tab')).not.toBeInTheDocument();
    expect(screen.queryByTestId('savings-tab')).not.toBeInTheDocument();
    expect(screen.queryByTestId('patterns-tab')).not.toBeInTheDocument();
  });

  it('switches to Spending tab on click', async () => {
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    renderReports();

    await user.click(screen.getByRole('tab', { name: 'Spending' }));
    await waitFor(() => {
      expect(screen.getByTestId('spending-tab')).toBeInTheDocument();
    });
  });

  it('switches to Patterns tab on click', async () => {
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    renderReports();

    await user.click(screen.getByRole('tab', { name: 'Patterns' }));
    await waitFor(() => {
      expect(screen.getByTestId('patterns-tab')).toBeInTheDocument();
    });
  });
});
