# SpenDrop Frontend Design System Specification

## Overview

This document specifies SpenDrop's frontend design system: a Material Design 3-inspired, custom-built design system using CSS custom properties, CSS Modules, and stylelint enforcement. The system supports dark and light themes with a Graphite Indigo palette.

**Design principles:**
- Elegant and simplistic to the max
- Professional, not vibrant — muted tones, subtle elevation
- Spreadsheet-like transaction entry with keyboard-first UX
- Token-driven: every color, spacing, and typography value flows through CSS custom properties
- Enforced by linting: no raw hex/rgb/hsl outside the token definition file

**Key decisions:**
- No component library (no MUI) — custom CSS Modules
- Inter Variable font (self-hosted)
- Collapsible icon sidebar (64px collapsed, 240px expanded)
- Inline editable transaction table (Google Sheets/Airtable model)
- Dark-first with light mode support

---

## 1. Token System Architecture

### File Structure

```
web/src/styles/
  tokens.css          — primitives + semantic tokens + light overrides (ONLY file with hex values)
  global.css          — reset, base elements, typography (imports tokens.css)
  *.module.css        — component styles (only var() references, never raw colors)
```

`tokens.css` is imported first in `global.css` via `@import './tokens.css'`. Stylelint's `overrides` block exempts only `tokens.css` from the hex ban. **All primitive definitions, semantic mappings, and `[data-theme="light"]` overrides live in `tokens.css`** — this ensures the light theme's `color-mix()` calls pass linting.

### Two-Tier Token Model

**Primitive tokens** — raw palette values, the only place hex appears:

```css
:root {
  /* Graphite scale (cool blue undertone) */
  --gray-50:  #F5F5F6;
  --gray-100: #E8E8EA;
  --gray-200: #D1D1D5;
  --gray-300: #A9A9B0;
  --gray-400: #78787F;
  --gray-500: #58585F;
  --gray-600: #39393F;
  --gray-700: #2A2A30;
  --gray-800: #1E1E23;
  --gray-900: #141418;
  --gray-950: #0C0C10;

  /* Indigo scale */
  --indigo-50:  #EEF2FF;
  --indigo-100: #E0E7FF;
  --indigo-200: #C7D2FE;
  --indigo-300: #A5B4FC;
  --indigo-400: #818CF8;
  --indigo-500: #6366F1;
  --indigo-600: #4F46E5;
  --indigo-700: #4338CA;
  --indigo-800: #3730A3;
  --indigo-900: #312E81;

  /* Functional hues */
  --green-400: #7EC89B;
  --green-600: #2D9D5E;
  --red-400:   #E88B9C;
  --red-600:   #D4556B;
  --amber-400: #E8A87C;
  --amber-600: #C97A3E;
  --blue-400:  #7CAFD4;
  --blue-600:  #3B82F6;

  /* Neutrals */
  --white: #FFFFFF;
}
```

**Semantic tokens** — dark theme default on `:root`, light overrides on `[data-theme="light"]`:

```css
:root {
  /* Surfaces (elevation via tonal progression) */
  --surface-base:    var(--gray-950);   /* page background */
  --surface-raised:  var(--gray-900);   /* cards, sections */
  --surface-overlay: var(--gray-800);   /* dropdowns, popovers */
  --surface-sunken:  var(--gray-950);   /* inset areas, inputs */
  --surface-hover:   var(--gray-700);   /* hover states on elevated surfaces */

  /* Text */
  --text-primary:   var(--gray-50);
  --text-secondary: var(--gray-400);
  --text-tertiary:  var(--gray-500);
  --text-inverse:   var(--gray-950);

  /* Brand / Interactive */
  --color-primary:       var(--indigo-400);
  --color-primary-hover: var(--indigo-300);

  /* Semantic */
  --color-expense: var(--red-400);
  --color-income:  var(--green-400);
  --color-warning: var(--amber-400);
  --color-info:    var(--blue-400);
  --color-error:   var(--red-400);

  /* Borders */
  --border-default: var(--gray-700);
  --border-muted:   var(--gray-800);

  /* Focus */
  --focus-ring: var(--indigo-500);

  /* Overlay */
  --backdrop: color-mix(in srgb, var(--gray-950) 60%, transparent);

  /* Elevation shadows (subtle in dark) */
  --shadow-sm: 0 1px 2px color-mix(in srgb, var(--gray-950) 20%, transparent);
  --shadow-md: 0 4px 8px color-mix(in srgb, var(--gray-950) 25%, transparent);
  --shadow-lg: 0 8px 24px color-mix(in srgb, var(--gray-950) 30%, transparent);
  --shadow-xl: 0 16px 48px color-mix(in srgb, var(--gray-950) 40%, transparent);

  /* Opacity utilities via color-mix() */
  --primary-a8:  color-mix(in srgb, var(--color-primary) 8%, transparent);
  --primary-a15: color-mix(in srgb, var(--color-primary) 15%, transparent);
  --primary-a50: color-mix(in srgb, var(--color-primary) 50%, transparent);
  --expense-a15: color-mix(in srgb, var(--color-expense) 15%, transparent);
  --income-a15:  color-mix(in srgb, var(--color-income) 15%, transparent);
}

[data-theme="light"] {
  --surface-base:    var(--gray-50);
  --surface-raised:  var(--white);
  --surface-overlay: var(--white);
  --surface-sunken:  var(--gray-100);
  --surface-hover:   var(--gray-200);

  --text-primary:   var(--gray-900);
  --text-secondary: var(--gray-600);
  --text-tertiary:  var(--gray-500);
  --text-inverse:   var(--gray-50);

  --color-primary:       var(--indigo-600);
  --color-primary-hover: var(--indigo-700);

  --color-expense: var(--red-600);
  --color-income:  var(--green-600);
  --color-warning: var(--amber-600);
  --color-info:    var(--blue-600);
  --color-error:   var(--red-600);

  --border-default: var(--gray-200);
  --border-muted:   var(--gray-100);

  --focus-ring: var(--indigo-500);

  --shadow-sm: 0 1px 2px color-mix(in srgb, var(--gray-950) 5%, transparent);
  --shadow-md: 0 4px 8px color-mix(in srgb, var(--gray-950) 8%, transparent);
  --shadow-lg: 0 8px 24px color-mix(in srgb, var(--gray-950) 12%, transparent);
  --shadow-xl: 0 16px 48px color-mix(in srgb, var(--gray-950) 16%, transparent);
}
```

**Rule:** Component CSS files use only semantic tokens. Never reference primitives (`--gray-700`) or raw values in `.module.css` files.

---

## 2. Typography

### Font

Inter Variable, self-hosted via `@fontsource-variable/inter`. System font stack as fallback.

```css
:root {
  --font-sans: 'Inter Variable', -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
  --font-mono: 'JetBrains Mono', 'Fira Code', ui-monospace, monospace; /* reserved for code/debug displays */
}

body {
  font-family: var(--font-sans);
  font-feature-settings: 'cv02', 'cv03', 'cv04', 'cv11';
}
```

Feature settings: `cv02` (straight a), `cv03` (open 6), `cv04` (open 9), `cv11` (single-storey l) — improves readability for data-heavy UIs.

### Type Scale

| Token | Size | Weight | Line Height | Usage |
|-------|------|--------|-------------|-------|
| `--type-heading-lg` | 24px | 600 | 32px | Page titles |
| `--type-heading-md` | 20px | 600 | 28px | Section headers |
| `--type-heading-sm` | 16px | 600 | 24px | Card titles, subsections |
| `--type-body-lg` | 16px | 400 | 24px | Form inputs, primary content |
| `--type-body-md` | 14px | 400 | 20px | Table data, descriptions |
| `--type-body-sm` | 12px | 400 | 16px | Timestamps, metadata |
| `--type-label-lg` | 14px | 500 | 20px | Buttons, table headers, nav labels |
| `--type-label-md` | 12px | 500 | 16px | Chips, badges, form labels |
| `--type-label-sm` | 11px | 500 | 16px | Overlines, helper text |
| `--type-amount` | 16px | 500 | 24px | Currency values (tabular-nums) |

### Implementation Pattern

Each type token is a shorthand reference — components apply size, weight, and line-height individually:

```css
/* Token definitions in tokens.css */
:root {
  --type-heading-lg-size: 24px;
  --type-heading-lg-weight: 600;
  --type-heading-lg-line-height: 32px;
  /* ... repeat for each role */
}

/* Usage in component.module.css */
.pageTitle {
  font-size: var(--type-heading-lg-size);
  font-weight: var(--type-heading-lg-weight);
  line-height: var(--type-heading-lg-line-height);
}
```

### Typography Rules

- Only weights 400 (regular), 500 (medium), 600 (semibold)
- No thin/light weights (enforced via stylelint `declaration-property-value-disallowed-list`)
- Minimum text size: 11px (`--type-label-sm`)
- All currency/number columns use `font-variant-numeric: tabular-nums` for alignment
- No uppercase text transforms (enforced via stylelint)

---

## 3. Spacing System

4px base unit, numeric naming where the number is the multiplier of 4px.

```css
:root {
  --space-0:  0;
  --space-1:  4px;    /* tight: icon gaps, inline badge padding */
  --space-2:  8px;    /* default small gap */
  --space-3:  12px;   /* form field padding */
  --space-4:  16px;   /* standard padding, paragraph gap */
  --space-5:  20px;   /* card internal padding */
  --space-6:  24px;   /* section spacing */
  --space-8:  32px;   /* large section gaps */
  --space-10: 40px;   /* page-level spacing */
  --space-12: 48px;   /* major divisions */
  --space-16: 64px;   /* max section separation */
}
```

### Shape (Border Radius)

```css
:root {
  --radius-sm:   4px;    /* inputs, tooltips */
  --radius-md:   8px;    /* chips, small cards */
  --radius-lg:   12px;   /* cards, sections */
  --radius-xl:   16px;   /* large cards, modals */
  --radius-2xl:  28px;   /* dialogs */
  --radius-full: 9999px; /* buttons, pills, badges */
}
```

---

## 4. Motion

### Easing Curves

```css
:root {
  --ease-standard:   cubic-bezier(0.2, 0, 0, 1);
  --ease-decelerate: cubic-bezier(0.05, 0.7, 0.1, 1);
  --ease-accelerate: cubic-bezier(0.3, 0, 0.8, 0.15);
}
```

### Duration Tokens

```css
:root {
  --duration-fast:   150ms;  /* tooltips, small fades */
  --duration-normal: 250ms;  /* menus, dropdowns */
  --duration-slow:   350ms;  /* sidebar, panels */
  --duration-page:   400ms;  /* page transitions */
}
```

### Usage Guide

| Interaction | Duration | Easing |
|-------------|----------|--------|
| Button hover/press | `--duration-fast` | `--ease-standard` |
| Dropdown open | `--duration-normal` | `--ease-decelerate` |
| Dropdown close | `--duration-normal` | `--ease-accelerate` |
| Sidebar expand | `--duration-slow` | `--ease-decelerate` |
| Sidebar collapse | `--duration-slow` | `--ease-accelerate` |
| Modal open | `--duration-normal` | `--ease-decelerate` |
| Page transition | `--duration-page` | `--ease-standard` |
| Toast enter | `--duration-normal` | `--ease-decelerate` |

---

## 5. Component Patterns

### 5.1 Sidebar (Navigation Rail)

- **Collapsed:** 64px width, icon-only
  - Icons: 24px, stroke-width 2, outline style (Lucide icon set recommended)
  - Active item: pill-shaped indicator, `--primary-a15` background, `--color-primary` icon color
  - Inactive items: `--text-secondary` icon color
  - Logo mark at top (SpenDrop "S" in a rounded square)
  - Bottom section: user avatar circle, theme toggle icon

- **Expanded:** 240px width
  - Icon + label (`--type-label-lg`)
  - Transition: `--duration-slow` with `--ease-decelerate` (expand) / `--ease-accelerate` (collapse)
  - Toggle: hamburger/arrow icon at top, or hover to expand

- **5 destinations:** Dashboard, Reports, Transactions, Categories, Settings

- **Background:** `--surface-base`
- **Right border:** 1px `--border-muted`

### 5.2 Cards

- Background: `--surface-raised`
- Border: 1px `--border-default`
- Border radius: `--radius-lg` (12px)
- Padding: `--space-5` (20px)
- Shadow: `--shadow-sm`
- Hover (interactive cards): `--shadow-md` transition

### 5.3 Buttons

- Height: 40px
- Border radius: `--radius-full` (pill shape, MD3 style)
- Padding: 0 `--space-6` (24px horizontal)
- Font: `--type-label-lg`
- Transition: `--duration-fast` `--ease-standard`

| Variant | Background | Text | Border |
|---------|-----------|------|--------|
| Primary (filled) | `--color-primary` | `--text-inverse` | none |
| Outlined | transparent | `--color-primary` | 1px `--border-default` |
| Ghost/Text | transparent | `--color-primary` | none |
| Danger | `--color-expense` | `--text-inverse` | none |

Hover state: apply `--primary-a8` overlay (or equivalent per variant).

Disabled state (all variants): `opacity: 0.38`, `cursor: not-allowed`, `pointer-events: none`.

### 5.4 Inputs

- Height: 40px
- Border radius: `--radius-sm` (4px) — MD3 style for text fields
- Background: `--surface-sunken`
- Border: 1px `--border-default`
- Focus: border becomes `--color-primary`, box-shadow `0 0 0 2px var(--primary-a50)`
- Padding: `--space-2` `--space-3` (8px 12px)
- Font: `--type-body-lg`
- Placeholder: `--text-tertiary`
- Disabled: `opacity: 0.38`, `cursor: not-allowed`
- Error: border becomes `--color-error`, helper text in `--color-error`

### 5.5 Chips / Badges

- Height: 28px
- Border radius: `--radius-md` (8px)
- Padding: 0 `--space-3` (12px horizontal)
- Font: `--type-label-md`

**Category badges:** dynamic background using `color-mix()` at 15% of the category color, text in the full category color.

**Filter chips:** outlined style, `--border-default`, active state fills with `--primary-a15`.

### 5.6 Data Table

- Header row: `--type-label-lg`, `--text-secondary`, border-bottom 1px `--border-default`
- Data rows: 44px height, `--type-body-md`, border-bottom 1px `--border-muted`
- Row hover: `--primary-a8` overlay
- Selected/editing row: `--primary-a15` overlay
- Amount column: `--type-amount`, right-aligned, `tabular-nums`
- Expense amounts: `--color-expense`
- Income amounts: `--color-income`

### 5.7 Dialogs / Modals

- Border radius: `--radius-2xl` (28px, MD3 style)
- Width: 400-560px
- Padding: `--space-6` (24px)
- Background: `--surface-overlay`
- Shadow: `--shadow-xl`
- Backdrop: `var(--backdrop)`
- Title: `--type-heading-sm`
- Body: `--type-body-md`
- Actions: right-aligned, `--space-2` gap

---

## 6. Transaction Table — Inline Editable Spreadsheet

### Two-Mode Interaction Model

Follows Google Sheets / Airtable conventions.

**Selection mode** (cell highlighted, not editing):

| Key | Action |
|-----|--------|
| Arrow keys | Move selection between cells |
| Enter | Enter edit mode |
| Tab | Move right one cell |
| Shift+Tab | Move left one cell |
| Escape | Deselect |
| Any character | Enter edit mode, replace content |
| Delete/Backspace | Clear cell |

**Edit mode** (cursor in cell):

| Key | Action |
|-----|--------|
| Enter | Confirm edit, move down. On last row: create new row, focus Amount |
| Tab | Confirm edit, move right |
| Shift+Enter | Confirm edit, move up |
| Shift+Tab | Confirm edit, move left |
| Escape | Cancel edit, revert to original value |
| Arrow keys | Move cursor within text (do NOT leave cell) |

### Column Order and Tab Flow

Date → Amount → Description → Category → Tags

Amount is positioned second for fast entry flow — after date auto-fills, the cursor lands on the most important field.

### New Row Behavior

- Enter on last field of last row creates a new blank row at the top (table displays reverse chronological — newest first)
- Auto-fills: today's date, last-used category (from `localStorage`)
- Focus jumps to Amount field
- Unsaved row shows left accent border in `--color-primary`

### Visual States

| State | Appearance |
|-------|-----------|
| Normal cell | Plain text, no borders, clean read |
| Hovered row | `--primary-a8` background overlay |
| Selected cell | 1px `--color-primary` border, `--primary-a8` background |
| Editing cell | `--surface-sunken` background, `--color-primary` focus border |
| Unsaved row | 2px left border in `--color-primary` |
| Expense amount | `--color-expense` text |
| Income amount | `--color-income` text |

### Category Cell

Click opens a compact dropdown with colored category badges. Displays as a colored badge when not editing.

### Empty State

Ghost row with placeholder text: "Start typing to add a transaction..." in `--text-tertiary`.

---

## 7. Dashboard Layout

### KPI Cards (Top Row)

4 cards in horizontal grid (`grid-template-columns: repeat(4, 1fr)`), collapsing to 2x2 on tablet, stacked on mobile.

| Card | Primary Value | Secondary Info | Accent Color |
|------|-------------|----------------|--------------|
| Net This Month | Signed amount | vs last month (arrow + %) | `--color-income` or `--color-expense` |
| Total Spent | Amount | vs budget amount | `--color-primary` |
| Top Category | Category — amount | % of total | Category color |
| Savings Progress | Current / target | % of goal | `--color-warning` |

Primary value: `--type-heading-md`, bold.
Secondary info: `--type-body-sm`, `--text-secondary`.
Comparison arrows: ↑ green (positive), ↓ rose (negative).

### Chart Section (Two-Column)

**Left column (60%):** Spending trend
- Stacked bar chart, last 12 months
- Muted jewel tones for categories (desaturated on dark backgrounds)
- Grid lines: `color-mix(in srgb, var(--border-muted) 8%, transparent)`
- Chart background: `--surface-raised` card

**Right column (40%):** Category breakdown
- Donut chart with top 5-6 categories, rest as "Other"
- Legend below or beside chart
- Click segment to filter

### Budget Progress Section

Horizontal progress bars per budget category:
- Bar background: `--border-muted`
- Fill color progression: green (0-75%), `--color-warning` (75-90%), `--color-expense` (90%+)
- Left label: `$spent / $budgeted`
- Right label: `$remaining`

### Recent Transactions

Compact table showing last 5 transactions with "View All →" link.

### Responsive Breakpoints

| Breakpoint | Layout |
|-----------|--------|
| > 1024px | Full two-column layout |
| 768-1024px | Single column, KPIs 2x2 |
| < 768px | Single column, KPIs stacked |

---

## 8. Dark/Light Mode

### FOUC Prevention

Inline blocking script in `index.html` `<head>`, before any CSS or JS loads:

```html
<script>
  (function() {
    var theme = localStorage.getItem('spendrop-theme');
    if (!theme) {
      theme = window.matchMedia('(prefers-color-scheme: light)').matches
        ? 'light' : 'dark';
    }
    document.documentElement.setAttribute('data-theme', theme);
  })();
</script>
```

### React ThemeProvider

- Context provides: `theme` ('dark' | 'light' | 'system'), `resolvedTheme` ('dark' | 'light'), `setTheme()`
- Persists to `localStorage('spendrop-theme')`
- Listens for OS `prefers-color-scheme` changes when in "system" mode
- Sets `data-theme` attribute on `<html>`

### Three Modes

- **Dark** (default) — Graphite Indigo palette
- **Light** — cool off-white with deeper accent colors
- **System** — follows OS preference, auto-switches

### Toggle Location

Bottom of sidebar, next to user avatar. Moon icon (dark) / sun icon (light) / monitor icon (system).

### Color Shifting Strategy

| Semantic | Dark Value | Light Value | Rationale |
|----------|-----------|-------------|-----------|
| Expense | `#E88B9C` (desaturated) | `#D4556B` (saturated) | Less eye strain on dark |
| Income | `#7EC89B` (desaturated) | `#2D9D5E` (saturated) | Same principle |
| Primary | `#818CF8` (lighter) | `#4F46E5` (deeper) | Contrast against respective backgrounds |
| Warning | `#E8A87C` (desaturated) | `#C97A3E` (saturated) | Consistent with pattern |
| Surfaces | Near-black → dark gray progression | Off-white → white progression | Tonal elevation |
| Shadows | Nearly invisible (tonal surfaces do the work) | Visible (primary depth signal) | Per MD3 guidelines |
| Chart colors | Muted/desaturated jewel tones | Richer/saturated versions | Readability |

---

## 9. Stylelint Enforcement

### Configuration (`.stylelintrc.json`)

```json
{
  "extends": [
    "stylelint-config-standard",
    "stylelint-config-css-modules"
  ],
  "rules": {
    "color-no-hex": true,
    "color-named": "never",
    "function-disallowed-list": ["rgb", "rgba", "hsl", "hsla"],
    "declaration-property-value-disallowed-list": {
      "font-weight": ["100", "200", "300"],
      "text-transform": ["uppercase"]
    },
    "font-weight-notation": "numeric",
    "custom-property-pattern": null,
    "selector-class-pattern": null
  },
  "overrides": [
    {
      "files": ["**/tokens.css"],
      "rules": {
        "color-no-hex": null,
        "function-disallowed-list": null
      }
    }
  ]
}
```

### Required Packages

```
stylelint
stylelint-config-standard
stylelint-config-css-modules
```

### Scripts (`package.json`)

```json
{
  "lint:css": "stylelint \"src/**/*.css\"",
  "lint": "tsc --noEmit && stylelint \"src/**/*.css\""
}
```

### CI Integration

Add `npm run lint:css` to the PR workflow (unlike MusicDrop which only runs it locally).

### What Gets Enforced

- No raw hex colors outside `tokens.css`
- No `rgb()`, `rgba()`, `hsl()`, `hsla()` outside `tokens.css`
- No named colors (`red`, `blue`, etc.) anywhere
- No font weights below 400 (no thin/light)
- Numeric font weights only (no `bold` keyword)
- CSS Modules class names allowed (camelCase pattern)

---

## 10. Icon System

**Library:** Lucide React (tree-shakeable, consistent with the outline style)

**Rules:**
- Size: 24px default (sidebar), 18px in buttons/chips, 16px in table actions
- Stroke width: 2
- Color: inherit from parent (uses text color tokens)
- No filled icons — outline only for consistency
- Accessible: `aria-hidden="true"` on decorative icons, `aria-label` on interactive ones

---

## 11. Accessibility

### Contrast Ratios (WCAG AA)

| Pair | Ratio Required | Status |
|------|---------------|--------|
| `--text-primary` on `--surface-base` | 4.5:1 min | ~18.5:1 (passes) |
| `--text-secondary` on `--surface-base` | 4.5:1 min | ~4.8:1 (passes) |
| `--text-tertiary` on `--surface-base` | 3:1 (large text only) | ~3.2:1 (large text only) |
| `--color-primary` on `--surface-base` | 3:1 (UI components) | ~7.5:1 (passes) |
| `--color-expense` on `--surface-base` | 3:1 (UI components) | passes |
| `--color-income` on `--surface-base` | 3:1 (UI components) | passes |

### Focus Management

- All interactive elements have visible focus rings: `0 0 0 2px var(--focus-ring)`
- Tab order follows logical reading order
- Transaction table cells are focusable via keyboard
- Sidebar navigation supports arrow key movement between items
- Escape closes modals, dropdowns, and cancels edits

### Screen Reader Support

- Semantic HTML elements (`<nav>`, `<main>`, `<table>`, `<thead>`, `<tbody>`)
- ARIA labels on icon-only buttons and sidebar collapsed state
- Live regions for toast notifications and form validation messages
- Transaction amounts include screen-reader-friendly formatting ("negative 45 dollars" not "dash 45")

---

## 12. Do / Don't Rules

### Do

- Use semantic tokens (`--surface-raised`) in all component CSS
- Use `color-mix()` for semi-transparent variants
- Use `tabular-nums` on all number/amount columns
- Use `--ease-decelerate` for enter animations, `--ease-accelerate` for exit
- Test all color pairs for WCAG AA contrast
- Use Inter's OpenType features for cleaner numerals

### Don't

- Use raw hex, rgb, or hsl values in `.module.css` files
- Use font weights below 400
- Use uppercase text transforms
- Use pure black (`#000000`) or pure white (`#FFFFFF`) for backgrounds
- Use box-shadow as the primary elevation signal in dark mode
- Add more than 600ms duration to any animation
- Use emoji for icons (replace current sidebar emoji with Lucide icons)
