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
    <div className="flex min-h-screen bg-background text-foreground">
      <Sidebar />
      <main
        className={
          sidebarExpanded
            ? 'flex-1 max-w-[1640px] py-8 pr-10 pl-[calc(240px+2.5rem)]'
            : 'flex-1 max-w-[1464px] py-8 pr-10 pl-[calc(48px+2.5rem)]'
        }
      >
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
      </main>
      <Toaster />
    </div>
  );
}
