# SpenDrop Design Guide

A practical reference for building pages that match the v3 dashboard style. Every value in this guide is extracted from the production dashboard implementation — use it as the single source of truth.

---

## Token Architecture

### File: `web/src/styles/tokens.css`

This is the **only file** allowed to contain raw color values (hex, rgb, hsl). Every other CSS file must use `var()` references.

### Two-Tier Model

```
Primitives (raw values)     Semantic tokens (contextual meaning)
--neutral-1: #111113   -->  --surface-base: var(--neutral-1)
--accent-7: #5347CE    -->  --color-primary: var(--accent-7)
--coral-dim: #EF8B6E   -->  --color-expense: var(--coral-dim)
```

**Rule:** Component `.module.css` files only use semantic tokens. Never reference primitives like `--neutral-7` directly.

---

## Color System

### Brand Colors

| Role | Token | Hex | Usage |
|------|-------|-----|-------|
| Primary | `--color-primary` | `#5347CE` | Buttons, links, active states, featured cards |
| Primary hover | `--color-primary-hover` | `#6E60E8` | Button/link hover states |
| Expense | `--color-expense` | `#EF8B6E` | Negative amounts, over-budget indicators |
| Income | `--color-income` | `#22C55E` | Positive amounts, savings indicators |
| Warning | `--color-warning` | `#F0C84D` | Budget approaching limit |
| Info | `--color-info` | `#4896FE` | Informational badges |
| Error | `--color-error` | `#EF8B6E` | Form validation, destructive actions |

### Positive / Negative Indicators

Dedicated green and coral for small indicators **only** — badges, percentage text, delta arrows. Do NOT use for large chart fills or backgrounds.

| Token | Value | Usage |
|-------|-------|-------|
| `--color-pos` | `#22C55E` | Income badges, positive deltas |
| `--color-neg` | `#EF8B6E` | Expense badges, negative deltas |

### Category Color Scale

11-step gradient (purple → gold) for chart segments, category badges, and progress bars:

| Token | Color | Name |
|-------|-------|------|
| `--cat-1` | `#4030A6` | Indigo |
| `--cat-2` | `#5347CE` | Purple |
| `--cat-3` | `#B794D8` | Orchid |
| `--cat-4` | `#7B8AFE` | Periwinkle |
| `--cat-5` | `#4896FE` | Blue |
| `--cat-6` | `#2DB3D9` | Cyan |
| `--cat-7` | `#16C8C7` | Teal |
| `--cat-8` | `#3EBD80` | Emerald |
| `--cat-9` | `#7EB854` | Sage |
| `--cat-10` | `#C4B83A` | Olive |
| `--cat-11` | `#F0C84D` | Gold |
| `--cat-muted` | `#B8BCC8` | Neutral/other |

### Opacity Utilities

Use `color-mix()` — never raw `rgba()`:

| Token | Value | Usage |
|-------|-------|-------|
| `--primary-a8` | primary at 8% | Row hover, focus ring |
| `--primary-a15` | primary at 15% | Active nav item, selected state |
| `--primary-a50` | primary at 50% | Focus ring glow |
| `--expense-a15` | expense at 15% | Error/expense background tint |
| `--income-a15` | income at 15% | Success/income background tint |

---

## Surfaces & Elevation

### Dark Theme (Default)

Uses tonal surface progression for depth, minimal shadows:

| Token | Value | Usage |
|-------|-------|-------|
| `--surface-base` | `#111113` | Page background |
| `--surface-raised` | `#19191B` | Cards, sidebar, sections |
| `--surface-overlay` | `#222225` | Dropdowns, modals, tooltips |
| `--surface-sunken` | `#0C0C0E` | Input backgrounds, progress tracks |
| `--surface-hover` | `#2A2A2D` | Hover state on surfaces |

### Light Theme

Clean cool-gray page background with crisp white cards:

| Token | Value | Usage |
|-------|-------|-------|
| `--surface-base` | `#EAEBF2` | Page background (cool lavender-gray) |
| `--surface-raised` | `#FFFFFF` | Cards, sidebar, sections |
| `--surface-overlay` | `#FFFFFF` | Dropdowns, modals |
| `--surface-sunken` | `#EAEBF2` | Input backgrounds, progress tracks |
| `--surface-hover` | `#EAEBF2` | Hover state on surfaces |

### Glass Surface (Dashboard Cards)

Frosted-glass effect for cards — provides depth via semi-transparency + backdrop blur:

```css
.card {
  background: var(--glass-bg);           /* rgba(255,255,255,0.85) in light */
  backdrop-filter: blur(var(--glass-blur));
  -webkit-backdrop-filter: blur(var(--glass-blur));
  border: 1px solid var(--glass-border); /* rgba(255,255,255,0.4) in light */
  border-radius: 14px;
  padding: 24px;
  box-shadow: var(--shadow-sm);
}
```

| Token | Dark | Light |
|-------|------|-------|
| `--glass-bg` | `raised @ 85%` | `rgba(255,255,255,0.85)` |
| `--glass-border` | `white @ 6%` | `rgba(255,255,255,0.4)` |
| `--glass-blur` | `12px` | `12px` |

### Text Tokens

| Token | Dark | Light | Usage |
|-------|------|-------|-------|
| `--text-primary` | `#EEEEF0` | `#111827` | Body text, headings, values |
| `--text-secondary` | `#B0B0BA` | `#6B7280` | Labels, metadata, nav items |
| `--text-tertiary` | `#6E6E79` | `#9CA3AF` | Placeholders, helper text, "vs" text |
| `--text-inverse` | `#111113` | `#EEEEF0` | Text on colored backgrounds |
| `--text-on-accent` | `#EEEEF0` | `#EEEEF0` | Text on `--color-primary` bg |

### Borders

| Token | Dark | Light | Usage |
|-------|------|-------|-------|
| `--border-default` | `--neutral-6` | `#D8DAE5` | Select borders, dividers |
| `--border-muted` | `--neutral-4` | `#E0E3ED` | Segmented controls, subtle dividers |

### Shadows

| Token | Light Value | Usage |
|-------|------------|-------|
| `--shadow-sm` | `0 1px 2px rgba(0,0,0,0.04)` | Card default, segmented active tab |
| `--shadow-md` | `0 2px 8px rgba(0,0,0,0.06)` | Card hover, elevated elements |
| `--shadow-lg` | `0 4px 16px rgba(0,0,0,0.08)` | Dropdown menus, overlays |
| `--shadow-xl` | `0 16px 48px rgba(0,0,0,0.12)` | Modals, dialogs |

---

## Typography

**Font:** Inter Variable (self-hosted via `@fontsource-variable/inter`)

**OpenType features:** `cv02` (straight a), `cv03` (open 6), `cv04` (open 9), `cv11` (single-storey l)

### Type Scale

| Role | Size | Weight | Line-height | Usage |
|------|------|--------|-------------|-------|
| `display` | 36px | 700 | 40px | Dashboard hero values (not currently used) |
| `heading-lg` | 24px | 600 | 32px | Page titles, welcome heading |
| `heading-md` | 20px | 600 | 28px | Section headers |
| `heading-sm` | 16px | 600 | 24px | Card titles |
| `body-lg` | 16px | 400 | 24px | Form inputs, primary content |
| `body-md` | 14px | 400 | 20px | Table data, descriptions, nav labels |
| `body-sm` | 12px | 400 | 16px | Timestamps, metadata |
| `label-lg` | 13px | 500 | 20px | Buttons, select text, nav labels |
| `label-md` | 12px | 500 | 16px | Chips, badges, form labels, legend items |
| `label-sm` | 11px | 500 | 16px | Overlines, section labels, percentage text |
| `amount` | 16px | 500 | 24px | Currency values (with `tabular-nums`) |

### Dashboard-Specific Typography

These values are used directly on the dashboard and should be adopted on any page with financial data:

| Element | Size | Weight | Extra |
|---------|------|--------|-------|
| Page title | 24px | 700 | `letter-spacing: -0.03em` |
| KPI value | 30px | 700 | `letter-spacing: -0.03em; line-height: 1` |
| KPI decimal | 18px | 500 | `color: var(--text-tertiary)` |
| Card title | 15px | 600 | `letter-spacing: -0.01em` |
| Card subtitle | 12px | – | `color: var(--text-tertiary)` |
| KPI label | 13px | 500 | `color: var(--text-secondary)` |
| Badge text | 12px | 600 | Inside colored badge pill |
| "vs last month" | 12px | – | `color: var(--text-tertiary)` |
| Gauge total | 30px | 700 | Same as KPI value |
| Gauge sub-label | 11px | – | `color: var(--text-tertiary)` |

### Rules

- Only weights: 400 (regular), 500 (medium), 600 (semibold), 700 (bold — page titles + KPI values only)
- No `text-transform: uppercase` except sidebar section labels (`11px/600/uppercase/0.06em`)
- Currency columns must use `font-variant-numeric: tabular-nums`
- Minimum text size: 11px

---

## Spacing

4px base unit. Token name = multiplier.

| Token | Value | Usage |
|-------|-------|-------|
| `--space-1` | 4px | Icon gaps, tight padding |
| `--space-2` | 8px | Small gaps, badge padding |
| `--space-3` | 12px | Grid gap, category row gap, form field padding |
| `--space-4` | 16px | Standard padding, section spacing |
| `--space-5` | 20px | Card header margin-bottom |
| `--space-6` | 24px | Card padding, sidebar section padding |
| `--space-8` | 32px | Page vertical padding, header margin-bottom |
| `--space-10` | 40px | Page horizontal padding |
| `--space-12` | 48px | Major divisions, error state padding |
| `--space-16` | 64px | Sidebar collapsed width |

---

## Border Radius

| Token | Value | Usage |
|-------|-------|-------|
| `--radius-sm` | 4px | Tooltips |
| `--radius-md` | 8px | Selects, segmented controls, nav items, badges |
| `--radius-lg` | 12px | Secondary cards |
| 14px (hardcoded) | 14px | Dashboard glass cards, KPI cards |
| `--radius-xl` | 16px | Large cards |
| `--radius-2xl` | 28px | Modals/dialogs |
| `--radius-full` | 9999px | Pill buttons, avatars, progress bars |

**Note:** Dashboard cards use `14px` directly — this is the standard card radius for the v3 design.

---

## Motion

| Token | Curve | Usage |
|-------|-------|-------|
| `--ease-standard` | `cubic-bezier(0.2, 0, 0, 1)` | General interactions |
| `--ease-decelerate` | `cubic-bezier(0.05, 0.7, 0.1, 1)` | Elements entering |
| `--ease-accelerate` | `cubic-bezier(0.3, 0, 0.8, 0.15)` | Elements exiting |

| Token | Value | Usage |
|-------|-------|-------|
| `--duration-fast` | 150ms | Hover, button press, tooltips |
| `--duration-normal` | 250ms | Dropdowns, modals |
| `--duration-slow` | 350ms | Sidebar expand/collapse |
| `--duration-page` | 400ms | Page transitions |

### Interaction Transitions

```css
/* Card hover — lift + shadow */
transition: box-shadow 0.2s, transform 0.2s;
/* On hover: */
box-shadow: var(--shadow-md);
transform: translateY(-1px);

/* Row hover — background fade */
transition: background 0.15s;
/* On hover: */
background: var(--surface-hover);

/* Link/button hover — color fade */
transition: color 0.15s, background-color 0.15s;
```

---

## Layout

### App Shell

```css
.main {
  padding: var(--space-8) var(--space-10) var(--space-8) calc(64px + var(--space-10));
  max-width: calc(1400px + 64px);
}
/* With expanded sidebar: */
.mainExpanded {
  padding-left: calc(240px + var(--space-10));
  max-width: calc(1400px + 240px);
}
```

### Page Structure

Every page follows this layout:

```
.page (flex column, gap: 0)
  └── .header (flex, space-between, margin-bottom: 32px)
  │     ├── left: title + subtitle
  │     └── right: selectors / controls
  └── .contentGrid (grid or main content area)
```

### Content Grid (Dashboard)

```css
.contentGrid {
  display: grid;
  grid-template-columns: 3fr 2fr;
  gap: 12px;
}
```

Cards inside can span full width with `grid-column: span 2`.

### Responsive Breakpoints

```css
@media (width <= 1200px) {
  /* KPI row: 2 columns instead of 4 */
  /* Content grid: single column */
}

@media (width <= 768px) {
  /* KPI row: single column */
  /* Header: stack vertically */
  /* Card headers: stack vertically */
}
```

---

## Component Patterns

### Glass Card

The standard card pattern for data sections:

```css
.card {
  background: var(--glass-bg);
  backdrop-filter: blur(var(--glass-blur));
  -webkit-backdrop-filter: blur(var(--glass-blur));
  border: 1px solid var(--glass-border);
  border-radius: 14px;
  padding: 24px;
  box-shadow: var(--shadow-sm);
  transition: box-shadow 0.2s;
}
.card:hover {
  box-shadow: var(--shadow-md);
}
```

### Card Header

Standard header inside every card:

```css
.cardHeader {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 20px;
}
.cardTitle {
  font-size: 15px;
  font-weight: 600;
  color: var(--text-primary);
  letter-spacing: -0.01em;
}
.cardSubtitle {
  font-size: 12px;
  color: var(--text-tertiary);
  margin-top: 2px;
}
.cardLink {
  font-size: 13px;
  font-weight: 500;
  color: var(--color-primary);
  /* "View all →" style link */
}
```

### Featured Card (Accent-Filled)

Used for the primary KPI (Total Balance):

```css
.featured {
  background: var(--color-primary);
  border-color: transparent;
}
.featured .kpiLabel { color: rgba(255, 255, 255, 0.7); }
.featured .kpiValue { color: #fff; }
.featured .kpiDecimal { color: rgba(255, 255, 255, 0.5); }
.featured .kpiVs { color: rgba(255, 255, 255, 0.6); }
.featured .kpiIcon { background: rgba(255, 255, 255, 0.15); color: #fff; }
.featured .kpiBadge { color: #fff; background: rgba(255, 255, 255, 0.15); }
```

### KPI Card

Four-across row for key metrics:

```css
.kpiRow {
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  gap: 12px;
  margin-bottom: 12px;
}
.kpiCard {
  /* Same glass-card base */
  padding: 22px 24px;
  gap: 14px;
  border-radius: 14px;
}
```

**Structure:**
```
.kpiCard
  ├── .kpiTop (flex, space-between)
  │     ├── .kpiLabel (13px/500, text-secondary)
  │     └── .kpiIcon (36px square, 10px radius, themed bg+fg)
  ├── .kpiValue (30px/700, letter-spacing -0.03em)
  │     └── .kpiDecimal (18px/500, text-tertiary)
  └── .kpiFooter (flex, gap: 8px, 12px text)
        ├── .kpiBadge (pill: 2px 8px, 6px radius, 12px/600)
        │     └── <ArrowUpRight/ArrowDownRight size={12}> + "4.2%"
        └── .kpiVs ("vs last month", text-tertiary)
```

**KPI Icon Badges** (light theme):

| Variant | Background | Foreground |
|---------|-----------|------------|
| Purple | `#F0EEFF` | `--accent-7` |
| Teal | `#E6FAF9` | `#16C8C7` |
| Red | `#F0EEFF` | `--accent-6` |
| Yellow | `#FDF8E8` | `#C9A030` |
| Blue | `#EEF4FF` | `#4896FE` |

### Currency Display (Split Formatting)

Dollar amounts use a split format with smaller decimals:

```tsx
function splitCurrency(amount: number): { dollars: string; cents: string } {
  const abs = Math.abs(amount);
  const dollars = Math.floor(abs).toLocaleString('en-US');
  const cents = (abs % 1).toFixed(2).slice(1); // ".52"
  return { dollars: `$${dollars}`, cents };
}

// Render:
<div className={styles.kpiValue}>
  {balanceSplit.dollars}
  <span className={styles.kpiDecimal}>{balanceSplit.cents}</span>
</div>
```

### Selects (Dropdown Controls)

Clean pill-button style selects:

```css
.select {
  height: 36px;
  padding: 0 26px 0 12px;
  background-color: var(--surface-raised);
  border: 1px solid var(--border-default);
  border-radius: 8px;
  color: var(--text-primary);
  font-size: 13px;
  font-weight: 500;
  font-family: var(--font-sans);
  cursor: pointer;
  appearance: none;
  /* Custom chevron SVG */
  background-image: url("data:image/svg+xml,...");
  background-repeat: no-repeat;
  background-position: right 10px center;
}
.select:hover {
  border-color: var(--text-tertiary);
}
.select:focus {
  outline: none;
  border-color: var(--color-primary);
  box-shadow: 0 0 0 3px var(--primary-a8);
}
```

**With labels:**
```tsx
<span className={styles.selectLabel}>Month</span>
<select className={styles.select}>...</select>
```

### Segmented Toggle (Pill Tabs)

Used for chart view switching (6M / 1Y):

```css
.cfToggle {
  display: flex;
  gap: 2px;
  background: var(--surface-sunken);
  border: 1px solid var(--border-muted);
  border-radius: 8px;
  padding: 3px;
}
.cfToggleBtn {
  padding: 4px 14px;
  border: none;
  background: none;
  font-size: 12px;
  font-weight: 500;
  color: var(--text-tertiary);
  border-radius: 6px;
  cursor: pointer;
}
.cfToggleBtnActive {
  background: var(--surface-raised);
  color: var(--text-primary);
  box-shadow: var(--shadow-sm);
}
```

### Data Row (Transactions)

Row layout for list items with icon + info + right-aligned data:

```css
.txRow {
  display: flex;
  align-items: center;
  gap: 12px;
  border-bottom: 1px solid var(--surface-sunken);
  padding: 10px 4px;
  margin: 0 -4px;
  border-radius: 8px;
  transition: background 0.15s;
  cursor: pointer;
}
.txRow:hover { background: var(--surface-hover); }
```

**Icon circle:**
```css
.txIcon {
  width: 38px;
  height: 38px;
  border-radius: 50%;
  display: grid;
  place-items: center;
  /* Dynamic: background = color-mix(in srgb, [category_color] 15%, transparent) */
  /* Dynamic: color = [category_color] */
}
```

**Amount coloring:**
- Expenses: `color: var(--text-primary)` (neutral, with `-` prefix)
- Income: `color: var(--color-pos)` (green, with `+` prefix)

### Category Row (Compact List)

Used in category breakdowns:

```css
.catRow {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 4px;
  margin: 0 -4px;
  border-radius: 6px;
  cursor: pointer;
}
/* Children: */
.catDot     /* 10px square, 3px radius — color swatch */
.catName    /* 13px/500, text-secondary, flex: 1 */
.catBar     /* 60px wide, 4px tall, rounded progress bar */
.catPct     /* 11px, text-tertiary, 32px min-width, right-aligned */
.catAmount  /* 13px/600, text-primary, 64px min-width, right-aligned */
```

### Budget Progress Bar

```css
.budgetTrack {
  flex: 1;
  height: 5px;
  background: var(--surface-sunken);
  border-radius: 99px;
  overflow: hidden;
}
.budgetFill {
  height: 100%;
  /* Dynamic gradient based on usage % */
}
```

**Gradient logic:**
- Under 85%: `linear-gradient(90deg, #16C8C7, #12B0AF)` (teal)
- 85–99%: `linear-gradient(90deg, #F0C84D, #E0B83D)` (amber)
- 100%+: `linear-gradient(90deg, #EF8B6E, #E07050)` (coral)

### Legend Item

Chart legend pattern:

```css
.legendItem {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
  color: var(--text-tertiary);
  font-weight: 500;
}
.legendDot {
  width: 10px;
  height: 10px;
  border-radius: 3px;
}
```

### Skeleton Loading

Shimmer animation for loading states:

```css
@keyframes shimmer {
  0% { background-position: -200% 0; }
  100% { background-position: 200% 0; }
}
.skeleton {
  background: linear-gradient(90deg,
    var(--border-muted) 25%,
    var(--surface-overlay) 50%,
    var(--border-muted) 75%
  );
  background-size: 200% 100%;
  animation: shimmer 1.5s infinite;
  border-radius: 8px;
}
```

---

## Sidebar

### Dimensions

- **Collapsed:** 64px width, icon-only
- **Expanded:** 240px width, icon + label
- Background: `var(--surface-raised)` (white in light theme)
- Border-right: `1px solid var(--border-default)`
- Padding: `24px 8px` collapsed, `24px 16px` expanded

### Structure

```
aside.sidebar
  ├── .header
  │     ├── .logoMark (34px square, 10px radius, primary bg, white "S")
  │     ├── .logoText (18px/700, hidden when collapsed)
  │     └── .toggleButton (24px circle, absolute right: -20px)
  ├── nav.nav
  │     ├── .navSection "Menu"
  │     │     └── Dashboard, Transactions, Reports, Categories
  │     └── .navSection "General"
  │           ├── Settings
  │           └── Logout (button, not link)
  ├── .themeToggle (above bottom separator)
  └── .bottomSection (below border-top)
        └── .userRow
              ├── .avatar (34px circle, primary tint bg, initial letter)
              └── .userInfo (name + email, hidden when collapsed)
```

### Nav Item

```css
.navLink {
  height: 38px;
  border-radius: 10px;
  font-size: 14px;
  font-weight: 500;
  color: var(--text-secondary);
  /* Collapsed: centered icon only */
  /* Expanded: flex-start, gap: 10px, padding: 9px 12px */
}
.active {
  color: var(--color-primary);
  background-color: var(--primary-a15);
}
```

### Section Labels

```css
.navSectionLabel {
  font-size: 11px;
  font-weight: 600;
  color: var(--text-tertiary);
  text-transform: uppercase;
  letter-spacing: 0.06em;
  padding: 0 12px;
  margin-bottom: 8px;
  /* Hidden when collapsed */
}
```

### Icons

- Library: Lucide React
- Size: **20px** in sidebar and dashboard
- Stroke width: **1.5** (not 2)
- Specific icons:
  - Dashboard: `LayoutGrid`
  - Transactions: `ArrowLeftRight`
  - Reports: `ChartNoAxesColumnIncreasing`
  - Categories: `Tag`
  - Settings: `Settings`
  - Logout: `LogOut`
  - Theme: `Moon` / `Sun` / `Monitor`

---

## Chart Theming

### useChartTheme() Hook

Returns resolved CSS token values for Recharts:

| Property | Token | Usage |
|----------|-------|-------|
| `axisStroke` | `--text-tertiary` | Axis tick text fill |
| `gridStroke` | `--border-muted` | CartesianGrid stroke |
| `tooltipBg` | `--surface-overlay` | Tooltip background |
| `tooltipBorder` | `--border-default` | Tooltip border |
| `tooltipText` | `--text-primary` | Tooltip text |
| `hoverBg` | `--primary-a8` | Bar hover cursor fill |
| `incomeColor` | `--color-primary` | Income series |
| `expenseColor` | `--cat-3` | Expense accent |
| `categoryColors` | `cat-*` palette | Multi-series charts |

### useChartPatterns() Hook

SVG pattern fills for visual differentiation beyond color:

- **Pattern types:** `solid`, `stripe` (45°), `stripe-reverse` (-45°), `dots`
- **Cash flow:** Income = solid brand color; Expense = striped pattern of same color
- **Categories:** Cycle through `[solid, stripe, solid, stripe-reverse, solid, dots]`
- Each pattern includes a `legendStyle` CSS object for matching legend dots

### Chart Configuration

**Bar chart defaults:**
```tsx
<BarChart margin={{ top: 4, right: 0, bottom: 0, left: 0 }} barGap={4}>
  <CartesianGrid strokeDasharray="3 3" vertical={false} />
  <XAxis tickLine={false} dy={4} fontSize={12} />
  <YAxis tickLine={false} axisLine={false} width={48} fontSize={12} />
  <Bar radius={[6, 6, 6, 6]} barSize={36} />
</BarChart>
```

**Half-gauge (donut):**
```tsx
<Pie
  startAngle={180} endAngle={0}
  cx="50%" cy="95%"
  innerRadius={130} outerRadius={175}
  cornerRadius={4} paddingAngle={1.5}
  stroke="none"
/>
```

---

## Dark/Light Theme

### Implementation

1. **FOUC prevention:** Inline `<script>` in `index.html` reads localStorage and sets `data-theme` before CSS loads
2. **ThemeProvider:** React context at `web/src/hooks/useTheme.tsx`
3. **CSS:** Semantic tokens auto-switch via `[data-theme="light"]` in `tokens.css`

### Three Modes

| Mode | Behavior | localStorage |
|------|----------|-------------|
| Dark | Graphite Indigo dark palette | `"dark"` |
| Light | Cool off-white + crisp white cards | `"light"` |
| System | Follows OS preference | `"system"` |

### Using in Components

```tsx
const { theme, resolvedTheme, setTheme } = useTheme();
// theme: 'dark' | 'light' | 'system'
// resolvedTheme: 'dark' | 'light' (actual applied)
```

---

## Icons

**Library:** Lucide React (tree-shakeable, outline style only)

### Sizes

| Context | Size | Stroke Width |
|---------|------|-------------|
| Sidebar navigation | 20px | 1.5 |
| KPI icon badges | 18px | 1.5 |
| Buttons, chips | 18px | 1.5 |
| Badge arrows (ArrowUpRight etc.) | 12px | 3 |
| Card link arrows | 14px | default |
| Sidebar toggle chevron | 14px | default |

### Rules

- Outline style only (no filled icons)
- Color: inherit from parent
- Decorative icons: `aria-hidden="true"`
- Interactive icon-only buttons: `aria-label="description"`

---

## Stylelint Rules

Configuration: `web/.stylelintrc.json`

| Rule | Effect |
|------|--------|
| `color-no-hex` | No hex colors outside `tokens.css` |
| `color-named: never` | No named colors |
| `function-disallowed-list` | No `rgb()`, `rgba()`, `hsl()`, `hsla()` |
| `font-weight-notation: numeric` | Must use numbers, not words |

```bash
cd web
npm run lint:css                           # all files
npx stylelint src/styles/MyFile.module.css # single file
```

---

## Adopting the Style on New Pages

When building or updating a page, follow this checklist:

### 1. Page Wrapper

```css
.page {
  display: flex;
  flex-direction: column;
  gap: 0;
}
```

### 2. Page Header

```css
.header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 32px;
}
.headerTitle {
  font-size: 24px;
  font-weight: 700;
  color: var(--text-primary);
  letter-spacing: -0.03em;
}
.subtitle {
  font-size: 14px;
  color: var(--text-secondary);
  margin-top: 2px;
}
```

### 3. Cards

Use glass card base. All cards get `14px` border-radius and `24px` padding. Add card header with title/subtitle/link pattern.

### 4. Data Lists

Use the transaction row pattern: `gap: 12px`, `border-bottom: 1px solid var(--surface-sunken)`, `padding: 10px 4px`, hover to `var(--surface-hover)`.

### 5. Selects & Controls

All selects: `height: 36px`, `border-radius: 8px`, custom SVG chevron, no box-shadow on default state, focus ring using `var(--primary-a8)`.

### 6. Loading States

Use skeleton shimmer pattern for every card.

### 7. Error States

Centered error message with retry button (`10px radius, primary bg, white text`).

### 8. Empty States

Centered muted text: `padding: 24px; color: var(--text-secondary); text-align: center`.

---

## Quick Reference: Do / Don't

### Do

- Use semantic tokens in all component CSS
- Use `color-mix()` for semi-transparent variants
- Use `tabular-nums` on number/currency columns
- Use `font-variant-numeric: tabular-nums` for financial data
- Use 14px border-radius for cards
- Use 12px gap for grids and rows
- Use 20px icons at 1.5 stroke-width
- Use the glass-card pattern for dashboard-style cards
- Use `transition: background 0.15s` for row hover effects
- Test both dark and light themes

### Don't

- Use raw hex, rgb, or hsl in `.module.css` files
- Use font weights below 400
- Use `text-transform: uppercase` (except sidebar section labels)
- Use pure black or pure white for backgrounds
- Use shadows as the primary depth signal in dark mode
- Add animations longer than 600ms
- Use 24px/strokeWidth:2 for icons (old style — use 20px/1.5)
- Mix glass-card and border-only card styles on the same page
- Skip loading skeletons — every data section needs one
