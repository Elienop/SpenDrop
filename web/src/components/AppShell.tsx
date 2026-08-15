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
        {/* The full stop is glued to the tag above, and stays that way. On its
            own line JSX collapses the newline and renders the same string, but
            nothing in the source says which was meant — the space that IS
            wanted, before the path, had to be written as `{' '}` for exactly
            that reason. */}.
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
      {/*
        FIRST child, and that ordering is load-bearing — do not move it back
        below <main> for tidiness (B50, 2026-08-15). Strictly, the functional
        invariant is "before the routed content" (Sidebar and MobileNav toast
        nothing today, so a Toaster after them would still work); it sits
        literally first so the a11y consequence written below stays exactly
        true, and AppShell.toaster.test.tsx pins the literal position.

        sonner's Toaster subscribes to the toast bus from a passive effect over
        state that starts empty (`useState([])`), and `Observer.addToast`
        publishes to whoever is subscribed AT THAT MOMENT and never replays
        (`sonner@2.0.7`: `addToast = (data) => { this.publish(data); … }`).
        React flushes passive effects in tree order, so a routed page's mount
        effect runs before the effects of any sibling that comes LATER in the
        DOM. With the Toaster last, every `toast.*` fired from a page's mount
        effect was published to zero subscribers and silently dropped on a cold
        load — Settings' one-shot `?tab=savings` forwarding toast was the
        reported case, and it worked only when reached by in-app navigation,
        where the Toaster was already mounted.

        Moving it first costs nothing visually. sonner renders an unstyled
        wrapper <section> that holds an <ol data-sonner-toaster>, and only the
        <ol> exists (`if (!filteredToasts.length) return null`) and only the
        <ol> is positioned — `[data-sonner-toaster]{position:fixed; …
        z-index:999999999}` in dist/styles.css. So the wrapper is an empty,
        zero-height block wherever it sits, and the toasts themselves are out of
        flow and carry an explicit z that beats the `z-50` dialog/sheet overlays
        and the `z-40` mobile header. Nothing between here and the document root
        opens a stacking context that could trap them.

        Accepted a11y consequence, recorded so a later pass does not "fix" it by
        moving this back: the always-mounted wrapper is
        `<section aria-label="Notifications alt+T" tabIndex={-1}
        aria-live="polite">`, so the Notifications region is now FIRST in the
        landmark list, and because each toast is an `<li tabIndex={0}>` inside
        the <ol>, a VISIBLE toast is the first Tab stop on the page. That is
        inherent to having the Toaster subscribe first, and it comes with the
        upside that a cold-load toast is now announced by the live region as
        well as shown — before this change it was neither.
      */}
      <Toaster />
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
    </div>
  );
}
