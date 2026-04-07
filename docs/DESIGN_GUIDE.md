# SpenDrop Design Guide

A practical reference for developers working on SpenDrop's frontend. This covers the token system, component patterns, and rules you need to follow.

---

## Palette: Graphite Indigo

Dark-first design with a cool blue undertone. Muted and professional, not vibrant.

| Role | Dark | Light | Usage |
|------|------|-------|-------|
| Primary | `#818CF8` (Indigo 400) | `#4F46E5` (Indigo 600) | Buttons, links, active states |
| Expense | `#E88B9C` (muted rose) | `#D4556B` (saturated rose) | Negative amounts, budget overage |
| Income | `#7EC89B` (muted green) | `#2D9D5E` (saturated green) | Positive amounts, savings |
| Warning | `#E8A87C` (muted amber) | `#C97A3E` (saturated amber) | Budget approaching limit |
| Info | `#7CAFD4` (muted blue) | `#3B82F6` (saturated blue) | Informational badges |
| Error | `#E88B9C` / `#D4556B` | Same as expense | Form validation, destructive actions |

**Why desaturated in dark mode?** Saturated colors on dark backgrounds cause eye strain. Colors shift to richer tones in light mode where the background provides contrast.

---

## Token Architecture

### File: `web/src/styles/tokens.css`

This is the **only file** allowed to contain raw color values (hex, rgb, hsl). Every other CSS file must use `var()` references.

### Two-Tier Model

```
Primitives (raw values)     Semantic tokens (contextual meaning)
--gray-950: #0C0C10    -->  --surface-base: var(--gray-950)
--indigo-400: #818CF8  -->  --color-primary: var(--indigo-400)
--red-400: #E88B9C     -->  --color-expense: var(--red-400)
```

**Rule:** Component `.module.css` files only use semantic tokens. Never reference primitives like `--gray-700` directly.

### Surface Tokens (Elevation)

Dark mode uses tonal surface progression instead of shadows for depth:

| Token | Dark | Light | Usage |
|-------|------|-------|-------|
| `--surface-base` | `#0C0C10` | `#F5F5F6` | Page background |
| `--surface-raised` | `#141418` | `#FFFFFF` | Cards, sections |
| `--surface-overlay` | `#1E1E23` | `#FFFFFF` | Dropdowns, modals |
| `--surface-sunken` | `#0C0C10` | `#E8E8EA` | Input backgrounds, inset areas |
| `--surface-hover` | `#2A2A30` | `#D1D1D5` | Hover state on surfaces |

### Text Tokens

| Token | Dark | Light | Usage |
|-------|------|-------|-------|
| `--text-primary` | `#F5F5F6` | `#141418` | Body text, headings |
| `--text-secondary` | `#78787F` | `#39393F` | Labels, metadata |
| `--text-tertiary` | `#58585F` | `#58585F` | Placeholders, helper text |
| `--text-inverse` | `#0C0C10` | `#F5F5F6` | Text on colored backgrounds |

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

**Current pattern (border-only card):**

```css
.card {
  background: transparent;
  border: 1px solid var(--border-muted);
  border-radius: var(--radius-lg);
  padding: var(--space-5);
}
```

This supersedes the previous surface-raised card pattern (`background-color: var(--surface-raised); box-shadow: var(--shadow-sm)`). Use the border-only pattern for all new cards. The transparent background keeps visual weight low and relies on the border to define boundaries.

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

```tsx
import { useChartTheme } from '../hooks/useChartTheme';

function MyChart() {
  const chart = useChartTheme();
  // ...
}
```

**Available properties:**

| Property | Token source | Usage |
|----------|-------------|-------|
| `axisStroke` | `--text-tertiary` | XAxis / YAxis `tick` fill |
| `gridStroke` | `--border-muted` | CartesianGrid stroke |
| `tooltipBg` | `--surface-overlay` | Tooltip background |
| `tooltipBorder` | `--border-default` | Tooltip border |
| `tooltipText` | `--text-primary` | Tooltip text color |
| `hoverBg` | `--primary-a8` | Active bar / dot hover fill |
| `incomeColor` | `--color-income` | Income series color |
| `expenseColor` | `--color-expense` | Expense series color |
| `categoryColors` | 6-slot palette | Multi-series category charts |

**Usage with Recharts:**

```tsx
<XAxis tick={{ fill: chart.axisStroke, fontSize: 12 }} />
<YAxis tick={{ fill: chart.axisStroke, fontSize: 12 }} />
<CartesianGrid strokeDasharray="3 3" stroke={chart.gridStroke} />
<Tooltip content={<ChartTooltip />} />
```

**`<ChartTooltip>` component** (`web/src/components/ChartTooltip.tsx`) is a pre-built Recharts custom tooltip. Pass it directly to the Recharts `<Tooltip content={} />` prop. It displays a label, a colored dot, and a formatted USD currency value per series row. Styling is handled via `ChartTooltip.module.css` using design tokens.

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
