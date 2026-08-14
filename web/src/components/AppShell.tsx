import { useState, useEffect } from 'react';
import { Routes, Route, Link, useLocation } from 'react-router-dom';
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
import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';
import { STORAGE_KEYS } from '@/lib/storage-keys';

/*
  Fallback pane for any path the literal routes below don't match. A panel,
  not a redirect to "/": a typo'd bookmark that silently lands on the
  dashboard hides the typo, and echoing the attempted path is what lets the
  user spot it. Shell chrome stays mounted around it, so navigation out is
  one tap even without the button.
*/
function RouteNotFound() {
  const { pathname } = useLocation();
  return (
    <div className="flex flex-col items-start gap-4">
      <h1 className="text-2xl font-semibold tracking-tight">Page not found</h1>
      {/* The echoed path is arbitrary user input — a mistyped bookmark can be
          one long unbroken token. `overflow-wrap:anywhere` rather than
          `break-words`: only the former lowers the span's min-content
          contribution, and this paragraph is a flex item in an `items-start`
          column, so it is sized from its content and would otherwise pan the
          page sideways at 360px (the same trap Categories' name clamp
          documents). `max-w-3xl` matches the explainer paragraphs on Trash and
          Settings. */}
      <p className="max-w-3xl text-sm text-muted-foreground">
        Nothing lives at{' '}
        <span className="font-medium text-foreground [overflow-wrap:anywhere]">
          {pathname}
        </span>
        .
      </p>
      {/* Button styles on the Link keep the 44px touch floor. */}
      <Button asChild variant="outline">
        <Link to="/">Back to Dashboard</Link>
      </Button>
    </div>
  );
}

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
            <Route path="*" element={<RouteNotFound />} />
          </Routes>
        </div>
      </main>
      <Toaster />
    </div>
  );
}
