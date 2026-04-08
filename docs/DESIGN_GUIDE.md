# SpenDrop Design Guide

A practical reference for developers working on SpenDrop's frontend. This covers the token system, component patterns, and rules you need to follow.

---

## Palette: Purple Accent

Dark-first design with a cool blue undertone and purple accent.

### Brand Colors

| Role | Dark | Light | Usage |
|------|------|-------|-------|
| Primary | `#5347CE` (Accent-7) | `#4030A6` (Accent-6) | Buttons, links, active states, featured cards |
| Expense | `#EF8B6E` (coral) | `#E07050` (coral bright) | Negative amounts, over-budget |
| Income | `#22C55E` (green) | `#16A34A` (green bright) | Positive amounts, savings |
| Warning | `#F0C84D` (amber) | `#D4A830` (amber bright) | Budget approaching limit |
| Info | `#4896FE` (blue) | `#3B82F6` (blue bright) | Informational badges |
| Error | Same as expense | Same as expense | Form validation, destructive actions |

### Category Color Scale

An 11-step gradient from purple through teal to gold, used for chart segments, category badges, and progress bars. Each category stores its own hex color in the database.

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
| `--cat-muted` | `#B8BCC8` | Neutral |

### Pos/Neg Semantic Colors

Dedicated green and coral for positive/negative indicators **only** — badges, percentage text, savings/loss amounts. Do NOT use these for large chart fills or backgrounds.

| Token | Dark | Light | Usage |
|-------|------|-------|-------|
| `--color-pos` | `#22C55E` | `#16A34A` | Income badges, positive deltas |
| `--color-neg` | `#EF8B6E` | `#E07050` | Expense badges, negative deltas |

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

**Rule:** Component `.module.css` files only use semantic tokens. Never reference primitives like `--gray-700` directly.

### Surface Tokens (Elevation)

Dark mode uses tonal surface progression instead of shadows for depth:

| Token | Dark | Light | Usage |
|-------|------|-------|-------|
| `--surface-base` | `#111113` | `#FAFAFA` | Page background |
| `--surface-raised` | `#19191B` | `#FFFFFF` | Cards, sections |
| `--surface-overlay` | `#222225` | `#FFFFFF` | Dropdowns, modals, tooltips |
| `--surface-sunken` | `#0C0C0E` | `#F0F0F2` | Input backgrounds, inset areas |
| `--surface-hover` | `#2A2A2D` | `#F0F0F2` | Hover state on surfaces |

### Glass Surface Tokens

Used for dashboard cards and floating elements with frosted-glass depth:

| Token | Dark | Light | Usage |
|-------|------|-------|-------|
| `--glass-bg` | raised @ 85% | white @ 85% | Card background |
| `--glass-border` | white @ 6% | white @ 40% | Subtle edge highlight |
| `--glass-blur` | `12px` | `12px` | Backdrop blur radius |

### Text Tokens

| Token | Dark | Light | Usage |
|-------|------|-------|-------|
| `--text-primary` | `#EEEEF0` | `#111113` | Body text, headings |
| `--text-secondary` | `#B0B0BA` | `#6E6E79` | Labels, metadata |
| `--text-tertiary` | `#6E6E79` | `#606069` | Placeholders, helper text |
| `--text-inverse` | `#111113` | `#EEEEF0` | Text on colored backgrounds |
| `--text-on-accent` | `#EEEEF0` | `#EEEEF0` | Text on `--color-primary` bg (both themes) |

### Opacity Utilities

Use `color-mix()` for semi-transparent variants:

| Token | Value | Usage |
|-------|-------|-------|
| `--primary-a8` | primary at 8% | Row hover |
| `--primary-a15` | primary at 15% | Active nav item, selected state |
| `--primary-a50` | primary at 50% | Focus ring |
| `--expense-a15` | expense at 15% | Error/expense background tint |
| `--income-a15` | income at 15% | Success/income background tint |

---

## Spacing

4px base unit. Token name = multiplier.

| Token | Value | Usage |
|-------|-------|-------|
| `--space-1` | 4px | Icon gaps, tight padding |
| `--space-2` | 8px | Small gaps, input padding |
| `--space-3` | 12px | Form field padding |
| `--space-4` | 16px | Standard padding |
| `--space-5` | 20px | Card internal padding |
| `--space-6` | 24px | Section spacing |
| `--space-8` | 32px | Large section gaps |
| `--space-10` | 40px | Page-level spacing |
| `--space-12` | 48px | Major divisions |
| `--space-16` | 64px | Max separation |

---

## Typography

**Font:** Inter Variable (self-hosted via `@fontsource-variable/inter`)

**OpenType features** enabled globally: `cv02` (straight a), `cv03` (open 6), `cv04` (open 9), `cv11` (single-storey l)

### Type Scale

Each role has three sub-tokens: `-size`, `-weight`, `-line-height`.

```css
/* Usage in a .module.css file */
.pageTitle {
  font-size: var(--type-heading-lg-size);       /* 24px */
  font-weight: var(--type-heading-lg-weight);   /* 600 */
  line-height: var(--type-heading-lg-line-height); /* 32px */
}
```

| Role | Size | Weight | Usage |
|------|------|--------|-------|
| `heading-lg` | 24px | 600 | Page titles |
| `heading-md` | 20px | 600 | Section headers |
| `heading-sm` | 16px | 600 | Card titles |
| `body-lg` | 16px | 400 | Form inputs, primary content |
| `body-md` | 14px | 400 | Table data, descriptions |
| `body-sm` | 12px | 400 | Timestamps, metadata |
| `label-lg` | 14px | 500 | Buttons, table headers, nav |
| `label-md` | 12px | 500 | Chips, badges, form labels |
| `label-sm` | 11px | 500 | Overlines, helper text |
| `amount` | 16px | 500 | Currency values (use with `tabular-nums`) |

### Rules

- Only weights: 400 (regular), 500 (medium), 600 (semibold)
- No `text-transform: uppercase` (enforced by stylelint)
- Currency columns must use `font-variant-numeric: tabular-nums`
- Minimum text size: 11px

---

## Border Radius

| Token | Value | Usage |
|-------|-------|-------|
| `--radius-sm` | 4px | Inputs, tooltips |
| `--radius-md` | 8px | Chips, small cards, nav items |
| `--radius-lg` | 12px | Cards, sections |
| `--radius-xl` | 16px | Large cards |
| `--radius-2xl` | 28px | Modals/dialogs (MD3 style) |
| `--radius-full` | 9999px | Pill buttons, avatars, badges |

---

## Motion

### Easing

| Token | Curve | Usage |
|-------|-------|-------|
| `--ease-standard` | `cubic-bezier(0.2, 0, 0, 1)` | General interactions |
| `--ease-decelerate` | `cubic-bezier(0.05, 0.7, 0.1, 1)` | Elements entering (expand, open) |
| `--ease-accelerate` | `cubic-bezier(0.3, 0, 0.8, 0.15)` | Elements exiting (collapse, close) |

### Duration

| Token | Value | Usage |
|-------|-------|-------|
| `--duration-fast` | 150ms | Hover, button press, tooltips |
| `--duration-normal` | 250ms | Dropdowns, modals |
| `--duration-slow` | 350ms | Sidebar expand/collapse |
| `--duration-page` | 400ms | Page transitions |

### Pattern

```css
/* Enter: decelerate (slows into place) */
.panel { transition: width var(--duration-slow) var(--ease-accelerate); }
.panel.open { transition: width var(--duration-slow) var(--ease-decelerate); }
```

---

## Component Patterns

### Sidebar

- **Collapsed:** 64px, icon-only
- **Expanded:** 240px, icon + label
- Expand/collapse is triggered by a **toggle button** (no hover — explicit click only)
- State persists in `localStorage` under key `spendrop-sidebar` (`"true"` / `"false"`)
- A `sidebar-toggle` event is dispatched on `window` after each toggle (for layout recalculation)
- Transition uses different easing for expand vs collapse
- 5 nav items: Dashboard, Reports, Transactions, Categories, Settings
- Bottom section: theme toggle (cycles dark → light → system), user avatar, logout
- Icons: Lucide React, 24px, stroke-width 2

### Cards

**Glass card (default for dashboard and floating sections):**

```css
.card {
  background: var(--glass-bg);
  backdrop-filter: blur(var(--glass-blur));
  -webkit-backdrop-filter: blur(var(--glass-blur));
  border: 1px solid var(--glass-border);
  border-radius: var(--radius-lg);
  padding: var(--space-5);
}
```

The frosted-glass effect provides depth via semi-transparent background + backdrop blur. In dark mode this is subtle; in light mode it creates a classic frosted-white effect.

**Featured card (accent-filled, e.g. Total Balance KPI):**

```css
.featured {
  background: var(--color-primary);
  border-color: transparent;
}
/* Child text uses --text-on-accent */
```

**Border-only card (for secondary content like settings/categories):**

```css
.cardBorder {
  background: transparent;
  border: 1px solid var(--border-muted);
  border-radius: var(--radius-lg);
  padding: var(--space-5);
}
```

### Tabs

Use the shared `<Tabs>` component at `web/src/components/Tabs.tsx`.

```tsx
import { Tabs } from '../components/Tabs';

<Tabs
  tabs={[{ key: 'monthly', label: 'Monthly' }, { key: 'yearly', label: 'Yearly' }]}
  activeKey={activeTab}
  onTabChange={setActiveTab}
/>
```

The component applies an **overlapping underline** technique: the active tab renders a 2px bottom border via `border-bottom: 2px solid var(--color-primary)` while `margin-bottom: -1px` causes it to overlap the container's `border-bottom: 1px solid var(--border-muted)`. This produces a seamless connected underline without a visible gap between the tab indicator and the container border.

The tab row uses `role="tablist"` / `role="tab"` / `aria-selected` for accessibility.

### Chart Theming

Use the `useChartTheme()` hook (`web/src/hooks/useChartTheme.ts`) to get resolved CSS token values for Recharts components. This ensures charts respect the active theme (dark/light) and use consistent design tokens.

**Available properties:**

| Property | Token source | Usage |
|----------|-------------|-------|
| `axisStroke` | `--text-tertiary` | XAxis / YAxis `tick` fill |
| `gridStroke` | `--border-muted` | CartesianGrid stroke |
| `tooltipBg` | `--surface-overlay` | Tooltip background |
| `tooltipBorder` | `--border-default` | Tooltip border |
| `tooltipText` | `--text-primary` | Tooltip text color |
| `hoverBg` | `--primary-a8` | Active bar / dot hover fill |
| `incomeColor` | `--color-primary` | Income / primary series color |
| `expenseColor` | `--cat-3` | Expense accent color |
| `categoryColors` | 12-slot cat-* palette | Multi-series category charts |

### Chart Pattern System

Use `useChartPatterns()` hook (`web/src/hooks/useChartPatterns.tsx`) for SVG pattern fills that visually differentiate chart series beyond color alone.

**Pattern types:** `solid`, `stripe` (45° diagonal lines), `stripe-reverse` (-45°), `dots`

**Cash flow bars:** Income uses solid brand color, expenses use a striped pattern of the same color with a stroke border. This differentiates them without needing a second color.

**Category donut segments:** Alternate between solid and patterned fills using the `PATTERN_CYCLE` array: `[solid, stripe, solid, stripe-reverse, solid, dots]`.

```tsx
import { useChartPatterns, ChartPatternDefs } from '../hooks/useChartPatterns';

const patterns = useChartPatterns();

<BarChart data={chartData}>
  <ChartPatternDefs patterns={patterns.cashFlowDefs} />
  <Bar dataKey="income" fill={patterns.cashFlow.income.fill} />
  <Bar dataKey="expense" fill={patterns.cashFlow.expense.fill}
       stroke={patterns.cashFlow.expense.stroke}
       strokeWidth={patterns.cashFlow.expense.strokeWidth} />
</BarChart>
```

**Legend dots:** Each `PatternConfig` includes a `legendStyle` CSS object that produces a matching CSS background (using `repeating-linear-gradient` or `radial-gradient`) for 10px rounded-square legend dots.

### ChartTooltip

`<ChartTooltip>` component (`web/src/components/ChartTooltip.tsx`) is a pre-built Recharts custom tooltip with pattern support.

- Accepts `patternStyles` prop: a `Record<string, CSSProperties>` mapping SVG fill values (e.g. `"url(#cf-stripe)"`) to CSS legend-dot styles
- Dots are 10px rounded squares (borderRadius 3px) matching the chart legend style
- Build the map using `patterns.buildStyleMap([...])` from the hook

```tsx
const cfStyles = patterns.buildStyleMap([patterns.cashFlow.income, patterns.cashFlow.expense]);
<Tooltip content={<ChartTooltip patternStyles={cfStyles} />} />
```

### Buttons

- Height: 40px, border-radius: `--radius-full` (pill shape)
- **Primary:** `--color-primary` background, `--text-inverse` text
- **Outlined:** transparent, `--color-primary` text, `--border-default` border
- **Ghost:** transparent, `--color-primary` text, no border
- **Danger:** `--color-expense` background, `--text-inverse` text
- Disabled: `opacity: 0.38`, `pointer-events: none`

### Inputs

- Height: 40px, radius: `--radius-sm`
- Background: `--surface-sunken`
- Focus: `--color-primary` border + `0 0 0 2px var(--primary-a50)` ring

### Chips/Badges

- Height: 28px, radius: `--radius-md`
- Category badges: 15% tinted background of category color

### Data Tables

- Header: `--type-label-lg`, `--text-secondary`
- Rows: 44px height, `--type-body-md`
- Row hover: `--primary-a8`
- Amounts: right-aligned, `tabular-nums`, expense in `--color-expense`, income in `--color-income`

---

## Dark/Light Theme

### How It Works

1. **FOUC prevention:** An inline `<script>` in `index.html` reads `localStorage` and sets `data-theme` before CSS loads
2. **ThemeProvider:** React context at `web/src/hooks/useTheme.tsx` manages state
3. **CSS:** Semantic tokens auto-switch via `[data-theme="light"]` selector in `tokens.css`

### Three Modes

| Mode | Behavior | localStorage |
|------|----------|-------------|
| Dark | Graphite Indigo dark palette | `"dark"` |
| Light | Cool off-white with deeper accents | `"light"` |
| System | Follows OS preference | `"system"` |

### Using in Components

```tsx
import { useTheme } from '../hooks/useTheme';

function MyComponent() {
  const { theme, resolvedTheme, setTheme } = useTheme();
  // theme: 'dark' | 'light' | 'system'
  // resolvedTheme: 'dark' | 'light' (actual applied theme)
}
```

Theme toggle is in the sidebar — cycles dark -> light -> system.

---

## Stylelint Rules

Configuration: `web/.stylelintrc.json`

### What's Enforced

| Rule | Effect |
|------|--------|
| `color-no-hex` | No hex colors outside `tokens.css` |
| `color-named: never` | No named colors (`red`, `blue`) |
| `function-disallowed-list` | No `rgb()`, `rgba()`, `hsl()`, `hsla()` |
| `font-weight-notation: numeric` | Must use `400` not `normal`, `600` not `bold` |
| `text-transform: uppercase` banned | No uppercase transforms |
| `font-weight < 400` banned | No thin/light weights |

### Fixing Violations

If stylelint flags your CSS, replace raw values with tokens:

```css
/* Bad */
.error { color: #e94560; background: rgba(233, 69, 96, 0.1); }

/* Good */
.error { color: var(--color-error); background-color: var(--expense-a15); }
```

### Running

```bash
cd web
npm run lint:css                           # all CSS files
npx stylelint src/styles/MyFile.module.css # single file
```

---

## Icons

**Library:** Lucide React (tree-shakeable)

```tsx
import { LayoutDashboard } from 'lucide-react';

<LayoutDashboard size={24} strokeWidth={2} />
```

### Sizes

| Context | Size |
|---------|------|
| Sidebar navigation | 24px |
| Buttons, chips | 18px |
| Table actions | 16px |

### Rules

- Outline style only (no filled icons)
- Color: inherit from parent (use text color tokens)
- Decorative icons: `aria-hidden="true"`
- Interactive icon-only buttons: `aria-label="description"`

---

## Quick Reference: Do / Don't

### Do

- Use semantic tokens in all component CSS
- Use `color-mix()` for semi-transparent variants
- Use `tabular-nums` on number/currency columns
- Use `--ease-decelerate` for enter, `--ease-accelerate` for exit
- Test color pairs for WCAG AA contrast
- Run `npm run lint:css` before committing CSS changes

### Don't

- Use raw hex, rgb, or hsl in `.module.css` files
- Use font weights below 400
- Use `text-transform: uppercase`
- Use pure black (`#000`) or pure white (`#FFF`) for backgrounds
- Use `box-shadow` as the primary depth signal in dark mode
- Add animations longer than 600ms
- Use emoji for icons (use Lucide)
- Reference primitive tokens (`--gray-700`) in component CSS
