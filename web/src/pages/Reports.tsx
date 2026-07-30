import { useState } from 'react';
import { Info } from 'lucide-react';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { OverviewTab } from '@/components/reports/OverviewTab';
import { SpendingTab } from '@/components/reports/SpendingTab';
import { SavingsTab } from '@/components/reports/SavingsTab';
import { PatternsTab } from '@/components/reports/PatternsTab';
import { useReportYearFloor } from '@/hooks/useReportYearFloor';
import { MIN_YEAR } from '@/lib/dates';

export function Reports() {
  const [activeTab, setActiveTab] = useState('overview');
  const { clamped } = useReportYearFloor();

  return (
    <div className="flex flex-col gap-6">
      <h1 className="text-2xl font-semibold tracking-tight">Reports</h1>

      {/* `clamped` is the only signal that some imported data is unreachable
          here: import accepts dates back to 1900, but the year pickers bottom
          out at MIN_YEAR because every year-param endpoint rejects anything
          older. Those amounts still count in each date-range aggregate, so
          nothing is broken and nothing is missing — this is informational, not
          a warning, which is why it uses the default Alert variant and the
          polite `status` role rather than an assertive `alert`.

          Rendered once for the whole page: the floor is a property of the
          household ledger, not of any one tab. It costs nothing for the
          households that have no pre-MIN_YEAR rows, because it does not
          render at all. */}
      {clamped && (
        <Alert role="status">
          <Info className="size-4" />
          <AlertDescription>
            Some transactions are dated before {MIN_YEAR}. Their amounts are
            still included in every total, but those years cannot be selected
            here.
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
