import { useState, useEffect } from 'react';
import { Routes, Route } from 'react-router-dom';
import { Sidebar } from './Sidebar';
import { Dashboard } from '../pages/Dashboard';
import { Transactions } from '../pages/Transactions';
import { Categories } from '../pages/Categories';
import { Reports } from '../pages/Reports';
import { Settings } from '../pages/Settings';

export function AppShell() {
  const [sidebarExpanded, setSidebarExpanded] = useState(
    () => localStorage.getItem('spendrop-sidebar') === 'true',
  );

  useEffect(() => {
    const handler = () => {
      setSidebarExpanded(localStorage.getItem('spendrop-sidebar') === 'true');
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
            : 'flex-1 max-w-[1464px] py-8 pr-10 pl-[calc(64px+2.5rem)]'
        }
      >
        <Routes>
          <Route path="/" element={<Dashboard />} />
          <Route path="/reports" element={<Reports />} />
          <Route path="/transactions" element={<Transactions />} />
          <Route path="/categories" element={<Categories />} />
          <Route path="/settings" element={<Settings />} />
        </Routes>
      </main>
    </div>
  );
}
