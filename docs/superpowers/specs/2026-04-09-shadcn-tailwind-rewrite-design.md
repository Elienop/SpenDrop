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
2. Adopt shadcn/ui component library (~20 primitives) as the canonical UI vocabulary.
3. Unify typography on Geist Sans + Geist Mono with tabular figures for all numbers.
4. Delete the 11-color per-category `cat-*` token scale and replace with an 11-slot chart palette assigned by `((category.id - 1) % 11) + 1`. Remove per-category color customization.
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
| Category color source | CSS-driven via `--chart-N` slot from `((category.id - 1) % 11) + 1` | Database `categories.color` column (preserved); expanded 20-slot palette |
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
│   │   ├── ... (~20 files)
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
└── main.tsx                 (imports @fontsource-variable/geist + @fontsource-variable/geist-mono)
```

**Deleted wholesale:**
- `src/styles/tokens.css`
- `src/styles/global.css`
- `src/styles/AppLayout.module.css`, `Auth.module.css`, `Categories.module.css`, `ChartTooltip.module.css`, `Dashboard.module.css`, `Reports.module.css`, `Settings.module.css`, `Sidebar.module.css`, `Tabs.module.css`, `Transactions.module.css`
- `src/hooks/useChartTheme.ts` and `useChartTheme.test.ts`
- `src/hooks/useChartPatterns.tsx`
- `src/hooks/useTheme.tsx` and `useTheme.test.tsx` (file exports both `ThemeProvider` and `useTheme` as named exports — there is no standalone `ThemeProvider.tsx`)
- `src/components/ChartTooltip.tsx` and `ChartTooltip.test.tsx`
- `src/components/Tabs.tsx` and `Tabs.test.tsx` (replaced by `ui/tabs`)
- `web/.stylelintrc.json` and all stylelint configuration

**Replaced in place (not deleted):**
- `AppLayout` is defined inline as a local function inside `web/src/App.tsx` (not a separate file). The rewrite replaces this function's body with an `<AppShell>` component; `App.tsx` itself is kept and its routing layout re-wired.

### 1 — Foundation

#### 1.1 Package changes

**Tailwind version: v3 (pinned).** Tailwind v4 is available but introduces a CSS-first config and breaks `eslint-plugin-tailwindcss` (unmaintained for v4). We pin `tailwindcss@^3` and stay on the `tailwind.config.ts` format that shadcn's docs still target as primary.

**Add:**
- `tailwindcss@^3`, `postcss`, `autoprefixer`, `tailwindcss-animate`
- `class-variance-authority`, `clsx`, `tailwind-merge`
- `@fontsource-variable/geist`, `@fontsource-variable/geist-mono` (self-hosted Geist Sans + Mono via fontsource — the `geist` npm package is Next.js-only and uses `next/font`, which Vite cannot consume)
- `react-hook-form`, `@hookform/resolvers`, `zod` (pulled in by shadcn `form`)
- `cmdk` (pulled in by shadcn `command`)
- `sonner` (toasts; pulled in by shadcn `sonner`)
- `@radix-ui/*` packages (pulled in by individual shadcn primitives)
- `prettier-plugin-tailwindcss` (dev — sorts utility classes)
- `eslint-plugin-tailwindcss` (dev — catches class typos; works on Tailwind v3)

**Keep:**
- `recharts`, `date-fns`, `react-router-dom`, `vite`, `vitest`, `@testing-library/*`, `lucide-react`

#### 1.2 Tailwind + shadcn init

```bash
cd web
npm install -D tailwindcss@^3 postcss autoprefixer tailwindcss-animate
npm install class-variance-authority clsx tailwind-merge \
  @fontsource-variable/geist @fontsource-variable/geist-mono
npx tailwindcss init -p
npx shadcn@latest init
# Style: default | Base color: neutral | CSS variables: yes
```

This generates `tailwind.config.ts`, `postcss.config.js`, `components.json`, and `src/lib/utils.ts` (containing the `cn()` helper).

**Preflight handling during migration.** Tailwind's base preflight reset normalizes `h1`–`h6`, `button`, `ul/ol`, and form elements. Enabling it in commit 1 while CSS Modules still render half the app would cause visible regressions (button padding, heading sizes, list bullets) on every non-migrated page. Therefore:

- **Commits 1–2:** Disable preflight via `corePlugins: { preflight: false }` in `tailwind.config.ts`. Old CSS Modules and Tailwind utilities coexist without reset collisions.
- **Commit 3 (AppShell + Sidebar):** Keep preflight disabled. AppShell and Sidebar are fully Tailwind.
- **Final cleanup commit:** Re-enable preflight (`corePlugins: { preflight: true }` or remove the override) once the last CSS Module is deleted. Visually regression-test each page immediately after.

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

The Vercel `geist` npm package is **Next.js-only** — it depends on `next/font` internals that Vite cannot consume. For Vite we self-host via fontsource, which ships the same SIL OFL binaries as plain CSS imports:

```ts
// main.tsx
import '@fontsource-variable/geist';
import '@fontsource-variable/geist-mono';
```

These imports register the `@font-face` declarations and expose the font-family names `"Geist Variable"` and `"Geist Mono Variable"`. Tailwind references them directly:

```ts
// tailwind.config.ts
theme: {
  extend: {
    fontFamily: {
      sans: ['"Geist Variable"', 'system-ui', 'sans-serif'],
      mono: ['"Geist Mono Variable"', 'ui-monospace', 'monospace'],
    },
  },
}
```

`globals.css` sets `font-family: theme('fontFamily.sans');` on `body` so the base face applies without `className="font-sans"` on every element.

Any element rendering a number uses `className="font-mono tabular-nums"`. This applies to KPI values, transaction amounts, chart axis labels, table date columns, and percentages.

#### 1.5 Path aliases

Shadcn expects `@/*` to resolve to `src/*`. Confirm `vite.config.ts` and `tsconfig.json` both declare this alias (they should already — if not, add them during the foundation commit).

### 2 — Component layer

#### 2.1 shadcn primitives (install once)

All installed in a single command during the shadcn-primitives commit:

```bash
npx shadcn@latest add button card input label select checkbox form \
  dialog sheet dropdown-menu popover command tabs table badge \
  skeleton tooltip sonner scroll-area calendar chart
```

20 primitives, each copied into `src/components/ui/`.

Not installed (deliberately): `switch` (no toggle controls in any page), `textarea` (no multi-line fields in current entry or forms), `separator` (layout uses Tailwind borders directly). If any of these becomes needed during implementation, add it with a one-line `npx shadcn add <name>` as part of the commit where it's first used.

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

- `src/hooks/useChartPatterns.tsx`
- `src/hooks/useChartTheme.ts`
- `src/components/ChartTooltip.tsx`
- All `<pattern>` / `<defs>` blocks in current chart code
- `patternStyles` prop on the old `ChartTooltip`

### 5 — Transaction entry row (primary app goal)

SpenDrop's entry flow is the main data-entry loop. The rewrite must make this flow faster and more robust, not just prettier. This section starts from an honest read of the **current** code and names every change as an improvement so nothing is papered over as "preserved."

#### 5.1 Current behavior (ground truth from `web/src/components/TransactionEntry.tsx`)

The current component is a classic HTML form, not a spreadsheet row:

- **Not persistent in-table.** It is rendered above the table inside a wrapper that the Transactions page toggles with `display: none` when the toolbar's "Add" button is pressed. It is a modal-ish panel, not a live table row.
- **Fields (in order):** Date (`<input type="date">`), Amount (`<input type="number" step="0.01" min="0">`), Description (`<input type="text">`), Category (native `<select>` populated from `categories`), Tags (`<TagInput>` — a custom chip input from `src/components/TagInput.tsx`).
- **Submit:** explicit `<button type="submit">Add</button>`. There is no Enter-to-save from arbitrary fields — Enter just submits the form the way any native form does (from the last field with a submit button present in tab order).
- **Keyboard:** no custom key handling at all. Tab moves between fields using the browser default. No field-to-field Enter navigation, no `⌘Enter`, no Shift+Enter, no Escape-to-reset, no `⌘Z`.
- **After save:** `amount`, `description`, and `tags` are cleared; `date` and `category_id` are preserved. The "most recently selected category" is also persisted to `localStorage` under `spendrop-last-category` and used as the initial value on next mount.
- **Focus after save:** the component does not explicitly refocus any field. The amount input keeps focus only because browsers preserve it across an in-place form reset.
- **Validation:** an early return if `amount`, `description`, or `category_id` is empty. No inline error messages — submission just silently no-ops.
- **Amount sign:** always stored as positive (`min="0"`). The transaction's expense/income polarity is derived from the category's `type`, not from the amount's sign.
- **Tags:** `TagInput` is a comma-separated chip input. Values are joined into a single string and sent as the `tags` field of the payload.

This is "fine" but far from the primary-app-goal bar. The rewrite makes it a genuinely fast, keyboard-driven flow.

#### 5.2 Target behavior (new — everything in this list is an improvement, not a preservation)

1. **Fuzzy category picker** — replace the native `<select>` with shadcn `Command` (built on `cmdk`). Type a few letters; matching categories appear; Enter selects. Massively faster than scrolling a select with 14+ entries.
2. **Field-to-field Enter navigation** — Enter on Amount → moves to Description → moves to Category picker → moves to Tags → submit. No more Tab-gymnastics. `⌘Enter` from any field submits immediately.
3. **Escape resets** — Escape on any field resets the form to its default values (same behavior as discarding the entry).
4. **Smart date default** — the date field defaults to the most recently used date (not always "today"), so entering a batch of transactions from yesterday doesn't require repeatedly fixing the date. The existing `spendrop-last-category` localStorage key is joined by `spendrop-last-date` (same `YYYY-MM-DD` format). A small "today" quick-button on the calendar popover snaps back to today.
5. **Undo last save** — after saving, show a Sonner toast "Saved. Undo (⌘Z)" for 4 seconds. Clicking Undo (or pressing ⌘Z while the toast is visible) deletes the just-saved transaction and restores the form fields to the saved values.
6. **Persistent focus on amount after save** — explicit ref + `amountRef.current?.focus()` in the post-save reset.
7. **Inline validation** — missing amount / description / category surfaces `FormMessage` from shadcn `Form`, not a silent no-op.
8. **Preserved from current behavior:** the `tags` field stays (implemented via a `FormField`-wrapped `TagInput`; the existing `TagInput` component is kept and used as-is); date + category are still preserved across saves; amount stays positive-only and polarity is still derived from the category's `type`. Shift-Enter sign toggle and minus-sign heuristics are **not** part of this rewrite — the backend has no concept of a signed amount on a category, and introducing one is out of scope.

#### 5.3 Implementation with react-hook-form + shadcn

Schema (Zod), including `tags`:

```ts
const entrySchema = z.object({
  date: z.string().regex(/^\d{4}-\d{2}-\d{2}$/, 'Invalid date'),
  amount: z.coerce.number().positive('Amount must be > 0'),
  description: z.string().min(1, 'Description required').max(200),
  category_id: z.number().int().positive('Category required'),
  tags: z.string().default(''), // comma-separated, same shape as today
});
type EntryForm = z.infer<typeof entrySchema>;
```

Form skeleton (abbreviated — full version lives in the implementation):

```tsx
const form = useForm<EntryForm>({
  resolver: zodResolver(entrySchema),
  defaultValues: {
    date: getLastDate(),
    amount: 0,
    description: '',
    category_id: getLastCategoryId(),
    tags: '',
  },
});

const undoBufferRef = useRef<{ saved: Transaction; values: EntryForm } | null>(null);

const onSubmit = async (values: EntryForm) => {
  const saved = await createTransaction(values);
  saveLastCategory(String(values.category_id));
  saveLastDate(values.date);
  undoBufferRef.current = { saved, values };
  toast.success('Transaction saved', {
    duration: 4000,
    action: {
      label: 'Undo (⌘Z)',
      onClick: () => undoLastSave(),
    },
    onAutoClose: () => { undoBufferRef.current = null; },
  });
  form.reset({
    date: values.date,
    amount: 0,
    description: '',
    category_id: values.category_id,
    tags: '',
  });
  amountRef.current?.focus();
};

const undoLastSave = useCallback(async () => {
  const buf = undoBufferRef.current;
  if (!buf) return;
  undoBufferRef.current = null;
  await deleteTransaction(buf.saved.id);
  form.reset(buf.values); // restore the form to what the user had typed
  amountRef.current?.focus();
}, [form]);
```

Fields use shadcn `Form` wrappers (`FormField`, `FormItem`, `FormControl`, `FormMessage`), which integrate with RHF's `control` while preserving accessibility (label association, aria-describedby for errors).

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
                  value={cat.name}
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

The **tags field** wraps the existing `TagInput` in a `FormField`:

```tsx
<FormField
  name="tags"
  control={form.control}
  render={({ field }) => (
    <FormItem>
      <FormControl>
        <TagInput value={field.value} onChange={field.onChange} placeholder="Add tags..." />
      </FormControl>
      <FormMessage />
    </FormItem>
  )}
/>
```

#### 5.4 Keyboard handling

A single `onKeyDown` handler on the form root manages:
- `Enter` on any field → focus next field in a hard-coded order (`date → amount → description → category → tags → submit`); prevents the native form submit on intermediate fields
- `⌘Enter` (or `Ctrl+Enter`) on any field → submit immediately
- `Escape` on any field → `form.reset()` to default values (no confirmation — the user can just re-type; Undo toast covers accidental saves, not accidental resets)
- `Tab` / `Shift+Tab` → browser default (relies on natural DOM order)
- `⌘Z` / `Ctrl+Z` while the post-save Sonner toast is still open → calls `undoLastSave()`, which reads from `undoBufferRef` and no-ops if the buffer has been cleared by auto-close

The undo-buffer race is avoided by the single-slot ref pattern: any new save overwrites the buffer with its own values, and the toast's `onAutoClose` clears the ref when its timer expires. A ⌘Z after the toast is gone simply finds `null` and does nothing.

The flow is implemented as a plain `useCallback` listener on the form element plus a global `window` listener for the ⌘Z case (scoped to "toast is visible"). No custom focus management beyond `amountRef` for post-save focus.

#### 5.5 Why RHF is worth the integration cost

- Form state, validation, and error surfacing all unified in one API
- `form.reset({ ...preserveDate, ...preserveCategory })` is the cleanest way to reset-with-preserved-values after each save
- shadcn `Form` handles ARIA wiring for free (no manual `aria-describedby`)
- Zod schema is reusable on the category edit form for consistent validation
- The `undoBufferRef` pattern sits alongside RHF cleanly — the buffer holds both the saved transaction (for the delete call) and the previous form values (for the reset on undo)

#### 5.6 Testing

Dedicated test file `TransactionEntryRow.test.tsx` using `userEvent`:
- Full save flow: type amount → Enter → type description → Enter → select category in Command → Enter → type tags → Enter → verify API call payload matches, verify `amount`/`description`/`tags` cleared, verify `date`/`category_id` preserved, verify amount input refocused
- Tags included in payload: verify `tags` field round-trips through the form
- Undo flow: save → click "Undo" in the toast → verify delete API call + form fields restored to the saved values
- Undo-after-auto-close: save → advance fake timers past 4s → press ⌘Z → verify delete is **not** called
- Validation: submit empty → verify error messages on amount + description + category via `FormMessage`
- Category search: type "gr" in the Command input → verify "Groceries" is highlighted → Enter selects it
- Escape resets: fill all fields → press Escape → verify all fields back to defaults
- localStorage: saving a transaction writes both `spendrop-last-category` and `spendrop-last-date`; re-mounting reads them back

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

- Delete `web/.stylelintrc.json`
- Remove `lint:css` and the `stylelint` portion of the `lint` script from `web/package.json` (currently `"lint": "tsc --noEmit && stylelint \"src/**/*.css\""` — becomes `"lint": "tsc --noEmit && eslint ."`)
- Remove stylelint dev deps (`stylelint`, `stylelint-config-standard`, `stylelint-config-css-modules`)
- CI: `.github/workflows/pr.yml` line 50 calls `npx tsc -b` directly — it does **not** run `npm run lint`, and there is no stylelint invocation anywhere in CI today. Removing stylelint is therefore a local-tooling change only; no CI edit is required in this commit. The `npx eslint .` CI step is added separately in the final cleanup commit (§8 commit 13).

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

### 7 — Database migration (`categories.color` removal)

Dropping the `categories.color` column is a **fully enumerated** change. This section lists every file that must be updated, in the same atomic commit where the migration lands, so CI stays green. Skip any file here and either sqlc, Go build, `tsc --noEmit`, vitest, or a test assertion will break.

#### 7.1 Migration SQL

`internal/database/migrations/004_drop_categories_color.sql`:

```sql
-- Drop categories.color column. Category colors are now derived from
-- the chart palette slot (((category.id - 1) % 11) + 1) at render time.
ALTER TABLE categories DROP COLUMN color;
```

SQLite's `ALTER TABLE DROP COLUMN` requires SQLite 3.35+ (March 2021) — well within supported versions. The existing `001_initial_schema.sql` seed that inserts `color` values must also be updated to drop the `color` parameter from its `INSERT INTO categories (...)` list. Migration ordering is by filename, so the seed runs before `004`; but the drop happens after the initial seed, so both shapes must stop referencing `color` in the same commit that drops the column. Practically: update the seed SQL in the same commit as `004`.

#### 7.2 Enumerated file changes

**SQL / sqlc layer:**
- `internal/database/migrations/001_initial_schema.sql` — remove `color` from the `categories` table definition **and** from the seed `INSERT INTO categories (name, type, color, sort_order) VALUES ...` statements (lines ~90–117). The final state: `INSERT INTO categories (name, type, sort_order) VALUES ...`.
- `internal/database/migrations/004_drop_categories_color.sql` — new file, SQL above.
- `internal/database/queries.sql`:
  - Line 48: `INSERT INTO categories (name, type, color, sort_order)` → `(name, type, sort_order)`
  - Line 63: `UPDATE categories SET name = ?, color = ?, icon = ?` → `SET name = ?, icon = ?`
  - Line 172: `SELECT c.id, c.name, c.color, CAST(COALESCE(...))` → drop `c.color,`
  - Line 216: `c.color,` inside `SumByCategoryForRange` → drop
- `internal/database/queries.sql.go` — **regenerated by `sqlc generate`**, not hand-edited. The generated `Category`, `CreateCategoryParams`, `UpdateCategoryParams`, `SumByCategoryForMonthRow`, and `SumByCategoryForRangeRow` structs all lose their `Color` field. Don't edit this file manually; just run `sqlc generate` and commit the diff.
- `internal/database/models.go` — if the generated `Category` model lives here rather than in `queries.sql.go` depending on the sqlc version, it also regenerates. Verify after running sqlc.

**Go handlers:**
- `internal/api/category_handlers.go`:
  - Delete `hexColorRegexp` and `isValidHexColor` (top of file, lines 14–18).
  - Delete the `Color string \`json:"color"\`` field from `categoryCreateRequest` (line 24) and `categoryUpdateRequest` (line 31).
  - Delete both `if req.Color != "" && !isValidHexColor(...)` validation blocks (lines 96–99 and 153–156).
  - Remove `Color: req.Color,` from `CreateCategoryParams` (line 108) and `UpdateCategoryParams` (line 164).
  - Update the function doc comment at line 119 ("updates an existing category's name, color, and icon") to drop "color".
- `internal/api/transaction_handlers.go`:
  - Delete the `CategoryColor string \`json:"category_color,omitempty"\`` field from `transactionResponse` (line 47).
  - Update the list query at line 180 from `c.name AS category_name, c.type AS category_type, c.color AS category_color` to `c.name AS category_name, c.type AS category_type` — the JOIN on `categories` still stands; only the projected column goes away.
  - Delete the local `categoryColor string` variable (line 208), its `&categoryColor` scan arg (line 213), and the `tr.CategoryColor = categoryColor` assignment (line 223). The scan arg list must match the new SELECT.
- `internal/api/dashboard_handlers.go`:
  - Delete the `Color string \`json:"color"\`` field on the category-breakdown response struct (line ~42).
  - Delete the `Color: row.Color,` assignment that populates it (line ~294).
- `internal/api/reports_handlers.go`:
  - Delete the `Color string \`json:"color"\`` field on the category-trend response struct (line ~96).
  - Delete `Color: row.Color,` in the row-to-response mapper (line ~148).

**Go tests:**
- `internal/api/category_handlers_test.go` — drop every `"color":"#..."` from JSON request bodies (lines 101, 130, 147, 164, 200, 217, 234, 251) and delete the single `resp["color"]` `if` block at lines 120–121 in its entirety. Some requests will then have empty bodies; keep the trailing fields that still matter (`name`, `type`, `sort_order`).
- `internal/api/transaction_handlers_test.go`:
  - `seedTestCategory` helper (lines 84–89) — drop the `color string` parameter and the `Color: color,` field. Update every call site within the file to match.
  - `TestHandleListTransactions_IncludesCategoryInfo` (lines 619–620) — delete the `txn["category_color"] != "#5347CE"` assertion entirely; the response no longer has that key.

**Frontend TypeScript:**
- `web/src/api/types.ts` — four fields to delete:
  - Line 20: `category_color: string;` on `Transaction`
  - Line 31: `color: string;` on `Category`
  - Line 85: `color: string;` on `CategoryBreakdownItem`
  - Line 147: `color: string;` on `CategoryTrendEntry`
- `web/src/pages/Categories.tsx`:
  - Delete `const [color, setColor] = useState(initial?.color ?? '#5347CE')` (line 24).
  - Delete the color `<input>` element and its surrounding label block (around line 50).
  - Delete the `<span className={styles.colorSwatch} style={{ backgroundColor: category.color }} />` at lines 108–109.
  - Delete the `color: cat.color,` entry in the edit-initial object at line 283.
  - Delete the `color` parameter from the edit form submit handler (wherever it posts the update).
- `web/src/pages/Dashboard.tsx`:
  - Delete the `color: cat.color,` entry in `gaugeData` at line 137.
  - Replace `cat.color` at lines 547 and 555 with `getCategoryColorVar(cat)` (new helper from §2.3).
  - Delete the `tx.category_color` references at lines 668–669 and replace with `getCategoryColorVar({ id: tx.category_id })`.
  - **Note:** the `useChartPatterns` call at line 145 and the `'Other' color: '#B8BCC8'` fallback at line 140 also reference color, but both sit inside chart-pattern code paths that are **already deleted in §8 commit 6** (Dashboard rewrite). By the time commit 12 runs, `Dashboard.tsx` no longer contains those lines — they are not part of this commit's edits.
- `web/src/pages/Reports.tsx` — line 219: replace `cat.color || 'var(--color-text-secondary)'` with `getCategoryColorVar(cat)`.
- `web/src/components/FilterPanel.tsx` — line 157: replace `{ backgroundColor: cat.color, borderColor: cat.color }` with `{ backgroundColor: getCategoryColorVar(cat), borderColor: getCategoryColorVar(cat) }`.
- `web/src/components/TransactionRow.tsx` — line 144: replace `color={transaction.category_color}` with `color={getCategoryColorVar({ id: transaction.category_id })}`. Or, since `TransactionRow` is being rewritten anyway as part of the Transactions page commit, inline the `getCategoryColorVar` call directly in the new Tailwind-based row.
- `web/src/hooks/useChartPatterns.tsx` — deleted wholesale as part of the final cleanup. It references `p.color` at lines 71, 77, 84, 90, 91 and `item.color` at line 148, but it's a hook that nothing will import after the Dashboard rewrite uses the new chart primitives.

**Frontend tests:**

Two classes of break exist here: (a) fixtures explicitly typed as `Category[]`, `CategoryBreakdownItem[]`, etc., which fail `tsc --noEmit` with TS2353 (excess property) the moment the field is removed from `types.ts`, and (b) untyped object literals which TypeScript won't flag but which carry stale data and will mislead future debugging. Both classes are enumerated.

**Typed fixtures — TS2353 breaks (must be fixed in this commit):**
- `web/src/pages/Settings.test.tsx` — lines 419, 420, 421: `mockCategories: Category[]` with three explicit `color: '#ff0000'` / `'#00ff00'` / `'#0000ff'` entries. Guaranteed TS2353 when `Category.color` is removed. Drop the three `color:` keys.
- `web/src/components/TransactionEntry.test.tsx` — **deleted wholesale in §8 commit 9** when the component is renamed to `TransactionEntryRow` and its new test is written TDD-first. The `Category[]`-typed `color: '#e94560'` fixture at line 12 disappears with the file, so no edit is required in this commit. Listed here only for completeness of the blast-radius grep.
- `web/src/components/TransactionRow.test.tsx` — line 12: `mockCategories: Category[]` with `color: '#e94560'`. Drop the key. (Line 32 `category_color: '#e94560'` on the transaction fixture is a separate field; drop it too.) Both edits live in the same commit.

**Untyped fixtures — TS silent but stale (must still be fixed for hygiene and so the §7.3 grep stays clean):**
- `web/src/pages/Dashboard.test.tsx` — line 51: `{ id: 1, name: 'Food', color: '#818CF8', total: 1200, transaction_count: 15 }`, line 52: `{ id: 2, name: 'Transport', color: '#7EC89B', total: 800, transaction_count: 8 }`, line 70: `category_color: '#818CF8'`. Drop all three.
- `web/src/pages/Transactions.test.tsx` — line 34: `category_color: '#e94560'` on the transaction fixture. Drop.
- `web/src/pages/Categories.test.tsx` — lines 31, 41, 51: three `color: '#ff0000'` / `'#00ff00'` / `'#0000ff'` entries inside an untyped `const mockCategories = [...]`. Drop all three.
- `web/src/pages/Reports.test.tsx` — line 51: `{ id: 1, name: 'Food', color: '#ff0000', type: 'expense', data: [...] }` inside an untyped mock. Drop the `color:` key.
- `web/src/hooks/useDashboard.test.ts` — lines 34, 35: two breakdown-item literals with `color: '#ff0000'` / `'#00ff00'`. Drop both keys.
- `web/src/App.test.tsx` — any fixture that embeds a category or transaction with a `color` field gets that field deleted.

**Test files deleted wholesale (not edited — the test goes away with its subject):**
- `web/src/hooks/useChartTheme.test.ts` — hook removed.
- `web/src/hooks/useChartPatterns.test.tsx` — if present, hook removed.
- `web/src/components/ChartTooltip.test.tsx` — the `color` strings at lines 13, 14 are payload item colors (not `Category.color`), but the component itself is removed in §4.6, so the test file goes too.

**Pre-flight check for commit 12 (expanded):** In addition to the `categories.color`, `cat.color`, `category_color`, `c.color` greps named in §8 commit 12, also grep for `\bcolor:\s*['"]#` (literal hex-color object-field syntax) and `category_color` (already listed, repeated here for emphasis). The only remaining hits inside the commit should be the deletions themselves. If any test or component still carries a stray `color: '#...'` fixture after this commit, add it to the deletion list and re-run the grep.

#### 7.3 Why it's all one commit

Every item above must move together. Sqlc regen after the query changes, Go build after the handler changes, `tsc --noEmit` after the types change, and vitest after the fixture changes — a partial commit breaks the build. The full change is still small (maybe 30 files, mostly 1–3 line deletes), so one atomic "drop categories.color" commit is tractable. Tests for each piece are run in the same commit's CI — nothing ships partial.

#### 7.4 What does **not** change

- The `categories` table schema otherwise: `id`, `name`, `type`, `icon`, `sort_order`, `is_active`, `created_at`, `updated_at` all stay.
- Any JOIN on `categories` stays; only the projected `c.color` columns disappear.
- The `/api/categories` response shape loses only the `color` key — all other keys are untouched.

### 8 — Migration order (commits in order)

Each step lands as its own commit with CI green at that commit. The critical constraint: the **DB migration that drops `categories.color` lands last, after every Go and frontend render site has already stopped reading it.** Landing it earlier would break CI in whichever commit still references the dropped column. The schema change is deferred precisely because the column is load-bearing in the current code.

Frontend tests move with the pages they cover — a page rewrite commit also rewrites that page's test file. Preflight stays disabled (`corePlugins: { preflight: false }`) from commit 1 until the final cleanup commit that deletes the last CSS Module.

1. **Foundation** — Tailwind v3 + PostCSS + autoprefixer + `tailwindcss-animate` installed. `shadcn init` generates `tailwind.config.ts`, `postcss.config.js`, `components.json`, `src/lib/utils.ts`. `globals.css` created with the full token block from §1.3. Geist fontsource packages installed and imported in `main.tsx`. Path alias `@/*` verified in `vite.config.ts` and `tsconfig.json`. Preflight disabled. App still renders with old CSS Modules — zero visible change.
2. **Install shadcn primitives** — one `shadcn add` command producing ~20 files under `src/components/ui/`. No app components consume them yet. Tests unchanged.
3. **`chart-colors.ts` helper** — create `web/src/lib/chart-colors.ts` with `getCategoryColorVar()` (§2.3). Trivial unit test. This helper is the mechanism that lets subsequent commits stop reading `cat.color` / `tx.category_color` without waiting for the DB migration.
4. **AppShell + Sidebar rewrite** — replace the inline `AppLayout` function in `App.tsx` with `<AppShell>`; rewrite `Sidebar.tsx` with Tailwind + shadcn `ScrollArea` + `Tooltip`. Delete `Sidebar.module.css` and `AppLayout.module.css`. Rewrite `Sidebar.test.tsx`. No other pages touched.
5. **Auth pages rewrite** — Login + Register rewritten with shadcn `Card` + `Form` + `Input` + `Button`. Delete `Auth.module.css`. Rewrite `Login.test.tsx` / `Register.test.tsx`.
6. **Dashboard rewrite** — page + `KpiCard` + `ChartCard` + Recharts via shadcn `chart` primitive. **Source all category colors via `getCategoryColorVar()`** — this commit stops reading `cat.color` and `tx.category_color` on the Dashboard, even though the DB column still exists. Delete `Dashboard.module.css`. Rewrite `Dashboard.test.tsx`, updating transaction/category fixtures to not rely on `color` visually (the fixture can still carry the field — it just won't be asserted).
7. **Categories page rewrite** — rewrite `Categories.tsx` + `CategoryEditor`. **The color picker is removed from the UI in this commit**, but the `color` field on the Category TypeScript interface and the Go handler still exist. The edit form simply stops submitting a `color` value; the backend silently keeps the old stored color until the final migration commit nukes it. Rewrite `Categories.test.tsx`.
8. **Transactions page rewrite (non-entry)** — `TransactionToolbar`, `FilterPanel`, `TransactionTable`, bulk actions, row actions. **All category-color rendering goes through `getCategoryColorVar()`** — no reads of `tx.category_color` or `cat.color` remain on this page. Delete `Transactions.module.css`, `Tabs.module.css`, `ChartTooltip.module.css`. Rewrite `Transactions.test.tsx` and `TransactionRow.test.tsx`. Entry row still uses the old classic-form component, wired into the new shell.
9. **Transaction entry row rewrite (§5)** — rename the component from `TransactionEntry` → `TransactionEntryRow` and rewrite with RHF + shadcn `Form` + `Command` + `TagInput` wrapper + Sonner toast + undo buffer. **Delete the old `web/src/components/TransactionEntry.tsx` and its sibling `web/src/components/TransactionEntry.test.tsx` in the same commit** — no orphaned files, no stale imports. Dedicated `TransactionEntryRow.test.tsx` written **first** (TDD) and lives in this commit. Update the import site in `web/src/pages/Transactions.tsx` to point at the new component. Highest-value commit of the whole migration.
10. **Reports rewrite** — page + `DateRangePicker` + chart cards. All category colors via `getCategoryColorVar()`. Delete `Reports.module.css`. Rewrite `Reports.test.tsx` (if it exists).
11. **Settings rewrite** — page + shadcn `Tabs` + `Form` + `Dialog` for data export/import. Delete `Settings.module.css`. Rewrite `Settings.test.tsx` (if it exists).
12. **Database migration + color cleanup (atomic)** — everything listed in §7.2 in one commit:
    - New migration `004_drop_categories_color.sql` + seed update in `001_initial_schema.sql`
    - `queries.sql` edits + `sqlc generate` + resulting `queries.sql.go` diff
    - Go handler field/validation deletions in `category_handlers.go`, `transaction_handlers.go`, `dashboard_handlers.go`, `reports_handlers.go`
    - Go test fixture updates in `category_handlers_test.go` + `transaction_handlers_test.go`
    - `web/src/api/types.ts` field deletions (four fields, enumerated in §7.2)
    - Frontend test fixture cleanups enumerated in §7.2 "Frontend tests" (Settings, TransactionRow, Categories, Dashboard, Transactions, Reports, useDashboard, App). §7.2 is exhaustive — this commit deletes every entry on that list, nothing extra.
    - **Pre-flight check before committing:** run the full grep set across `internal/` and `web/src/`:
      - `categories\.color`
      - `cat\.color`
      - `category_color`
      - `c\.color`
      - `\bcolor:\s*['"]#` (literal hex-color object-field syntax — catches typed and untyped fixtures alike)
      - `\.color\b` inside `web/src/pages/` and `web/src/components/` (catches any renamed but forgotten consumer)

      The `\bcolor:\s*['"]#` pattern and the broader `.color\b` sweep will produce false positives for unrelated uses (e.g. `style.color`, CSS-in-JS object literals, third-party library fields). Skim past the noise — the only relevant hits are those touching `Category`, `Transaction`, `CategoryBreakdownItem`, or `CategoryTrendEntry` shapes. The only remaining **relevant** hits should be inside this commit's own deletions. If any page/test still consumes the field, add it to the deletion list and re-run the grep until clean.
    - Enforces the §7.2 "all one commit" rule.
13. **Final cleanup** — three top-line goals:
    1. **Delete the "Deleted wholesale" leftovers** — anything from the §Deleted-wholesale list that wasn't already removed by a page rewrite: `tokens.css`, `global.css`, any remaining `*.module.css`, `.stylelintrc.json`, stylelint dev deps, `useChartTheme.ts` + its test, `useChartPatterns.tsx`, `useTheme.tsx` + its test (removes both `ThemeProvider` and `useTheme` named exports), `ChartTooltip.tsx` + its test, `Tabs.tsx` + its test, `@fontsource-variable/inter`.
    2. **Re-enable Tailwind preflight** — flip `corePlugins: { preflight: false }` off in `tailwind.config.ts` (or remove the override). This is a behavioral change: preflight resets default browser styles (margins, list markers, form element appearance, etc.). Do a full visual sweep of every page after enabling, with particular attention to forms, lists, and any element that relied on default browser styles during the migration. Fix any regressions in the same commit. This is the reason preflight was held until the end.
    3. **Add the `npx eslint .` CI step** to the frontend job in `.github/workflows/pr.yml` so the ESLint-driven Tailwind class linting becomes a hard CI gate.

At each commit, CI must be green: `go test -race ./...`, `npx tsc -b`, `npx vitest run`, `npx eslint .` (added in commit 1 even if the step doesn't run in CI yet), and `docker build` for the full-stack job.

### 9 — Risks

| Risk | Likelihood | Mitigation |
|---|---|---|
| Entry row rewrite introduces a keyboard-flow regression that makes the primary app loop worse instead of better | Medium | Test file written first (TDD — §5.6); current behavior honestly documented in §5.1 so improvements in §5.2 can't accidentally regress preservation items; dedicated single commit so it can be reverted cleanly |
| Recharts + CSS var color resolution fails on production data shapes | Low | Seed local DB with real export; visually verify every chart before moving to the next commit |
| Sheet + router navigation state leakage | Low | Use controlled `open`/`onOpenChange`, never imperative |
| Geist FOUC on first paint via fontsource | Low | fontsource ships `@font-face` blocks with `font-display: swap`; verify Network waterfall after prod build |
| `categories.color` drop breaks sqlc-generated code silently | Medium | §7.2 enumerates every query that references `color`; run `sqlc generate` inside the migration commit and verify the `queries.sql.go` diff matches the enumerated list |
| `category_color` in transaction list response is a JOIN projection, not a direct column — dropping `categories.color` breaks the JOIN's SELECT list | High if overlooked | Handled by §7.2 explicitly and by §8 commit 12's grep pre-flight; the list query at `transaction_handlers.go:180` is named out in full |
| Dashboard / Reports / FilterPanel still read `cat.color` or `tx.category_color` when the DB column is dropped | High if commits are re-ordered | §8 commit 12 is the last commit; every prior page rewrite (commits 6, 8, 10) routes category colors through `getCategoryColorVar()` using the `chart-colors.ts` helper introduced in commit 3 |
| Tailwind preflight reset causes visible regressions on half-migrated pages | High if enabled early | Preflight disabled from commit 1; re-enabled only in the final cleanup commit (§1.2) |
| Tailwind v4 vs v3 mismatch — `eslint-plugin-tailwindcss` and some shadcn examples differ by version | Medium | Pin `tailwindcss@^3` explicitly in §1.1; document the choice so implementers don't pick v4 by accident |
| Fontsource package names wrong — typo would silently fall back to system font | Low | Package names pinned to `@fontsource-variable/geist` and `@fontsource-variable/geist-mono` in §1.1 and §1.2; verify via browser DevTools → Computed style that `font-family` resolves to Geist after commit 1 |
| Undo toast race — two rapid saves leave an orphan buffer and ⌘Z deletes the wrong transaction | Medium | Single-slot `undoBufferRef` with `onAutoClose` ref-clearing; documented explicitly in §5.4 |
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
