import { useState } from 'react';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { OverviewTab } from '@/components/reports/OverviewTab';
import { SpendingTab } from '@/components/reports/SpendingTab';
import { SavingsTab } from '@/components/reports/SavingsTab';
import { PatternsTab } from '@/components/reports/PatternsTab';

export function Reports() {
  const [activeTab, setActiveTab] = useState('overview');

  return (
    <div className="space-y-6 p-6">
      <h1 className="text-2xl font-semibold tracking-tight">Reports</h1>

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
