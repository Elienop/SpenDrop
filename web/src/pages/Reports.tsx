import { useState } from 'react';
import { Info } from 'lucide-react';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { OverviewTab } from '@/components/reports/OverviewTab';
import { SpendingTab } from '@/components/reports/SpendingTab';
import { SavingsTab } from '@/components/reports/SavingsTab';
import { PatternsTab } from '@/components/reports/PatternsTab';
import { useReportYears } from '@/hooks/useReportYears';

export function Reports() {
  const [activeTab, setActiveTab] = useState('overview');
  const { outOfRangeYears, futureYears, currentYear } = useReportYears();

  return (
    <div className="flex flex-col gap-6">
      <h1 className="text-2xl font-semibold tracking-tight">Reports</h1>

      {/* Between them, `outOfRangeYears` and `futureYears` are the only signal
          that the ledger holds a row this page cannot show. The year pickers
          offer every year the ledger has, so for an ordinary household both
          lists are EMPTY and nothing renders — which is the point.

          TWO CAUSES, TWO SENTENCES, ONE NOTICE. They used to share one list
          and one sentence, and that sentence told a household who had planned
          a 2027 bill that their deliberate entry was unreachable, in the same
          words used for a corrupt 1850 row. It was not false — it was the
          wrong thing to say, because the two are not the same problem and one
          of them is not a problem at all.

          Everything below was measured against a binary built from this tree,
          on a throwaway DB, with sentinel rows seeded past the API's own
          validator: 1850 ($777), 2027 ($999), 3021 ($888) and 2026 ($111).
          Server clock 2026-07-31.

          OUT OF RANGE — outside the data window [1900, 2100]:
            - GET /api/reports/years returned out_of_range_years=[3021,1850].
            - POST /api/transactions REJECTED both dates (400, "date must be
              between 1900-01-01 and 2100-12-31"), so the app itself considers
              them invalid.
            - budget-vs-actual?year=, dashboard/summary?year= and
              year-over-year?year= all returned 400 for 1850 and 3021.
            - Every income-expenses window ends at the current month, and the
              widest one the UI ever requests (All time -> months=1524 here)
              did not contain $777 or $888.
          So: DO NOT soften this to "the amounts still count". They do not, and
          an earlier version of this notice said they did. And DO NOT let "All
          time" imply otherwise — All time spans back to the OLDEST OFFERED
          year, and an out-of-range year is by definition not offered.
          (months=2412 does contain the 1850 row; no control requests that.)

          FUTURE — later than the current year, inside the window:
            - GET /api/reports/years returned future_years=[2027].
            - POST /api/transactions ACCEPTED 2027-03-04 (201). This is a row
              the app invited the user to create.
            - budget-vs-actual?year=2027 returned 200 with actual=999 in month
              3; dashboard/summary?year=2027 returned savings_ytd=-999;
              spending-heatmap?year=2027 contained 999. The reports can
              already see this row — only the picker's cap withholds it.
            - No income-expenses window contains it: months=12/24/1524/2412 all
              ended at 2026-07.
          So the true statement is about SCOPE, not about their data. The
          forward-looking half — "appears once its year begins" — is pinned
          server-side by TestReportYears_FutureYearBecomesOfferableWhenItArrives,
          which advances the clock to 2027 and asserts 2027 enters `years`.

          NO YEAR IS NAMED TWICE. 3021 is out of range AND in the future; the
          server's precedence rule puts it in out_of_range_years only, so these
          two sentences never collide.

          `currentYear` comes from the server, the same clock that capped the
          picker — the browser's could disagree across a New Year boundary and
          contradict the Select sitting next to this notice.

          Years are NAMED rather than described: "some transactions are outside
          the range" sends the user hunting; "dated 1850, 3021" tells them what
          to search for under Transactions.

          ONE Alert, never two stacked: two boxes read as two problems and push
          the tab strip down the page. Informational rather than a warning
          (default variant, polite `status` role) — nothing is broken, the
          ledger is intact, and the user's only action is to know. Rendered once
          for the whole page because this is a property of the household ledger,
          not of any one tab. */}
      {(outOfRangeYears.length > 0 || futureYears.length > 0) && (
        <Alert role="status">
          <Info className="size-4" />
          <AlertDescription className="flex flex-col gap-2">
            {outOfRangeYears.length > 0 && (
              <p>
                Some transactions are dated {outOfRangeYears.join(', ')}. Those
                years cannot be selected here, and their amounts are not
                included in any total on this page — not even under All time.
              </p>
            )}
            {futureYears.length > 0 && (
              <p>
                Reports cover years up to {currentYear}. Transactions dated{' '}
                {futureYears.join(', ')} are not shown here yet — each appears
                once its year begins.
              </p>
            )}
            <p>
              Those transactions are still in your ledger and listed under
              Transactions.
            </p>
          </AlertDescription>
        </Alert>
      )}

      <Tabs value={activeTab} onValueChange={setActiveTab}>
        <TabsList>
          <TabsTrigger value="overview">Overview</TabsTrigger>
          <TabsTrigger value="spending">Spending</TabsTrigger>
          <TabsTrigger value="savings">Savings</TabsTrigger>
          <TabsTrigger value="patterns">Patterns</TabsTrigger>
        </TabsList>

        <TabsContent value="overview" className="mt-6">
          {activeTab === 'overview' && <OverviewTab />}
        </TabsContent>
        <TabsContent value="spending" className="mt-6">
          {activeTab === 'spending' && <SpendingTab />}
        </TabsContent>
        <TabsContent value="savings" className="mt-6">
          {activeTab === 'savings' && <SavingsTab />}
        </TabsContent>
        <TabsContent value="patterns" className="mt-6">
          {activeTab === 'patterns' && <PatternsTab />}
        </TabsContent>
      </Tabs>
    </div>
  );
}
