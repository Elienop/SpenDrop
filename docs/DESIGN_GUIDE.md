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
- **Savings filled:** `hsl(var(--primary))`
- **Savings remaining:** `hsl(var(--muted))`
- **YoY current year:** `hsl(var(--primary))`
- **YoY previous year:** `hsl(var(--muted-foreground))`

### Category Colors (chart-1 through chart-11)

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

**Rule:** `getCategoryColorVar({ id })` is the single source of truth for mapping category IDs to colors. Slot = `((id - 1) % 11) + 1`.

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
| Page title | `text-2xl font-bold tracking-tight` | Welcome heading |
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
  dollars="$2,847"              // Main value
  cents=".32"                   // Muted decimal
  delta={{ percent: 3.2, direction: 'up' }}  // Badge with TrendingUp/Down
  footnote="vs last month"     // CardFooter text
/>
```

- **Label:** `CardDescription` with `text-sm font-medium`
- **Delta:** `Badge variant="outline"` with `TrendingUp`/`TrendingDown` icons (3x3)
- **Value:** `font-mono text-2xl font-semibold tabular-nums`
- **Cents:** `text-lg font-medium text-muted-foreground`
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

Used for KPI deltas with `variant="outline"`:

```tsx
<Badge variant="outline" className="gap-1 text-xs">
  <TrendingUp className="h-3 w-3" aria-hidden="true" />
  +3.2%
</Badge>
```

---

## Layout

### Page Structure

```tsx
<div className="flex flex-col gap-6">
  {/* Header */}
  <div className="flex flex-col gap-3 md:flex-row md:items-center md:justify-between">
    <div>
      <h1 className="text-2xl font-bold tracking-tight">Title</h1>
      <p className="mt-0.5 text-sm text-muted-foreground">Subtitle</p>
    </div>
    <div className="flex items-center gap-2">{/* Controls */}</div>
  </div>

  {/* KPI row */}
  <div className="grid grid-cols-1 gap-3 md:grid-cols-2 xl:grid-cols-4">
    <KpiCard ... />
  </div>

  {/* Content grid */}
  <div className="grid grid-cols-1 gap-3 lg:grid-cols-5">
    <ChartCard className="lg:col-span-3">...</ChartCard>
    <Card className="lg:col-span-2">...</Card>
  </div>
</div>
```

### Grid Gaps

- `gap-3` (12px) between cards in grids
- `gap-6` (24px) between major sections
- `gap-2` (8px) between controls

### Responsive Breakpoints

| Breakpoint | KPI grid | Content grid |
|------------|----------|-------------|
| Default | 1 column | 1 column |
| `md` (768px) | 2 columns | — |
| `lg` (1024px) | — | 5-column grid |
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

- Collapsible: 64px (collapsed) / 240px (expanded)
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

Every data section needs a skeleton:

```tsx
<Card className="p-6">
  <Skeleton className="mb-3 h-3 w-1/2" />
  <Skeleton className="mb-3 h-8 w-3/4" />
  <Skeleton className="h-3 w-2/3" />
</Card>
```

### Error State

```tsx
<Card className="flex flex-col items-center gap-4 p-12 text-center" role="alert">
  <p className="text-muted-foreground">{error}</p>
  <Button onClick={() => window.location.reload()}>Retry</Button>
</Card>
```

### Empty State

```tsx
<div className="flex flex-1 items-center justify-center p-8 text-sm text-muted-foreground">
  No data yet.
</div>
```

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
