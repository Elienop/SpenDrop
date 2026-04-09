# SpenDrop v2 — shadcn + Tailwind Rewrite

**Status:** Design
**Date:** 2026-04-09
**Author:** Elienop + Claude (brainstorm session)
**Branch target:** `feat/design-system-v3` (new branch, big-bang migration)

## Summary

SpenDrop's current frontend uses CSS Modules with a 50+ token `tokens.css`, stylelint-enforced token-only colors, Inter font, purple accent (`#5347CE`), and glass/blur card surfaces. It works, but the visual identity is bespoke, maintenance cost is high, and the design is hard to evolve without touching dozens of module files.

This document specifies a **full rewrite of the frontend styling layer** to use Tailwind CSS + [shadcn/ui](https://ui.shadcn.com) primitives. The data layer, API hooks, routing, and Go backend are **unchanged**. Only the render layer is replaced.

The redesign follows a **monochrome chrome + colorful data** pattern used by Monarch, Copilot, Linear, and Vercel: the app shell, buttons, cards, and typography are near-colorless, and all chromatic weight lives in charts and category indicators. The primary accent shifts from purple to shadcn's near-white default. Numbers are rendered in Geist Mono with tabular figures. The theme is dark-only.

This rewrite also elevates the **transaction entry row** — SpenDrop's spreadsheet-like keyboard flow — to a first-class concern. It is the primary app loop and must be easier and faster to use after the rewrite, not just prettier.

## Goals

1. Replace every `*.module.css` file with Tailwind utilities and shadcn primitives.
2. Adopt shadcn/ui component library (~24 primitives) as the canonical UI vocabulary.
3. Unify typography on Geist Sans + Geist Mono with tabular figures for all numbers.
4. Delete the 11-color per-category `cat-*` token scale and replace with an 11-slot chart palette assigned by `category.id % 11`. Remove per-category color customization.
5. Drop light mode entirely — dark-only.
6. Delete glass/blur surfaces, SVG chart patterns, and all theming hooks.
7. Rewrite the transaction entry row so that keyboard-first data entry is faster and more robust than it is today, using react-hook-form + shadcn `Command` for category selection.
8. Preserve all existing features and API contracts.

## Non-goals

- Changing the Go backend, SQL schema (except dropping `categories.color`), or API shapes beyond what this rewrite strictly requires.
- Mobile-first rework or a dedicated mobile nav drawer (responsive grids only).
- Light mode (deliberately dropped).
- Multi-locale / i18n.
- Storybook or visual regression tooling.
- Animation polish beyond shadcn's built-in transitions.
- Per-category color customization (dropped).
- Global `⌘K` launcher (only the `command` primitive for category selection).
- Chart pattern fills for colorblind mode.

## Decisions record

Locked in during brainstorming, 2026-04-09.

| Decision | Choice | Alternatives considered |
|---|---|---|
| Styling system | Tailwind CSS + shadcn/ui copy-paste primitives | Keep CSS Modules; Mantine; Chakra; Park UI |
| Card surface | Flat (`bg-card` + hairline border) | Glass with `backdrop-filter: blur(12px)` |
| Charts | Recharts via shadcn `chart` primitive wrappers | Tremor (too opinionated); raw Recharts with custom theme |
| Data palette | Hybrid — monochrome chrome + 11 muted Radix hues for data | Pure monochrome (unreadable at 11 categories); full Radix everywhere (too colorful) |
| Primary accent | shadcn near-white default (`hsl(0 0% 98%)`) | Keep SpenDrop purple `#5347CE`; pick a new single Radix accent |
| Typography | Geist Sans (body) + Geist Mono (numbers) | Inter everywhere; Geist Sans everywhere without mono |
| Theme scope | Dark-only | Dark + light; dark-only-for-now-with-light-ready |
| Migration strategy | Big-bang single branch | Slice-by-page ship-as-you-go; foundation PR + slices |
| Category color source | CSS-driven via `--chart-N` slot from `category.id % 11` | Database `categories.color` column (preserved); expanded 20-slot palette |
| Category color picker | **Removed** — `categories.color` column **dropped** in migration | Leave column, ignore in UI |
| Entry row integration | react-hook-form + shadcn `Form` + `Command` (primary app goal) | Keep uncontrolled fallback |
| Cash flow bar colors | Green + crimson (convention) | Teal + crimson (WCAG-safe but unconventional for finance) |

## Architecture

### High-level

```
web/src/
├── lib/
│   ├── utils.ts             (cn() helper — Tailwind class merging)
│   └── chart-colors.ts      (getCategoryColorVar — single source of truth for category→slot mapping)
├── components/
│   ├── ui/                  (shadcn primitives, copy-pasted from CLI)
│   │   ├── button.tsx
│   │   ├── card.tsx
│   │   ├── input.tsx
│   │   ├── ... (~24 files)
│   ├── AppShell.tsx         (replaces AppLayout)
│   ├── Sidebar.tsx          (rewritten)
│   ├── KpiCard.tsx          (new)
│   ├── ChartCard.tsx        (new)
│   ├── CategoryBadge.tsx    (new)
│   ├── TransactionTable.tsx (rewritten)
│   ├── TransactionToolbar.tsx (rewritten)
│   ├── FilterPanel.tsx      (rewritten)
│   ├── TransactionEntryRow.tsx (rewritten — see §5)
│   ├── CategoryEditor.tsx   (rewritten)
│   └── DateRangePicker.tsx  (new)
├── pages/
│   ├── Auth/
│   ├── Dashboard.tsx
│   ├── Transactions.tsx
│   ├── Categories.tsx
│   ├── Reports.tsx
│   └── Settings.tsx
├── styles/
│   └── globals.css          (Tailwind directives + CSS var layer; replaces tokens.css + global.css)
└── main.tsx                 (imports geist/font/sans + geist/font/mono)
```

**Deleted wholesale:**
- `src/styles/tokens.css`
- `src/styles/global.css`
- All `*.module.css` files (under `src/styles/` and `src/pages/`)
- `src/hooks/useChartTheme.ts`
- `src/hooks/useChartPatterns.ts`
- `src/hooks/useTheme.ts`
- `src/components/ThemeProvider.tsx`
- `src/components/ChartTooltip.tsx`
- `src/components/Tabs.tsx` (replaced by `ui/tabs`)
- `.stylelintrc.json` and all stylelint configuration

### 1 — Foundation

#### 1.1 Package changes

**Add:**
- `tailwindcss` + `postcss` + `autoprefixer` + `tailwindcss-animate`
- `class-variance-authority`, `clsx`, `tailwind-merge`
- `geist` (Vercel's font package; ships Geist Sans and Geist Mono as Vite-compatible imports)
- `react-hook-form`, `@hookform/resolvers`, `zod` (pulled in by shadcn `form`)
- `cmdk` (pulled in by shadcn `command`)
- `sonner` (toasts; pulled in by shadcn `sonner`)
- `@radix-ui/*` packages (pulled in by individual shadcn primitives)
- `prettier-plugin-tailwindcss` (dev — sorts utility classes)
- `eslint-plugin-tailwindcss` (dev — catches class typos)

**Remove:**
- `@fontsource-variable/inter`
- `stylelint`, `stylelint-config-standard`, `stylelint-config-css-modules`, any stylelint plugins

**Keep:**
- `recharts`, `date-fns`, `react-router-dom`, `vite`, `vitest`, `@testing-library/*`, `lucide-react`

#### 1.2 Tailwind + shadcn init

```bash
cd web
npm install tailwindcss postcss autoprefixer tailwindcss-animate class-variance-authority clsx tailwind-merge geist
npx tailwindcss init -p
npx shadcn@latest init
# Style: default | Base color: neutral | CSS variables: yes
```

This generates `tailwind.config.ts`, `postcss.config.js`, `components.json`, and `src/lib/utils.ts` (containing the `cn()` helper).

#### 1.3 Design tokens in `globals.css`

Dark-only, so the `:root` block is the only scope. Values below are HSL triples (Tailwind's convention) with hex comments for reference.

```css
@tailwind base;
@tailwind components;
@tailwind utilities;

@layer base {
  :root {
    /* Neutrals — 12-step Radix-inspired, cool undertone (same as current tokens.css) */
    --background:       240 3.7% 7.1%;    /* #111113 */
    --foreground:       240 5% 93.3%;     /* #EEEEF0 */
    --card:             240 4% 9%;        /* #18181B */
    --card-foreground:  240 5% 93.3%;
    --popover:          240 4% 10%;
    --popover-foreground: 240 5% 93.3%;
    --muted:            240 4% 14%;
    --muted-foreground: 240 4% 57%;       /* #8E8E95 */
    --border:           240 4% 18%;
    --input:            240 4% 18%;
    --ring:             0 0% 98%;

    /* Primary is near-white (shadcn default) */
    --primary:              0 0% 98%;
    --primary-foreground:   240 6% 10%;

    /* Secondary + accent subtle (dark muted) */
    --secondary:            240 4% 14%;
    --secondary-foreground: 240 5% 93%;
    --accent:               240 4% 14%;
    --accent-foreground:    240 5% 93%;

    /* Destructive — used for errors and expense delta pills */
    --destructive:            0 72% 51%;
    --destructive-foreground: 0 0% 98%;

    /* Chart palette — 11 muted Radix hues for categories */
    --chart-1:  263 55% 53%;   /* violet  */
    --chart-2:  226 70% 56%;   /* indigo  */
    --chart-3:  206 100% 39%;  /* blue    */
    --chart-4:  189 94% 39%;   /* cyan    */
    --chart-5:  173 80% 36%;   /* teal    */
    --chart-6:  142 53% 42%;   /* green   */
    --chart-7:  88 60% 45%;    /* lime    */
    --chart-8:  48 96% 53%;    /* amber   */
    --chart-9:  25 95% 53%;    /* orange  */
    --chart-10: 346 77% 50%;   /* crimson */
    --chart-11: 286 68% 55%;   /* orchid  */

    --radius: 0.625rem;
  }
}
```

`tailwind.config.ts` references these via `hsl(var(--background))` etc., following the shadcn convention. The full Tailwind config is generated by `shadcn init` — no manual mapping needed beyond adding the chart-N colors.

#### 1.4 Fonts

```ts
// main.tsx
import 'geist/font/sans';
import 'geist/font/mono';
```

Tailwind config references the CSS variables the Geist package installs:

```ts
// tailwind.config.ts
theme: {
  extend: {
    fontFamily: {
      sans: ['var(--font-geist-sans)', 'system-ui', 'sans-serif'],
      mono: ['var(--font-geist-mono)', 'ui-monospace', 'monospace'],
    },
  },
}
```

Any element rendering a number uses `className="font-mono tabular-nums"`. This applies to KPI values, transaction amounts, chart axis labels, table date columns, and percentages.

#### 1.5 Path aliases

Shadcn expects `@/*` to resolve to `src/*`. Confirm `vite.config.ts` and `tsconfig.json` both declare this alias (they should already — if not, add them during the foundation commit).

### 2 — Component layer

#### 2.1 shadcn primitives (install once)

All installed in a single command during the shadcn-primitives commit:

```bash
npx shadcn@latest add button card input label select textarea checkbox form \
  dialog sheet dropdown-menu popover command tabs table badge separator \
  skeleton tooltip sonner scroll-area switch calendar chart
```

24 primitives, each copied into `src/components/ui/`.

#### 2.2 App components (built on primitives)

| Component | Built from | Purpose |
|---|---|---|
| `AppShell` | bare Tailwind | Top-level layout (sidebar + main content column) |
| `Sidebar` | `ScrollArea` + `Tooltip` + Lucide icons | Nav + collapse toggle + user footer + logout |
| `KpiCard` | `Card` | Dashboard hero tiles: label + mono value + delta |
| `ChartCard` | `Card` + shadcn `chart` | Wraps a chart with title, period toggle, loading `Skeleton` |
| `CategoryBadge` | `Badge` | Dot + name, auto-colored via `getCategoryColorVar()` |
| `TransactionTable` | `Table` + `DropdownMenu` + `Checkbox` | Dense rows, mono amounts, bulk select, row actions |
| `TransactionToolbar` | `Button` + `Popover` + `Badge` | Rewritten with shadcn primitives; same UX as current toolbar |
| `FilterPanel` | `Sheet` + `Tabs` + `Checkbox` + `Calendar` | Filter panel moved into right-side `Sheet` |
| `TransactionEntryRow` | `Form` + `Input` + `Select` + `Command` + `Calendar` | See §5 — the spreadsheet-like entry row, elevated to first-class |
| `CategoryEditor` | `Sheet` + `Form` + `Input` + `Select` | Category CRUD without color picker |
| `DateRangePicker` | `Popover` + `Calendar` | Reusable date range input |

#### 2.3 Category color mapping

`src/lib/chart-colors.ts`:

```ts
// Single source of truth: category → chart palette slot.
// Stable: a category's color never changes once created.
// Collisions only occur beyond 11 categories in a single chart, which is
// intentionally unsupported (consolidate categories if you hit this).
export function getCategoryColorVar(category: { id: number }): string {
  const slot = ((category.id - 1) % 11) + 1;
  return `hsl(var(--chart-${slot}))`;
}
```

`CategoryBadge`, `TransactionTable` category cells, and every chart that needs per-category colors go through this helper. Changing the palette is a one-file edit in `globals.css`.

### 3 — Page rewrites

All pages are rewritten; data fetching hooks (`useTransactions`, `useCategories`, `useAuth`, etc.) stay unchanged.

**Auth** — Centered `Card` with `Form` + `Input` + `Label` + `Button`. No hero.

**Dashboard** — Three zones stacked vertically:
1. Hero KPI row — 4 `KpiCard`s (`grid-cols-4` → `grid-cols-2` md → `grid-cols-1` sm). Label uppercase `text-muted-foreground text-xs`; value `font-mono text-3xl`; delta `font-mono tabular-nums` in green/red.
2. Cash flow chart — Full-width `ChartCard` with Recharts `BarChart`, 12 months. Income bars `--chart-6` (green), expense bars `--chart-10` (crimson). Period toggle (6M/12M) in the card header as `Tabs`.
3. Bottom grid — two columns: Category breakdown (`ChartCard` with donut + `CategoryBadge` legend); Recent transactions (`Card` with compact `TransactionTable`, last 5 rows, "View all →" link).
4. Savings progress card (below grid) — `ChartCard` with mini donut + YTD stats.

Page title `h1` is "Welcome back, {display_name}" in `text-2xl font-semibold tracking-tight`; month/year selectors become shadcn `Select`s on the right.

**Transactions** — `TransactionToolbar` + `FilterPanel` (right `Sheet`) + `TransactionTable` + inline entry row. Row actions in `DropdownMenu`. Category cells use `CategoryBadge`. Bulk select uses `Checkbox`. Bulk categorize uses `Dialog`. Amount column `font-mono tabular-nums`. Row hover `hover:bg-muted/50`.

**Categories** — Single `Card` with `Table` listing name, type (`Badge`), transaction count, kebab actions. "Add category" opens `Sheet` with `Form` + `Input` + `Select` (no color picker).

**Reports** — Period `Tabs` (This Month / Last Month / YTD / Custom — Custom triggers `DateRangePicker`). Grid of `ChartCard`s + drill-down `TransactionTable`.

**Settings** — Vertical `Tabs` (Profile / Household / Savings / Data) on md+, horizontal on sm. Each tab is a `Form` with appropriate primitives. Data tab uses `Button` + `Dialog` for export/import confirmation.

**AppShell / Sidebar** — Structurally identical to today. Sidebar: fixed 240px / 64px collapsed, toggle persists to `localStorage`, nav groups, user footer (name + username + initial), logout as a nav item. Styling moves to Tailwind + shadcn `ScrollArea` + `Tooltip` for collapsed-state labels. No top header bar (matches current layout).

### 4 — Charts + palette wiring

#### 4.1 shadcn chart anatomy

`npx shadcn add chart` installs a `ChartContainer` wrapper around Recharts with:
- Dark-theme tooltip styling via `--popover` and `--border`
- CSS-var-driven color injection via a `ChartConfig` object
- Theme-reactive axes

Minimal usage:

```tsx
import {
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
  type ChartConfig,
} from '@/components/ui/chart';
import { Bar, BarChart, XAxis, YAxis } from 'recharts';

const chartConfig = {
  income:  { label: 'Income',  color: 'hsl(var(--chart-6))' },
  expense: { label: 'Expense', color: 'hsl(var(--chart-10))' },
} satisfies ChartConfig;

<ChartContainer config={chartConfig} className="h-[280px] w-full">
  <BarChart data={months}>
    <XAxis dataKey="month" />
    <YAxis />
    <ChartTooltip content={<ChartTooltipContent />} />
    <Bar dataKey="income"  fill="var(--color-income)"  radius={4} />
    <Bar dataKey="expense" fill="var(--color-expense)" radius={4} />
  </BarChart>
</ChartContainer>
```

`ChartContainer` scopes `--color-income` and `--color-expense` derived from `chartConfig`, which Recharts' `fill` prop reads at paint time.

#### 4.2 Dynamic category palette

Charts where color count depends on category count (category donut, stacked area) generate `chartConfig` at render time via `getCategoryColorVar()`:

```tsx
const chartConfig = categories.reduce<ChartConfig>((acc, cat) => {
  acc[cat.name] = { label: cat.name, color: getCategoryColorVar(cat) };
  return acc;
}, {});
```

#### 4.3 Per-chart treatments

| Chart | Primitive | Palette |
|---|---|---|
| Cash flow (12M bars) | `BarChart` | Two colors: `--chart-6` (income, green) + `--chart-10` (expense, crimson). Solid fills, no patterns. |
| Category donut (spending) | `PieChart` + `Pie` | Per-slice color from `getCategoryColorVar(category)`. 2px gap between slices. |
| Savings donut | `PieChart` + `Pie` | Two-segment: filled uses `--chart-1` (violet), unfilled uses `--muted`. |
| Reports stacked area | `AreaChart` | Stacked by category via `getCategoryColorVar`. Opacity 0.85 on fills. |

#### 4.4 Axes, grids, tooltips

- Axis lines: `stroke="hsl(var(--border))"`
- Grid lines: `stroke="hsl(var(--border))"`, `strokeOpacity={0.4}`, `strokeDasharray="3 3"`
- Axis labels: `fontSize={11}`, `fill="hsl(var(--muted-foreground))"`, class `font-mono tabular-nums` via `className`
- Tooltip body: handled by shadcn `ChartTooltipContent` (reads `--popover`, `--border`)
- Tooltip values: formatted via `Intl.NumberFormat`, rendered `font-mono tabular-nums`
- Hover cursor: `fill="hsl(var(--muted))"`, `fillOpacity={0.3}`

#### 4.5 Legends

- Cash flow: shadcn `ChartLegend` (built-in, two entries)
- Category donut: **custom legend** — column of `CategoryBadge` + percentage (`font-mono text-xs`). Rendered beside the donut.
- Savings donut: no legend, percentage centered inside the donut

#### 4.6 Deleted

- `src/hooks/useChartPatterns.ts`
- `src/hooks/useChartTheme.ts`
- `src/components/ChartTooltip.tsx`
- All `<pattern>` / `<defs>` blocks in current chart code
- `patternStyles` prop on the old `ChartTooltip`

### 5 — Transaction entry row (primary app goal)

SpenDrop's spreadsheet-like keyboard flow is the main data-entry loop. The rewrite must make this flow faster and more robust, not just prettier. This section specifies exactly how.

#### 5.1 Current behavior (preserved)

- A persistent empty row is always visible at the top of the table.
- User types amount → Tab/Enter to move to next field (date → description → category).
- On Enter in the final field (or via a save button), the row is persisted and a new empty row appears with the amount input focused.
- Validation failures (missing amount, bad date) show inline and don't clear the row.

#### 5.2 Target behavior (improvements)

1. **Fuzzy category picker** — replace the current `<select>` with shadcn `Command` (built on `cmdk`). User types a few letters; matching categories appear; Enter selects. Massively faster than scrolling a select with 14+ entries.
2. **Smart date default** — the date field defaults to the most recently used date (not always "today"), so entering a batch of transactions from yesterday doesn't require repeatedly fixing the date. A small "today" quick-button in the `Popover` calendar header snaps back to today.
3. **Amount field heuristics** — detects minus sign for explicit expense entry; otherwise uses the selected category's type to determine sign. Shift+Enter to force-swap sign.
4. **Undo last save** — after saving, show a Sonner toast "Saved. Undo (⌘Z)" for 4 seconds. ⌘Z deletes the just-saved transaction and restores the row data.
5. **Keyboard-only flow** — no field should require the mouse. Tab / Shift+Tab cycles; Enter commits; Escape cancels edit; ⌘Enter saves immediately from any field.

#### 5.3 Implementation with react-hook-form + shadcn

Schema (Zod):

```ts
const entrySchema = z.object({
  amount: z.coerce.number().refine((n) => n !== 0, 'Amount required'),
  date: z.string().regex(/^\d{4}-\d{2}-\d{2}$/),
  description: z.string().max(200),
  category_id: z.number().int().positive('Category required'),
});
type EntryForm = z.infer<typeof entrySchema>;
```

Form component skeleton:

```tsx
const form = useForm<EntryForm>({
  resolver: zodResolver(entrySchema),
  defaultValues: { amount: 0, date: lastUsedDate, description: '', category_id: 0 },
});

const onSubmit = async (values: EntryForm) => {
  const saved = await createTransaction(values);
  toast.success('Saved', {
    action: {
      label: 'Undo (⌘Z)',
      onClick: () => deleteTransaction(saved.id),
    },
  });
  form.reset({ amount: 0, date: values.date, description: '', category_id: 0 });
  amountRef.current?.focus();
};
```

Fields use shadcn `Form` wrappers (`FormField`, `FormItem`, `FormControl`, `FormMessage`), which integrate with RHF's `register`/`control` while preserving accessibility (label association, aria-describedby for errors).

The **category field** uses a custom `FormField` that renders a `Popover` with `Command` inside:

```tsx
<FormField
  name="category_id"
  control={form.control}
  render={({ field }) => (
    <FormItem>
      <Popover>
        <PopoverTrigger asChild>
          <Button variant="ghost" className="w-full justify-start font-normal">
            {categoryNameById(field.value) ?? 'Select category'}
          </Button>
        </PopoverTrigger>
        <PopoverContent className="p-0" align="start">
          <Command>
            <CommandInput placeholder="Search category..." />
            <CommandList>
              {categories.map((cat) => (
                <CommandItem
                  key={cat.id}
                  onSelect={() => field.onChange(cat.id)}
                >
                  <CategoryBadge category={cat} /> {cat.name}
                </CommandItem>
              ))}
            </CommandList>
          </Command>
        </PopoverContent>
      </Popover>
      <FormMessage />
    </FormItem>
  )}
/>
```

#### 5.4 Keyboard handling

A single `onKeyDown` handler on the form root manages:
- `Enter` on any field → focus next field
- `Enter` on last field (or `⌘Enter` anywhere) → submit
- `Escape` → reset form to empty defaults (with confirmation only if dirty)
- `Tab` / `Shift+Tab` → browser default (relies on natural DOM order)
- `Shift+Enter` → toggle amount sign (expense ↔ income)
- `⌘Z` after save → restore from last-saved buffer, delete the saved transaction, refocus amount

The flow is implemented as a plain `useCallback` listener on the form element. No custom focus management beyond a ref on the amount input for post-save focus.

#### 5.5 Why RHF is worth the integration cost

- Form state, validation, and error surfacing all unified in one API
- `formState.isDirty` powers the Escape confirmation
- `form.reset({ ...preserveDate })` is the cleanest way to reset-with-preserved-values after each save
- shadcn `Form` handles ARIA wiring for free (no manual `aria-describedby`)
- Zod schema is reusable on the category edit form for consistent validation

#### 5.6 Testing

Dedicated test file `TransactionEntryRow.test.tsx` using `userEvent`:
- Full save flow: type amount → Enter → type description → Enter → select category → Enter → verify API call + row reset
- Undo flow: save → ⌘Z → verify delete API call + form restored
- Validation: submit empty → verify error message on amount field
- Category search: type "gr" → verify "Groceries" is selected highlight
- Sign toggle: Shift+Enter → verify amount sign flip

This test file is written **before** the implementation (TDD) — it serves as the executable spec for the entry row.

### 6 — Testing, linting, CI

#### 6.1 Test impact

| Test class | Status |
|---|---|
| Backend Go tests | Unaffected |
| Hook tests (`useTransactions`, etc.) | Unaffected |
| Component tests querying `styles.foo` | All rewrite required |
| Component tests using `getByRole`, `getByText`, `getByLabelText` | Unaffected |
| Snapshot tests | All regenerate (none currently exist to delete) |

Current files requiring rewrite:
- `web/src/pages/Dashboard.test.tsx`
- `web/src/pages/Transactions.test.tsx`
- `web/src/App.test.tsx`
- Any `*.test.tsx` under `src/components/`

Selection strategy: prefer role/text/label queries. Fall back to `data-testid` for unlabeled icon buttons. Never query by class name — Tailwind classes are not stable identifiers.

New tests to add (beyond entry row covered above):
- `FilterPanel` open/close + applied-filters chip state
- `TransactionToolbar` filter count badge + row action menus

#### 6.2 Stylelint removal

- Delete `.stylelintrc.json` (or wherever it lives in the `web/` tree)
- Remove stylelint-related scripts from `web/package.json`
- Remove stylelint dev deps
- Remove stylelint CI step if it exists (currently no stylelint step in `.github/workflows/pr.yml`, so nothing to remove in CI)

Replacement: none. Tailwind's utility classes structurally prevent the problems stylelint was enforcing (no raw hex, no arbitrary tokens).

#### 6.3 ESLint + Prettier

- Keep existing ESLint config
- Add `prettier-plugin-tailwindcss` — sorts utility classes deterministically, zero config
- Add `eslint-plugin-tailwindcss` — catches class typos and validates class names
- Add a `web/package.json` script: `"lint": "eslint ."` (if not present)
- Add a CI step in `pr.yml` frontend job: `npx eslint .`

#### 6.4 CI workflow changes

[.github/workflows/pr.yml](.github/workflows/pr.yml):
- **Backend job** — unchanged
- **Frontend job** — add `Lint` step (`npx eslint .`) before `Type check`
- **Docker job** — unchanged

### 7 — Database migration

`internal/database/migrations/004_drop_categories_color.sql`:

```sql
-- Drop categories.color column. Category colors are now derived from
-- the chart palette slot (category.id % 11) at render time.
ALTER TABLE categories DROP COLUMN color;
```

**Impact check:**
- Sqlc queries that `SELECT * FROM categories` auto-regenerate without the column
- Sqlc queries that explicitly reference `color` must be updated (grep for `color` in `internal/database/queries/`)
- The `/api/categories` response shape changes: `color` field disappears
- `web/src/api/types.ts` `Category` interface loses the `color` field
- Any Go tests asserting on `color` need updating (already confirmed the only one is `TestHandleListTransactions_IncludesCategoryInfo`, which also asserts `category_color` through the join — needs to drop that assertion)

SQLite's `ALTER TABLE DROP COLUMN` requires SQLite 3.35+ (March 2021); well within supported versions.

### 8 — Migration order (commits in order)

Each step lands as its own commit with the build green at that commit. Frontend tests will be partially broken during steps 3-10 — they are rewritten alongside the page they cover.

1. **Foundation** — Tailwind + PostCSS config, shadcn init, `components.json`, `lib/utils.ts`, `globals.css` with token vars, Geist imports in `main.tsx`, path alias verification. App renders with old CSS Modules unchanged. No visible change.
2. **Install shadcn primitives** — one `shadcn add` command producing ~24 files under `src/components/ui/`. Tests unchanged.
3. **AppShell + Sidebar rewrite** — Tailwind-only. Delete `Sidebar.module.css`, any `AppLayout.module.css`. Sidebar tests rewritten in this commit.
4. **Auth pages rewrite** — login + register. Auth tests rewritten.
5. **Database migration** — `004_drop_categories_color.sql`. Go tests updated. sqlc regen. `types.ts` Category interface updated. This lands before the pages that depended on the color column.
6. **Dashboard rewrite** — page + test.
7. **Categories page rewrite** — page + `CategoryEditor` + tests. `CategoryEditor` has no color picker after this commit.
8. **Transactions page rewrite (first pass — non-entry)** — toolbar, filter panel, table, bulk actions, tests. Entry row stays old.
9. **Transaction entry row rewrite (§5)** — RHF + shadcn Form + Command. Dedicated test file written first. This is the highest-value commit of the whole migration.
10. **Reports rewrite** — page + tests.
11. **Settings rewrite** — page + tests.
12. **Cleanup** — delete `tokens.css`, `global.css`, all `*.module.css`, `.stylelintrc.json`, stylelint deps, `useChartTheme`, `useChartPatterns`, `ChartTooltip.tsx`, `ThemeProvider.tsx`, `useTheme.ts`, `@fontsource-variable/inter`, `Tabs.tsx`.

At each commit, CI must be green: `go test -race ./...`, `npx tsc -b`, `npx vitest run`, `npx eslint .`, `docker build`.

### 9 — Risks

| Risk | Likelihood | Mitigation |
|---|---|---|
| Entry row keyboard flow regression during RHF integration | Medium | Test file written first (TDD); manual verification against current behavior spec in §5.1; dedicated single commit so it can be reverted cleanly |
| Recharts + CSS var color resolution fails on production data shapes | Low | Seed local DB with real export; visually verify every chart before moving to the next commit |
| Sheet + router navigation state leakage | Low | Use controlled `open`/`onOpenChange`, never imperative |
| Geist font FOUC on first paint | Low | `geist` package preloads via Vite; verify Network waterfall after prod build |
| `categories.color` drop breaks sqlc-generated code silently | Medium | Run `sqlc generate` immediately after migration commit; run Go tests; check `types.ts` regen |
| `category_color` in transaction list response was a JOIN, not a direct column — dropping `categories.color` breaks the JOIN | High if overlooked | Grep `category_color` across Go + SQL + TS before commit 5; update list query + test |
| Big-bang "no rollback" | Accepted | Main stays clean; worst case `git reset --hard feat/design-system-v3` |
| prettier-plugin-tailwindcss reorders classes and creates diff churn on first run | Low | Run Prettier once during the foundation commit to normalize; subsequent diffs are stable |

### 10 — Open questions / YAGNI

**Deferred deliberately:**
- Light mode
- Per-category custom colors
- SVG pattern fills for colorblind mode
- Global `⌘K` navigation launcher
- Animation polish beyond shadcn defaults
- Storybook
- Mobile nav drawer
- i18n
- Visual regression testing

**Open questions none remaining.** All brainstorming decisions are locked in §Decisions record.

## References

- [shadcn/ui documentation](https://ui.shadcn.com)
- [shadcn chart primitive](https://ui.shadcn.com/docs/components/chart)
- [Radix Colors reference](https://www.radix-ui.com/colors)
- [Geist font](https://vercel.com/font)
- Current tokens file: [web/src/styles/tokens.css](web/src/styles/tokens.css)
- Current CI: [.github/workflows/pr.yml](.github/workflows/pr.yml)
- Current sidebar: [web/src/components/Sidebar.tsx](web/src/components/Sidebar.tsx)
- Previous toolbar spec (lands on v2 branch): [docs/superpowers/specs/2026-04-09-transactions-toolbar-design.md](docs/superpowers/specs/2026-04-09-transactions-toolbar-design.md)
