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

One deliberate exception: `--logo-badge` stores `H S% L% / A` — the alpha lives INSIDE the
token. The LogoWordmark badge needs a different tint *and* a different alpha per theme
(`--primary / 0.4` inverts across them: a 98%-white wash in dark becomes a 40% black slab in
light), and a consumer writing `hsl(var(--token) / N)` can only vary the alpha. Consumed bare
as `hsl(var(--logo-badge))`; adding a second slash-alpha to it is invalid CSS and silently
drops the declaration.

Values below are the `.dark` block's; `:root` holds the light counterpart of each (they are not
mirrored — light `--muted-foreground` is `240 3.8% 45%`, capped by its own 4.5:1 floor on
`--muted`, not by symmetry with dark's `57%`).

| Token | HSL Value (`.dark`) | Role |
|-------|---------------------|------|
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

### Partial Transparency — put the alpha in the COLOR, never on the element

A tint of a token is `hsl(var(--token) / N)` in a **class or a stylesheet**, and
`color-mix(in oklab, hsl(var(--token)) N%, transparent)` in an **inline `style`**. Both
composite to the same pixel (verified in Chrome). What is never right is
`style={{ backgroundColor: 'hsl(var(--token))', opacity: N }}`.

Why the split matters more than it looks:

- **CSS `opacity` fades the whole subtree and the element's own ring.** Reaching for it forces
  workarounds that then read as considered design — a second span so the label does not fade
  with its chip, a marker ring relocated off the faded element. The spending heatmap carried
  both. Alpha in the color touches only the background.
- **happy-dom 20.11.1 drops the slash-alpha `hsl()` from an inline style declaration
  entirely** — measured, and not because of the `var()`: literal `hsl(240 5.9% 10% / 0.5)` goes
  too, while `rgb(0 0 0 / 0.5)` survives. The property reads back as `''`, so a painted element
  is indistinguishable from an unpainted one and any test asserting "this paints nothing" passes
  for a fully painted element. Tailwind's own `bg-primary/35` is unaffected: it compiles into a
  stylesheet, which happy-dom never parses. **The rule is inline-style-specific.**

`color-mix` is already the repo's inline idiom (`CategoryBadge.tsx`, `CategoryChips.tsx`), and
mixing with `transparent` scales only the alpha whatever the interpolation space, because the
other endpoint contributes no color.

Related: `--primary` and `--ring` are the same value, so a ring drawn on a full-intensity
`--primary` surface vanishes — `ring-offset-2 ring-offset-card` is the fix. `--foreground` and
`--primary` are near-identical at both ends of both themes, so a marker ring on a `--primary`
chip measures ~1.1–3.2:1 against it and needs to sit in a card-colored gutter instead.

### Color Philosophy: Neutral-First

**Non-category charts use neutral colors only.** The `--primary` and `--muted-foreground` tokens differentiate series — never `chart-N` variables. This keeps the interface clean and reserves color for meaning.

- **Income bars:** `hsl(var(--primary))` (near-white, high contrast)
- **Expense bars:** `hsl(var(--muted-foreground))` (mid-gray, secondary)
- **YoY current year:** `hsl(var(--primary))`
- **YoY previous year:** `hsl(var(--muted-foreground))`

### Category Colors (chart-1 through chart-20)

Reserved exclusively for category-specific visualizations (spending breakdown pie chart, category trend lines). Mapped via `getCategoryColorVar()` in `lib/chart-colors.ts`.

**Two blocks, one hue family per slot.** The values below are the `.dark` block's — the single
`HSL` column describes dark mode only. `:root` (light) carries contrast-retuned values of the
same hues: each slot is deepened until it reaches **>= 5.0:1 as text on white**, because
`CategoryChips` / `CategoryBadge` paint the raw slot color as TEXT over a 12–15% self-tinted
wash. Reusing the dark values in light mode put 16 of the 20 under the floor, `--chart-8` (Gold)
worst at 1.53:1. Both halves of the invariant — the 5.0 floor
and Δhue <= 12 against the dark slot, so a category's identity does not shift between themes —
are pinned by `src/lib/chart-colors.test.ts`, which parses `globals.css` itself and runs in the
suite. Pair separation is not gate-able at 20 slots; see the comment above the light block for
`scripts/validate_palette.js` and what it can and cannot tell you.

| Token | HSL (`.dark`) | Visual |
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

- **Amounts:** sign and colour both follow the **displayed value**, never the row's type. `displayAmount(amount, type)` flips an expense's stored magnitude to negative and leaves income as-is; `formatSignedCurrency` then emits the one sign (`signDisplay: 'exceptZero'`). Displayed value `> 0` → `text-emerald-500`, otherwise default foreground. Never compose a `+`/`-` prefix onto a signing formatter — amounts are signed in storage, so a refund (negative expense) displays `+$20.00` in the inflow colour and the old `type === 'expense' ? '-' : '+'` rule renders `--$20.00`.
- **The word beside such an amount:** a sign that disagrees with the type is labelled by `AmountSignNote` — "Refund" on a negative expense, "Reversal" on a negative income — in the metadata register (`text-xs text-muted-foreground`, `gap-1.5`, `size-3.5` icon), placed **before** the figure so it is announced first.
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
- **Carve-out: chart tick labels may go below 12px.** The month axes render at
  **9px** (`MONTH_TICK` in `components/reports/utils.ts`). This is not a
  loosening of the rule for convenience — the tick font appears in BOTH terms
  of the collision condition for rotated labels, `spacing x sin(angle) >= ink
  height`, and in the gutter the label overhangs, so shrinking it is the only
  lever that buys fit and plot width at the same time. Measured at 360px: at
  12px/-30 the axes needed 66px of a 263px chart as gutter and still collided;
  at 9px/-45 they need 36px and clear every chart on the page. The alternative
  was thinning to 3-6 labels, which the owner rejected — losing the months was
  the complaint. Applies to SVG tick text only; it is not licence for 9px in
  ordinary UI copy.

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

### AmountCurrencyInput (`components/AmountCurrencyInput.tsx`)

Composite input for typing a monetary amount together with its currency. Renders a numeric `<Input>` with a currency-code suffix button that opens a Popover + Command picker. Calls `onValueChange` and `onCurrencyChange` separately so parents can hold joint `{amount, currency}` state without coupling.

Use for: transaction entry row, transaction edit row. Not used for display.

Props: `value: number`, `onValueChange: (v: number) => void`, `currency: string`, `onCurrencyChange: (code: string) => void`, `baseCode: string`, `currencies: Currency[]`, `hideInactive: boolean` (true in entry row, false in edit row so historical rows with inactive currencies round-trip), `rateFor: (code: string) => number | null`, plus optional `loading`, `error`, `disabled`, `inputRef`, `dataEntryField`, `id`.

The inner `<input>` is not self-labeled — wrap with a `<Label htmlFor={id}>` (or pass through a shadcn `FormControl` whose Radix `Slot` forwards `id={formItemId}`).

The inner `<input>` already pairs `type="number"` with `inputMode="decimal"`, so every consumer gets the phone keypad for free — see the numeric-input rule in Quick Reference. A hand-rolled amount field does not; use this component rather than re-deriving one.

```tsx
<AmountCurrencyInput
  value={amount}
  onValueChange={setAmount}
  currency={currency}
  onCurrencyChange={setCurrency}
  baseCode={baseCode}
  currencies={currencies}
  hideInactive={true}
  rateFor={rateFor}
/>
```

### AmountDisplay (`components/AmountDisplay.tsx`)

Read-only display for an already-persisted transaction amount. Renders the base-currency amount as the primary line; if the row has non-null `originalAmount` + `originalCurrency` (and the currency differs from `baseCode`), renders a muted secondary line with the original value. Never shows a `~=` prefix — the base amount came from the backend and is canonical.

Use for: transaction list rows. Not used for inputs or pre-save previews.

Props: `amount: number`, `originalAmount: number | null`, `originalCurrency: string | null`, `type: TransactionType`, `baseCode: string`, `className?: string`.

```tsx
<AmountDisplay
  amount={transaction.amount}
  originalAmount={transaction.original_amount}
  originalCurrency={transaction.original_currency}
  type={transaction.category_type}
  baseCode={baseCode}
/>
```

### CreatorLabel (`components/CreatorLabel.tsx`)

The one way to name who entered a transaction. Renders the whole attribution line: an aria-hidden `User` icon beside a `flex min-w-0 flex-1 items-center` row holding two spans — a `min-w-0 truncate` span with the `sr-only` "Entered by " prefix and the creator's display name, then a `shrink-0` span with the creator's ` @username`. The register comes from the wrapping `<p>` (`mt-0.5 flex items-center gap-1.5 text-xs text-muted-foreground`) and nothing restates it — the line is already the metadata register, and there is no lighter step that clears the contrast floor. **The `mt-0.5` is part of the contract, not a call-site concern:** a wrapper can add spacing around this line but cannot take the baked margin away, so placing it first in a block is a known 2px, not a surprise.

Three details are load-bearing, and none of them are cosmetic. **The handle is attribution, not decoration:** `display_name` has been self-service since `PATCH /api/auth/me` shipped (any role, and it is the only writable field there), so a member can set theirs to another member's exact string — and because `created_by` comes from a live JOIN, the relabel applies retroactively to every row they have ever entered and reverts on rename-back. A display name is a label, not an identity; `created_by_username` is the half nobody can collide, which is why the wire carries both and why neither is rendered alone. **The display name clips and the handle survives:** this is why there are two spans instead of one, and it is the opposite of what the obvious markup does. With both strings in a single truncating span the tail ellipsis eats `@elienop` FIRST and leaves the spoofable half standing alone — and with `MaxDisplayNameLength` at 64 against a 360px card, that is the common case, not a corner. So the name carries `min-w-0 truncate` and absorbs the shortfall, the handle is `shrink-0` and keeps its width, and `max-w-[50%]` stops the reverse starvation (`MaxUsernameLength` is 32). The percentage needs a definite basis, which is what `flex-1` on the inner row is for — a content-sized container makes the cap cyclic and clips the handle even when there is room. **The separator is a rendered space character, never `ml-*`:** a margin does not separate words for a screen reader, so a gap-only version announces "Elie@elienop" — one token, and one that reads as an email address. The space lives in the handle's own text and `whitespace-pre` is what keeps it, because a flex item is block-level and a block's leading white space is otherwise collapsed away. Either half empty suppresses the handle entirely — `""` is the wire's "the creator's user row is gone" value, a bare `@` with nothing after it is a bug, and `Unknown @elienop` names the person the line has just said it cannot name. An empty display name still renders `Unknown`, never a blank.

Use for: every surface that names a row's creator — the ledger table row and phone card, the heatmap day sheet, Trash's table and card, the QuickAdd recents panel. Not for the signed-in user: the sidebar and phone-nav footers stack `display_name` over `@username` in their own idiom.

Props: `createdBy: string`, `createdByUsername: string`. **There is no `className` prop, and that is a decision, not an oversight** — `AmountDisplay` and `AmountSignNote` both expose one, because a caller legitimately needs to align a figure against its neighbours. This line has no such need and a worse failure mode: six hand-copied versions of this markup existed before the component did, and the escape hatch is how they drift apart again. Worse, the classes a caller would most want to reach for (`truncate`, `shrink`, `max-w-*`) are the exact ones the clipping order depends on. A surface needing different spacing takes a wrapper, not a prop.

`title` carries the untruncated "Name @handle" for the desktop tables, where this line shares the description cell — the slack column (`w-full max-w-0`), so it clips rather than widening the table. Mirrors the description's own `title` one line up. It is dead on touch — which is why the clipping order above is the fix and the tooltip is a bonus.

```tsx
<CreatorLabel
  createdBy={transaction.created_by}
  createdByUsername={transaction.created_by_username}
/>
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

A plain axis — no month labels, so no rotation and no gutter to reserve:

```tsx
<CartesianGrid strokeDasharray="3 3" stroke="hsl(var(--border))" vertical={false} />
<XAxis tickLine={false} axisLine={false} stroke="hsl(var(--muted-foreground))" />
<YAxis tickLine={false} axisLine={false} stroke="hsl(var(--muted-foreground))" />
```

### Month Axes — use the shared constants, do not hand-roll

**A month axis needs four props that the snippet above does not have.** Copying
the plain recipe for a month axis reproduces the exact collision this app spent
a batch of commits removing: labels overlapping at 360px, and `Mar'26` clipped
off the left edge because a rotated end-anchored label hangs past its tick.

```tsx
import {
  monthAxisInterval,
  MONTH_TICK,
  MONTH_TICK_ANGLE,
  ROTATED_MONTH_TICK_PADDING,            // no <YAxis> on this chart
  ROTATED_MONTH_TICK_PADDING_INSIDE_YAXIS, // chart already has a <YAxis>
} from '@/components/reports/utils';

<XAxis
  dataKey="month"
  tickFormatter={formatMonthTick}
  tickLine={false}
  axisLine={false}
  stroke="hsl(var(--muted-foreground))"
  angle={MONTH_TICK_ANGLE}
  tick={MONTH_TICK}
  textAnchor="end"
  padding={ROTATED_MONTH_TICK_PADDING}
  interval={monthAxisInterval(data.length)}
/>
```

Pick the padding constant by whether the chart renders a `<YAxis>`: its 80px
width IS the left gutter, so a chart that has one must use the `_INSIDE_YAXIS`
variant or it pays for the gutter twice. The right half is the same in both and
is set by recharts' footprint check on the last tick, not by the ink overhang —
shrinking it collapses `equidistantPreserveEnd` to a single label.

The measurements behind every number are in the docblocks in
`components/reports/utils.ts`, and the wiring is pinned by
`components/reports/chartAxis.seam.test.ts` — add a new month axis and that test
requires it to carry these props.

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
- **Amber framing for the group header.** `bg-amber-500/15` background, `border-l-2 border-l-amber-500` left rule, and an `AlertTriangle` (lucide) icon at `size-4 shrink-0 text-amber-500`. Amber is the attention-without-alarm register — the action is reversible, so red (`--destructive`) is too loud. `size-4` rather than `h-4 w-4` (one token, one conflict group) and `shrink-0` because the header is a flex row whose label wraps — without it the icon is the thing that gives way, and an ellipsed triangle is not a warning.
- **Never an actionless bar.** If the header says N rows need attention, it carries at least one button. Some remedies live elsewhere (a currency added in Settings, a sheet corrected and re-uploaded) and the automated fixes then do not apply — the bar still offers the skip, because the status line's "fix or skip" has to be true from where the user is standing.
- **Place focus when the burst ends.** A bulk action can unmount the button that fired it, which drops focus to `document.body`. End the burst by focusing the bar's own heading (`tabIndex={-1}`) if it survived, or the surface's primary action if it did not. Never focus an `aria-live` region — screen readers then announce it twice, and it is not a place anyone can act from.
- **Skip sticky semantics.** Once the user marks a row as skipped, only an explicit un-check clears it. No upstream edit (re-mapping a category, editing a duplicate into uniqueness) should reset a row's skip state — the server is the source of truth for every row's skip flag.

### Bulk irreversible-destructive actions

Introduced on the Trash page in `pages/Trash.tsx` (`Restore all N` / `Purge all N`, rendered into the table toolbar via `PaginationBar`'s `leadingActions` slot). Use this when a page-level button hits a whole-surface endpoint where at least one action (purge) is irreversible.

- **Pair the reversible twin with the destructive trigger.** A page that can wipe itself should also be able to undo that wipe in one click. Ship `Restore all` and `Purge all` as siblings so the operator never feels cornered.
- **Embed the total in both labels.** Write `Restore all 42` and `Purge all 42`, not `Restore all` / `Purge all`. The count is a commitment device — the operator reads the scope as part of the click.
- **Co-locate with the table's other scope controls.** The pair lives inline with `Rows per page` in the table toolbar (separated by a `border-l pl-3` divider), not above the card. "How many rows am I looking at?" and "act on all of them" are the same conceptual question, and keeping them in one strip avoids a second header competing for attention.
- **Reversible fires directly; irreversible always walks through a dialog.** `Restore all` has no confirm (you can always re-delete), but `Purge all` always routes through `ConfirmPurgeAllDialog` which echoes the count a second time in the body copy and a third in the confirm button. Three surfaces showing the same number is not redundant — it's the only reliable defense against "muscle-memory clicked the wrong button".
- **Variant matches consequence, not aesthetics.** `Restore all` is `variant="outline"` with `RotateCcw`; `Purge all` is `variant="destructive"` with `Trash2`. The visual weight is not decorative — it is the system's signal about which click you can survive.
- **Distinct accessible names for trigger vs. confirm.** The toolbar button and the dialog's confirm button must NOT share a label. Use `Purge all N` on the trigger and `Purge all permanently` in the dialog — screen readers and integration tests both need to disambiguate "open the dialog" from "execute the destructive action".
- **Hide the buttons when the surface is empty.** No trash, no buttons. The empty state is the signal; a disabled button next to "Trash is empty" is visual noise and an accessibility hazard.
- **Cross-disable the pair.** `disabled={restoringAll || purgingAll}` on both buttons so the operator can't stack a restore and a purge into a race where the purge observes rows the restore was trying to save.

### Show-once secret reveal

Introduced on **Settings → API tokens** (`pages/Settings.tsx`, `ApiTokensSection` → `ShowOnceReveal`). Use this whenever the server returns a plaintext secret that will never be retrievable again (API tokens, backup signing keys, one-time recovery codes). The pattern guarantees that the plaintext lives in the render tree for exactly one user-visible moment and is unreachable after the dialog closes.

- **The secret lives in local component state, not a ref or a context.** A `useState<string | null>` holds the plaintext for the duration of the dialog; the parent component's `onOpenChange` handler sets it back to `null` on close. React's unmount handling removes the DOM node carrying the plaintext as soon as the state flips, so a subsequent re-open of the dialog starts empty. Caching the plaintext in a ref or context would survive the unmount and defeats the whole point.
- **Dialog `open` flows one state source, not two.** `<Dialog open={createOpen}>` — not `open={createOpen || revealed !== null}`. Two sources compose into double-fire `onOpenChange` calls (opening to reveal re-fires open even though we're already open). Pick one source of truth; in the API tokens flow it is the boolean the Create button flips.
- **Input is `readOnly`, not `disabled`.** `readOnly` lets the user click into the field, select-all, and copy via keyboard shortcut. `disabled` defeats keyboard copy on Safari and screen readers, both of which refuse to read out a disabled form field's `value`.
- **Lead with an `Alert variant="warning"`, not a raw styled div.** The reveal uses `Alert variant="warning"` with `AlertTriangle` + `AlertTitle` ("Copy it now") + `AlertDescription` ("We hash tokens at rest. If you lose this one, you'll have to revoke it and create a new one."). Semantic tokens (amber-500/50 border, amber-50/amber-950 surface) keep the banner theme-aware without hand-rolling dark-mode overrides.
- **DialogTitle is directive, not status.** "Save your new token" — imperative, tells the user what to do right now. Not "Token created" (status, done) or "API token ready" (ambiguous). DialogDescription states the one-shot constraint ("This is the only time your token will be shown.") so the warning Alert below doesn't have to duplicate it.
- **Footer button label acknowledges the action, not the dialog.** "I've saved my token" — a commitment, not a dismissal. Not "Done" or "Close"; those let the user tab-through without engaging with the fact the plaintext is gone after close.
- **Primary copy button uses `navigator.clipboard.writeText` with a toast.** Success toast: `Copied to clipboard`. On rejection (insecure context, permission denial), focus + select the reveal `<Input>` and fire `Press Ctrl/Cmd+C to copy — clipboard blocked in this context.` so a keyboard copy still works. The button does not mutate its own label ("Copied!") because a label that flickers between states is a visual-bug vector on unmount.
- **Regression-locked by a test.** `Settings.apiTokens.test.tsx` seeds one create flow, closes the reveal dialog, re-opens the create button, and asserts the plaintext is absent from the DOM. Any future refactor that hoists the plaintext into a cache, memo, or parent ref fails this test. Do not ship a new show-once flow without the same regression lock.
- **Never log the plaintext.** Not to `console.log`, not to `toast.info`, not to any error path. A logged plaintext is as bad as a stored plaintext.

### API tokens settings layout

Introduced on `pages/Settings.tsx` → `ApiTokensSection` as the sixth tab (`api-tokens`). Reuse these choices when adding any future "list of credentials / integrations" surface.

- **Card header is title + description, not title alone.** `CardTitle` = "API tokens"; `CardDescription` = "Personal access tokens for scripts, dashboards, and third-party integrations. Each token has full access to your account." The description stays generic — no single integration (Homepage, n8n, curl) is elevated to first-class in the UI copy, because the token is general-purpose.
- **Table columns: Name, Token, Last used, Expires, Created, Actions.** The token cell uses `font-mono` on the `spdr_` prefix. Last-used uses `formatDistanceToNowStrict` from `date-fns` with `"Never used"` as the null placeholder (screen readers treat an em-dash as silence; "never used" is a meaningful distinction).
- **Revoke uses `AlertDialog`, not `Dialog`.** Revoke is an irreversible destructive confirmation — the semantic primitive is `AlertDialog` (ARIA `role="alertdialog"`, escape/enter bound by Radix). The action button carries `bg-destructive text-destructive-foreground hover:bg-destructive/90` and calls `e.preventDefault()` inside `onClick` so the dialog stays open while the DELETE is in flight, exposing the "Revoking…" label until the mutation resolves.
- **Dialog title quotes the token name in `font-mono`.** `Revoke "<Homepage dashboard>"?` — the monospace span makes it unambiguous which row is about to die. A `revokingTokenName` ref (sticky across the Radix close-exit animation) prevents the title from flashing `Revoke ""?` mid-animation.
- **Revoke-all lives on the section header, not per-row.** Title "Revoke all tokens?" / body "Every script or dashboard you've connected will stop working until you create new tokens. This cannot be undone." Success toast counts the server-reported revoked rows: ``Revoked ${n} token${n === 1 ? '' : 's'}`` — not a hard-coded "All tokens revoked", because another tab or background expiry may have changed the set.
- **No scopes UI, no password re-confirm.** Every token has full account access; adding scopes is a separate design problem. The backend does not require a password re-confirm on create (Bearer is rate-limited via the shared auth-fail limiter); the create form is name + expiry only.
- **Create button is "Create token", not "+ New token".** The `+` prefix implied a plural-of-many and is an icon-as-text antipattern; `data-icon` or a real `Plus` icon is the correct pattern if an icon is ever needed here.
- **Empty state is plain prose, no illustration.** "No API tokens yet. Create one to connect a script, dashboard, or other tool to SpenDrop." Generic wording — no single integration is named. Matches other settings-tab empty states (Savings with no goals, Currencies before the base is set).

### Bulk-edit dialog pattern

The Transactions bulk-edit dialog uses the `'noChange'` sentinel pattern (Lidarr-derived):

- shadcn `Select` first option is `— No change —`.
- Free-form inputs (text) default to empty + placeholder `— Keep same —`.
- Native `<input type="date">` ignores `placeholder`, so a leading "Set date" `<Checkbox>` gates the date picker — unchecked = no change.
- Multi-value fields (tags) get an additional Add / Remove / Replace radio above the input. The radio is disabled while the input is empty to prevent the "I selected Replace but the input is empty" footgun.

Layout: `grid grid-cols-1 md:grid-cols-[120px_1fr_140px_1fr]` collapses to a vertical stack below the `md:` breakpoint.

The confirm AlertDialog (all-matching scope only) uses the **default primary** palette, NOT `destructiveActionClass` — bulk-edit is recoverable from the audit trail, unlike `delete-by-filter`.

---

## Quick Reference

### Do

- Use Tailwind utility classes for all styling
- Use `hsl(var(--token))` for colors in inline styles (charts)
- Use `color-mix(in oklab, hsl(var(--token)) N%, transparent)` for a runtime tint in an
  inline style — see Partial Transparency; never an element `opacity`
- Use `font-mono tabular-nums` on all numeric columns
- Use `gap-3` between cards, `gap-6` between sections
- Use neutral colors (`--primary`, `--muted-foreground`) for non-category charts
- Use `getCategoryColorVar()` for category-specific colors
- Name a row's creator with `<CreatorLabel>`, never a bare `created_by`. Display names are
  self-service, so one member can wear another's; only the `@username` beside it identifies
  who entered the row. Never hand-roll the line — the DISPLAY NAME is the half that clips
  and the handle is `shrink-0` so it survives, and the separator is a rendered space (a
  margin announces "Elie@elienop")
- Pair every `type="number"` `<Input>` with an `inputMode` — `decimal` for money and rates,
  `numeric` for integer years and counts. `type` says what the value is, `inputMode` says which
  soft keypad opens for it, and Android does not reliably infer one from the other (checked on
  the household S24). A missing hint is drift; the reasoning lives at `<MonthlyBudgetCard>` in
  `Budgets.tsx`. A shared cell editor serving several columns derives the hint per field rather
  than putting it on the element — see `ImportPreviewTable`
- Test BOTH themes. Dark is the default (`ThemeProvider defaultTheme="dark"`, `<html class="dark">`),
  but `ModeToggle` sits in the sidebar and the phone nav, so light is one tap away from every
  screen — and the two blocks in `globals.css` no longer hold the same chart values

### Don't

- Use CSS Modules or standalone CSS files
- Put an alpha on the ELEMENT (`opacity`) when you mean a translucent color
- Use `chart-N` variables for non-category charts
- Use colored fills (green/red) for income/expense bars
- Add `shadow-sm` or other shadows to cards
- Use `text-transform: uppercase` in main content
- Skip loading skeletons for data sections
- Use raw hex/rgb/hsl values — always reference design tokens
