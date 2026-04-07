# UI/UX Overhaul Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Transform SpenDrop's frontend from a functional prototype into a polished fintech-grade dashboard with toggle sidebar, max-width layout, themed charts, border-only cards, overlapping tabs, and a redesigned dashboard page.

**Architecture:** CSS-first approach — most changes are styling updates using existing design tokens. New components (`Tabs`, `ChartTooltip`) are small and reusable. The dashboard page gets a full rewrite with Recharts bidirectional bar chart and donut chart. No backend changes — all data comes from existing API responses.

**Tech Stack:** React 19 + TypeScript 6 + Vite 8, Recharts 3.8.1, Lucide React, CSS Modules, Vitest + React Testing Library

**Spec:** `docs/superpowers/specs/2026-04-07-ui-ux-overhaul.md`

---

## Chunk 1: Foundation (Sidebar, Layout, Tokens)

### Task 1: Add display typography token

**Files:**
- Modify: `web/src/styles/tokens.css`

- [ ] **Step 1: Add the `--type-display` token to tokens.css**

In `web/src/styles/tokens.css`, inside the `:root` block, after the existing `--type-amount-*` tokens, add:

```css
/* Display — hero numbers (Total Balance, Savings %) */
--type-display-size: 36px;
--type-display-weight: 700;
--type-display-line-height: 40px;
```

- [ ] **Step 2: Verify the token is parseable**

Run: `cd web && npx stylelint src/styles/tokens.css`
Expected: No errors (tokens.css is exempt from hex/color rules)

- [ ] **Step 3: Commit**

```bash
git add web/src/styles/tokens.css
git commit -m "feat: add --type-display token for hero numbers (36px/700)"
```

---

### Task 2: Sidebar toggle pin (no hover)

**Files:**
- Modify: `web/src/components/Sidebar.tsx`
- Modify: `web/src/styles/Sidebar.module.css`
- Modify: `web/src/components/Sidebar.test.tsx`

- [ ] **Step 1: Write failing tests for sidebar toggle behavior**

In `web/src/components/Sidebar.test.tsx`, add these tests:

```typescript
import { vi, describe, test, expect, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { Sidebar } from './Sidebar';

// ... existing mock setup for useAuth, useTheme ...

describe('Sidebar toggle', () => {
  test('renders collapsed by default', () => {
    render(<MemoryRouter><Sidebar /></MemoryRouter>);
    const sidebar = screen.getByRole('navigation');
    expect(sidebar.className).not.toContain('expanded');
  });

  beforeEach(() => {
    localStorage.clear();
  });

  test('does not expand on mouse hover', async () => {
    const user = userEvent.setup();
    render(<MemoryRouter><Sidebar /></MemoryRouter>);
    const sidebar = screen.getByRole('navigation');
    await user.hover(sidebar);
    expect(sidebar.className).not.toContain('expanded');
  });

  test('expands when toggle button is clicked', async () => {
    const user = userEvent.setup();
    render(<MemoryRouter><Sidebar /></MemoryRouter>);
    const toggle = screen.getByLabelText('Toggle sidebar');
    await user.click(toggle);
    const sidebar = screen.getByRole('navigation');
    expect(sidebar.className).toContain('expanded');
  });

  test('persists expanded state to localStorage', async () => {
    const user = userEvent.setup();
    render(<MemoryRouter><Sidebar /></MemoryRouter>);
    const toggle = screen.getByLabelText('Toggle sidebar');
    await user.click(toggle);
    expect(localStorage.getItem('spendrop-sidebar')).toBe('true');
  });

  test('reads initial state from localStorage', () => {
    localStorage.setItem('spendrop-sidebar', 'true');
    render(<MemoryRouter><Sidebar /></MemoryRouter>);
    const sidebar = screen.getByRole('navigation');
    expect(sidebar.className).toContain('expanded');
  });
});
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd web && npx vitest run src/components/Sidebar.test.tsx`
Expected: New tests FAIL (no toggle button, hover still expands)

- [ ] **Step 3: Update Sidebar.tsx — remove hover, add toggle**

In `web/src/components/Sidebar.tsx`:

1. Replace the `useState(false)` for `expanded` with localStorage-backed state:

```typescript
const [expanded, setExpanded] = useState(() => {
  return localStorage.getItem('spendrop-sidebar') === 'true';
});

const toggleSidebar = () => {
  setExpanded(prev => {
    const next = !prev;
    localStorage.setItem('spendrop-sidebar', String(next));
    return next;
  });
};
```

2. Remove `onMouseEnter` and `onMouseLeave` handlers from the `<nav>` element.

3. Add a toggle button after the logo, before the nav items:

```tsx
<button
  className={styles.toggleButton}
  onClick={toggleSidebar}
  aria-label="Toggle sidebar"
>
  {expanded ? <PanelLeftClose size={18} /> : <PanelLeftOpen size={18} />}
</button>
```

Import `PanelLeftClose` and `PanelLeftOpen` from `lucide-react`.

- [ ] **Step 4: Update Sidebar.module.css — fix icon centering, add toggle styles**

In `web/src/styles/Sidebar.module.css`:

1. On `.sidebar`, ensure `align-items: center` is set (centers all children including `.navLink`).

2. Add toggle button styles:

```css
.toggleButton {
  width: 40px;
  height: 40px;
  border-radius: var(--radius-md);
  border: none;
  background: transparent;
  color: var(--text-secondary);
  display: flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
  margin-bottom: var(--space-2);
  transition: color var(--duration-fast) var(--ease-standard),
              background var(--duration-fast) var(--ease-standard);
}

.toggleButton:hover {
  color: var(--text-primary);
  background: var(--primary-a8);
}

/* Directional easing: expand decelerates, collapse accelerates */
.sidebar {
  transition: width var(--duration-slow) var(--ease-accelerate);
}

.expanded {
  transition: width var(--duration-slow) var(--ease-decelerate);
}
```

3. Ensure `.navLink` has `width: 40px` when collapsed and the `.active` background highlight is centered. The sidebar's `align-items: center` handles this. When expanded, `.navLink` should be `width: 100%` with `padding: 0 var(--space-3)`.

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd web && npx vitest run src/components/Sidebar.test.tsx`
Expected: ALL tests PASS

- [ ] **Step 6: Run TypeScript check**

Run: `cd web && npx tsc -b --noEmit`
Expected: No errors

- [ ] **Step 7: Commit**

```bash
git add web/src/components/Sidebar.tsx web/src/styles/Sidebar.module.css web/src/components/Sidebar.test.tsx
git commit -m "feat: replace sidebar hover with toggle pin, persist to localStorage"
```

---

### Task 3: Page max-width and sidebar-aware layout

**Files:**
- Modify: `web/src/styles/AppLayout.module.css`
- Modify: the component that renders the layout (check if `AppLayout.tsx` or `App.tsx` uses these styles)

- [ ] **Step 1: Update AppLayout.module.css**

Replace the current `.main` styles in `web/src/styles/AppLayout.module.css` with:

```css
.layout {
  display: flex;
  min-height: 100vh;
}

.main {
  flex: 1;
  padding-left: calc(64px + var(--space-10));
  padding-right: var(--space-10);
  padding-top: var(--space-8);
  padding-bottom: var(--space-8);
  max-width: calc(1400px + 64px);
  margin: 0 auto;
  transition: padding-left var(--duration-slow) var(--ease-accelerate);
}

.mainExpanded {
  padding-left: calc(240px + var(--space-10));
  max-width: calc(1400px + 240px);
  transition: padding-left var(--duration-slow) var(--ease-decelerate);
}
```

- [ ] **Step 2: Wire up the `mainExpanded` class to sidebar state**

The layout component needs to know if the sidebar is expanded. Two approaches:
- **Option A (recommended):** Read `localStorage('spendrop-sidebar')` directly in the layout component and apply the class conditionally.
- **Option B:** Lift sidebar state to a context/provider.

For simplicity, use Option A in the layout component:

```typescript
const [sidebarExpanded, setSidebarExpanded] = useState(() => {
  return localStorage.getItem('spendrop-sidebar') === 'true';
});

// Listen for sidebar toggle events (custom event dispatched by Sidebar component)
useEffect(() => {
  const handler = () => {
    setSidebarExpanded(localStorage.getItem('spendrop-sidebar') === 'true');
  };
  window.addEventListener('sidebar-toggle', handler);
  return () => {
    window.removeEventListener('sidebar-toggle', handler);
  };
}, []);
```

In `Sidebar.tsx`, dispatch a custom event after toggling:

```typescript
const toggleSidebar = () => {
  setExpanded(prev => {
    const next = !prev;
    localStorage.setItem('spendrop-sidebar', String(next));
    window.dispatchEvent(new Event('sidebar-toggle'));
    return next;
  });
};
```

Then in the layout JSX:

```tsx
<main className={`${styles.main}${sidebarExpanded ? ` ${styles.mainExpanded}` : ''}`}>
```

- [ ] **Step 3: Verify visually**

Run: `cd web && npm run dev`
Check: Page content is constrained to ~1400px and centered. Sidebar toggle changes padding smoothly.

- [ ] **Step 4: Commit**

```bash
git add web/src/styles/AppLayout.module.css web/src/components/Sidebar.tsx
git commit -m "feat: add max-width 1400px constraint and sidebar-aware padding"
```

---

## Chunk 2: Shared Components (Tabs, ChartTooltip, useChartTheme)

### Task 4: Create shared `<Tabs>` component

**Files:**
- Create: `web/src/components/Tabs.tsx`
- Create: `web/src/styles/Tabs.module.css`
- Create: `web/src/components/Tabs.test.tsx`

- [ ] **Step 1: Write failing tests**

Create `web/src/components/Tabs.test.tsx`:

```typescript
import { vi, describe, test, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Tabs } from './Tabs';

const tabs = [
  { key: 'general', label: 'General' },
  { key: 'advanced', label: 'Advanced' },
  { key: 'data', label: 'Data' },
];

describe('Tabs', () => {
  test('renders all tab labels', () => {
    render(<Tabs tabs={tabs} activeKey="general" onTabChange={() => {}} />);
    expect(screen.getByText('General')).toBeInTheDocument();
    expect(screen.getByText('Advanced')).toBeInTheDocument();
    expect(screen.getByText('Data')).toBeInTheDocument();
  });

  test('marks active tab with aria-selected', () => {
    render(<Tabs tabs={tabs} activeKey="advanced" onTabChange={() => {}} />);
    expect(screen.getByRole('tab', { name: 'Advanced' })).toHaveAttribute('aria-selected', 'true');
    expect(screen.getByRole('tab', { name: 'General' })).toHaveAttribute('aria-selected', 'false');
  });

  test('calls onTabChange when a tab is clicked', async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(<Tabs tabs={tabs} activeKey="general" onTabChange={onChange} />);
    await user.click(screen.getByText('Data'));
    expect(onChange).toHaveBeenCalledWith('data');
  });
});
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd web && npx vitest run src/components/Tabs.test.tsx`
Expected: FAIL (module not found)

- [ ] **Step 3: Create Tabs.module.css**

Create `web/src/styles/Tabs.module.css`:

```css
.tabRow {
  position: relative;
  display: flex;
  gap: var(--space-6);
  border-bottom: 1px solid var(--border-muted);
}

.tab {
  padding: var(--space-2) 0 var(--space-3) 0;
  color: var(--text-secondary);
  font-size: var(--type-label-lg-size);
  font-weight: var(--type-label-lg-weight);
  line-height: var(--type-label-lg-line-height);
  border: none;
  border-bottom: 2px solid transparent;
  margin-bottom: -1px;
  cursor: pointer;
  background: transparent;
  font-family: inherit;
  transition: color var(--duration-fast) var(--ease-standard);
}

.tab:hover {
  color: var(--text-primary);
}

.tabActive {
  color: var(--text-primary);
  border-bottom-color: var(--color-primary);
}
```

- [ ] **Step 4: Create Tabs.tsx**

Create `web/src/components/Tabs.tsx`:

```typescript
import styles from '../styles/Tabs.module.css';

interface Tab {
  key: string;
  label: string;
}

interface TabsProps {
  tabs: Tab[];
  activeKey: string;
  onTabChange: (key: string) => void;
}

export function Tabs({ tabs, activeKey, onTabChange }: TabsProps) {
  return (
    <div className={styles.tabRow} role="tablist">
      {tabs.map(tab => (
        <button
          key={tab.key}
          role="tab"
          aria-selected={tab.key === activeKey}
          className={`${styles.tab}${tab.key === activeKey ? ` ${styles.tabActive}` : ''}`}
          onClick={() => onTabChange(tab.key)}
        >
          {tab.label}
        </button>
      ))}
    </div>
  );
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd web && npx vitest run src/components/Tabs.test.tsx`
Expected: ALL PASS

- [ ] **Step 6: Commit**

```bash
git add web/src/components/Tabs.tsx web/src/styles/Tabs.module.css web/src/components/Tabs.test.tsx
git commit -m "feat: add shared Tabs component with overlapping underline style"
```

---

### Task 5: Create `useChartTheme` hook

**Files:**
- Create: `web/src/hooks/useChartTheme.ts`
- Create: `web/src/hooks/useChartTheme.test.ts`

- [ ] **Step 1: Write failing test**

Create `web/src/hooks/useChartTheme.test.ts`:

```typescript
import { describe, test, expect } from 'vitest';
import { renderHook } from '@testing-library/react';
import { useChartTheme } from './useChartTheme';

describe('useChartTheme', () => {
  test('returns all required theme properties', () => {
    const { result } = renderHook(() => useChartTheme());
    const theme = result.current;

    expect(theme).toHaveProperty('axisStroke');
    expect(theme).toHaveProperty('gridStroke');
    expect(theme).toHaveProperty('tooltipBg');
    expect(theme).toHaveProperty('tooltipBorder');
    expect(theme).toHaveProperty('tooltipText');
    expect(theme).toHaveProperty('hoverBg');
    expect(theme).toHaveProperty('incomeColor');
    expect(theme).toHaveProperty('expenseColor');
    expect(theme).toHaveProperty('categoryColors');
    expect(theme.categoryColors).toHaveLength(6);
  });

  test('returns string values for all color properties', () => {
    const { result } = renderHook(() => useChartTheme());
    const theme = result.current;

    // All values should be strings (resolved CSS colors or fallbacks)
    expect(typeof theme.axisStroke).toBe('string');
    expect(typeof theme.incomeColor).toBe('string');
    expect(typeof theme.expenseColor).toBe('string');
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && npx vitest run src/hooks/useChartTheme.test.ts`
Expected: FAIL (module not found)

- [ ] **Step 3: Create useChartTheme.ts**

Create `web/src/hooks/useChartTheme.ts`:

```typescript
import { useMemo } from 'react';

interface ChartTheme {
  axisStroke: string;
  gridStroke: string;
  tooltipBg: string;
  tooltipBorder: string;
  tooltipText: string;
  hoverBg: string;
  incomeColor: string;
  expenseColor: string;
  categoryColors: string[];
}

function getCSSVar(name: string, fallback: string): string {
  if (typeof window === 'undefined') return fallback;
  const value = getComputedStyle(document.documentElement).getPropertyValue(name).trim();
  return value || fallback;
}

export function useChartTheme(): ChartTheme {
  return useMemo(() => ({
    axisStroke: getCSSVar('--text-tertiary', '#58585F'),
    gridStroke: getCSSVar('--border-muted', '#1E1E23'),
    tooltipBg: getCSSVar('--surface-overlay', '#1E1E23'),
    tooltipBorder: getCSSVar('--border-default', '#2A2A30'),
    tooltipText: getCSSVar('--text-primary', '#F5F5F6'),
    hoverBg: getCSSVar('--primary-a8', 'rgba(129,140,248,0.08)'),
    incomeColor: getCSSVar('--color-income', '#7EC89B'),
    expenseColor: getCSSVar('--color-expense', '#E88B9C'),
    categoryColors: [
      getCSSVar('--color-primary', '#818CF8'),
      getCSSVar('--color-income', '#7EC89B'),
      getCSSVar('--color-expense', '#E88B9C'),
      getCSSVar('--color-warning', '#E8A87C'),
      getCSSVar('--color-info', '#7CAFD4'),
      getCSSVar('--text-tertiary', '#58585F'),
    ],
  }), []);
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd web && npx vitest run src/hooks/useChartTheme.test.ts`
Expected: ALL PASS

- [ ] **Step 5: Commit**

```bash
git add web/src/hooks/useChartTheme.ts web/src/hooks/useChartTheme.test.ts
git commit -m "feat: add useChartTheme hook for Recharts dark theme integration"
```

---

### Task 6: Create `<ChartTooltip>` component

**Files:**
- Create: `web/src/components/ChartTooltip.tsx`
- Create: `web/src/styles/ChartTooltip.module.css`
- Create: `web/src/components/ChartTooltip.test.tsx`

- [ ] **Step 1: Write failing test**

Create `web/src/components/ChartTooltip.test.tsx`:

```typescript
import { describe, test, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { ChartTooltip } from './ChartTooltip';

describe('ChartTooltip', () => {
  test('renders nothing when not active', () => {
    const { container } = render(<ChartTooltip active={false} payload={[]} label="" />);
    expect(container.firstChild).toBeNull();
  });

  test('renders label and payload when active', () => {
    const payload = [
      { name: 'Income', value: 3200, color: '#7EC89B' },
      { name: 'Expenses', value: -1248, color: '#E88B9C' },
    ];
    render(<ChartTooltip active={true} payload={payload} label="Apr" />);
    expect(screen.getByText('Apr')).toBeInTheDocument();
    expect(screen.getByText('$3,200')).toBeInTheDocument();
    expect(screen.getByText('$1,248')).toBeInTheDocument();
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && npx vitest run src/components/ChartTooltip.test.tsx`
Expected: FAIL

- [ ] **Step 3: Create ChartTooltip.module.css**

Create `web/src/styles/ChartTooltip.module.css`:

```css
.tooltip {
  background: var(--surface-overlay);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-md);
  padding: var(--space-2) var(--space-3);
  pointer-events: none;
}

.label {
  font-size: var(--type-label-sm-size);
  font-weight: var(--type-label-sm-weight);
  color: var(--text-tertiary);
  margin-bottom: var(--space-1);
}

.row {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  margin-bottom: 2px;
}

.row:last-child {
  margin-bottom: 0;
}

.dot {
  width: 6px;
  height: 6px;
  border-radius: 1px;
  flex-shrink: 0;
}

.name {
  font-size: var(--type-body-sm-size);
  color: var(--text-secondary);
  flex: 1;
}

.value {
  font-size: var(--type-body-sm-size);
  font-weight: 600;
  color: var(--text-primary);
  font-variant-numeric: tabular-nums;
}
```

- [ ] **Step 4: Create ChartTooltip.tsx**

Create `web/src/components/ChartTooltip.tsx`:

```typescript
import styles from '../styles/ChartTooltip.module.css';

interface TooltipPayloadItem {
  name: string;
  value: number;
  color: string;
}

interface ChartTooltipProps {
  active?: boolean;
  payload?: TooltipPayloadItem[];
  label?: string;
}

function formatCurrency(value: number): string {
  return '$' + Math.abs(value).toLocaleString('en-US', {
    minimumFractionDigits: 0,
    maximumFractionDigits: 0,
  });
}

export function ChartTooltip({ active, payload, label }: ChartTooltipProps) {
  if (!active || !payload?.length) return null;

  return (
    <div className={styles.tooltip}>
      <div className={styles.label}>{label}</div>
      {payload.map((entry, i) => (
        <div key={i} className={styles.row}>
          <div className={styles.dot} style={{ background: entry.color }} />
          <span className={styles.name}>{entry.name}</span>
          <span className={styles.value}>{formatCurrency(entry.value)}</span>
        </div>
      ))}
    </div>
  );
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd web && npx vitest run src/components/ChartTooltip.test.tsx`
Expected: ALL PASS

- [ ] **Step 6: Commit**

```bash
git add web/src/components/ChartTooltip.tsx web/src/styles/ChartTooltip.module.css web/src/components/ChartTooltip.test.tsx
git commit -m "feat: add ChartTooltip component for dark-themed Recharts tooltips"
```

---

## Chunk 3: Card & Tab Styling Updates

### Task 7: Update card styles to border-only pattern

**Files:**
- Modify: `web/src/styles/Dashboard.module.css`
- Modify: `web/src/styles/Settings.module.css`
- Modify: `web/src/styles/Categories.module.css`
- Modify: `web/src/styles/Reports.module.css`

- [ ] **Step 1: Update Dashboard.module.css card styles**

In `web/src/styles/Dashboard.module.css`, find all `.kpiCard`, `.chartCard`, `.recentSection`, or similar card classes and change:
- `background: var(--surface-raised)` → `background: transparent`
- `box-shadow: var(--shadow-sm)` → remove
- Keep: `border: 1px solid var(--border-muted)`, `border-radius: var(--radius-lg)`, `padding: var(--space-5)`

- [ ] **Step 2: Update Settings.module.css card styles**

In `web/src/styles/Settings.module.css`, find `.card` and `.section` classes:
- `background: var(--surface-raised)` → `background: transparent`
- Remove `box-shadow` if present
- Keep borders and border-radius

- [ ] **Step 3: Update Categories.module.css card styles**

In `web/src/styles/Categories.module.css`, find `.section` and `.card`:
- `background: var(--surface-raised)` → `background: transparent`
- `.card` (individual category cards): `background: var(--surface-sunken)` → `background: transparent`, keep border

- [ ] **Step 4: Update Reports.module.css card styles**

In `web/src/styles/Reports.module.css`, find `.section`:
- `background: var(--surface-raised)` → `background: transparent`
- Remove `box-shadow` if present

- [ ] **Step 5: Apply grouped sections divider pattern to Settings**

In `web/src/styles/Settings.module.css`, update form section containers to use divider-based layout instead of card wrappers:

```css
.sectionGroup {
  border-top: 1px solid var(--border-muted);
}

.sectionGroup:last-child {
  border-bottom: 1px solid var(--border-muted);
}

.sectionItem {
  padding: var(--space-4) 0;
  border-bottom: 1px solid var(--border-muted);
}

.sectionItem:last-child {
  border-bottom: none;
}
```

Apply the same pattern to `Categories.module.css` if it uses card wrappers for grouped items. Also check `Transactions.module.css` for any card containers that need updating.

- [ ] **Step 6: Visual verification**

Run: `cd web && npm run dev`
Check all pages: Dashboard, Settings, Categories, Reports, Transactions — all cards should have transparent backgrounds with visible borders only. Settings form sections should use divider-based layout.

- [ ] **Step 7: Commit**

```bash
git add web/src/styles/Dashboard.module.css web/src/styles/Settings.module.css web/src/styles/Categories.module.css web/src/styles/Reports.module.css
git commit -m "feat: switch all cards to border-only style (transparent backgrounds)"
```

---

### Task 8: Integrate `<Tabs>` into Settings and Categories

**Files:**
- Modify: `web/src/pages/Settings.tsx`
- Modify: `web/src/styles/Settings.module.css`
- Modify: `web/src/pages/Categories.tsx` (only if it has tabs)

- [ ] **Step 1: Replace Settings tab implementation with `<Tabs>` component**

In `web/src/pages/Settings.tsx`:

1. Import the new component: `import { Tabs } from '../components/Tabs';`
2. Find the inline tab rendering (search for `role="tablist"` or the `.tabs` className usage) and replace with:

```tsx
<Tabs
  tabs={tabs.filter(t => !t.adminOnly || isAdmin)}
  activeKey={activeTab}
  onTabChange={(key) => setActiveTab(key as SettingsTab)}
/>
```

3. Remove the old `.tabs` and `.tab` / `.tabActive` CSS class references from Settings.module.css (they're replaced by the shared Tabs styles).

- [ ] **Step 2: Remove unused tab styles from Settings.module.css**

Delete the `.tabs`, `.tab`, `.tabActive` rules from `web/src/styles/Settings.module.css` — they're now in `Tabs.module.css`.

- [ ] **Step 3: Check if Categories page has tabs**

Read `web/src/pages/Categories.tsx`. If it has inline tab rendering, replace with `<Tabs>` the same way. If it only has section headers (Expense/Income sections), skip this step.

- [ ] **Step 4: Run existing tests**

Run: `cd web && npx vitest run src/pages/Settings.test.tsx src/pages/Categories.test.tsx`
Expected: ALL PASS (tab behavior should be preserved)

- [ ] **Step 5: Run TypeScript check**

Run: `cd web && npx tsc -b --noEmit`
Expected: No errors

- [ ] **Step 6: Commit**

```bash
git add web/src/pages/Settings.tsx web/src/styles/Settings.module.css web/src/pages/Categories.tsx web/src/styles/Categories.module.css
git commit -m "refactor: replace inline tabs with shared Tabs component"
```

---

## Chunk 4: Dashboard Redesign

### Task 9: Dashboard page — Hero Row

**Files:**
- Modify: `web/src/pages/Dashboard.tsx`
- Modify: `web/src/styles/Dashboard.module.css`

- [ ] **Step 1: Add hero row styles to Dashboard.module.css**

Add these new classes to `web/src/styles/Dashboard.module.css`:

```css
/* Hero Row */
.heroRow {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: var(--space-4);
  margin-bottom: var(--space-6);
}

.heroCard {
  padding: var(--space-6);
  border-radius: var(--radius-lg);
  border: 1px solid var(--border-muted);
  background: transparent;
}

.heroLabel {
  font-size: var(--type-body-sm-size);
  font-weight: var(--type-body-sm-weight);
  color: var(--text-secondary);
  margin-bottom: var(--space-2);
}

.heroValue {
  font-size: var(--type-display-size);
  font-weight: var(--type-display-weight);
  line-height: var(--type-display-line-height);
  color: var(--text-primary);
  font-variant-numeric: tabular-nums;
  letter-spacing: -0.5px;
}

.heroSub {
  font-size: var(--type-body-sm-size);
  color: var(--text-tertiary);
  margin-top: var(--space-2);
}

.heroDelta {
  font-weight: 600;
}

/* Savings Goal */
.savingsRow {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  margin-bottom: var(--space-3);
}

.savingsPct {
  font-size: var(--type-display-size);
  font-weight: var(--type-display-weight);
  line-height: var(--type-display-line-height);
  font-variant-numeric: tabular-nums;
}

.savingsAmounts {
  font-size: var(--type-body-md-size);
  font-weight: 500;
  color: var(--text-secondary);
  font-variant-numeric: tabular-nums;
}

.savingsBar {
  width: 100%;
  height: var(--space-1);
  background: var(--border-muted);
  border-radius: var(--radius-sm);
  overflow: hidden;
}

.savingsBarFill {
  height: 100%;
  border-radius: var(--radius-sm);
  transition: width var(--duration-normal) var(--ease-decelerate);
}

.savingsDetail {
  font-size: var(--type-body-sm-size);
  color: var(--text-tertiary);
  margin-top: var(--space-2);
}
```

- [ ] **Step 2: Add hero row JSX to Dashboard.tsx**

In `web/src/pages/Dashboard.tsx`, replace the KPI card row section with the hero row. Remove the `<KPICard>` imports and usage. Add:

```tsx
function getSavingsColor(progress: number): string {
  if (progress >= 75) return 'var(--color-income)';
  if (progress >= 25) return 'var(--color-warning)';
  return 'var(--color-expense)';
}

// Inside the component render, after the header:
const totalBalance = (summary?.total_income ?? 0) - (summary?.total_spent ?? 0);
const savingsColor = getSavingsColor(summary?.savings_goal_progress ?? 0);

<div className={styles.heroRow}>
  <div className={styles.heroCard}>
    <div className={styles.heroLabel}>Total Balance</div>
    <div className={styles.heroValue}>
      ${totalBalance.toLocaleString('en-US', { minimumFractionDigits: 0 })}
    </div>
    <div className={styles.heroSub}>
      {(() => {
        const delta = summary?.remaining ?? 0;
        const isPositive = delta >= 0;
        return (
          <>
            <span className={styles.heroDelta} style={{ color: isPositive ? 'var(--color-income)' : 'var(--color-expense)' }}>
              {isPositive ? '+' : '-'}${Math.abs(delta).toLocaleString()}
            </span>{' '}
            from last month
          </>
        );
      })()}
    </div>
  </div>

  <div className={styles.heroCard}>
    <div className={styles.heroLabel}>Savings Goal</div>
    <div className={styles.savingsRow}>
      <span className={styles.savingsPct} style={{ color: savingsColor }}>
        {summary?.savings_goal_progress ?? 0}%
      </span>
      <span className={styles.savingsAmounts}>
        ${(summary?.savings_ytd ?? 0).toLocaleString()} / ${(summary?.savings_goal ?? 0).toLocaleString()}
      </span>
    </div>
    <div className={styles.savingsBar}>
      <div
        className={styles.savingsBarFill}
        style={{
          width: `${Math.min(summary?.savings_goal_progress ?? 0, 100)}%`,
          background: savingsColor,
        }}
      />
    </div>
    <div className={styles.savingsDetail}>
      ${((summary?.savings_goal ?? 0) - (summary?.savings_ytd ?? 0)).toLocaleString()} remaining to reach goal
    </div>
  </div>
</div>
```

- [ ] **Step 3: Remove KPICard import and component file**

1. Remove `import { KPICard } from '../components/KPICard'` from Dashboard.tsx
2. Delete `web/src/components/KPICard.tsx` (no longer used per spec Section 9)

- [ ] **Step 4: Run TypeScript check**

Run: `cd web && npx tsc -b --noEmit`
Expected: No errors

- [ ] **Step 5: Commit**

```bash
git add web/src/pages/Dashboard.tsx web/src/styles/Dashboard.module.css
git rm web/src/components/KPICard.tsx
git commit -m "feat: replace KPI cards with hero row (Total Balance + Savings Goal)"
```

---

### Task 10: Dashboard — Cash Flow section with bidirectional Recharts bar chart

**Files:**
- Modify: `web/src/pages/Dashboard.tsx`
- Modify: `web/src/styles/Dashboard.module.css`

- [ ] **Step 1: Add Cash Flow styles to Dashboard.module.css**

```css
/* Cash Flow Section */
.cashFlow {
  border: 1px solid var(--border-muted);
  border-radius: var(--radius-lg);
  padding: var(--space-6);
  background: transparent;
  margin-bottom: var(--space-6);
}

.cfTopBar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: var(--space-6);
}

.cfTitle {
  font-size: var(--type-heading-sm-size);
  font-weight: var(--type-heading-sm-weight);
}

.cfTopRight {
  display: flex;
  align-items: center;
  gap: var(--space-6);
}

.cfStatsInline {
  display: flex;
  align-items: center;
  gap: var(--space-5);
}

.cfStatInline {
  display: flex;
  align-items: center;
  gap: var(--space-2);
}

.cfStatDot {
  width: 8px;
  height: 8px;
  border-radius: 2px;
}

.cfStatLabel {
  font-size: var(--type-body-sm-size);
  color: var(--text-secondary);
}

.cfStatVal {
  font-size: var(--type-body-md-size);
  font-weight: 600;
  font-variant-numeric: tabular-nums;
}

.cfToggle {
  display: flex;
  border: 1px solid var(--border-default);
  border-radius: var(--radius-md);
  overflow: hidden;
}

.cfToggleBtn {
  padding: var(--space-1) var(--space-3);
  font-size: var(--type-label-sm-size);
  font-weight: var(--type-label-sm-weight);
  color: var(--text-secondary);
  background: transparent;
  border: none;
  cursor: pointer;
  font-family: inherit;
  transition: color var(--duration-fast) var(--ease-standard),
              background var(--duration-fast) var(--ease-standard);
}

.cfToggleBtnActive {
  background: var(--primary-a15);
  color: var(--color-primary);
}

.cfChartWrap {
  aspect-ratio: 3 / 1;
  min-height: 200px;
}
```

- [ ] **Step 2: Add Cash Flow JSX with Recharts bidirectional bar chart**

In `web/src/pages/Dashboard.tsx`, add the cash flow section after the hero row. Import the chart theme hook and tooltip:

```tsx
import { useChartTheme } from '../hooks/useChartTheme';
import { ChartTooltip } from '../components/ChartTooltip';
import {
  ResponsiveContainer, BarChart, Bar, XAxis, YAxis,
  CartesianGrid, Tooltip, ReferenceLine,
} from 'recharts';
```

Transform trend data for bidirectional bars (monthly and yearly views):

```typescript
const monthNames = ['Jan','Feb','Mar','Apr','May','Jun','Jul','Aug','Sep','Oct','Nov','Dec'];

const monthlyData = trend.map(item => ({
  name: monthNames[item.month - 1],
  Income: item.total_income,
  Expenses: -item.total_spent, // Negative for downward bars
})).reverse(); // Oldest first for left-to-right chronological

// Yearly: aggregate monthly data by year (up to 5 years)
const yearlyData = Object.values(
  trend.reduce<Record<number, { name: string; Income: number; Expenses: number }>>((acc, item) => {
    if (!acc[item.year]) {
      acc[item.year] = { name: String(item.year), Income: 0, Expenses: 0 };
    }
    acc[item.year].Income += item.total_income;
    acc[item.year].Expenses -= item.total_spent;
    return acc;
  }, {})
).slice(-5); // Last 5 years

const chartData = cfView === 'yearly' ? yearlyData : monthlyData;
```

Cash Flow JSX:

```tsx
const [cfView, setCfView] = useState<'monthly' | 'yearly'>('monthly');
const theme = useChartTheme();

<div className={styles.cashFlow}>
  <div className={styles.cfTopBar}>
    <span className={styles.cfTitle}>Cash Flow</span>
    <div className={styles.cfTopRight}>
      <div className={styles.cfStatsInline}>
        <div className={styles.cfStatInline}>
          <div className={styles.cfStatDot} style={{ background: theme.incomeColor }} />
          <span className={styles.cfStatLabel}>Income</span>
          <span className={styles.cfStatVal} style={{ color: theme.incomeColor }}>
            ${(summary?.total_income ?? 0).toLocaleString()}
          </span>
        </div>
        <div className={styles.cfStatInline}>
          <div className={styles.cfStatDot} style={{ background: theme.expenseColor }} />
          <span className={styles.cfStatLabel}>Expenses</span>
          <span className={styles.cfStatVal} style={{ color: theme.expenseColor }}>
            ${(summary?.total_spent ?? 0).toLocaleString()}
          </span>
        </div>
      </div>
      <div className={styles.cfToggle}>
        <button
          className={`${styles.cfToggleBtn}${cfView === 'monthly' ? ` ${styles.cfToggleBtnActive}` : ''}`}
          onClick={() => setCfView('monthly')}
        >Monthly</button>
        <button
          className={`${styles.cfToggleBtn}${cfView === 'yearly' ? ` ${styles.cfToggleBtnActive}` : ''}`}
          onClick={() => setCfView('yearly')}
        >Yearly</button>
      </div>
    </div>
  </div>

  <div className={styles.cfChartWrap}>
    <ResponsiveContainer width="100%" height="100%">
      <BarChart data={chartData} barGap={2}>
        <CartesianGrid
          strokeDasharray="3 3"
          stroke={theme.gridStroke}
          strokeOpacity={0.4}
          vertical={false}
        />
        <XAxis
          dataKey="name"
          tick={{ fill: theme.axisStroke, fontSize: 12, fontFamily: 'Inter Variable' }}
          axisLine={{ stroke: theme.gridStroke }}
          tickLine={false}
        />
        <YAxis
          tick={{ fill: theme.axisStroke, fontSize: 12, fontFamily: 'Inter Variable' }}
          axisLine={false}
          tickLine={false}
          tickFormatter={(v: number) => {
            const abs = Math.abs(v);
            return abs >= 1000 ? `${abs / 1000}k` : String(abs);
          }}
        />
        <ReferenceLine y={0} stroke={theme.gridStroke} />
        <Tooltip
          content={<ChartTooltip />}
          cursor={{ fill: theme.hoverBg }}
        />
        <Bar dataKey="Income" fill={theme.incomeColor} radius={[4, 4, 0, 0]} />
        <Bar dataKey="Expenses" fill={theme.expenseColor} radius={[4, 4, 0, 0]} />
      </BarChart>
    </ResponsiveContainer>
  </div>
</div>
```

- [ ] **Step 3: Remove old chart section from Dashboard.tsx**

Delete the old BarChart and PieChart sections (the ones in the two-column `.chartsRow` layout). They'll be replaced by the cash flow section and the bottom grid in the next task.

- [ ] **Step 4: Run TypeScript check**

Run: `cd web && npx tsc -b --noEmit`
Expected: No errors

- [ ] **Step 5: Commit**

```bash
git add web/src/pages/Dashboard.tsx web/src/styles/Dashboard.module.css
git commit -m "feat: add Cash Flow section with bidirectional Recharts bar chart"
```

---

### Task 11: Dashboard — Bottom grid (Categories donut + Transactions table)

**Files:**
- Modify: `web/src/pages/Dashboard.tsx`
- Modify: `web/src/styles/Dashboard.module.css`

- [ ] **Step 1: Add bottom grid styles to Dashboard.module.css**

```css
/* Bottom Grid */
.bottomGrid {
  display: grid;
  grid-template-columns: 2fr 3fr;
  gap: var(--space-4);
}

/* Card shared */
.card {
  border: 1px solid var(--border-muted);
  border-radius: var(--radius-lg);
  padding: var(--space-6);
  background: transparent;
}

.cardHeader {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: var(--space-5);
}

.cardTitle {
  font-size: var(--type-heading-sm-size);
  font-weight: var(--type-heading-sm-weight);
}

.cardLink {
  font-size: var(--type-body-sm-size);
  color: var(--color-primary);
  text-decoration: none;
  font-weight: 500;
}

/* Categories */
.catLayout {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: var(--space-5);
}

.donutWrap {
  position: relative;
  width: 160px;
  height: 160px;
}

.donutCenter {
  position: absolute;
  top: 50%;
  left: 50%;
  transform: translate(-50%, -50%);
  text-align: center;
}

.donutTotal {
  font-size: var(--type-heading-sm-size);
  font-weight: var(--type-heading-sm-weight);
  color: var(--text-primary);
  font-variant-numeric: tabular-nums;
}

.donutSub {
  font-size: var(--type-label-sm-size);
  color: var(--text-tertiary);
}

.catList {
  width: 100%;
}

.catItem {
  display: flex;
  align-items: center;
  padding: var(--space-2) 0;
  border-top: 1px solid var(--border-muted);
  gap: var(--space-2);
}

.catDot {
  width: 8px;
  height: 8px;
  border-radius: 2px;
  flex-shrink: 0;
}

.catName {
  font-size: var(--type-body-md-size);
  color: var(--text-primary);
  flex: 1;
}

.catAmount {
  font-size: var(--type-body-md-size);
  color: var(--text-secondary);
  font-variant-numeric: tabular-nums;
  font-weight: 500;
}

.catBar {
  width: 48px;
  height: 4px;
  background: var(--border-muted);
  border-radius: var(--radius-sm);
  overflow: hidden;
  flex-shrink: 0;
}

.catBarFill {
  height: 100%;
  border-radius: var(--radius-sm);
}

/* Transactions Table */
.txTable {
  width: 100%;
  border-collapse: collapse;
}

.txTable th {
  font-size: var(--type-label-sm-size);
  font-weight: var(--type-label-sm-weight);
  color: var(--text-tertiary);
  text-align: left;
  padding: 0 var(--space-3) var(--space-3) 0;
  border-bottom: 1px solid var(--border-muted);
  text-transform: uppercase;
  letter-spacing: 0.5px;
}

.txTable th:last-child {
  text-align: right;
  padding-right: 0;
}

.txTable td {
  padding: var(--space-3) var(--space-3) var(--space-3) 0;
  border-bottom: 1px solid var(--border-muted);
  font-size: var(--type-body-md-size);
  vertical-align: middle;
}

.txTable td:last-child {
  padding-right: 0;
}

.txTable tr:last-child td {
  border-bottom: none;
}

.txName {
  font-weight: 500;
  color: var(--text-primary);
}

.txDate {
  color: var(--text-tertiary);
  font-size: var(--type-body-sm-size);
}

.txBadge {
  font-size: var(--type-label-sm-size);
  padding: 2px var(--space-2);
  border-radius: var(--radius-full);
  font-weight: 500;
  display: inline-block;
}

.txAmount {
  text-align: right;
  font-weight: 600;
  font-variant-numeric: tabular-nums;
}

/* Responsive */
@media (max-width: 1200px) {
  .bottomGrid {
    grid-template-columns: 1fr;
  }

  .heroRow {
    grid-template-columns: 1fr 1fr;
  }
}

@media (max-width: 768px) {
  .heroRow {
    grid-template-columns: 1fr;
  }
}
```

- [ ] **Step 2: Add bottom grid JSX — Categories donut**

In `web/src/pages/Dashboard.tsx`, use Recharts `PieChart` for the donut:

```tsx
import { PieChart, Pie, Cell } from 'recharts';

// Transform category data for the donut
const donutData = categories.map(cat => ({
  name: cat.name,
  value: cat.total,
  color: cat.color,
}));

const totalSpent = categories.reduce((sum, c) => sum + c.total, 0);
```

Categories card JSX:

```tsx
<div className={styles.bottomGrid}>
  <div className={styles.card}>
    <div className={styles.cardHeader}>
      <span className={styles.cardTitle}>Categories</span>
      <a href="/categories" className={styles.cardLink}>All →</a>
    </div>
    <div className={styles.catLayout}>
      <div className={styles.donutWrap}>
        <ResponsiveContainer width="100%" height="100%">
          <PieChart>
            <Pie
              data={donutData}
              dataKey="value"
              innerRadius={55}
              outerRadius={70}
              cornerRadius={3}
              paddingAngle={2}
              stroke="none"
            >
              {donutData.map((entry, i) => (
                <Cell key={i} fill={entry.color || theme.categoryColors[i % 6]} />
              ))}
            </Pie>
          </PieChart>
        </ResponsiveContainer>
        <div className={styles.donutCenter}>
          <div className={styles.donutTotal}>${totalSpent.toLocaleString()}</div>
          <div className={styles.donutSub}>total</div>
        </div>
      </div>
      <div className={styles.catList}>
        {categories.slice(0, 6).map(cat => (
          <div key={cat.id} className={styles.catItem}>
            <div className={styles.catDot} style={{ background: cat.color }} />
            <span className={styles.catName}>{cat.name}</span>
            <span className={styles.catAmount}>${cat.total.toLocaleString()}</span>
            <div className={styles.catBar}>
              <div
                className={styles.catBarFill}
                style={{
                  width: `${totalSpent > 0 ? (cat.total / totalSpent) * 100 : 0}%`,
                  background: cat.color,
                }}
              />
            </div>
          </div>
        ))}
      </div>
    </div>
  </div>
```

- [ ] **Step 3: Add bottom grid JSX — Transactions table**

The Dashboard currently shows recent transactions. Restructure to table format:

```tsx
  <div className={styles.card}>
    <div className={styles.cardHeader}>
      <span className={styles.cardTitle}>Recent Transactions</span>
      <a href="/transactions" className={styles.cardLink}>View All →</a>
    </div>
    <table className={styles.txTable}>
      <thead>
        <tr>
          <th>Name</th>
          <th>Date</th>
          <th>Category</th>
          <th>Amount</th>
        </tr>
      </thead>
      <tbody>
        {/* Use first 6 transactions from trend data or add a recent transactions fetch */}
        {recentTransactions.slice(0, 6).map(tx => (
          <tr key={tx.id}>
            <td className={styles.txName}>{tx.description}</td>
            <td className={styles.txDate}>{new Date(tx.date).toLocaleDateString('en-US', { month: 'short', day: 'numeric', year: 'numeric' })}</td>
            <td>
              <span
                className={styles.txBadge}
                style={{
                  background: `color-mix(in srgb, ${tx.category_color} 15%, transparent)`,
                  color: tx.category_color,
                }}
              >
                {tx.category_name}
              </span>
            </td>
            <td className={styles.txAmount} style={{
              color: tx.category_type === 'income' ? 'var(--color-income)' : 'var(--color-expense)',
            }}>
              {tx.category_type === 'income' ? '+' : '-'}${Math.abs(tx.amount).toLocaleString('en-US', { minimumFractionDigits: 2 })}
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  </div>
</div>
```

**Note:** The dashboard may need to fetch recent transactions separately. Check if `useDashboard` returns them or if a separate `api.get('transactions?per_page=6')` call is needed. Add to the `useDashboard` hook if necessary.

- [ ] **Step 4: Remove old recent transactions section**

Delete the old card-list style recent transactions section from Dashboard.tsx. The new table format replaces it.

- [ ] **Step 5: Add loading skeletons and error/empty states**

Add skeleton styles to Dashboard.module.css:

```css
/* Skeleton */
@keyframes skeletonPulse {
  0%, 100% { opacity: 0.4; }
  50% { opacity: 0.8; }
}

.skeleton {
  background: var(--border-muted);
  border-radius: var(--radius-sm);
  animation: skeletonPulse 1.5s ease-in-out infinite;
}

.skeletonText {
  height: 14px;
  margin-bottom: var(--space-2);
}

.skeletonHeading {
  height: 36px;
  width: 200px;
  margin-bottom: var(--space-2);
}

.emptyState {
  text-align: center;
  padding: var(--space-8);
  color: var(--text-tertiary);
  font-size: var(--type-body-md-size);
}

.errorState {
  text-align: center;
  padding: var(--space-6);
  color: var(--text-tertiary);
  font-size: var(--type-body-md-size);
}

.retryButton {
  margin-top: var(--space-3);
  padding: var(--space-2) var(--space-6);
  border: 1px solid var(--color-primary);
  border-radius: var(--radius-full);
  background: transparent;
  color: var(--color-primary);
  font-family: inherit;
  font-size: var(--type-label-lg-size);
  font-weight: var(--type-label-lg-weight);
  cursor: pointer;
}
```

In Dashboard.tsx, wrap the dashboard content with loading/error checks:

```tsx
if (loading) {
  return (
    <div className={styles.page}>
      {/* Header */}
      <div className={styles.heroRow}>
        <div className={styles.heroCard}>
          <div className={`${styles.skeleton} ${styles.skeletonText}`} style={{ width: '100px' }} />
          <div className={`${styles.skeleton} ${styles.skeletonHeading}`} />
          <div className={`${styles.skeleton} ${styles.skeletonText}`} style={{ width: '150px' }} />
        </div>
        <div className={styles.heroCard}>
          <div className={`${styles.skeleton} ${styles.skeletonText}`} style={{ width: '100px' }} />
          <div className={`${styles.skeleton} ${styles.skeletonHeading}`} />
          <div className={`${styles.skeleton} ${styles.skeletonText}`} style={{ width: '150px' }} />
        </div>
      </div>
      <div className={styles.cashFlow}>
        <div className={`${styles.skeleton}`} style={{ aspectRatio: '3/1' }} />
      </div>
      <div className={styles.bottomGrid}>
        <div className={styles.card}>
          <div className={`${styles.skeleton}`} style={{ width: '160px', height: '160px', borderRadius: '50%', margin: '0 auto' }} />
        </div>
        <div className={styles.card}>
          {Array.from({ length: 6 }).map((_, i) => (
            <div key={i} className={`${styles.skeleton} ${styles.skeletonText}`} style={{ width: '100%', marginBottom: 'var(--space-3)' }} />
          ))}
        </div>
      </div>
    </div>
  );
}

if (error) {
  return (
    <div className={styles.page}>
      <div className={styles.errorState}>
        <p>Failed to load dashboard data</p>
        <button className={styles.retryButton} onClick={() => window.location.reload()}>
          Retry
        </button>
      </div>
    </div>
  );
}
```

- [ ] **Step 6: Run TypeScript check and tests**

Run: `cd web && npx tsc -b --noEmit && npx vitest run src/pages/Dashboard.test.tsx`
Expected: No TS errors. Tests may need updates for new DOM structure — update selectors as needed.

- [ ] **Step 7: Commit**

```bash
git add web/src/pages/Dashboard.tsx web/src/styles/Dashboard.module.css
git commit -m "feat: add bottom grid (categories donut + transactions table) and loading states"
```

---

## Chunk 5: Reports Theming + Cleanup

### Task 12: Apply chart theme to Reports page

**Files:**
- Modify: `web/src/pages/Reports.tsx`

- [ ] **Step 1: Import and apply useChartTheme to Reports**

In `web/src/pages/Reports.tsx`:

1. Add imports:
```tsx
import { useChartTheme } from '../hooks/useChartTheme';
import { ChartTooltip } from '../components/ChartTooltip';
```

2. Inside the component: `const theme = useChartTheme();`

3. Update all `<XAxis>` instances:
```tsx
<XAxis tick={{ fill: theme.axisStroke, fontSize: 12, fontFamily: 'Inter Variable' }} axisLine={{ stroke: theme.gridStroke }} tickLine={false} />
```

4. Update all `<YAxis>` instances:
```tsx
<YAxis tick={{ fill: theme.axisStroke, fontSize: 12, fontFamily: 'Inter Variable' }} axisLine={false} tickLine={false} />
```

5. Update all `<CartesianGrid>` instances:
```tsx
<CartesianGrid strokeDasharray="3 3" stroke={theme.gridStroke} strokeOpacity={0.4} vertical={false} />
```

6. Update all `<Tooltip>` instances:
```tsx
<Tooltip content={<ChartTooltip />} cursor={{ fill: theme.hoverBg }} />
```

7. Update `<Bar>` instances to use `radius={[4, 4, 0, 0]}`.

- [ ] **Step 2: Update Reports.module.css card styles**

Ensure `.section` in Reports.module.css uses border-only pattern (should already be done from Task 7).

- [ ] **Step 3: Run TypeScript check**

Run: `cd web && npx tsc -b --noEmit`
Expected: No errors

- [ ] **Step 4: Commit**

```bash
git add web/src/pages/Reports.tsx
git commit -m "feat: apply dark chart theme to Reports page"
```

---

### Task 13: Update Dashboard tests for new structure

**Files:**
- Modify: `web/src/pages/Dashboard.test.tsx`

- [ ] **Step 1: Update Dashboard test mocks and assertions**

The Dashboard DOM structure has changed significantly. Update the test file:

1. Remove `KPICard` mock/assertions
2. Add assertions for new elements: "Total Balance", "Savings Goal", "Cash Flow", "Categories", "Recent Transactions"
3. Mock `useChartTheme` to return test values
4. Keep Recharts mocks (they return simple divs)

```typescript
vi.mock('../hooks/useChartTheme', () => ({
  useChartTheme: () => ({
    axisStroke: '#58585F',
    gridStroke: '#1E1E23',
    tooltipBg: '#1E1E23',
    tooltipBorder: '#2A2A30',
    tooltipText: '#F5F5F6',
    hoverBg: 'rgba(129,140,248,0.08)',
    incomeColor: '#7EC89B',
    expenseColor: '#E88B9C',
    categoryColors: ['#818CF8', '#7EC89B', '#E88B9C', '#E8A87C', '#7CAFD4', '#58585F'],
  }),
}));
```

Update test assertions:
```typescript
test('renders hero row with Total Balance', async () => {
  // ... render Dashboard with mocked data
  expect(screen.getByText('Total Balance')).toBeInTheDocument();
  expect(screen.getByText('Savings Goal')).toBeInTheDocument();
});

test('renders Cash Flow section', () => {
  expect(screen.getByText('Cash Flow')).toBeInTheDocument();
});

test('renders Categories and Recent Transactions', () => {
  expect(screen.getByText('Categories')).toBeInTheDocument();
  expect(screen.getByText('Recent Transactions')).toBeInTheDocument();
});
```

- [ ] **Step 2: Run all tests**

Run: `cd web && npx vitest run`
Expected: ALL PASS

- [ ] **Step 3: Commit**

```bash
git add web/src/pages/Dashboard.test.tsx
git commit -m "test: update Dashboard tests for new hero + cash flow + grid layout"
```

---

### Task 14: Update documentation

**Files:**
- Modify: `docs/DESIGN_GUIDE.md`
- Modify: `README.md`

- [ ] **Step 1: Update DESIGN_GUIDE.md**

Add/update these sections in `docs/DESIGN_GUIDE.md`:

1. **Card Pattern** — Document the border-only card pattern:
   - `background: transparent; border: 1px solid var(--border-muted); border-radius: var(--radius-lg);`
   - Note: supersedes previous surface-raised card pattern

2. **Tab Pattern** — Document the overlapping underline tab:
   - `margin-bottom: -1px` technique
   - Reference the shared `<Tabs>` component

3. **Chart Theming** — Document `useChartTheme()` hook:
   - Available properties
   - Usage with Recharts components
   - `<ChartTooltip>` component

4. **Sidebar** — Update to document toggle pin behavior (no hover)

- [ ] **Step 2: Update README.md**

Update the features section if it references old UI patterns. Add mention of:
- Toggle sidebar with localStorage persistence
- Max-width 1400px layout
- Dark-themed charts

- [ ] **Step 3: Commit**

```bash
git add docs/DESIGN_GUIDE.md README.md
git commit -m "docs: update DESIGN_GUIDE and README with new UI patterns"
```

---

### Task 15: Final TypeScript and lint check

- [ ] **Step 1: Run full TypeScript check**

Run: `cd web && npx tsc -b --noEmit`
Expected: No errors

- [ ] **Step 2: Run full test suite**

Run: `cd web && npx vitest run`
Expected: ALL PASS

- [ ] **Step 3: Run stylelint**

Run: `cd web && npx stylelint "src/**/*.css"`
Expected: No errors (all styles use tokens, no raw hex)

- [ ] **Step 4: Test Docker build**

Run: `docker compose build`
Expected: Build succeeds

- [ ] **Step 5: Final commit if any remaining changes**

```bash
git status
# If any uncommitted fixes, stage specific files:
git add <changed-files>
git commit -m "fix: address lint and build issues from UI/UX overhaul"
```
