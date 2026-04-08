# Dashboard Data Wiring Fix — Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix broken data wiring in the dashboard: remove the fake budget card, add a savings progress card, fix category bar math, make transactions respect month filter, and fix the cash flow toggle.

**Architecture:** All changes are frontend-only. The backend already returns all needed data. Five independent fixes applied to `Dashboard.tsx` and its CSS module, with test updates.

**Tech Stack:** React, TypeScript, Recharts, CSS Modules, Vitest

**Spec:** `docs/superpowers/specs/2026-04-09-dashboard-data-wiring-design.md`

**Note:** Spec Section 5 ("Use Server-Computed savings_this_month for Total Balance KPI") is deliberately excluded — it's a minor optional cleanup with no user-visible change. Can be done later.

---

## Chunk 1: Cash Flow Toggle + Category Bar Fix + Budget Removal

### Task 1: Fix Cash Flow Toggle — 6M / 12M

**Files:**
- Modify: `web/src/pages/Dashboard.tsx`

- [ ] **Step 1: Update the CashFlowView type and state**

In `web/src/pages/Dashboard.tsx`, change the type definition:

```ts
// Before
type CashFlowView = 'monthly' | 'yearly';

// After
type CashFlowView = '6m' | '12m';
```

Change the state initialization:

```ts
// Before
const [cashFlowView, setCashFlowView] = useState<CashFlowView>('monthly');

// After
const [cashFlowView, setCashFlowView] = useState<CashFlowView>('6m');
```

- [ ] **Step 2: Replace chart data computation**

Replace the `monthlyChartData`, `yearlyChartData`, and `chartData` block (the three computed values between `/* ── Derived data ── */` and `// Categories`) with a single value:

```ts
  // Cash flow chart data
  const chartData = (() => {
    const sorted = [...trend].reverse();
    const sliced = cashFlowView === '6m' ? sorted.slice(-6) : sorted;
    return sliced.map((item) => ({
      name: SHORT_MONTHS[item.month - 1],
      income: item.total_income,
      expense: -item.total_spent,
    }));
  })();
```

- [ ] **Step 3: Update toggle button values**

Find the Cash Flow toggle buttons (the `<div className={styles.cfToggle}>` block) and update:

```tsx
<div className={styles.cfToggle}>
  <button
    className={`${styles.cfToggleBtn} ${cashFlowView === '6m' ? styles.cfToggleBtnActive : ''}`}
    onClick={() => setCashFlowView('6m')}
  >
    6M
  </button>
  <button
    className={`${styles.cfToggleBtn} ${cashFlowView === '12m' ? styles.cfToggleBtnActive : ''}`}
    onClick={() => setCashFlowView('12m')}
  >
    12M
  </button>
</div>
```

- [ ] **Step 4: Run type check**

Run: `cd web && npx tsc --noEmit`
Expected: No errors

- [ ] **Step 5: Run tests**

Run: `cd web && npx vitest run src/pages/Dashboard.test.tsx`
Expected: All tests pass

- [ ] **Step 6: Commit**

```bash
git add web/src/pages/Dashboard.tsx
git commit -m "fix: replace 1Y cash flow toggle with 12M individual bars"
```

---

### Task 2: Fix Category Bar/Percentage Denominator Mismatch

**Files:**
- Modify: `web/src/pages/Dashboard.tsx`

- [ ] **Step 1: Fix the bar width calculation**

In `web/src/pages/Dashboard.tsx`, find the category list rendering inside `gaugeData.map`. There are two variables computed per category:

```ts
// pct — already uses totalCategorySpent (correct)
const pct = totalCategorySpent > 0
  ? Math.round((cat.value / totalCategorySpent) * 100)
  : 0;
// barPct — uses gaugeData[0].value (WRONG denominator)
const barPct = totalCategorySpent > 0
  ? (cat.value / gaugeData[0].value) * 100
  : 0;
```

Delete the entire `barPct` declaration. Then update the bar fill style to use `pct` instead of `barPct`:

```tsx
<div
  className={styles.catBarFill}
  style={{
    width: `${Math.min(100, pct)}%`,
    background: cat.color,
    opacity: 0.7,
  }}
/>
```

- [ ] **Step 2: Run type check**

Run: `cd web && npx tsc --noEmit`
Expected: No errors (no dead `barPct` variable)

- [ ] **Step 3: Run tests**

Run: `cd web && npx vitest run src/pages/Dashboard.test.tsx`
Expected: All tests pass

- [ ] **Step 4: Commit**

```bash
git add web/src/pages/Dashboard.tsx
git commit -m "fix: use same denominator for category bar width and percentage label"
```

---

### Task 3: Remove Monthly Budget Card

**Files:**
- Modify: `web/src/pages/Dashboard.tsx`
- Modify: `web/src/styles/Dashboard.module.css`

- [ ] **Step 1: Remove the `budgetGradient` function**

Delete the entire budget gradient section in `web/src/pages/Dashboard.tsx` — the comment `/* ── Budget bar gradient helpers ── */`, the blank line, and the `budgetGradient` function:

```ts
/* ── Budget bar gradient helpers ── */

function budgetGradient(pct: number): string {
  if (pct >= 100) return 'linear-gradient(90deg, #EF8B6E, #E07050)';
  if (pct >= 85) return 'linear-gradient(90deg, #F0C84D, #E0B83D)';
  return 'linear-gradient(90deg, #16C8C7, #12B0AF)';
}
```

- [ ] **Step 2: Remove `budgetTotal` variable**

Delete the line:

```ts
  const budgetTotal = summary?.budget ?? 0;
```

- [ ] **Step 3: Remove the Monthly Budget JSX block**

Delete the entire block from `{/* ── Monthly Budget ── */}` through its closing `</div>`. This is the card containing "Monthly Budget" title, budget bars, and the "No budget set" fallback.

- [ ] **Step 4: Remove budget CSS classes**

In `web/src/styles/Dashboard.module.css`, delete the entire section from `/* ===== Monthly Budget ===== */` through the closing brace of `.budgetPctOver` (which includes `composes: budgetPct`). All classes to remove: `.budgetList`, `.budgetItem`, `.budgetInfo`, `.budgetName`, `.budgetAmounts`, `.budgetLimit`, `.budgetLimitOver`, `.budgetBarRow`, `.budgetTrack`, `.budgetFill`, `.budgetPct`, `.budgetPctOver`.

- [ ] **Step 5: Run type check + stylelint**

Run: `cd web && npx tsc --noEmit && npx stylelint "src/**/*.css"`
Expected: No errors

- [ ] **Step 6: Run tests**

Run: `cd web && npx vitest run src/pages/Dashboard.test.tsx`
Expected: All tests pass (no test asserted "Monthly Budget")

- [ ] **Step 7: Commit**

```bash
git add web/src/pages/Dashboard.tsx web/src/styles/Dashboard.module.css
git commit -m "fix: remove broken monthly budget card with fake per-category bars"
```

---

## Chunk 2: Savings Progress Card

### Task 4: Add Savings Progress Card — CSS

**Files:**
- Modify: `web/src/styles/Dashboard.module.css` (add styles where the budget section was, before `/* ===== Recent Transactions ===== */`)

- [ ] **Step 1: Add savings card CSS classes and toggle link class**

Insert these styles in `web/src/styles/Dashboard.module.css` where the budget styles were removed, before `/* ===== Recent Transactions ===== */`:

```css
/* ===== Savings Progress ===== */
.savingsRing {
  position: relative;
  width: 160px;
  height: 160px;
  margin: 0 auto 16px;
}

.savingsCenter {
  position: absolute;
  top: 50%;
  left: 50%;
  transform: translate(-50%, -50%);
  text-align: center;
  pointer-events: none;
}

.savingsCenterPct {
  font-size: 28px;
  font-weight: 700;
  color: var(--color-primary);
  display: block;
}

.savingsCenterLabel {
  font-size: 12px;
  color: var(--text-tertiary);
}

.savingsStats {
  display: flex;
  justify-content: space-between;
  margin-top: 12px;
  padding-top: 12px;
  border-top: 1px solid var(--border-default);
}

.savingsStat {
  text-align: center;
  flex: 1;
}

.savingsStatValue {
  font-size: 15px;
  font-weight: 600;
  color: var(--text-primary);
  font-variant-numeric: tabular-nums;
}

.savingsStatLabel {
  font-size: 11px;
  color: var(--text-tertiary);
  margin-top: 2px;
}

.savingsEmptyMsg {
  font-size: 14px;
  color: var(--text-secondary);
  text-align: center;
  padding: 32px 16px;
}

.savingsEmptyMsg a {
  color: var(--color-primary);
  font-weight: 500;
}

/* Toggle link button (used in Recent Transactions header) */
.toggleLink {
  font-size: 13px;
  font-weight: 500;
  color: var(--color-primary);
  background: none;
  border: none;
  padding: 0;
  cursor: pointer;
  font-family: var(--font-sans);
}

.toggleLink:hover {
  opacity: 0.8;
}
```

- [ ] **Step 2: Run stylelint**

Run: `cd web && npx stylelint "src/**/*.css"`
Expected: No errors

- [ ] **Step 3: Commit**

```bash
git add web/src/styles/Dashboard.module.css
git commit -m "feat: add savings progress card and toggle link styles"
```

---

### Task 5: Add Savings Progress Card — JSX

**Files:**
- Modify: `web/src/pages/Dashboard.tsx` (add `Link` import, add savings card JSX where budget card was)

- [ ] **Step 1: Add the Link import**

At the top of `web/src/pages/Dashboard.tsx`, add the `Link` import from react-router-dom. This import does not currently exist in the file:

```ts
import { Link } from 'react-router-dom';
```

- [ ] **Step 2: Add savings card JSX**

Where the Monthly Budget card was removed (after the Spending by Category card, before the Recent Transactions card), insert:

```tsx
        {/* ── Savings Progress ── */}
        <div className={styles.card}>
          <div className={styles.cardHeader}>
            <div>
              <div className={styles.cardTitle}>Savings Progress</div>
              <div className={styles.cardSubtitle}>
                {selectedYear} annual goal
              </div>
            </div>
          </div>
          {/* Outer guard: savings_goal > 0 ensures savings_goal_progress is valid (no division by zero on backend) */}
          {summary && (summary.savings_goal ?? 0) > 0 ? (
            <>
              <div className={styles.savingsRing}>
                <ResponsiveContainer width="100%" height="100%">
                  <PieChart>
                    <Pie
                      data={[
                        {
                          name: 'Saved',
                          value: Math.min(100, Math.max(0, summary.savings_goal_progress)),
                        },
                        {
                          name: 'Remaining',
                          value: Math.max(0, 100 - Math.min(100, Math.max(0, summary.savings_goal_progress))),
                        },
                      ]}
                      dataKey="value"
                      startAngle={90}
                      endAngle={-270}
                      cx="50%"
                      cy="50%"
                      innerRadius={55}
                      outerRadius={75}
                      cornerRadius={4}
                      paddingAngle={0}
                      stroke="none"
                    >
                      <Cell fill="var(--color-primary)" />
                      <Cell fill="var(--border-default)" />
                    </Pie>
                  </PieChart>
                </ResponsiveContainer>
                <div className={styles.savingsCenter}>
                  <span className={styles.savingsCenterPct}>
                    {Math.round(Math.min(100, Math.max(0, summary.savings_goal_progress)))}%
                  </span>
                  <span className={styles.savingsCenterLabel}>of goal</span>
                </div>
              </div>
              <div className={styles.savingsStats}>
                <div className={styles.savingsStat}>
                  <div className={styles.savingsStatValue}>{formatCompact(summary.savings_ytd)}</div>
                  <div className={styles.savingsStatLabel}>Saved YTD</div>
                </div>
                <div className={styles.savingsStat}>
                  <div className={styles.savingsStatValue}>{formatCompact(summary.savings_goal)}</div>
                  <div className={styles.savingsStatLabel}>Annual Goal</div>
                </div>
                <div className={styles.savingsStat}>
                  <div className={styles.savingsStatValue}>{formatCompact(summary.savings_this_month)}</div>
                  <div className={styles.savingsStatLabel}>This Month</div>
                </div>
              </div>
            </>
          ) : (
            <div className={styles.savingsEmptyMsg}>
              <Link to="/settings">Set a savings goal</Link> to track progress
            </div>
          )}
        </div>
```

- [ ] **Step 3: Run type check**

Run: `cd web && npx tsc --noEmit`
Expected: No errors

- [ ] **Step 4: Run tests**

Run: `cd web && npx vitest run src/pages/Dashboard.test.tsx`
Expected: All tests pass

- [ ] **Step 5: Commit**

```bash
git add web/src/pages/Dashboard.tsx
git commit -m "feat: add savings progress card with donut chart and YTD stats"
```

---

## Chunk 3: Recent Transactions Filter + Test Updates

### Task 6: Fix Recent Transactions — Respect Month/Year Filter

**Files:**
- Modify: `web/src/pages/Dashboard.tsx`

- [ ] **Step 1: Add `showLatest` state**

In `web/src/pages/Dashboard.tsx`, after the `recentTransactions` state declaration, add:

```ts
  const [showLatest, setShowLatest] = useState(false);
```

- [ ] **Step 2: Update the useEffect to respect filters**

Replace the existing transactions useEffect (the one with empty `[]` dependency array):

```ts
  // Before
  useEffect(() => {
    api
      .get<PaginatedResponse<Transaction>>('transactions?per_page=6')
      .then((data) => setRecentTransactions(data.transactions))
      .catch(() => { /* silent — non-critical */ });
  }, []);
```

With:

```ts
  useEffect(() => {
    let url = 'transactions?per_page=6';
    if (!showLatest) {
      const mm = String(selectedMonth).padStart(2, '0');
      const lastDay = new Date(selectedYear, selectedMonth, 0).getDate();
      const dd = String(lastDay).padStart(2, '0');
      url += `&date_from=${selectedYear}-${mm}-01&date_to=${selectedYear}-${mm}-${dd}`;
    }
    api
      .get<PaginatedResponse<Transaction>>(url)
      .then((data) => setRecentTransactions(data.transactions))
      .catch(() => { /* silent — non-critical */ });
  }, [selectedYear, selectedMonth, showLatest]);
```

- [ ] **Step 3: Update the Recent Transactions card header JSX**

Find the Recent Transactions card header and replace it:

```tsx
        {/* ── Recent Transactions ── */}
        <div className={styles.card}>
          <div className={styles.cardHeader}>
            <div>
              <div className={styles.cardTitle}>Recent Transactions</div>
              <div className={styles.cardSubtitle}>
                {showLatest ? 'Latest activity' : `${MONTHS[selectedMonth - 1]} ${selectedYear}`}
              </div>
            </div>
            <div style={{ display: 'flex', gap: 12, alignItems: 'center' }}>
              <button
                className={styles.toggleLink}
                onClick={() => setShowLatest(!showLatest)}
              >
                {showLatest ? `Show ${MONTHS[selectedMonth - 1]} \u2192` : 'Show latest \u2192'}
              </button>
              <a href="/transactions" className={styles.cardLink}>View all &rarr;</a>
            </div>
          </div>
```

- [ ] **Step 4: Run type check**

Run: `cd web && npx tsc --noEmit`
Expected: No errors

- [ ] **Step 5: Run tests**

Run: `cd web && npx vitest run src/pages/Dashboard.test.tsx`
Expected: All tests pass

- [ ] **Step 6: Commit**

```bash
git add web/src/pages/Dashboard.tsx
git commit -m "fix: recent transactions respect month/year filter with toggle"
```

---

### Task 7: Update Tests

**Files:**
- Modify: `web/src/pages/Dashboard.test.tsx`

- [ ] **Step 1: Verify api mock works for filtered URLs**

The `api.get` mock uses `vi.fn().mockResolvedValue(...)` which returns the same data regardless of URL. No change needed — the mock handles filtered requests automatically.

- [ ] **Step 2: Add savings progress test**

Add a new test after the existing tests:

```ts
  test('renders Savings Progress section', async () => {
    render(<MemoryRouter><Dashboard /></MemoryRouter>);
    await waitFor(() => {
      expect(screen.getByText('Savings Progress')).toBeInTheDocument();
      expect(screen.getByText('of goal')).toBeInTheDocument();
      expect(screen.getByText('Saved YTD')).toBeInTheDocument();
      expect(screen.getByText('Annual Goal')).toBeInTheDocument();
    });
  });
```

- [ ] **Step 3: Add budget removal regression test**

```ts
  test('does not render removed Monthly Budget section', () => {
    render(<MemoryRouter><Dashboard /></MemoryRouter>);
    expect(screen.queryByText('Monthly Budget')).not.toBeInTheDocument();
  });
```

- [ ] **Step 4: Add cash flow 12M toggle test**

```ts
  test('renders 6M and 12M toggle buttons', () => {
    render(<MemoryRouter><Dashboard /></MemoryRouter>);
    expect(screen.getByText('6M')).toBeInTheDocument();
    expect(screen.getByText('12M')).toBeInTheDocument();
  });
```

- [ ] **Step 5: Run all tests**

Run: `cd web && npx vitest run`
Expected: All tests pass across all test files

- [ ] **Step 6: Run full verification**

Run: `cd web && npx tsc --noEmit && npx stylelint "src/**/*.css"`
Expected: No errors from either tool

- [ ] **Step 7: Commit**

```bash
git add web/src/pages/Dashboard.test.tsx
git commit -m "test: update dashboard tests for savings card, budget removal, and toggle"
```
