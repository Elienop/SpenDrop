import { useState } from 'react';
import { Info } from 'lucide-react';
import { Alert, AlertDescription } from '@/components/ui/alert';
import {
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
  scrollableTabsList,
} from '@/components/ui/tabs';
import { OverviewTab } from '@/components/reports/OverviewTab';
import { SpendingTab } from '@/components/reports/SpendingTab';
import { SavingsTab } from '@/components/reports/SavingsTab';
import { PatternsTab } from '@/components/reports/PatternsTab';
import { useReportYears } from '@/hooks/useReportYears';

/**
 * Trigger sizing for THIS strip's four tabs, applied at the call site rather
 * than to the shared `TabsTrigger` — Settings' strip could not fit on a phone
 * at any padding (measured 2026-08-09, when it had six triggers; it has five
 * now and swaps to a Select below `md` rather than scrolling), and FilterPanel
 * and QuickAdd lay their own tabs out on a grid.
 *
 * WIDTH — phone-only. Measured on the built container at a 360px viewport, the
 * width of the owner's main phone: the four triggers came to 326.2px of
 * content (Overview 85.3, Spending 86.1, Savings 76.1, Patterns 78.7) plus the
 * list's 8px of padding, against 298px of usable width. The strip overflowed
 * by 36px, so Patterns was cut off and reachable only by dragging the strip
 * sideways — the owner's report was "the scroll is not good for the nav bar
 * top". `px-2 text-xs` brings the four to 261.4px, which fits with room to
 * spare; `px-2 text-sm` alone came to 302.2px and would still have overflowed
 * by 4px, so both halves are load-bearing. Restored to the shared values at
 * `sm`, where the strip has always fitted and nothing needed changing.
 *
 * HEIGHT is NOT here. It moved to the shared `TabsTrigger` as `min-h-11` once
 * the owner approved the other four tablists getting it too, and this constant
 * deliberately does not repeat it: a duplicate would have masked the shared one
 * from the test below — verified by mutation, deleting the shared class left
 * the whole suite green while this string still carried its own copy.
 *
 * The reasoning, kept because it is what decided the shared version: `TabsList`'s `h-10` left the triggers
 * 32px tall, under the 44px touch floor the rest of the phone shell keeps
 * (`MobileNav`, `CategoryChips`, `TransactionEditSheet`). It is NOT gated on a
 * breakpoint, and both ends of the range are the reason: the household's tablet
 * is ~720px in portrait, below `md`, so a `sm:`-gated floor would drop it back
 * to 32px on a device taking the phone layout — and the same tablet in
 * LANDSCAPE is ~1152px, so an `md:`-gated one would drop it on a device that is
 * still touched. There is no breakpoint that separates "touch" from "pointer".
 * The floor itself now lives on the shared `TabsTrigger` (every tablist in
 * the app wanted it), so this constant only carries the WIDTH squeeze plus a
 * restatement of why the height is ungated.
 *
 * THE FIT IS NOT UNCONDITIONAL, and the failure mode is deliberate. These
 * classes are rem-based and the container is not, so a larger system font eats
 * the 43.6px of headroom this leaves at 360px (269.4px of strip in 313px) —
 * roughly 16%. Past that the strip goes back to scrolling, which is why
 * `scrollableTabsList` stays on the list rather than being replaced by a
 * `grid grid-cols-4`. Degrading to the old behaviour is the point; widening the
 * page is what must not happen. Not tested at any accessibility text size.
 *
 * IF YOU RE-MEASURE THIS, READ THIS FIRST. Every width in this comment came
 * from headless Chrome at an emulated viewport, and those numbers are ~15px
 * PESSIMISTIC against a real phone. `overflow-x: auto` on the list forces
 * `overflow-y` to compute to `auto` as well, and desktop Chrome then reserves a
 * classic-scrollbar gutter: the same element measures 313px by
 * `getBoundingClientRect()` and 298px by `clientWidth`. A phone has overlay
 * scrollbars and no gutter, so the real deficit on the owner's S24 was nearer
 * 21px than the 36px recorded above. Sizing against the pessimistic number is
 * deliberate; reading it as the on-device figure is not. The same correction
 * applies to every width this project has recorded from a resized desktop
 * browser.
 */
const REPORTS_TAB_TRIGGER = 'px-2 text-xs sm:px-3 sm:text-sm';

export function Reports() {
  const [activeTab, setActiveTab] = useState('overview');
  const { outOfRangeYears, futureYears, currentYear } = useReportYears();

  return (
    <div className="flex flex-col gap-6">
      <h1 className="text-2xl font-semibold tracking-tight">Reports</h1>

      {/* Between them, `outOfRangeYears` and `futureYears` are the only signal
          that the ledger holds a row this page cannot show. The year pickers
          offer every year the ledger has, so for an ordinary household both
          lists are EMPTY and nothing renders — which is the point.

          TWO CAUSES, TWO SENTENCES, ONE NOTICE. They used to share one list
          and one sentence, and that sentence told a household who had planned
          a 2027 bill that their deliberate entry was unreachable, in the same
          words used for a corrupt 1850 row. It was not false — it was the
          wrong thing to say, because the two are not the same problem and one
          of them is not a problem at all.

          Everything below was measured against a binary built from this tree,
          on a throwaway DB, with sentinel rows seeded past the API's own
          validator: 1850 ($777), 2027 ($999), 3021 ($888) and 2026 ($111).
          Server clock 2026-07-31.

          OUT OF RANGE — outside the data window [1900, 2100]:
            - GET /api/reports/years returned out_of_range_years=[3021,1850].
            - POST /api/transactions REJECTED both dates (400, "date must be
              between 1900-01-01 and 2100-12-31"), so the app itself considers
              them invalid.
            - budget-vs-actual?year=, dashboard/summary?year= and
              year-over-year?year= all returned 400 for 1850 and 3021.
            - Every income-expenses window ends at the current month, and the
              widest one the UI ever requests (All time -> months=1524 here)
              did not contain $777 or $888.
          So: DO NOT soften this to "the amounts still count". They do not, and
          an earlier version of this notice said they did. And DO NOT let "All
          time" imply otherwise — All time spans back to the OLDEST OFFERED
          year, and an out-of-range year is by definition not offered.
          (months=2412 does contain the 1850 row; no control requests that.)

          FUTURE — later than the current year, inside the window:
            - GET /api/reports/years returned future_years=[2027].
            - POST /api/transactions ACCEPTED 2027-03-04 (201). This is a row
              the app invited the user to create.
            - budget-vs-actual?year=2027 returned 200 with actual=999 in month
              3; dashboard/summary?year=2027 returned savings_ytd=-999;
              spending-heatmap?year=2027 contained 999. The reports can
              already see this row — only the picker's cap withholds it.
            - No income-expenses window contains it: months=12/24/1524/2412 all
              ended at 2026-07.
          So the true statement is about SCOPE, not about their data. The
          forward-looking half — "appears once its year begins" — is pinned
          server-side by TestReportYears_FutureYearBecomesOfferableWhenItArrives,
          which advances the clock to 2027 and asserts 2027 enters `years`.

          NO YEAR IS NAMED TWICE. 3021 is out of range AND in the future; the
          server's precedence rule puts it in out_of_range_years only, so these
          two sentences never collide.

          `currentYear` comes from the server, the same clock that capped the
          picker — the browser's could disagree across a New Year boundary and
          contradict the Select sitting next to this notice.

          Years are NAMED rather than described: "some transactions are outside
          the range" sends the user hunting; "dated 1850, 3021" tells them what
          to search for under Transactions.

          ONE Alert, never two stacked: two boxes read as two problems and push
          the tab strip down the page. Informational rather than a warning
          (default variant, polite `status` role) — nothing is broken, the
          ledger is intact, and the user's only action is to know. Rendered once
          for the whole page because this is a property of the household ledger,
          not of any one tab. */}
      {(outOfRangeYears.length > 0 || futureYears.length > 0) && (
        <Alert role="status">
          <Info className="size-4" />
          <AlertDescription className="flex flex-col gap-2">
            {outOfRangeYears.length > 0 && (
              <p>
                Some transactions are dated {outOfRangeYears.join(', ')}. Those
                years cannot be selected here, and their amounts are not
                included in any total on this page — not even under All time.
              </p>
            )}
            {futureYears.length > 0 && (
              <p>
                Reports cover years up to {currentYear}. Transactions dated{' '}
                {futureYears.join(', ')} are not shown here yet — each appears
                once its year begins.
              </p>
            )}
            <p>
              Those transactions are still in your ledger and listed under
              Transactions.
            </p>
          </AlertDescription>
        </Alert>
      )}

      <Tabs value={activeTab} onValueChange={setActiveTab}>
        {/* `scrollableTabsList` STAYS. It is not the defect — it is the
            fallback, and the sizing above is what stops the fallback from
            being the everyday experience at 360px. A strip that is made to fit
            today can stop fitting tomorrow (a fifth tab, a longer label, a
            larger system font), and without these three classes the overflow
            goes back to widening the page instead of scrolling. */}
        <TabsList className={scrollableTabsList}>
          <TabsTrigger value="overview" className={REPORTS_TAB_TRIGGER}>
            Overview
          </TabsTrigger>
          <TabsTrigger value="spending" className={REPORTS_TAB_TRIGGER}>
            Spending
          </TabsTrigger>
          <TabsTrigger value="savings" className={REPORTS_TAB_TRIGGER}>
            Savings
          </TabsTrigger>
          <TabsTrigger value="patterns" className={REPORTS_TAB_TRIGGER}>
            Patterns
          </TabsTrigger>
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
