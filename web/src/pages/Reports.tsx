import { useState } from 'react';
import { Info } from 'lucide-react';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { OverviewTab } from '@/components/reports/OverviewTab';
import { SpendingTab } from '@/components/reports/SpendingTab';
import { SavingsTab } from '@/components/reports/SavingsTab';
import { PatternsTab } from '@/components/reports/PatternsTab';
import { useReportYearFloor } from '@/hooks/useReportYearFloor';
import { PLANNING_MIN_YEAR } from '@/lib/dates';

export function Reports() {
  const [activeTab, setActiveTab] = useState('overview');
  const { clamped } = useReportYearFloor();

  return (
    <div className="flex flex-col gap-6">
      <h1 className="text-2xl font-semibold tracking-tight">Reports</h1>

      {/* `clamped` is the only signal that some data is unreachable here.
          Transaction dates have no lower bound on create, and import accepts
          back to 1900, but the year pickers bottom out at PLANNING_MIN_YEAR because
          every year-param endpoint rejects anything older.

          Do NOT soften this to "the amounts still count". They do not, and an
          earlier version of this notice said they did. Measured against a
          real 1984 row: /reports/income-expenses at the 24- and 324-month
          windows the UI actually requests, /reports/budget-vs-actual?year=,
          and the dashboard summary ALL exclude it. The only response that
          contains it is income-expenses at months=2412, which no control ever
          asks for. The row is in the ledger and listed under Transactions —
          it is absent from every report total the user can see.

          Still informational rather than a warning (default variant, polite
          `status` role): nothing is broken, and the fix is a product decision
          about PLANNING_MIN_YEAR, not something the user can act on beyond knowing.

          Rendered once for the whole page: the floor is a property of the
          household ledger, not of any one tab. It costs nothing for the
          households that have no pre-PLANNING_MIN_YEAR rows, because it does not
          render at all. */}
      {clamped && (
        <Alert role="status">
          <Info className="size-4" />
          <AlertDescription>
            Some transactions are dated before {PLANNING_MIN_YEAR}. They are in your
            ledger and listed under Transactions, but they are not included in
            these reports and their years cannot be selected here.
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
