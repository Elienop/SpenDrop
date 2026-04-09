# Frontend Design System Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace SpenDrop's ad-hoc dark theme with a two-tier token-driven design system supporting dark/light modes, enforced by stylelint.

**Architecture:** CSS custom properties defined in a single `tokens.css` file (primitives + semantics), imported by `global.css`. All component `.module.css` files reference only semantic tokens. A React `ThemeProvider` manages dark/light/system mode with localStorage persistence and FOUC prevention via an inline blocking script. The sidebar is rebuilt as a collapsible navigation rail using Lucide icons.

**Tech Stack:** CSS Modules, CSS custom properties, `color-mix()`, `@fontsource-variable/inter`, `lucide-react`, `stylelint` + `stylelint-config-standard` + `stylelint-config-css-modules`

**Spec:** `docs/superpowers/specs/2026-04-07-frontend-design-system.md`

---

## File Map

### New Files
| File | Purpose |
|------|---------|
| `web/src/styles/tokens.css` | Primitive palette + semantic tokens + `[data-theme="light"]` overrides |
| `web/src/hooks/useTheme.tsx` | ThemeProvider context, `setTheme()`, localStorage + OS preference |
| `web/src/hooks/useTheme.test.tsx` | Tests for theme switching, localStorage, OS media query |
| `web/src/styles/AppLayout.module.css` | Flex layout for sidebar + main content |
| `web/.stylelintrc.json` | Stylelint config banning raw colors outside tokens.css |

### Modified Files
| File | Changes |
|------|---------|
| `web/index.html` | Add FOUC prevention script, update title to "SpenDrop", add Inter Variable font preload |
| `web/package.json` | Add `@fontsource-variable/inter`, `lucide-react`, `stylelint`, `stylelint-config-standard`, `stylelint-config-css-modules` |
| `web/src/main.tsx` | Wrap app in `ThemeProvider`, import `tokens.css` |
| `web/src/styles/global.css` | Remove all `:root` token definitions, `@import './tokens.css'`, update reset to use new tokens |
| `web/src/App.tsx` | Extract `AppLayout` to use CSS module, adjust margin for collapsible sidebar |
| `web/src/components/Sidebar.tsx` | Rewrite: collapsible 64px/240px, Lucide icons, theme toggle, user avatar |
| `web/src/components/Sidebar.test.tsx` | Update tests for new sidebar behavior |
| `web/src/styles/Sidebar.module.css` | Full rewrite for collapsible sidebar with transitions |
| `web/src/styles/Dashboard.module.css` | Migrate tokens: `--bg-card` → `--surface-raised`, etc. |
| `web/src/styles/Transactions.module.css` | Migrate tokens |
| `web/src/styles/Categories.module.css` | Migrate tokens |
| `web/src/styles/Settings.module.css` | Migrate tokens |
| `web/src/styles/Reports.module.css` | Migrate tokens |
| `web/src/styles/Auth.module.css` | Migrate tokens |

### Token Migration Map
Every `.module.css` file needs these replacements:

| Old Token | New Token |
|-----------|-----------|
| `--bg-primary` | `--surface-base` |
| `--bg-card` | `--surface-raised` |
| `--bg-surface` | `--surface-sunken` |
| `--color-primary` | `--color-primary` (same name, new value) |
| `--color-danger` | `--color-expense` or `--color-error` (context-dependent) |
| `--color-warning` | `--color-warning` (same name, new value) |
| `--color-info` | `--color-info` (same name, new value) |
| `--color-text` | `--text-primary` |
| `--color-text-secondary` | `--text-secondary` |
| `--border-color` | `--border-default` |
| `--transition` | `var(--duration-fast) var(--ease-standard)` (or appropriate combo) |
| `--font-family` | `--font-sans` |
| `--space-xs` | `--space-1` |
| `--space-sm` | `--space-2` |
| `--space-md` | `--space-4` |
| `--space-lg` | `--space-6` |
| `--space-xl` | `--space-8` |
| `rgba(233, 69, 96, 0.1)` | `var(--expense-a15)` |
| `rgba(78, 204, 163, 0.15)` | `var(--income-a15)` |
| `rgba(78, 204, 163, 0.1)` | `var(--income-a15)` |
| `rgba(123, 104, 238, 0.1)` | `var(--primary-a15)` |
| `rgba(136, 136, 136, 0.1)` | `var(--primary-a8)` |
| `#3db890` | `var(--color-primary)` |
| `#fff` | `var(--text-inverse)` |

---

## Chunk 1: Token Foundation

### Task 1: Install Dependencies

**Files:**
- Modify: `web/package.json`

- [ ] **Step 1: Install production dependencies**

```bash
cd web && npm install @fontsource-variable/inter lucide-react
```

- [ ] **Step 2: Install dev dependencies**

```bash
cd web && npm install -D stylelint stylelint-config-standard stylelint-config-css-modules
```

- [ ] **Step 3: Verify installation**

```bash
cd web && node -e "require('@fontsource-variable/inter'); console.log('inter OK')" && node -e "require('lucide-react'); console.log('lucide OK')" && npx stylelint --version
```

- [ ] **Step 4: Commit**

```bash
git add web/package.json web/package-lock.json
git commit -m "chore: add design system dependencies (Inter, Lucide, stylelint)"
```

---

### Task 2: Create tokens.css

**Files:**
- Create: `web/src/styles/tokens.css`

- [ ] **Step 1: Create tokens.css with all primitives, semantics, and light overrides**

Write `web/src/styles/tokens.css` with the complete token definitions from the spec:

```css
/* SpenDrop Design Tokens
 * This is the ONLY file allowed to contain hex values.
 * All other CSS files must use var() references to semantic tokens.
 */

/* ===== PRIMITIVES ===== */
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

  /* ===== TYPOGRAPHY ===== */
  --font-sans: 'Inter Variable', -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
  --font-mono: 'JetBrains Mono', 'Fira Code', ui-monospace, monospace;

  /* Type scale — size */
  --type-heading-lg-size: 24px;
  --type-heading-md-size: 20px;
  --type-heading-sm-size: 16px;
  --type-body-lg-size: 16px;
  --type-body-md-size: 14px;
  --type-body-sm-size: 12px;
  --type-label-lg-size: 14px;
  --type-label-md-size: 12px;
  --type-label-sm-size: 11px;
  --type-amount-size: 16px;

  /* Type scale — weight */
  --type-heading-lg-weight: 600;
  --type-heading-md-weight: 600;
  --type-heading-sm-weight: 600;
  --type-body-lg-weight: 400;
  --type-body-md-weight: 400;
  --type-body-sm-weight: 400;
  --type-label-lg-weight: 500;
  --type-label-md-weight: 500;
  --type-label-sm-weight: 500;
  --type-amount-weight: 500;

  /* Type scale — line-height */
  --type-heading-lg-line-height: 32px;
  --type-heading-md-line-height: 28px;
  --type-heading-sm-line-height: 24px;
  --type-body-lg-line-height: 24px;
  --type-body-md-line-height: 20px;
  --type-body-sm-line-height: 16px;
  --type-label-lg-line-height: 20px;
  --type-label-md-line-height: 16px;
  --type-label-sm-line-height: 16px;
  --type-amount-line-height: 24px;

  /* ===== SPACING (4px base) ===== */
  --space-0:  0;
  --space-1:  4px;
  --space-2:  8px;
  --space-3:  12px;
  --space-4:  16px;
  --space-5:  20px;
  --space-6:  24px;
  --space-8:  32px;
  --space-10: 40px;
  --space-12: 48px;
  --space-16: 64px;

  /* ===== SHAPE ===== */
  --radius-sm:   4px;
  --radius-md:   8px;
  --radius-lg:   12px;
  --radius-xl:   16px;
  --radius-2xl:  28px;
  --radius-full: 9999px;

  /* ===== MOTION ===== */
  --ease-standard:   cubic-bezier(0.2, 0, 0, 1);
  --ease-decelerate: cubic-bezier(0.05, 0.7, 0.1, 1);
  --ease-accelerate: cubic-bezier(0.3, 0, 0.8, 0.15);

  --duration-fast:   150ms;
  --duration-normal: 250ms;
  --duration-slow:   350ms;
  --duration-page:   400ms;

  /* ===== SEMANTIC TOKENS (Dark Theme — Default) ===== */

  /* Surfaces */
  --surface-base:    var(--gray-950);
  --surface-raised:  var(--gray-900);
  --surface-overlay: var(--gray-800);
  --surface-sunken:  var(--gray-950);
  --surface-hover:   var(--gray-700);

  /* Text */
  --text-primary:   var(--gray-50);
  --text-secondary: var(--gray-400);
  --text-tertiary:  var(--gray-500);
  --text-inverse:   var(--gray-950);

  /* Brand / Interactive */
  --color-primary:       var(--indigo-400);
  --color-primary-hover: var(--indigo-300);

  /* Semantic colors */
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

  /* Shadows */
  --shadow-sm: 0 1px 2px color-mix(in srgb, var(--gray-950) 20%, transparent);
  --shadow-md: 0 4px 8px color-mix(in srgb, var(--gray-950) 25%, transparent);
  --shadow-lg: 0 8px 24px color-mix(in srgb, var(--gray-950) 30%, transparent);
  --shadow-xl: 0 16px 48px color-mix(in srgb, var(--gray-950) 40%, transparent);

  /* Opacity utilities */
  --primary-a8:  color-mix(in srgb, var(--color-primary) 8%, transparent);
  --primary-a15: color-mix(in srgb, var(--color-primary) 15%, transparent);
  --primary-a50: color-mix(in srgb, var(--color-primary) 50%, transparent);
  --expense-a15: color-mix(in srgb, var(--color-expense) 15%, transparent);
  --income-a15:  color-mix(in srgb, var(--color-income) 15%, transparent);
}

/* ===== LIGHT THEME OVERRIDES ===== */
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

  --backdrop: color-mix(in srgb, var(--gray-950) 40%, transparent);

  --shadow-sm: 0 1px 2px color-mix(in srgb, var(--gray-950) 5%, transparent);
  --shadow-md: 0 4px 8px color-mix(in srgb, var(--gray-950) 8%, transparent);
  --shadow-lg: 0 8px 24px color-mix(in srgb, var(--gray-950) 12%, transparent);
  --shadow-xl: 0 16px 48px color-mix(in srgb, var(--gray-950) 16%, transparent);
}
```

- [ ] **Step 2: Verify tokens.css is valid CSS**

```bash
cd web && npx stylelint src/styles/tokens.css --fix
```

Expected: no errors (tokens.css is exempted from hex ban).

- [ ] **Step 3: Commit**

```bash
git add web/src/styles/tokens.css
git commit -m "feat: add design system tokens (Graphite Indigo palette, dark/light)"
```

---

### Task 3: Rewrite global.css

**Files:**
- Modify: `web/src/styles/global.css`

- [ ] **Step 1: Rewrite global.css to import tokens and use new token names**

Replace the entire contents of `web/src/styles/global.css`:

```css
/* SpenDrop Global Styles */
@import './tokens.css';

/* CSS Reset */
*,
*::before,
*::after {
  box-sizing: border-box;
  margin: 0;
  padding: 0;
}

/* Base Element Styles */
html {
  font-size: 16px;
  -webkit-font-smoothing: antialiased;
  -moz-osx-font-smoothing: grayscale;
}

body {
  font-family: var(--font-sans);
  font-feature-settings: 'cv02', 'cv03', 'cv04', 'cv11';
  background-color: var(--surface-base);
  color: var(--text-primary);
  line-height: 1.5;
  min-height: 100vh;
}

#root {
  min-height: 100vh;
}

h1, h2, h3, h4, h5, h6 {
  line-height: 1.2;
}

h1 {
  font-size: var(--type-heading-lg-size);
  font-weight: var(--type-heading-lg-weight);
  line-height: var(--type-heading-lg-line-height);
}

h2 {
  font-size: var(--type-heading-md-size);
  font-weight: var(--type-heading-md-weight);
  line-height: var(--type-heading-md-line-height);
}

h3 {
  font-size: var(--type-heading-sm-size);
  font-weight: var(--type-heading-sm-weight);
  line-height: var(--type-heading-sm-line-height);
}

a {
  color: var(--color-primary);
  text-decoration: none;
  transition: opacity var(--duration-fast) var(--ease-standard);
}

a:hover {
  opacity: 0.8;
}

button {
  font-family: inherit;
  cursor: pointer;
  border: none;
  background: none;
  color: inherit;
  font-size: inherit;
}

input,
select,
textarea {
  font-family: inherit;
  font-size: inherit;
  color: inherit;
  background-color: var(--surface-sunken);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-sm);
  padding: var(--space-2) var(--space-3);
  transition: border-color var(--duration-fast) var(--ease-standard);
}

input:focus,
select:focus,
textarea:focus {
  outline: none;
  border-color: var(--color-primary);
  box-shadow: 0 0 0 2px var(--primary-a50);
}

ul, ol {
  list-style: none;
}

img {
  max-width: 100%;
  display: block;
}

/* Scrollbar (Webkit) */
::-webkit-scrollbar {
  width: 8px;
}

::-webkit-scrollbar-track {
  background: var(--surface-sunken);
}

::-webkit-scrollbar-thumb {
  background: var(--border-default);
  border-radius: var(--radius-sm);
}

::-webkit-scrollbar-thumb:hover {
  background: var(--text-secondary);
}
```

- [ ] **Step 2: Import Inter font in main.tsx**

Add to the top of `web/src/main.tsx` (before other imports):

```typescript
import '@fontsource-variable/inter';
```

- [ ] **Step 3: Verify the app still builds**

```bash
cd web && npx tsc --noEmit && npx vite build
```

Expected: build succeeds. The app will look different (new colors) but should render.

- [ ] **Step 4: Commit**

```bash
git add web/src/styles/global.css web/src/main.tsx
git commit -m "feat: rewrite global.css with token system, add Inter font"
```

---

### Task 4: Add Stylelint Configuration

**Files:**
- Create: `web/.stylelintrc.json`
- Modify: `web/package.json` (scripts)

- [ ] **Step 1: Create .stylelintrc.json**

Write `web/.stylelintrc.json`:

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

- [ ] **Step 2: Add lint scripts to package.json**

Add to `web/package.json` scripts:

```json
"lint:css": "stylelint \"src/**/*.css\"",
"lint": "tsc --noEmit && stylelint \"src/**/*.css\""
```

- [ ] **Step 3: Run stylelint to check current state**

```bash
cd web && npx stylelint "src/**/*.css"
```

Expected: should report violations in the module files that still use old tokens (these will be fixed in Chunk 4). `tokens.css` and `global.css` should pass.

- [ ] **Step 4: Commit**

```bash
git add web/.stylelintrc.json web/package.json
git commit -m "feat: add stylelint config enforcing token-only colors"
```

---

### Task 5: Update index.html

**Files:**
- Modify: `web/index.html`

- [ ] **Step 1: Add FOUC prevention script and update title**

Replace `web/index.html`:

```html
<!doctype html>
<html lang="en" data-theme="dark">
  <head>
    <meta charset="UTF-8" />
    <link rel="icon" type="image/svg+xml" href="/favicon.svg" />
    <meta name="viewport" content="width=device-width, initial-scale=1.0" />
    <title>SpenDrop</title>
    <script>
      (function() {
        var stored = localStorage.getItem('spendrop-theme');
        var theme;
        if (stored === 'light' || stored === 'dark') {
          theme = stored;
        } else {
          theme = window.matchMedia('(prefers-color-scheme: light)').matches
            ? 'light' : 'dark';
        }
        document.documentElement.setAttribute('data-theme', theme);
      })();
    </script>
  </head>
  <body>
    <div id="root"></div>
    <script type="module" src="/src/main.tsx"></script>
  </body>
</html>
```

- [ ] **Step 2: Commit**

```bash
git add web/index.html
git commit -m "feat: add FOUC prevention script, update title to SpenDrop"
```

---

## Chunk 2: Theme System

### Task 6: Create ThemeProvider

**Files:**
- Create: `web/src/hooks/useTheme.tsx`
- Create: `web/src/hooks/useTheme.test.tsx`

- [ ] **Step 1: Write the failing test**

Write `web/src/hooks/useTheme.test.tsx`:

```tsx
import { renderHook, act } from '@testing-library/react';
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { ThemeProvider, useTheme } from './useTheme';
import type { ReactNode } from 'react';

function wrapper({ children }: { children: ReactNode }) {
  return <ThemeProvider>{children}</ThemeProvider>;
}

describe('useTheme', () => {
  beforeEach(() => {
    localStorage.clear();
    document.documentElement.setAttribute('data-theme', 'dark');
  });

  it('defaults to dark theme', () => {
    const { result } = renderHook(() => useTheme(), { wrapper });
    expect(result.current.theme).toBe('dark');
    expect(result.current.resolvedTheme).toBe('dark');
  });

  it('switches to light theme', () => {
    const { result } = renderHook(() => useTheme(), { wrapper });
    act(() => result.current.setTheme('light'));
    expect(result.current.theme).toBe('light');
    expect(result.current.resolvedTheme).toBe('light');
    expect(document.documentElement.getAttribute('data-theme')).toBe('light');
    expect(localStorage.getItem('spendrop-theme')).toBe('light');
  });

  it('persists theme to localStorage', () => {
    localStorage.setItem('spendrop-theme', 'light');
    document.documentElement.setAttribute('data-theme', 'light');
    const { result } = renderHook(() => useTheme(), { wrapper });
    expect(result.current.theme).toBe('light');
  });

  it('supports system mode', () => {
    const matchMediaMock = vi.fn().mockReturnValue({
      matches: true,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    });
    vi.stubGlobal('matchMedia', matchMediaMock);

    const { result } = renderHook(() => useTheme(), { wrapper });
    act(() => result.current.setTheme('system'));
    expect(result.current.theme).toBe('system');
    expect(result.current.resolvedTheme).toBe('light');

    vi.unstubAllGlobals();
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd web && npx vitest run src/hooks/useTheme.test.tsx
```

Expected: FAIL — module `./useTheme` not found.

- [ ] **Step 3: Write ThemeProvider implementation**

Write `web/src/hooks/useTheme.tsx`:

```tsx
import { createContext, useContext, useState, useEffect, useCallback } from 'react';
import type { ReactNode } from 'react';

type ThemeMode = 'dark' | 'light' | 'system';
type ResolvedTheme = 'dark' | 'light';

interface ThemeContextValue {
  theme: ThemeMode;
  resolvedTheme: ResolvedTheme;
  setTheme: (theme: ThemeMode) => void;
}

const STORAGE_KEY = 'spendrop-theme';

const ThemeContext = createContext<ThemeContextValue | null>(null);

function getSystemTheme(): ResolvedTheme {
  if (typeof window === 'undefined') return 'dark';
  return window.matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark';
}

function resolveTheme(mode: ThemeMode): ResolvedTheme {
  return mode === 'system' ? getSystemTheme() : mode;
}

function getInitialTheme(): ThemeMode {
  if (typeof window === 'undefined') return 'dark';
  const stored = localStorage.getItem(STORAGE_KEY);
  if (stored === 'dark' || stored === 'light' || stored === 'system') return stored;
  return 'dark';
}

export function ThemeProvider({ children }: { children: ReactNode }) {
  const [theme, setThemeState] = useState<ThemeMode>(getInitialTheme);
  const [resolvedTheme, setResolvedTheme] = useState<ResolvedTheme>(() => resolveTheme(getInitialTheme()));

  const setTheme = useCallback((newTheme: ThemeMode) => {
    setThemeState(newTheme);
    const resolved = resolveTheme(newTheme);
    setResolvedTheme(resolved);
    document.documentElement.setAttribute('data-theme', resolved);
    localStorage.setItem(STORAGE_KEY, newTheme);
  }, []);

  useEffect(() => {
    if (theme !== 'system') return;

    const mql = window.matchMedia('(prefers-color-scheme: light)');
    const handler = () => {
      const resolved = resolveTheme('system');
      setResolvedTheme(resolved);
      document.documentElement.setAttribute('data-theme', resolved);
    };
    mql.addEventListener('change', handler);
    return () => mql.removeEventListener('change', handler);
  }, [theme]);

  return (
    <ThemeContext.Provider value={{ theme, resolvedTheme, setTheme }}>
      {children}
    </ThemeContext.Provider>
  );
}

export function useTheme(): ThemeContextValue {
  const context = useContext(ThemeContext);
  if (!context) throw new Error('useTheme must be used within ThemeProvider');
  return context;
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
cd web && npx vitest run src/hooks/useTheme.test.tsx
```

Expected: all 4 tests PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/hooks/useTheme.tsx web/src/hooks/useTheme.test.tsx
git commit -m "feat: add ThemeProvider with dark/light/system mode support"
```

---

### Task 7: Integrate ThemeProvider into App

**Files:**
- Modify: `web/src/main.tsx`

- [ ] **Step 1: Wrap app in ThemeProvider**

In `web/src/main.tsx`, add `ThemeProvider` import and wrap around `<App />`:

```typescript
import '@fontsource-variable/inter';
import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { BrowserRouter } from 'react-router-dom';
import { AuthProvider } from './hooks/useAuth';
import { ThemeProvider } from './hooks/useTheme';
import App from './App';
import './styles/global.css';

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <BrowserRouter>
      <AuthProvider>
        <ThemeProvider>
          <App />
        </ThemeProvider>
      </AuthProvider>
    </BrowserRouter>
  </StrictMode>,
);
```

- [ ] **Step 2: Verify build**

```bash
cd web && npx tsc --noEmit && npx vite build
```

Expected: build succeeds.

- [ ] **Step 3: Run all existing tests**

```bash
cd web && npx vitest run
```

Expected: all tests pass. Existing tests should not break since token names changed in CSS but the DOM structure is the same.

- [ ] **Step 4: Commit**

```bash
git add web/src/main.tsx
git commit -m "feat: integrate ThemeProvider into app root"
```

---

## Chunk 3: Collapsible Sidebar

### Task 8: Rewrite Sidebar Component

**Files:**
- Modify: `web/src/components/Sidebar.tsx`
- Modify: `web/src/styles/Sidebar.module.css`

- [ ] **Step 1: Rewrite Sidebar.module.css for collapsible rail**

Replace `web/src/styles/Sidebar.module.css`:

```css
.sidebar {
  position: fixed;
  top: 0;
  left: 0;
  width: 64px;
  height: 100vh;
  background-color: var(--surface-base);
  display: flex;
  flex-direction: column;
  border-right: 1px solid var(--border-muted);
  z-index: 100;
  transition: width var(--duration-slow) var(--ease-accelerate);
  overflow: hidden;
}

.sidebar.expanded {
  width: 240px;
  transition: width var(--duration-slow) var(--ease-decelerate);
}

.header {
  display: flex;
  align-items: center;
  height: 64px;
  padding: 0 var(--space-5);
  gap: var(--space-3);
  flex-shrink: 0;
}

.logoMark {
  width: 28px;
  height: 28px;
  border-radius: var(--radius-md);
  background-color: var(--surface-raised);
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  font-size: var(--type-label-lg-size);
  font-weight: 600;
  color: var(--color-primary);
}

.logoText {
  font-size: var(--type-heading-sm-size);
  font-weight: 600;
  color: var(--text-primary);
  white-space: nowrap;
  opacity: 0;
  transition: opacity var(--duration-normal) var(--ease-standard);
}

.sidebar.expanded .logoText {
  opacity: 1;
}

.toggleButton {
  position: absolute;
  top: var(--space-5);
  right: var(--space-4);
  width: 24px;
  height: 24px;
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--text-secondary);
  border-radius: var(--radius-sm);
  opacity: 0;
  transition: opacity var(--duration-fast) var(--ease-standard),
              background-color var(--duration-fast) var(--ease-standard);
}

.sidebar.expanded .toggleButton {
  opacity: 1;
}

.toggleButton:hover {
  background-color: var(--surface-hover);
  color: var(--text-primary);
}

.nav {
  flex: 1;
  padding: var(--space-2) 0;
  overflow-y: auto;
}

.navList {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  padding: 0 var(--space-3);
}

.navLink {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  height: 40px;
  padding: 0 var(--space-3);
  color: var(--text-secondary);
  font-size: var(--type-label-lg-size);
  font-weight: var(--type-label-lg-weight);
  line-height: var(--type-label-lg-line-height);
  border-radius: var(--radius-md);
  transition: color var(--duration-fast) var(--ease-standard),
              background-color var(--duration-fast) var(--ease-standard);
  text-decoration: none;
  white-space: nowrap;
  overflow: hidden;
}

.navLink:hover {
  color: var(--text-primary);
  background-color: var(--surface-hover);
  opacity: 1;
}

.active {
  color: var(--color-primary);
  background-color: var(--primary-a15);
}

.active:hover {
  background-color: var(--primary-a15);
}

.navIcon {
  width: 24px;
  height: 24px;
  flex-shrink: 0;
  display: flex;
  align-items: center;
  justify-content: center;
}

.navLabel {
  opacity: 0;
  transition: opacity var(--duration-normal) var(--ease-standard);
}

.sidebar.expanded .navLabel {
  opacity: 1;
}

.bottomSection {
  padding: var(--space-3);
  border-top: 1px solid var(--border-muted);
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.themeToggle {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  height: 40px;
  padding: 0 var(--space-3);
  color: var(--text-secondary);
  border-radius: var(--radius-md);
  transition: color var(--duration-fast) var(--ease-standard),
              background-color var(--duration-fast) var(--ease-standard);
}

.themeToggle:hover {
  color: var(--text-primary);
  background-color: var(--surface-hover);
}

.userRow {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  height: 40px;
  padding: 0 var(--space-3);
  overflow: hidden;
}

.avatar {
  width: 24px;
  height: 24px;
  border-radius: var(--radius-full);
  background-color: var(--color-primary);
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  font-size: var(--type-label-sm-size);
  font-weight: var(--type-label-lg-weight);
  color: var(--text-inverse);
}

.userName {
  font-size: var(--type-body-sm-size);
  font-weight: var(--type-body-md-weight);
  color: var(--text-secondary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  opacity: 0;
  transition: opacity var(--duration-normal) var(--ease-standard);
}

.sidebar.expanded .userName {
  opacity: 1;
}

.logoutButton {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  height: 40px;
  padding: 0 var(--space-3);
  color: var(--text-secondary);
  border-radius: var(--radius-md);
  transition: color var(--duration-fast) var(--ease-standard),
              background-color var(--duration-fast) var(--ease-standard);
}

.logoutButton:hover {
  color: var(--color-expense);
  background-color: var(--expense-a15);
}
```

- [ ] **Step 2: Rewrite Sidebar.tsx with Lucide icons and collapse toggle**

Replace `web/src/components/Sidebar.tsx`:

```tsx
import { useState } from 'react';
import { NavLink } from 'react-router-dom';
import {
  LayoutDashboard,
  TrendingUp,
  List,
  Grid2X2,
  Settings,
  Moon,
  Sun,
  Monitor,
  PanelLeftClose,
  LogOut,
} from 'lucide-react';
import { useAuth } from '../hooks/useAuth';
import { useTheme } from '../hooks/useTheme';
import styles from '../styles/Sidebar.module.css';

const navItems = [
  { to: '/', label: 'Dashboard', icon: LayoutDashboard },
  { to: '/reports', label: 'Reports', icon: TrendingUp },
  { to: '/transactions', label: 'Transactions', icon: List },
  { to: '/categories', label: 'Categories', icon: Grid2X2 },
  { to: '/settings', label: 'Settings', icon: Settings },
];

const themeIcons = { dark: Moon, light: Sun, system: Monitor } as const;
const themeOrder: Array<'dark' | 'light' | 'system'> = ['dark', 'light', 'system'];

export function Sidebar() {
  const { user, logout } = useAuth();
  const { theme, setTheme } = useTheme();
  const [expanded, setExpanded] = useState(false);

  const ThemeIcon = themeIcons[theme];

  const cycleTheme = () => {
    const idx = themeOrder.indexOf(theme);
    setTheme(themeOrder[(idx + 1) % themeOrder.length]);
  };

  const initial = user?.display_name?.charAt(0).toUpperCase() ?? '?';

  return (
    <aside
      className={`${styles.sidebar}${expanded ? ` ${styles.expanded}` : ''}`}
      onMouseEnter={() => setExpanded(true)}
      onMouseLeave={() => setExpanded(false)}
    >
      <div className={styles.header}>
        <span className={styles.logoMark}>S</span>
        <span className={styles.logoText}>SpenDrop</span>
        <button
          className={styles.toggleButton}
          onClick={() => setExpanded(false)}
          aria-label="Collapse sidebar"
        >
          <PanelLeftClose size={18} />
        </button>
      </div>

      <nav className={styles.nav} aria-label="Main navigation">
        <ul className={styles.navList}>
          {navItems.map((item) => (
            <li key={item.to}>
              <NavLink
                to={item.to}
                end={item.to === '/'}
                className={({ isActive }) =>
                  `${styles.navLink}${isActive ? ` ${styles.active}` : ''}`
                }
              >
                <span className={styles.navIcon} aria-hidden="true">
                  <item.icon size={24} strokeWidth={2} />
                </span>
                <span className={styles.navLabel}>{item.label}</span>
              </NavLink>
            </li>
          ))}
        </ul>
      </nav>

      <div className={styles.bottomSection}>
        <button
          className={styles.themeToggle}
          onClick={cycleTheme}
          aria-label={`Theme: ${theme}. Click to change.`}
        >
          <span className={styles.navIcon}>
            <ThemeIcon size={24} strokeWidth={2} />
          </span>
          <span className={styles.navLabel}>{theme.charAt(0).toUpperCase() + theme.slice(1)}</span>
        </button>

        <div className={styles.userRow}>
          <span className={styles.avatar}>{initial}</span>
          <span className={styles.userName}>{user?.display_name}</span>
        </div>

        <button
          className={styles.logoutButton}
          onClick={() => void logout()}
          aria-label="Log out"
        >
          <span className={styles.navIcon}>
            <LogOut size={24} strokeWidth={2} />
          </span>
          <span className={styles.navLabel}>Log out</span>
        </button>
      </div>
    </aside>
  );
}
```

- [ ] **Step 3: Update AppLayout in App.tsx for collapsible sidebar**

Create `web/src/styles/AppLayout.module.css`:

```css
.layout {
  display: flex;
  min-height: 100vh;
}

.main {
  margin-left: 64px;
  flex: 1;
  padding: var(--space-6);
  transition: margin-left var(--duration-slow) var(--ease-decelerate);
}
```

Update `web/src/App.tsx` to use the CSS module:

```tsx
import { Routes, Route } from 'react-router-dom';
import { Sidebar } from './components/Sidebar';
import { ProtectedRoute } from './components/ProtectedRoute';
import { Login } from './pages/Login';
import { Register } from './pages/Register';
import { Dashboard } from './pages/Dashboard';
import { Transactions } from './pages/Transactions';
import { Categories } from './pages/Categories';
import { Settings } from './pages/Settings';
import { Reports } from './pages/Reports';
import layoutStyles from './styles/AppLayout.module.css';

function AppLayout() {
  return (
    <div className={layoutStyles.layout}>
      <Sidebar />
      <main className={layoutStyles.main}>
        <Routes>
          <Route path="/" element={<Dashboard />} />
          <Route path="/reports" element={<Reports />} />
          <Route path="/transactions" element={<Transactions />} />
          <Route path="/categories" element={<Categories />} />
          <Route path="/settings" element={<Settings />} />
        </Routes>
      </main>
    </div>
  );
}

function App() {
  return (
    <Routes>
      <Route path="/login" element={<Login />} />
      <Route path="/register" element={<Register />} />
      <Route
        path="/*"
        element={
          <ProtectedRoute>
            <AppLayout />
          </ProtectedRoute>
        }
      />
    </Routes>
  );
}

export default App;
```

- [ ] **Step 4: Update Sidebar tests**

Update `web/src/components/Sidebar.test.tsx` to account for new DOM structure (Lucide icons instead of emoji, theme toggle, collapsible state). Key things to test:
- Renders all 5 nav links
- Active link gets highlighted
- User name displayed
- Logout button works
- Theme toggle button exists

- [ ] **Step 5: Run tests and verify build**

```bash
cd web && npx vitest run && npx tsc --noEmit
```

Expected: all tests pass, TypeScript clean.

- [ ] **Step 6: Commit**

```bash
git add web/src/components/Sidebar.tsx web/src/components/Sidebar.test.tsx web/src/styles/Sidebar.module.css web/src/styles/AppLayout.module.css web/src/App.tsx
git commit -m "feat: collapsible sidebar with Lucide icons and theme toggle"
```

---

## Chunk 4: Token Migration

### Task 9: Migrate Dashboard.module.css

**Files:**
- Modify: `web/src/styles/Dashboard.module.css`

- [ ] **Step 1: Apply token migration**

Find-and-replace old tokens with new tokens per the migration map. Key changes:
- `--bg-card` → `--surface-raised`
- `--bg-surface` → `--surface-sunken`
- `--border-color` → `--border-default`
- `--color-text` → `--text-primary`
- `--color-text-secondary` → `--text-secondary`
- `--color-danger` → `--color-expense`
- `--color-primary` (for income) → `--color-income`
- `--space-xs` → `--space-1`, `--space-sm` → `--space-2`, `--space-md` → `--space-4`, `--space-lg` → `--space-6`, `--space-xl` → `--space-8`
- `--radius-md` → `--radius-md` (same name, keep)
- `--transition` → `var(--duration-fast) var(--ease-standard)`
- `rgba(233, 69, 96, 0.1)` → `var(--expense-a15)`

- [ ] **Step 2: Run stylelint on this file**

```bash
cd web && npx stylelint src/styles/Dashboard.module.css
```

Expected: no violations.

- [ ] **Step 3: Commit**

```bash
git add web/src/styles/Dashboard.module.css
git commit -m "feat: migrate Dashboard styles to design tokens"
```

---

### Task 10: Migrate Transactions.module.css

**Files:**
- Modify: `web/src/styles/Transactions.module.css`

- [ ] **Step 1: Apply token migration (same mapping as Task 9)**

- [ ] **Step 2: Run stylelint**

```bash
cd web && npx stylelint src/styles/Transactions.module.css
```

Expected: no violations.

- [ ] **Step 3: Commit**

```bash
git add web/src/styles/Transactions.module.css
git commit -m "feat: migrate Transactions styles to design tokens"
```

---

### Task 11: Migrate Categories.module.css

**Files:**
- Modify: `web/src/styles/Categories.module.css`

- [ ] **Step 1: Apply token migration**

- [ ] **Step 2: Run stylelint**

```bash
cd web && npx stylelint src/styles/Categories.module.css
```

- [ ] **Step 3: Commit**

```bash
git add web/src/styles/Categories.module.css
git commit -m "feat: migrate Categories styles to design tokens"
```

---

### Task 12: Migrate Settings.module.css

**Files:**
- Modify: `web/src/styles/Settings.module.css`

- [ ] **Step 1: Apply token migration**

- [ ] **Step 2: Run stylelint**

```bash
cd web && npx stylelint src/styles/Settings.module.css
```

- [ ] **Step 3: Commit**

```bash
git add web/src/styles/Settings.module.css
git commit -m "feat: migrate Settings styles to design tokens"
```

---

### Task 13: Migrate Reports.module.css

**Files:**
- Modify: `web/src/styles/Reports.module.css`

- [ ] **Step 1: Apply token migration**

- [ ] **Step 2: Run stylelint**

```bash
cd web && npx stylelint src/styles/Reports.module.css
```

- [ ] **Step 3: Commit**

```bash
git add web/src/styles/Reports.module.css
git commit -m "feat: migrate Reports styles to design tokens"
```

---

### Task 14: Migrate Auth.module.css

**Files:**
- Modify: `web/src/styles/Auth.module.css`

- [ ] **Step 1: Apply token migration**

- [ ] **Step 2: Run stylelint**

```bash
cd web && npx stylelint src/styles/Auth.module.css
```

- [ ] **Step 3: Commit**

```bash
git add web/src/styles/Auth.module.css
git commit -m "feat: migrate Auth styles to design tokens"
```

---

### Task 15: Final Validation

**Files:** None (validation only)

- [ ] **Step 1: Run full stylelint across all CSS**

```bash
cd web && npx stylelint "src/**/*.css"
```

Expected: zero violations.

- [ ] **Step 2: Run TypeScript check**

```bash
cd web && npx tsc --noEmit
```

Expected: zero errors.

- [ ] **Step 3: Run full test suite**

```bash
cd web && npx vitest run
```

Expected: all tests pass.

- [ ] **Step 4: Build production bundle**

```bash
cd web && npx vite build
```

Expected: clean build, no warnings.

- [ ] **Step 5: Commit any remaining changes**

If any test files needed updating:

```bash
git add -A web/src/
git commit -m "fix: update tests for design system token migration"
```
