import { useState, useEffect } from 'react';
import { Routes, Route } from 'react-router-dom';
import { Sidebar } from './Sidebar';
import { MobileNav } from './MobileNav';
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
      <MobileNav />
      {/*
        From `md` up, <main> covers the viewport area to the right of the
        fixed sidebar (pl-60 / pl-12 = sidebar width when expanded /
        collapsed), and the padding-left transitions so toggling the
        sidebar slides the content over smoothly.

        Below `md` there IS no fixed sidebar (Sidebar is `hidden`, MobileNav
        takes over), so EVERY sidebar-width padding here is `md:`-gated —
        that gate is what keeps a persisted `spendrop-sidebar` of 'true'
        from stealing 240px of a 390px phone viewport. The transition is
        md:-gated for the same reason: the phone drawer animates as a
        transform, and nothing about the content column should move with it.

        Inner wrapper centers page content with `mx-auto max-w-[1400px]`, on
        a 16px phone gutter widening to 40px once there is room for it.
      */}
      <main
        className={cn(
          // The 3.5rem subtrahend IS MobileNav's `h-14` top bar, which is
          // sticky and therefore in normal flow above this element. Change
          // the bar's height and this must change with it, or every phone
          // page gains (or loses) that much scroll at the bottom.
          'min-h-[calc(100dvh-3.5rem)] py-4 md:min-h-screen md:py-8 md:transition-[padding] md:duration-200 md:ease-linear',
          sidebarExpanded ? 'md:pl-60' : 'md:pl-12',
        )}
      >
        <div className="mx-auto max-w-[1400px] px-4 md:px-10">
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
