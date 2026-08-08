import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';

const yearsResult = {
  years: [2026, 2019],
  currentYear: 2026,
  hasTransactions: true,
  outOfRangeYears: [] as number[],
  futureYears: [] as number[],
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

  // Four triggers come to ~346px, which clears a 390px phone by a dozen
  // pixels and clears nothing at all on a 360px one. The strip therefore has
  // to scroll rather than widen the page — and `justify-start` is the half
  // that is easy to lose: TabsList's own `justify-center` centres the
  // overflow, so Overview ends up past the left edge where scrollLeft cannot
  // follow it. Whether it actually scrolls is a browser check; that the strip
  // is not centred is pinnable here.
  it('lets the tab strip scroll sideways instead of centring its overflow', () => {
    renderReports();
    const strip = screen.getByRole('tablist');
    expect(strip).toHaveClass('overflow-x-auto', 'max-w-full', 'justify-start');
    expect(strip).not.toHaveClass('justify-center');
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
      // Two legacy rows from opposite ends of the window are two separate
      // things the user has to go look at. Naming one and silently dropping
      // the rest is the same silent-drop bug in miniature.
      useReportYears.mockReturnValue({
        ...yearsResult,
        outOfRangeYears: [3021, 1899, 1850],
      });
      renderReports();

      const note = screen.getByRole('status');
      for (const year of ['3021', '1899', '1850']) {
        expect(note).toHaveTextContent(year);
      }
    });

    // An out-of-range year must NOT pick up the future framing. 3021 is in
    // the future too, but the endpoints will never accept it — telling the
    // user it "appears once its year begins" is a promise nothing can keep.
    it('does not promise an out-of-range year will arrive later', () => {
      useReportYears.mockReturnValue({
        ...yearsResult,
        outOfRangeYears: [3021],
      });
      renderReports();

      const note = screen.getByRole('status');
      expect(note).not.toHaveTextContent(/once its year begins/i);
      expect(note).not.toHaveTextContent(/reports cover years up to/i);
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
        futureYears: [],
      });
      renderReports();

      expect(screen.queryByRole('status')).not.toBeInTheDocument();
    });

    it('stays silent while the years are still loading', () => {
      // Both reject lists default to [] before the response lands; asserting
      // it here stops a future refactor from flashing the note on first paint.
      useReportYears.mockReturnValue({
        ...yearsResult,
        outOfRangeYears: [],
        futureYears: [],
        loading: true,
      });
      renderReports();

      expect(screen.queryByRole('status')).not.toBeInTheDocument();
    });
  });

  // A FUTURE-dated row is not a defect. Planning a 2027 bill is a normal
  // workflow, and the previous notice — one list, one sentence — told those
  // users their deliberate entry could not be reached, in the same breath it
  // used for a corrupt 1850 row.
  //
  // Measured against a binary built from this tree (throwaway DB, sentinel
  // rows seeded past the API's own validator at 1850/$777, 2027/$999,
  // 3021/$888 and 2026/$111, clock 2026-07-31):
  //
  //   - POST /api/transactions accepted 2027-03-04 (201) and REJECTED
  //     1850-06-01 and 3021-01-02 (400, "date must be between 1900-01-01 and
  //     2100-12-31"). The future row is something the app itself let the user
  //     create.
  //   - budget-vs-actual?year=2027 -> 200 with actual=999 in month 3;
  //     dashboard/summary?year=2027 -> 200 with savings_ytd=-999;
  //     spending-heatmap?year=2027 -> 200 containing 999. The same three
  //     endpoints 400 for 1850 and 3021. So the reports can already SEE the
  //     2027 row; only the picker's cap withholds it.
  //   - Every income-expenses window ends at the current month (months=12,
  //     24, 1524 and 2412 all ended 2026-07), so the 2027 amount is in none of
  //     them — including "All time".
  //   - All four rows were present in GET /api/transactions.
  //
  // Hence the split wording: out-of-range is a LIMITATION ("cannot be
  // selected"), future is SCOPE ("reports cover years up to 2026 … appears
  // once its year begins"). The forward-looking half is pinned server-side by
  // TestReportYears_FutureYearBecomesOfferableWhenItArrives, which advances
  // the clock to 2027 and asserts 2027 moves into `years`.
  describe('future-dated ledger rows', () => {
    it('describes the scope of the reports rather than a data problem', () => {
      useReportYears.mockReturnValue({
        ...yearsResult,
        futureYears: [2027],
      });
      renderReports();

      const note = screen.getByRole('status');
      expect(note).toHaveTextContent(/2027/);
      expect(note).toHaveTextContent(/reports cover years up to 2026/i);
      expect(note).toHaveTextContent(/once its year begins/i);
    });

    it('does not reuse the out-of-range wording for a planned year', () => {
      // The whole point of the split. "Cannot be selected here" and "not
      // included in any total" describe a row the endpoints refuse; they
      // refuse nothing about 2027.
      useReportYears.mockReturnValue({
        ...yearsResult,
        futureYears: [2027],
      });
      renderReports();

      const note = screen.getByRole('status');
      expect(note).not.toHaveTextContent(/cannot be selected/i);
      expect(note).not.toHaveTextContent(/not included in any total/i);
      expect(note).not.toHaveTextContent(/not even under all time/i);
    });

    it('names EVERY future year, not just the first', () => {
      useReportYears.mockReturnValue({
        ...yearsResult,
        futureYears: [2030, 2027],
      });
      renderReports();

      const note = screen.getByRole('status');
      expect(note).toHaveTextContent('2030');
      expect(note).toHaveTextContent('2027');
    });

    it('takes the ceiling from the server clock, not the browser', () => {
      // `currentYear` is the clock that CAPPED the list. Across a New Year
      // boundary the browser disagrees, and "reports cover years up to
      // <wrong year>" would contradict the picker sitting next to it.
      useReportYears.mockReturnValue({
        ...yearsResult,
        currentYear: 2019,
        years: [2019],
        futureYears: [2027],
      });
      renderReports();

      expect(screen.getByRole('status')).toHaveTextContent(
        /reports cover years up to 2019/i,
      );
    });

    it('stays silent when nothing is future-dated', () => {
      useReportYears.mockReturnValue({ ...yearsResult, futureYears: [] });
      renderReports();

      expect(screen.queryByRole('status')).not.toBeInTheDocument();
    });
  });

  // Both causes at once. Two stacked alerts would read as two problems and
  // bury the tab strip below them, so the page renders ONE notice carrying
  // both sentences. `getByRole('status')` throws on more than one match, so
  // the count assertion below is what actually holds that.
  describe('both causes at once', () => {
    const both = {
      ...yearsResult,
      outOfRangeYears: [3021, 1850],
      futureYears: [2027],
    };

    it('renders exactly one notice', () => {
      useReportYears.mockReturnValue(both);
      renderReports();

      expect(screen.getAllByRole('status')).toHaveLength(1);
    });

    it('says the true thing about each cause in that one notice', () => {
      useReportYears.mockReturnValue(both);
      renderReports();

      const note = screen.getByRole('status');
      expect(note).toHaveTextContent(/cannot be selected/i);
      expect(note).toHaveTextContent(/not even under all time/i);
      expect(note).toHaveTextContent(/reports cover years up to 2026/i);
      expect(note).toHaveTextContent(/once its year begins/i);
      for (const year of ['3021', '1850', '2027']) {
        expect(note).toHaveTextContent(year);
      }
    });

    it('does not name a year under both causes', () => {
      // The server's precedence rule (window beats future) is what makes this
      // hold: 3021 is out of range and is NOT in futureYears, so it is named
      // once. If a future refactor sent it to both, the same year would be
      // called unreachable and "arriving later" in one notice.
      useReportYears.mockReturnValue(both);
      renderReports();

      const text = screen.getByRole('status').textContent ?? '';
      expect(text.match(/3021/g)).toHaveLength(1);
    });
  });
});
