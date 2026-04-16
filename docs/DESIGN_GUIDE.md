# SpenDrop Design Guide

A practical reference for building pages that match the current dashboard style. Every value here is extracted from the production implementation — use it as the single source of truth.

---

## Stack

- **UI framework:** shadcn/ui (Radix primitives + Tailwind CSS)
- **Styling:** Tailwind CSS utility classes — no CSS Modules, no raw CSS files
- **Design tokens:** CSS custom properties in `globals.css` (HSL space-separated format)
- **Charts:** Recharts wrapped in shadcn's `ChartContainer` / `ChartTooltip` / `ChartLegend`
- **Icons:** Lucide React (tree-shakeable, outline style)
- **Fonts:** Geist Variable (sans) + Geist Mono Variable (mono), self-hosted via `@fontsource-variable`

---

## Color System

### Design Tokens (`globals.css`)

All colors use HSL space-separated format: `H S% L%`. Consumed via `hsl(var(--token))`.

| Token | HSL Value | Role |
|-------|-----------|------|
| `--background` | `240 3.7% 7.1%` | Page background |
| `--foreground` | `240 5% 93.3%` | Primary text |
| `--card` | `240 4% 9%` | Card background |
| `--card-foreground` | `240 5% 93.3%` | Card text |
| `--popover` | `240 4% 10%` | Dropdown/tooltip background |
| `--muted` | `240 4% 14%` | Muted surface (toggle tracks, empty gauges) |
| `--muted-foreground` | `240 4% 57%` | Secondary text, labels, metadata |
| `--border` | `240 4% 15%` | All borders (cards, inputs, dividers) |
| `--input` | `240 4% 15%` | Input borders |
| `--ring` | `0 0% 98%` | Focus ring |
| `--primary` | `0 0% 98%` | Primary actions, emphasis, chart default |
| `--primary-foreground` | `240 6% 10%` | Text on primary background |
| `--secondary` | `240 4% 14%` | Secondary surfaces |
| `--accent` | `240 4% 14%` | Accent surfaces (hover, active states) |
| `--destructive` | `0 72% 51%` | Error, delete actions |
| `--radius` | `0.625rem` | Base border radius |

### Color Philosophy: Neutral-First

**Non-category charts use neutral colors only.** The `--primary` and `--muted-foreground` tokens differentiate series — never `chart-N` variables. This keeps the interface clean and reserves color for meaning.

- **Income bars:** `hsl(var(--primary))` (near-white, high contrast)
- **Expense bars:** `hsl(var(--muted-foreground))` (mid-gray, secondary)
- **YoY current year:** `hsl(var(--primary))`
- **YoY previous year:** `hsl(var(--muted-foreground))`

### Category Colors (chart-1 through chart-20)

Reserved exclusively for category-specific visualizations (spending breakdown pie chart, category trend lines). Mapped via `getCategoryColorVar()` in `lib/chart-colors.ts`.

| Token | HSL | Visual |
|-------|-----|--------|
| `--chart-1` | `263 55% 53%` | Purple |
| `--chart-2` | `226 70% 56%` | Blue |
| `--chart-3` | `206 100% 39%` | Ocean |
| `--chart-4` | `189 94% 39%` | Cyan |
| `--chart-5` | `173 80% 36%` | Teal |
| `--chart-6` | `142 53% 42%` | Green |
| `--chart-7` | `88 60% 45%` | Lime |
| `--chart-8` | `48 96% 53%` | Gold |
| `--chart-9` | `25 95% 53%` | Orange |
| `--chart-10` | `346 77% 50%` | Rose |
| `--chart-11` | `286 68% 55%` | Violet |
| `--chart-12` | `6 75% 52%` | Red |
| `--chart-13` | `37 88% 50%` | Amber |
| `--chart-14` | `68 58% 43%` | Chartreuse |
| `--chart-15` | `115 48% 40%` | Spring Green |
| `--chart-16` | `157 60% 38%` | Jade |
| `--chart-17` | `244 52% 52%` | Indigo |
| `--chart-18` | `305 50% 48%` | Orchid |
| `--chart-19` | `328 65% 50%` | Hot Pink |
| `--chart-20` | `100 52% 44%` | Moss |

**Rule:** `getCategoryColorVar({ id })` is the single source of truth for mapping category IDs to colors. Slot = `((id - 1) % 20) + 1`. All 19 seed categories get unique colors; wrapping only occurs beyond 20 categories.

### Semantic Colors in Transaction Lists

- **Income amounts:** `text-emerald-500` with `+` prefix
- **Expense amounts:** default foreground (neutral) with `-` prefix
- **Category dots:** `color-mix(in srgb, [category_color] 15%, transparent)` for tinted circles

---

## Typography

### Fonts

```ts
// tailwind.config.ts
fontFamily: {
  sans: ['"Geist Variable"', 'system-ui', 'sans-serif'],
  mono: ['"Geist Mono Variable"', 'ui-monospace', 'monospace'],
}
```

### Scale (Tailwind classes)

| Role | Classes | Usage |
|------|---------|-------|
| Page title | `text-2xl font-semibold tracking-tight` | Welcome heading |
| Card title | `text-base font-semibold tracking-tight` | ChartCard titles |
| Card description | `text-sm font-medium` + `text-muted-foreground` | KPI labels via `CardDescription` |
| KPI value | `font-mono text-2xl font-semibold tabular-nums` | Dollar amounts |
| KPI cents | `text-lg font-medium text-muted-foreground` | Decimal portion |
| Body text | `text-sm` | General content |
| Metadata | `text-xs text-muted-foreground` | Timestamps, footnotes |
| Badge text | `text-xs` inside `Badge variant="outline"` | Delta percentages |

### Rules

- All currency/numeric columns: `font-mono tabular-nums`
- No `text-transform: uppercase` in main content
- Minimum text size: `text-xs` (12px)

---

## Components

### Card (shadcn `ui/card.tsx`)

Base classes: `rounded-lg border bg-card text-card-foreground`

No shadow — depth comes from the border alone. The `* { @apply border-border; }` base rule in `globals.css` ensures all borders use the `--border` token (not `currentColor`).

```tsx
import { Card, CardHeader, CardContent, CardFooter, CardDescription } from '@/components/ui/card';
```

### KpiCard (`components/KpiCard.tsx`)

Follows the shadcn dashboard pattern:

```tsx
<KpiCard
  label="Total Balance"        // CardDescription
  dollars="$2,847"              // Integer portion
  cents=".32"                   // Decimal portion (same style as dollars)
  delta={{ percent: 3.2, direction: 'up' }}  // Badge with TrendingUp/Down
  footnote="vs last month"     // CardFooter text
/>
```

- **Label:** `CardDescription` with `text-sm font-medium`
- **Delta:** `Badge variant="outline"` with `TrendingUp`/`TrendingDown` icons (3x3)
- **Value:** `font-mono text-2xl font-semibold tabular-nums` (dollars + cents rendered as single text)
- **Footnote:** `CardFooter` > `text-xs text-muted-foreground`

No icon badges, no featured/accent variant, no gradient backgrounds.

### ChartCard (`components/ChartCard.tsx`)

Standard wrapper for chart sections:

```tsx
<ChartCard
  title="Cash Flow"
  subtitle="Income vs expenses over time"
  action={<Tabs>...</Tabs>}    // Optional right-side control
>
  <ChartContainer>...</ChartContainer>
</ChartCard>
```

### Badge (`ui/badge.tsx`)

Used for KPI deltas with `variant="outline"` and transaction tags with `variant="secondary"`:

```tsx
// KPI delta
<Badge variant="outline" className="gap-1 text-xs">
  <TrendingUp className="h-3 w-3" aria-hidden="true" />
  +3.2%
</Badge>

// Transaction tags
<Badge variant="secondary" className="mr-1 font-normal">
  {tag}
</Badge>
```

### Alert (`ui/alert.tsx`)

Used for all error callouts. Never use hand-rolled `<div>` or `<p>` error banners.

```tsx
import { Alert, AlertDescription } from '@/components/ui/alert';
import { AlertCircle } from 'lucide-react';

<Alert variant="destructive">
  <AlertCircle className="h-4 w-4" />
  <AlertDescription>{errorMessage}</AlertDescription>
</Alert>
```

### Separator (`ui/separator.tsx`)

Used for visual dividers between sections. Never use `<hr>` or `<div className="border-t">`.

```tsx
import { Separator } from '@/components/ui/separator';

<Separator className="my-6" />
```

### Skeleton (`ui/skeleton.tsx`)

Used for loading placeholders. Never use "Loading..." text.

```tsx
import { Skeleton } from '@/components/ui/skeleton';

<Skeleton className="h-[300px] w-full" />
```

### ButtonGroup (`ui/button-group.tsx`)

Used for segmented action controls (e.g. 6M/12M toggle on Cash Flow chart):

```tsx
import { ButtonGroup } from '@/components/ui/button-group';

<ButtonGroup>
  <Button variant={active === '6m' ? 'secondary' : 'outline'} size="sm" onClick={...}>6M</Button>
  <Button variant={active === '12m' ? 'secondary' : 'outline'} size="sm" onClick={...}>12M</Button>
</ButtonGroup>
```

### Avatar (`ui/avatar.tsx`)

Used for user initials display. Always include `AvatarFallback`.

```tsx
import { Avatar, AvatarFallback } from '@/components/ui/avatar';

<Avatar className="size-8 text-sm font-medium">
  <AvatarFallback>{initial}</AvatarFallback>
</Avatar>
```

---

## Layout

### Page Structure

```tsx
<div className="flex flex-col gap-6">
  {/* Header */}
  <div className="flex flex-col gap-3 md:flex-row md:items-center md:justify-between">
    <div>
      <h1 className="text-2xl font-semibold tracking-tight">Title</h1>
      <p className="mt-0.5 text-sm text-muted-foreground">Subtitle</p>
    </div>
    <div className="flex items-center gap-2">{/* Controls */}</div>
  </div>

  {/* KPI row */}
  <div className="grid grid-cols-1 gap-3 md:grid-cols-2 xl:grid-cols-4">
    <KpiCard ... />
  </div>

  {/* Full-width content sections */}
  <ChartCard>...</ChartCard>
  <Card>...</Card>
</div>
```

### Grid Gaps

- `gap-3` (12px) between cards in grids
- `gap-6` (24px) between major sections
- `gap-2` (8px) between controls

### Responsive Breakpoints

| Breakpoint | KPI grid | Content sections |
|------------|----------|-----------------|
| Default | 1 column | Full width, stacked |
| `md` (768px) | 2 columns | — |
| `xl` (1280px) | 4 columns | — |

---

## Charts

### Configuration Pattern

```tsx
const config: ChartConfig = {
  income: { label: 'Income', color: 'hsl(var(--primary))' },
  expense: { label: 'Expense', color: 'hsl(var(--muted-foreground))' },
};

<ChartContainer config={config} className="h-64 w-full">
  <ResponsiveContainer>
    <BarChart data={data}>
      <Bar dataKey="income" fill="var(--color-income)" radius={[4, 4, 0, 0]} />
    </BarChart>
  </ResponsiveContainer>
</ChartContainer>
```

shadcn's `ChartContainer` generates `--color-<key>` CSS variables from the config.

### Axis Styling

```tsx
<CartesianGrid strokeDasharray="3 3" stroke="hsl(var(--border))" vertical={false} />
<XAxis tickLine={false} axisLine={false} stroke="hsl(var(--muted-foreground))" />
<YAxis tickLine={false} axisLine={false} stroke="hsl(var(--muted-foreground))" />
```

### Category Charts

Category pie charts and trend lines use `getCategoryColorVar()` — these are the only charts that use the `chart-N` palette:

```tsx
// Pie chart
{gaugeData.map((slice) => (
  <Cell key={slice.id} fill={slice.color} />
))}

// Line chart
{categories.map((cat) => (
  <Line stroke={getCategoryColorVar({ id: cat.id })} />
))}
```

---

## Sidebar

- Collapsible: 48px / w-12 (collapsed) / 240px (expanded)
- Background: `bg-card` with right border
- Nav items: Lucide icons at 20px
- Active state: primary-tinted background

---

## Icons

**Library:** Lucide React

| Context | Size | Stroke Width |
|---------|------|-------------|
| Sidebar navigation | 20px | 1.5 |
| KPI delta badges | `h-3 w-3` (12px) | default |
| Buttons, controls | 18px | default |

### Rules

- Outline style only
- Decorative: `aria-hidden="true"`
- Interactive icon-only: `aria-label="description"`

---

## Loading & Error States

### Loading Skeletons

Use the `Skeleton` component for all loading placeholders. Never use "Loading..." text.

```tsx
// Chart loading
<Skeleton className="h-[300px] w-full" />

// Card field loading
<Card className="p-6">
  <Skeleton className="mb-3 h-3 w-1/2" />
  <Skeleton className="mb-3 h-8 w-3/4" />
  <Skeleton className="h-3 w-2/3" />
</Card>
```

### Error State

Use the `Alert` component for all error displays:

```tsx
<Alert variant="destructive">
  <AlertCircle className="h-4 w-4" />
  <AlertDescription>{error}</AlertDescription>
</Alert>
```

### Empty State

```tsx
<div className="flex flex-1 items-center justify-center p-8 text-sm text-muted-foreground">
  No data yet.
</div>
```

---

## Patterns

These are higher-level interaction patterns built on top of the shadcn primitives above. When a new screen needs one of these, reuse the approach rather than reinventing the keyboard / focus / ARIA plumbing.

### Editable cells in data tables

First introduced on the Import preview (`components/ImportPreviewTable.tsx`). Use this whenever a table row's fields should be edited in place without opening a modal or drawer.

Principles:

- **Single edit slot.** Only one cell is in edit mode at a time. Local state is a `{ rowID, field }` pair — never a per-row map of drafts. Cross-cell drafts are an unresolvable ambiguity (what does Tab commit mean when three cells are dirty?).
- **Entry points:** double-click (mouse), Enter or F2 (keyboard) from a focused cell. Enter matches Google Sheets, F2 matches Excel — either is expected.
- **Exit points:** Enter / blur commits, Escape cancels, Tab commits and advances focus natively.
- **Focus anchor:** capture the `<td>` element on edit-entry in a ref. On Enter and Escape, restore focus to the anchor so keyboard users do not land on `document.body` when the `<Input>` unmounts. Tab explicitly opts out of the anchor so the browser's native focus-advance is not clobbered.
- **`tabIndex={isEditing ? -1 : 0}` on the cell.** While an Input is mounted, the surrounding `<td>` is out of tab order — Shift+Tab from the Input lands directly on the previous editable cell instead of the enclosing cell.
- **Imperative mirror for edit state.** `useState<EditingCell>` is read by React on re-render; the Input's `onBlur` handler fires synchronously inside the same tick as `cellEl.focus()` and gets the pre-render closure value. Mirror the edit target in a `useRef` and null it synchronously in `commitEdit` / `cancelEdit` before `setEditing(null)` — that is the only signal fast enough to tell the chained blur "the edit was already handled, don't fire PATCH again."
- **ARIA:** combine the row / group context id and any per-cell error message id in `aria-describedby` via a space-separated IDREFS list. Attach the same tokens to the idle `<td>` and the active `<Input>` so screen readers announce context regardless of which state the cell is in.
- **Inline error surface:** server 400s render as a small `<p>` below the cell, marked with `id="cell-error-<rowID>-<field>"` and referenced by the Input's `aria-describedby`. Never pop a toast for a per-cell validation error — the cell is the correction surface.

### Bulk soft-destructive actions

Introduced on collision group headers in `components/ImportPreviewTable.tsx`. Use this when a button applies a reversible change (skip, archive, unpublish) to multiple items at once.

- **Embed the scope in the label.** Write `Skip all 3 in group`, not `Skip all in group`. Showing the count before the click is a UX safeguard — the user reads the destructive scope as part of the commitment.
- **Disable while pending.** Any button that fires a burst of mutations must `disabled={pendingCount > 0}` so a double-click cannot decouple the client's in-flight counter from the server's actual queue depth.
- **Amber framing for the group header.** `bg-amber-500/15` background, `border-l-2 border-l-amber-500` left rule, and an `AlertTriangle` (lucide) icon at `h-4 w-4 text-amber-500`. Amber is the attention-without-alarm register — the action is reversible, so red (`--destructive`) is too loud.
- **Skip sticky semantics.** Once the user marks a row as skipped, only an explicit un-check clears it. No upstream edit (re-mapping a category, editing a duplicate into uniqueness) should reset a row's skip state — the server is the source of truth for every row's skip flag.

### Bulk irreversible-destructive actions

Introduced on the Trash page header in `pages/Trash.tsx` (`Restore all N` / `Purge all N`). Use this when a page-level button hits a whole-surface endpoint where at least one action (purge) is irreversible.

- **Pair the reversible twin with the destructive trigger.** A page that can wipe itself should also be able to undo that wipe in one click. Ship `Restore all` and `Purge all` as siblings so the operator never feels cornered.
- **Embed the total in both labels.** Write `Restore all 42` and `Purge all 42`, not `Restore all` / `Purge all`. The count is a commitment device — the operator reads the scope as part of the click.
- **Reversible fires directly; irreversible always walks through a dialog.** `Restore all` has no confirm (you can always re-delete), but `Purge all` always routes through `ConfirmPurgeAllDialog` which echoes the count a second time in the body copy and a third in the confirm button. Three surfaces showing the same number is not redundant — it's the only reliable defense against "muscle-memory clicked the wrong button".
- **Variant matches consequence, not aesthetics.** `Restore all` is `variant="outline"` with `RotateCcw`; `Purge all` is `variant="destructive"` with `Trash2`. The visual weight is not decorative — it is the system's signal about which click you can survive.
- **Distinct accessible names for trigger vs. confirm.** The header button and the dialog's confirm button must NOT share a label. Use `Purge all N` on the trigger and `Purge all permanently` in the dialog — screen readers and integration tests both need to disambiguate "open the dialog" from "execute the destructive action".
- **Hide the buttons when the surface is empty.** No trash, no buttons. The empty state is the signal; a disabled button next to "Trash is empty" is visual noise and an accessibility hazard.
- **Cross-disable the pair.** `disabled={restoringAll || purgingAll}` on both buttons so the operator can't stack a restore and a purge into a race where the purge observes rows the restore was trying to save.

---

## Quick Reference

### Do

- Use Tailwind utility classes for all styling
- Use `hsl(var(--token))` for colors in inline styles (charts)
- Use `font-mono tabular-nums` on all numeric columns
- Use `gap-3` between cards, `gap-6` between sections
- Use neutral colors (`--primary`, `--muted-foreground`) for non-category charts
- Use `getCategoryColorVar()` for category-specific colors
- Test dark theme (default and only theme currently)

### Don't

- Use CSS Modules or standalone CSS files
- Use `chart-N` variables for non-category charts
- Use colored fills (green/red) for income/expense bars
- Add `shadow-sm` or other shadows to cards
- Use `text-transform: uppercase` in main content
- Skip loading skeletons for data sections
- Use raw hex/rgb/hsl values — always reference design tokens
