import { useState, useEffect } from 'react';
import { Routes, Route } from 'react-router-dom';
import { Sidebar } from './Sidebar';
import { Dashboard } from '../pages/Dashboard';
import { Transactions } from '../pages/Transactions';
import { Budgets } from '../pages/Budgets';
import { Savings } from '../pages/Savings';
import { Categories } from '../pages/Categories';
import { Reports } from '../pages/Reports';
import { Settings } from '../pages/Settings';
import { Trash } from '../pages/Trash';
import { Toaster } from '@/components/ui/sonner';
import { cn } from '@/lib/utils';
import { STORAGE_KEYS } from '@/lib/storage-keys';

export function AppShell() {
  const [sidebarExpanded, setSidebarExpanded] = useState(
    () => localStorage.getItem(STORAGE_KEYS.sidebar) === 'true',
  );

  useEffect(() => {
    const handler = () => {
      setSidebarExpanded(
        localStorage.getItem(STORAGE_KEYS.sidebar) === 'true',
      );
    };
    window.addEventListener('sidebar-toggle', handler);
    return () => {
      window.removeEventListener('sidebar-toggle', handler);
    };
  }, []);

  return (
    <div className="min-h-screen bg-background text-foreground">
      <Sidebar />
      {/*
        Outer <main> covers the viewport area to the right of the fixed
        sidebar (pl-60 / pl-12 = sidebar width when expanded / collapsed).
        Inner wrapper centers the page content with `mx-auto max-w-[1400px]`
        so on wide screens the content sits in the middle of the available
        space instead of hugging the sidebar. The padding-left transitions
        so toggling the sidebar slides the content over smoothly.
      */}
      <main
        className={cn(
          'min-h-screen py-8 transition-[padding] duration-200 ease-linear',
          sidebarExpanded ? 'pl-60' : 'pl-12',
        )}
      >
        <div className="mx-auto max-w-[1400px] px-10">
          <Routes>
            <Route path="/" element={<Dashboard />} />
            <Route path="/reports" element={<Reports />} />
            <Route path="/transactions" element={<Transactions />} />
            <Route path="/budgets" element={<Budgets />} />
            <Route path="/savings" element={<Savings />} />
            <Route path="/categories" element={<Categories />} />
            <Route path="/trash" element={<Trash />} />
            <Route path="/settings" element={<Settings />} />
          </Routes>
        </div>
      </main>
      <Toaster />
    </div>
  );
}
