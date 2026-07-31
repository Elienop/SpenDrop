import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';

const floorResult = {
  floorYear: 2019,
  hasTransactions: true,
  clamped: false,
  loading: false,
};
const useReportYearFloor = vi.fn(() => floorResult);
vi.mock('@/hooks/useReportYearFloor', () => ({
  useReportYearFloor: () => useReportYearFloor(),
}));

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
  beforeEach(() => {
    useReportYearFloor.mockReturnValue(floorResult);
  });

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

  // `clamped` is the ONLY signal that some data is unreachable in Reports:
  // rows dated before MIN_YEAR are in the ledger but no year picker can select
  // their year, AND they are absent from every report aggregate the UI asks
  // for. Leaving it unrendered would be a smaller version of the exact defect
  // this feature fixes, so it gets a quiet informational note — shown once for
  // the whole page, because it is a property of the ledger, not of any one tab.
  describe('pre-MIN_YEAR ledger rows', () => {
    it('says so when the server reports the floor was clamped', () => {
      useReportYearFloor.mockReturnValue({ ...floorResult, clamped: true });
      renderReports();

      const note = screen.getByRole('status');
      expect(note).toHaveTextContent(/before 2000/i);
      expect(note).toHaveTextContent(/cannot be selected/i);
    });

    // Regression guard. The first version of this notice claimed the amounts
    // were "still included in every total". Measured against a real 1984 row,
    // that was false: income-expenses at the 24- and 324-month windows the UI
    // requests, budget-vs-actual?year=, and the dashboard summary all exclude
    // it — only months=1212 contains it, and nothing requests that. Reassuring
    // the user their old amounts still count, when they do not, is worse than
    // saying nothing at all.
    it('does not claim the excluded amounts still count toward totals', () => {
      useReportYearFloor.mockReturnValue({ ...floorResult, clamped: true });
      renderReports();

      const note = screen.getByRole('status');
      expect(note).not.toHaveTextContent(/included in every total/i);
      expect(note).not.toHaveTextContent(/still (count|included)/i);
      expect(note).toHaveTextContent(/not included in these reports/i);
    });

    it('stays silent for the ordinary household', () => {
      useReportYearFloor.mockReturnValue({ ...floorResult, clamped: false });
      renderReports();

      expect(screen.queryByRole('status')).not.toBeInTheDocument();
    });

    it('stays silent while the floor is still loading', () => {
      // `clamped` defaults to false before the response lands; asserting it
      // here stops a future refactor from flashing the note on first paint.
      useReportYearFloor.mockReturnValue({
        ...floorResult,
        clamped: false,
        loading: true,
      });
      renderReports();

      expect(screen.queryByRole('status')).not.toBeInTheDocument();
    });
  });
});
