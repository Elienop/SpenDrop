import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';

const yearsResult = {
  years: [2026, 2019],
  currentYear: 2026,
  hasTransactions: true,
  outOfRangeYears: [] as number[],
  loading: false,
};
const useReportYears = vi.fn(() => yearsResult);
vi.mock('@/hooks/useReportYears', () => ({
  useReportYears: () => useReportYears(),
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
    useReportYears.mockReturnValue(yearsResult);
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

  // `outOfRangeYears` is the ONLY signal that some data is unreachable in
  // Reports: those rows are in the ledger and listed under Transactions, but
  // no year picker can select their year AND they are absent from every report
  // total the UI asks for. Leaving it unrendered would be a smaller version of
  // the exact defect this feature fixes, so it gets a quiet informational note
  // — shown once for the whole page, because it is a property of the ledger,
  // not of any one tab.
  describe('out-of-range ledger rows', () => {
    it('names the years the reports cannot reach', () => {
      useReportYears.mockReturnValue({
        ...yearsResult,
        outOfRangeYears: [3021, 1850],
      });
      renderReports();

      const note = screen.getByRole('status');
      expect(note).toHaveTextContent(/3021/);
      expect(note).toHaveTextContent(/1850/);
      expect(note).toHaveTextContent(/cannot be selected/i);
    });

    it('names EVERY out-of-range year, not just the first', () => {
      // A future-dated row and a legacy row can coexist, and each is a
      // separate thing the user has to go look at. Naming one and silently
      // dropping the rest is the same silent-drop bug in miniature.
      useReportYears.mockReturnValue({
        ...yearsResult,
        outOfRangeYears: [3021, 2027, 1850],
      });
      renderReports();

      const note = screen.getByRole('status');
      for (const year of ['3021', '2027', '1850']) {
        expect(note).toHaveTextContent(year);
      }
    });

    // Regression guard. The first version of this notice claimed the amounts
    // were "still included in every total". Measured against a real
    // out-of-window row, that was false, and it is still false: with sentinel
    // rows at 1850/2027/3021 seeded into a live database, budget-vs-actual and
    // the dashboard summary 400 on 1850 and 3021, and the All-time
    // income-expenses window the UI requests excludes all three. Reassuring
    // the user their old amounts still count, when they do not, is worse than
    // saying nothing at all.
    it('does not claim the excluded amounts still count toward totals', () => {
      useReportYears.mockReturnValue({
        ...yearsResult,
        outOfRangeYears: [1850],
      });
      renderReports();

      const note = screen.getByRole('status');
      expect(note).not.toHaveTextContent(/included in every total/i);
      expect(note).not.toHaveTextContent(/still (count|included)/i);
      expect(note).toHaveTextContent(/not included in any total/i);
    });

    // The new way to get this wrong. Overview now has an "All time" window,
    // which sounds like it covers everything and does not: it spans back to
    // the OLDEST OFFERED year, and an out-of-range year is by definition not
    // offered. Measured — All time resolves to months=1524 for a ledger whose
    // oldest offered year is 1900, and the 1850 row is absent from that
    // response. (It does appear at months=2412, which no control requests.)
    it('does not promise that All time reaches these years', () => {
      useReportYears.mockReturnValue({
        ...yearsResult,
        outOfRangeYears: [1850],
      });
      renderReports();

      const note = screen.getByRole('status');
      expect(note).not.toHaveTextContent(
        /all time (covers|includes|reaches|shows)/i,
      );
      expect(note).toHaveTextContent(/not even under all time/i);
    });

    it('stays silent for the ordinary household', () => {
      useReportYears.mockReturnValue({
        ...yearsResult,
        outOfRangeYears: [],
      });
      renderReports();

      expect(screen.queryByRole('status')).not.toBeInTheDocument();
    });

    it('stays silent while the years are still loading', () => {
      // `outOfRangeYears` defaults to [] before the response lands; asserting
      // it here stops a future refactor from flashing the note on first paint.
      useReportYears.mockReturnValue({
        ...yearsResult,
        outOfRangeYears: [],
        loading: true,
      });
      renderReports();

      expect(screen.queryByRole('status')).not.toBeInTheDocument();
    });
  });
});
