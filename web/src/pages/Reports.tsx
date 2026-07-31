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
  const { outOfRangeYears } = useReportYears();

  return (
    <div className="flex flex-col gap-6">
      <h1 className="text-2xl font-semibold tracking-tight">Reports</h1>

      {/* `outOfRangeYears` is the only signal that some data is unreachable
          here. The year pickers now offer every year the ledger holds, so for
          an ordinary household this list is EMPTY and nothing renders — which
          is the point. It fires only for a year no report can reach: outside
          the data window [1900, 2100], or dated in the future.

          The years are NAMED rather than described. "Some transactions are
          outside the range" sends the user hunting; "dated 1850, 3021" tells
          them what to search for under Transactions.

          Every claim below was re-measured against a live database seeded with
          sentinel rows at 1850 ($777), 2027 ($999) and 3021 ($888), alongside
          in-window rows at 1900 and 2026:

            - GET /api/reports/years returned
              years=[2026,1900], out_of_range_years=[3021,2027,1850].
            - budget-vs-actual?year= and dashboard/summary?year= returned 400
              "invalid year" for 1850 and 3021. They answer for 2027, but no
              picker offers 2027, so no control can ask.
            - income-expenses at months=1524 — what "All time" resolves to for
              this ledger — contained the 1900 and 2026 rows and NONE of the
              three sentinels.
            - All five rows were present in GET /api/transactions.

          So: DO NOT soften this to "the amounts still count". They do not, and
          an earlier version of this notice said they did.

          And DO NOT let "All time" imply otherwise — that is the new way to
          get this wrong. All time spans back to the OLDEST OFFERED year, and
          an out-of-range year is by definition not offered. (months=2412 does
          contain the 1850 row, but nothing in the UI requests that width.)

          Informational rather than a warning (default variant, polite `status`
          role): nothing is broken, the ledger is intact, and the user's only
          action is to know. Rendered once for the whole page because this is a
          property of the household ledger, not of any one tab. */}
      {outOfRangeYears.length > 0 && (
        <Alert role="status">
          <Info className="size-4" />
          <AlertDescription>
            Some transactions are dated {outOfRangeYears.join(', ')}. Those
            years cannot be selected here, and their amounts are not included
            in any total on this page — not even under All time. The
            transactions are still in your ledger and listed under
            Transactions.
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
