# SpenDrop v3 — shadcn + Tailwind Rewrite Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the SpenDrop frontend's CSS-Module styling layer with Tailwind CSS v3 + shadcn/ui primitives, rewrite every page against the new primitives, elevate the transaction entry row to a keyboard-first RHF+Zod flow, and drop the `categories.color` column — all in one branch-based big-bang migration.

**Architecture:** Thirteen sequential commits on `feat/design-system-v3`. Commits 1–3 lay the Tailwind/shadcn foundation with preflight disabled so old CSS Modules keep rendering unchanged. Commits 4–11 rewrite pages one at a time, routing every category color through a new `getCategoryColorVar()` helper that reads from an 11-slot `--chart-N` palette by `((category.id - 1) % 11) + 1`. Commit 12 drops `categories.color` atomically across SQL, sqlc, Go handlers, Go tests, frontend types, and frontend fixtures. Commit 13 re-enables Tailwind preflight, deletes the remaining cruft, and wires ESLint into CI. The data layer, API hooks, routing, and Go handlers (except the color-field deletions in commit 12) are untouched.

**Tech Stack:**
- **Frontend:** React 19 + TypeScript (Vite), Tailwind CSS v3 (pinned), shadcn/ui primitives, Recharts via shadcn `chart`, react-hook-form + Zod, cmdk (via shadcn `Command`), Sonner, `@fontsource-variable/geist` + `@fontsource-variable/geist-mono`
- **Backend:** Go + chi + sqlc (no changes except commit 12 `color` field deletions)
- **Database:** SQLite (migration 004 drops `categories.color` — requires SQLite 3.35+)
- **Tests:** vitest + `@testing-library/react` + `@testing-library/user-event`, `go test -race`
- **Lint:** ESLint + `eslint-plugin-tailwindcss` + `prettier-plugin-tailwindcss` (stylelint removed)
- **Skill usage:** Each task follows TDD (@superpowers:test-driven-development). Commit 9 (entry row rewrite) uses strict TDD — test file first, implementation after.

---

## Reference

- Spec: [docs/superpowers/specs/2026-04-09-shadcn-tailwind-rewrite-design.md](docs/superpowers/specs/2026-04-09-shadcn-tailwind-rewrite-design.md)
- Project context: `.claude/CLAUDE.md` (never commit to main, branch check before every commit, conventional commits, strict type safety, `git add -f` for gitignored `docs/superpowers/` path)
- **Branch:** Work lands on `feat/design-system-v3`. This branch already exists (created during brainstorming) and already contains the four spec commits `9f20ce0`, `6a151f6`, `3d110d5`, `4b6ff6f`. Before starting Task 1, verify `git branch --show-current` returns `feat/design-system-v3`. If not, check it out. Do **not** create a new branch — use the existing one.

## File Structure

New files (created across commits):

| File | Responsibility | Created in |
|---|---|---|
| `web/tailwind.config.ts` | Tailwind config — token references, 11 chart color slots, fontFamily (Geist Sans/Mono), preflight off | Task 1 |
| `web/postcss.config.js` | PostCSS pipeline for Tailwind + autoprefixer | Task 1 |
| `web/components.json` | shadcn CLI config | Task 1 |
| `web/src/lib/utils.ts` | shadcn `cn()` helper (clsx + tailwind-merge) | Task 1 |
| `web/src/styles/globals.css` | Tailwind directives + `:root` token block (11 chart slots) | Task 1 |
| `web/src/components/ui/*.tsx` | 21 shadcn primitives — 20 from spec §2.1 (button, card, input, label, select, checkbox, form, dialog, sheet, dropdown-menu, popover, command, tabs, table, badge, skeleton, tooltip, sonner, scroll-area, calendar) plus `chart` from §4.1 | Task 2 |
| `web/src/lib/chart-colors.ts` | `getCategoryColorVar({ id })` — single source of truth for category→slot | Task 3 |
| `web/src/lib/chart-colors.test.ts` | Unit test for `getCategoryColorVar` | Task 3 |
| `web/src/components/AppShell.tsx` | Top-level layout (sidebar + main content column) | Task 4 |
| `web/src/components/KpiCard.tsx` | Dashboard hero tile (label + mono value + delta) | Task 6 |
| `web/src/components/ChartCard.tsx` | Card wrapper with title + period toggle + loading Skeleton | Task 6 |
| `web/src/components/TransactionTable.tsx` | Dense rows, bulk select, mono amounts, row actions | Task 8 |
| `web/src/components/TransactionEntryRow.tsx` | New RHF + shadcn entry row (replaces `TransactionEntry.tsx`) | Task 9 |
| `web/src/components/TransactionEntryRow.test.tsx` | TDD spec written **before** implementation | Task 9 |
| `web/src/components/CategoryEditor.tsx` | Sheet + Form category editor (no color picker) | Task 7 |
| `web/src/components/DateRangePicker.tsx` | Popover + Calendar wrapper | Task 10 |
| `internal/database/migrations/004_drop_categories_color.sql` | `ALTER TABLE categories DROP COLUMN color;` | Task 12 |

Files replaced in place:

| File | Edit scope | Touched in |
|---|---|---|
| `web/package.json` | Add Tailwind/shadcn/RHF/fontsource/sonner/eslint deps; remove stylelint deps; rewrite `lint` script | Task 1 (add), Task 13 (cleanup) |
| `web/vite.config.ts` | Verify/add `@/*` path alias | Task 1 |
| `web/tsconfig.app.json` | Verify/add `@/*` path alias under `paths` | Task 1 |
| `web/src/main.tsx` | Import `globals.css` + Geist fontsource packages; drop `@fontsource-variable/inter` in Task 13 | Task 1 (add fonts), Task 13 (drop Inter) |
| `web/src/App.tsx` | Replace inline `AppLayout` function with `<AppShell>` | Task 4 |
| `web/src/components/Sidebar.tsx` | Rewritten with Tailwind + `ScrollArea` + `Tooltip` | Task 4 |
| `web/src/components/Sidebar.test.tsx` | Rewritten with role/label queries | Task 4 |
| `web/src/pages/Login.tsx` + `Login.test.tsx` | Rewritten with shadcn `Card` + `Form` | Task 5 |
| `web/src/pages/Register.tsx` + `Register.test.tsx` | Rewritten with shadcn `Card` + `Form` | Task 5 |
| `web/src/pages/Dashboard.tsx` + `Dashboard.test.tsx` | Rewritten with `KpiCard`, `ChartCard`, shadcn `chart`. All category colors via `getCategoryColorVar()` | Task 6 |
| `web/src/pages/Categories.tsx` + `Categories.test.tsx` | Rewritten with `Table` + `Sheet`-based editor. Color picker **removed from UI** (backend field still exists until Task 12) | Task 7 |
| `web/src/pages/Transactions.tsx` + `Transactions.test.tsx` | Rewritten with new `TransactionToolbar`, `FilterPanel`, `TransactionTable`. Inline entry row still uses old `TransactionEntry` in this commit | Task 8 |
| `web/src/components/TransactionToolbar.tsx` | Rewritten with shadcn `Button` + `Popover` + `Badge` | Task 8 |
| `web/src/components/FilterPanel.tsx` | Rewritten with `Sheet` + `Tabs` + `Checkbox` + `Calendar` | Task 8 |
| `web/src/components/TransactionRow.tsx` + `TransactionRow.test.tsx` | Rewritten or inlined into `TransactionTable` | Task 8 |
| `web/src/pages/Transactions.tsx` (import swap) | Swap import from `TransactionEntry` → `TransactionEntryRow` | Task 9 |
| `web/src/pages/Reports.tsx` + `Reports.test.tsx` | Rewritten with `Tabs` + `DateRangePicker` + `ChartCard`s. Colors via `getCategoryColorVar()` | Task 10 |
| `web/src/pages/Settings.tsx` + `Settings.test.tsx` | Rewritten with vertical `Tabs` + `Form` + `Dialog` | Task 11 |
| `internal/database/migrations/001_initial_schema.sql` | Drop `color` column from `categories` CREATE TABLE and from seed `INSERT INTO categories` statements | Task 12 |
| `internal/database/queries.sql` | Drop `color` column/param at lines 48, 63, 172, 216 | Task 12 |
| `internal/database/queries.sql.go` | Regenerated via `sqlc generate` (do not hand-edit) | Task 12 |
| `internal/api/category_handlers.go` | Delete `hexColorRegexp`/`isValidHexColor`, `Color` request fields (lines 24, 31), validation blocks (96–99, 153–156), `CreateCategoryParams.Color` / `UpdateCategoryParams.Color` (108, 164) | Task 12 |
| `internal/api/transaction_handlers.go` | Delete `transactionResponse.CategoryColor` (line 47), drop `c.color` from SELECT at line 180, remove scan var/arg/assignment at 208, 213, 223 | Task 12 |
| `internal/api/dashboard_handlers.go` | Delete `Color` field + assignment on category-breakdown struct (~42, ~294) | Task 12 |
| `internal/api/reports_handlers.go` | Delete `Color` field + assignment on category-trend struct (~96, ~148) | Task 12 |
| `internal/api/category_handlers_test.go` | Drop `"color":"#..."` from JSON bodies at 101, 130, 147, 164, 200, 217, 234, 251; delete `resp["color"]` if-block at 120–121 | Task 12 |
| `internal/api/transaction_handlers_test.go` | `seedTestCategory` (84–89) drop `color` param + field; delete assertion at 619–620 | Task 12 |
| `web/src/api/types.ts` | Delete `Transaction.category_color` (20), `Category.color` (31), `CategoryBreakdownItem.color` (85), `CategoryTrendEntry.color` (147) | Task 12 |
| `web/src/pages/Settings.test.tsx` | Drop `color` key from three `Category[]`-typed fixtures at 419/420/421 | Task 12 |
| `web/src/components/TransactionRow.test.tsx` | Drop `color` at line 12 + `category_color` at line 32 (or file is deleted in Task 8; re-check) | Task 12 |
| `web/src/pages/Dashboard.test.tsx` | Drop `color` / `category_color` at lines 51, 52, 70 | Task 12 |
| `web/src/pages/Transactions.test.tsx` | Drop `category_color` at line 34 | Task 12 |
| `web/src/pages/Categories.test.tsx` | Drop `color` keys at lines 31, 41, 51 | Task 12 |
| `web/src/pages/Reports.test.tsx` | Drop `color` key at line 51 | Task 12 |
| `web/src/hooks/useDashboard.test.ts` | Drop `color` keys at lines 34, 35 | Task 12 |
| `web/src/App.test.tsx` | Drop any `color` keys from embedded category/transaction fixtures | Task 12 |
| `web/tailwind.config.ts` | Flip `corePlugins: { preflight: false }` off | Task 13 |
| `.github/workflows/pr.yml` | Add `npx eslint .` step to frontend job | Task 13 |

Files deleted:

| File | Deleted in |
|---|---|
| `web/.stylelintrc.json` | Task 13 |
| `web/src/styles/tokens.css` | Task 13 |
| `web/src/styles/global.css` | Task 13 |
| `web/src/styles/AppLayout.module.css` | Task 4 |
| `web/src/styles/Sidebar.module.css` | Task 4 |
| `web/src/styles/Auth.module.css` | Task 5 |
| `web/src/styles/Dashboard.module.css` | Task 6 |
| `web/src/styles/Categories.module.css` | Task 7 |
| `web/src/styles/Transactions.module.css` | Task 8 |
| `web/src/styles/Tabs.module.css` | Task 8 |
| `web/src/styles/ChartTooltip.module.css` | Task 8 |
| `web/src/styles/Reports.module.css` | Task 10 |
| `web/src/styles/Settings.module.css` | Task 11 |
| `web/src/components/TransactionEntry.tsx` | Task 9 (replaced by `TransactionEntryRow.tsx`) |
| `web/src/components/TransactionEntry.test.tsx` | Task 9 |
| `web/src/hooks/useChartTheme.ts` + `useChartTheme.test.ts` | Task 13 |
| `web/src/hooks/useChartPatterns.tsx` (+ test if present) | Task 13 |
| `web/src/hooks/useTheme.tsx` + `useTheme.test.tsx` (both `ThemeProvider` and `useTheme` exports removed) | Task 13 |
| `web/src/components/ChartTooltip.tsx` + `ChartTooltip.test.tsx` | Task 13 |
| `web/src/components/Tabs.tsx` + `Tabs.test.tsx` (replaced by `ui/tabs`) | Task 13 |

---

## Chunk 1: Foundation, shadcn primitives, and chart-colors helper

Lays the Tailwind + shadcn + Geist foundation with preflight **disabled** so old CSS Modules keep rendering. Installs all 20 shadcn primitives in one shot. Adds the `getCategoryColorVar` helper that every subsequent page rewrite depends on. No user-visible change yet — the app still renders with old CSS Modules.

### Task 1: Foundation — Tailwind v3 + shadcn init + Geist fonts

**Context:** Right now `web/package.json` has no Tailwind, no shadcn, no RHF, no Zod, no sonner. `main.tsx` imports `@fontsource-variable/inter`. CSS Modules drive everything through `tokens.css`. Preflight disabling is critical — enabling it while CSS Modules still render half the app would visibly regress every page.

**Files:**
- Modify: `web/package.json`
- Create: `web/tailwind.config.ts`, `web/postcss.config.js`, `web/components.json`, `web/src/lib/utils.ts`, `web/src/styles/globals.css`
- Modify: `web/src/main.tsx`
- Verify/modify: `web/vite.config.ts`, `web/tsconfig.app.json`

**Spec references:** §1.1, §1.2, §1.3, §1.4, §1.5, §8 commit 1

- [ ] **Step 1: Install runtime deps**

```bash
cd web && npm install class-variance-authority clsx tailwind-merge \
  @fontsource-variable/geist @fontsource-variable/geist-mono
```

Expected: lockfile + node_modules updated, no peer-dep warnings beyond the pre-existing React 19 noise. `tailwindcss` is installed as a **dev** dep in Step 2, not here.

- [ ] **Step 2: Install dev deps**

```bash
cd web && npm install -D tailwindcss@^3.4.17 postcss autoprefixer tailwindcss-animate \
  prettier prettier-plugin-tailwindcss eslint-plugin-tailwindcss
```

Expected: `tailwindcss@^3.4.17` lands in `devDependencies` (not `dependencies`) — Tailwind is a build-time tool and must not ship to runtime.

- [ ] **Step 3: Add `@/*` path alias to `vite.config.ts` and `tsconfig.app.json`**

This step runs **before** `shadcn init` (Step 5). shadcn init reads `tsconfig` and writes the alias values into `components.json`; if the alias isn't in tsconfig yet, shadcn init produces a broken components.json that later `shadcn add` commands will trip over.

`web/vite.config.ts` — add at the top:

```ts
import path from 'node:path';
```

And inside the existing `defineConfig({ ... })` call, add a `resolve` block (alongside `plugins`, `server`, `test`):

```ts
resolve: {
  alias: {
    '@': path.resolve(__dirname, './src'),
  },
},
```

`web/tsconfig.app.json` — add under `compilerOptions`:

```json
"baseUrl": ".",
"paths": {
  "@/*": ["./src/*"]
},
```

Verify the edit: `npx tsc -b` from `web/` still passes.

- [ ] **Step 4: Run `tailwindcss init -p`**

```bash
cd web && npx tailwindcss init -p
```

Expected: `tailwind.config.js` + `postcss.config.js` created. Delete the generated `tailwind.config.js` (`rm web/tailwind.config.js`) — shadcn init regenerates it as `.ts` in the next step. Keep `postcss.config.js` as-is.

- [ ] **Step 5: Run `shadcn init`**

```bash
cd web && npx shadcn@latest init
```

Answer every prompt (the CLI asks more than three):
- Would you like to use TypeScript? **yes**
- Which style would you like to use? **Default**
- Which color would you like to use as base color? **Neutral**
- Where is your global CSS file? **`src/styles/globals.css`**
- Would you like to use CSS variables for colors? **yes**
- Are you using a custom tailwind prefix? **(empty — press Enter)**
- Where is your `tailwind.config.ts` located? **`tailwind.config.ts`**
- Configure the import alias for components? **`@/components`**
- Configure the import alias for utils? **`@/lib/utils`**
- Are you using React Server Components? **no**
- Write configuration to `components.json`. Proceed? **yes**

Expected: `components.json`, `tailwind.config.ts`, `src/lib/utils.ts` created, and `src/styles/globals.css` created (possibly as a stub — we rewrite it wholesale in Step 7). If shadcn init writes into `src/index.css` instead, delete that file and continue — Step 7 is the source of truth for CSS.

- [ ] **Step 6: Rewrite `tailwind.config.ts`**

Replace the shadcn-generated `tailwind.config.ts` with the version below. Critical differences vs. the generator's default:
1. `corePlugins: { preflight: false }` — keeps old CSS Modules working
2. `--chart-1` through `--chart-11` (shadcn generates only 5)
3. `fontFamily.sans` and `.mono` point at Geist Variable
4. `content` covers `src/**/*.{ts,tsx}` and the shadcn `src/components/ui/**` path

```ts
import type { Config } from 'tailwindcss';

const config: Config = {
  darkMode: 'class',
  content: ['./index.html', './src/**/*.{ts,tsx}'],
  corePlugins: {
    preflight: false,
  },
  theme: {
    container: {
      center: true,
      padding: '2rem',
      screens: { '2xl': '1400px' },
    },
    extend: {
      fontFamily: {
        sans: ['"Geist Variable"', 'system-ui', 'sans-serif'],
        mono: ['"Geist Mono Variable"', 'ui-monospace', 'monospace'],
      },
      colors: {
        border: 'hsl(var(--border))',
        input: 'hsl(var(--input))',
        ring: 'hsl(var(--ring))',
        background: 'hsl(var(--background))',
        foreground: 'hsl(var(--foreground))',
        primary: {
          DEFAULT: 'hsl(var(--primary))',
          foreground: 'hsl(var(--primary-foreground))',
        },
        secondary: {
          DEFAULT: 'hsl(var(--secondary))',
          foreground: 'hsl(var(--secondary-foreground))',
        },
        destructive: {
          DEFAULT: 'hsl(var(--destructive))',
          foreground: 'hsl(var(--destructive-foreground))',
        },
        muted: {
          DEFAULT: 'hsl(var(--muted))',
          foreground: 'hsl(var(--muted-foreground))',
        },
        accent: {
          DEFAULT: 'hsl(var(--accent))',
          foreground: 'hsl(var(--accent-foreground))',
        },
        popover: {
          DEFAULT: 'hsl(var(--popover))',
          foreground: 'hsl(var(--popover-foreground))',
        },
        card: {
          DEFAULT: 'hsl(var(--card))',
          foreground: 'hsl(var(--card-foreground))',
        },
        chart: {
          '1': 'hsl(var(--chart-1))',
          '2': 'hsl(var(--chart-2))',
          '3': 'hsl(var(--chart-3))',
          '4': 'hsl(var(--chart-4))',
          '5': 'hsl(var(--chart-5))',
          '6': 'hsl(var(--chart-6))',
          '7': 'hsl(var(--chart-7))',
          '8': 'hsl(var(--chart-8))',
          '9': 'hsl(var(--chart-9))',
          '10': 'hsl(var(--chart-10))',
          '11': 'hsl(var(--chart-11))',
        },
      },
      borderRadius: {
        lg: 'var(--radius)',
        md: 'calc(var(--radius) - 2px)',
        sm: 'calc(var(--radius) - 4px)',
      },
      keyframes: {
        'accordion-down': {
          from: { height: '0' },
          to: { height: 'var(--radix-accordion-content-height)' },
        },
        'accordion-up': {
          from: { height: 'var(--radix-accordion-content-height)' },
          to: { height: '0' },
        },
      },
      animation: {
        'accordion-down': 'accordion-down 0.2s ease-out',
        'accordion-up': 'accordion-up 0.2s ease-out',
      },
    },
  },
  plugins: [require('tailwindcss-animate')],
};

export default config;
```

- [ ] **Step 7: Create `web/src/styles/globals.css`**

Overwrite any stub shadcn init may have written. Final content:

```css
@tailwind base;
@tailwind components;
@tailwind utilities;

@layer base {
  :root {
    --background:       240 3.7% 7.1%;
    --foreground:       240 5% 93.3%;
    --card:             240 4% 9%;
    --card-foreground:  240 5% 93.3%;
    --popover:          240 4% 10%;
    --popover-foreground: 240 5% 93.3%;
    --muted:            240 4% 14%;
    --muted-foreground: 240 4% 57%;
    --border:           240 4% 18%;
    --input:            240 4% 18%;
    --ring:             0 0% 98%;

    --primary:              0 0% 98%;
    --primary-foreground:   240 6% 10%;
    --secondary:            240 4% 14%;
    --secondary-foreground: 240 5% 93%;
    --accent:               240 4% 14%;
    --accent-foreground:    240 5% 93%;
    --destructive:            0 72% 51%;
    --destructive-foreground: 0 0% 98%;

    --chart-1:  263 55% 53%;
    --chart-2:  226 70% 56%;
    --chart-3:  206 100% 39%;
    --chart-4:  189 94% 39%;
    --chart-5:  173 80% 36%;
    --chart-6:  142 53% 42%;
    --chart-7:  88 60% 45%;
    --chart-8:  48 96% 53%;
    --chart-9:  25 95% 53%;
    --chart-10: 346 77% 50%;
    --chart-11: 286 68% 55%;

    --radius: 0.625rem;
  }

  body {
    font-family: theme('fontFamily.sans');
    background: hsl(var(--background));
    color: hsl(var(--foreground));
  }
}
```

- [ ] **Step 8: Update `web/src/main.tsx` to import Geist + globals.css**

First read the current `web/src/main.tsx` to see its existing import list verbatim — do not guess. Then add these three imports at the top of the file **above** any other imports:

```ts
import '@fontsource-variable/geist';
import '@fontsource-variable/geist-mono';
import './styles/globals.css';
```

Keep the existing `@fontsource-variable/inter` import and keep any existing `./styles/tokens.css` / `./styles/global.css` imports untouched. Both old and new CSS files coexist throughout Tasks 1–12; Task 13 does the final cleanup.

During the coexistence window, the new `body` rule in `globals.css` (`font-family: theme('fontFamily.sans')`) sets Geist Sans, but the old `body` rules in `global.css`/`tokens.css` may also set `font-family` to Inter. CSS cascade order is determined by the order of imports in `main.tsx`: `globals.css` must import **after** the old `tokens.css`/`global.css` so its `body { font-family: ... }` wins. Verify in Step 13's DevTools check that the Computed `font-family` on `body` is Geist.

- [ ] **Step 9: Verify `web/src/lib/utils.ts` exists and exports `cn`**

shadcn init creates this file. Confirm it contains:

```ts
import { clsx, type ClassValue } from 'clsx';
import { twMerge } from 'tailwind-merge';

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}
```

If missing, create it manually.

- [ ] **Step 10: Verify preflight is disabled**

Sanity-check that `web/tailwind.config.ts` from Step 6 has `corePlugins: { preflight: false }`. Grep:

```bash
grep -n "preflight" web/tailwind.config.ts
```

Expected output: `corePlugins: { preflight: false },` on one line. If missing, go back to Step 6.

- [ ] **Step 11: Run type check to confirm the new config compiles**

```bash
cd web && npx tsc -b
```

Expected: passes (no new errors — all code still CSS-Module-backed).

- [ ] **Step 12: Run existing tests**

```bash
cd web && npx vitest run
```

Expected: all existing tests pass (no app code touched). If any test fails with a new CSS-related error, investigate — it likely means a stray `@tailwind` directive is being parsed twice or preflight slipped through.

- [ ] **Step 13: Boot the dev server and visually confirm no regression**

```bash
cd web && npm run dev
```

Expected: app renders identically to before the commit. Open browser DevTools → Computed styles on `body` → verify `font-family` resolves to `"Geist Variable"` (spec §1.4 calls this check out explicitly). Open the Network tab → filter by `font` → confirm both Geist Variable and Geist Mono Variable .woff2 files load with `font-display: swap`. No visible layout regressions.

- [ ] **Step 14: Commit**

```bash
git add web/package.json web/package-lock.json web/tailwind.config.ts web/postcss.config.js \
  web/components.json web/src/lib/utils.ts web/src/styles/globals.css \
  web/src/main.tsx web/vite.config.ts web/tsconfig.app.json
git commit -m "feat(web): add Tailwind v3 + shadcn init + Geist fonts (preflight off)"
```

---

### Task 2: Install all 21 shadcn primitives in one shot

**Context:** Installing primitives per-commit creates a lot of little `shadcn add` commits with no visible app change. Doing them all at once keeps the noise in one commit. None of these files are imported by app code yet, so this commit is pure-addition and risk-free. Spec §2.1 lists 20 primitives; `chart` is listed separately in §4.1. The `shadcn add` command below includes all 21.

**Files:**
- Create: `web/src/components/ui/button.tsx`, `card.tsx`, `input.tsx`, `label.tsx`, `select.tsx`, `checkbox.tsx`, `form.tsx`, `dialog.tsx`, `sheet.tsx`, `dropdown-menu.tsx`, `popover.tsx`, `command.tsx`, `tabs.tsx`, `table.tsx`, `badge.tsx`, `skeleton.tsx`, `tooltip.tsx`, `sonner.tsx`, `scroll-area.tsx`, `calendar.tsx`, `chart.tsx` (21 files)
- Modified: `web/package.json` / `web/package-lock.json` (transitively — `cmdk`, `sonner`, `react-hook-form`, `@hookform/resolvers`, `zod`, `@radix-ui/*`, `react-day-picker`, `recharts` peer updates if any)

**Spec references:** §2.1, §8 commit 2

- [ ] **Step 1: Run the shadcn add command**

```bash
cd web && npx shadcn@latest add button card input label select checkbox form \
  dialog sheet dropdown-menu popover command tabs table badge \
  skeleton tooltip sonner scroll-area calendar chart
```

Accept any transitive install prompts (cmdk, sonner, react-hook-form, @hookform/resolvers, zod, @radix-ui/*, react-day-picker). Say yes to overwrite if prompted for any file that shadcn init created as a stub.

Expected: 21 files created under `web/src/components/ui/`. `package.json` gains all the Radix peer deps plus `cmdk`, `sonner`, `react-hook-form`, `@hookform/resolvers`, `zod`, `react-day-picker`.

- [ ] **Step 2: Verify each file is present**

```bash
ls web/src/components/ui/
```

Expected: 21 `.tsx` files matching the names in the add command.

- [ ] **Step 3: Type check**

```bash
cd web && npx tsc -b
```

Expected: passes. The primitives are well-typed out of the box.

- [ ] **Step 4: Run existing tests**

```bash
cd web && npx vitest run
```

Expected: all existing tests still pass. No app code imports the new primitives yet.

- [ ] **Step 5: Boot the dev server**

```bash
cd web && npm run dev
```

Expected: app still renders identically.

- [ ] **Step 6: Commit**

```bash
git add web/src/components/ui/ web/package.json web/package-lock.json
git commit -m "feat(web): add shadcn/ui primitives (21 components)"
```

---

### Task 3: `chart-colors.ts` helper (TDD)

**Context:** This is the load-bearing helper that lets every subsequent page rewrite (commits 4–11) stop reading `cat.color` and `tx.category_color` *without* waiting for the DB migration in commit 12. The helper maps category id → CSS variable by `((category.id - 1) % 11) + 1`. A trivial function but it must be tested because the modulo math is easy to get off-by-one.

**Files:**
- Create: `web/src/lib/chart-colors.ts`
- Create: `web/src/lib/chart-colors.test.ts`

**Spec references:** §2.3, §8 commit 3

- [ ] **Step 1: Write the failing test**

Create `web/src/lib/chart-colors.test.ts`:

```ts
import { describe, it, expect } from 'vitest';
import { getCategoryColorVar } from './chart-colors';

describe('getCategoryColorVar', () => {
  it('maps category id 1 to --chart-1', () => {
    expect(getCategoryColorVar({ id: 1 })).toBe('hsl(var(--chart-1))');
  });

  it('maps category id 11 to --chart-11 (boundary)', () => {
    expect(getCategoryColorVar({ id: 11 })).toBe('hsl(var(--chart-11))');
  });

  it('wraps id 12 to --chart-1 (modulo wrap)', () => {
    expect(getCategoryColorVar({ id: 12 })).toBe('hsl(var(--chart-1))');
  });

  it('wraps id 22 to --chart-11', () => {
    expect(getCategoryColorVar({ id: 22 })).toBe('hsl(var(--chart-11))');
  });

  it('wraps id 23 to --chart-1', () => {
    expect(getCategoryColorVar({ id: 23 })).toBe('hsl(var(--chart-1))');
  });
});
```

- [ ] **Step 2: Run the test to confirm it fails**

```bash
cd web && npx vitest run src/lib/chart-colors.test.ts
```

Expected: fails with "Cannot find module './chart-colors'".

- [ ] **Step 3: Implement the helper**

Create `web/src/lib/chart-colors.ts`:

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

- [ ] **Step 4: Run the test to confirm it passes**

```bash
cd web && npx vitest run src/lib/chart-colors.test.ts
```

Expected: all 5 assertions pass.

- [ ] **Step 5: Type check + full test run**

```bash
cd web && npx tsc -b && npx vitest run
```

Expected: both pass.

- [ ] **Step 6: Commit**

```bash
git add web/src/lib/chart-colors.ts web/src/lib/chart-colors.test.ts
git commit -m "feat(web): add getCategoryColorVar chart palette helper"
```

---

## Chunk 2: AppShell + Auth pages rewrite

Two page-rewrite commits that together replace the app shell and the auth
surface with shadcn primitives. These are the first commits that actually
delete CSS Module files and stop consuming the old `useTheme` hook (sidebar
theme toggle is removed — the spec drops light mode).

**Spec references:**
- §3 "Auth" (page rewrites)
- §3 "AppShell / Sidebar" (structural description — preserve layout, switch
  styling to Tailwind + `ScrollArea` + `Tooltip`)
- §8 commit 4 (AppShell + Sidebar)
- §8 commit 5 (Auth pages)
- §Deleted-wholesale list: `AppLayout.module.css`, `Sidebar.module.css`,
  `Auth.module.css`, `useTheme.tsx`

**What is NOT in these tasks (stays for later chunks):**
- Deleting `useTheme.tsx` itself (the hook file is deleted in Task 13 along
  with every other leftover — these tasks just stop importing it).
- Deleting `@fontsource-variable/inter` (also Task 13).
- Any other page. Dashboard / Categories / Transactions / Reports / Settings
  still render via their existing CSS Modules. `<AppShell>` wraps them
  unchanged.
- Touching `tailwind.config.ts`. Preflight stays **disabled** from Chunk 1
  until Task 13 because seven CSS Modules are still live after Chunk 2
  (Dashboard, Categories, Transactions, Reports, Settings, ChartTooltip,
  Tabs) and preflight would reset their baseline styling. Do not re-enable
  preflight in this chunk.

---

### Task 4: AppShell + Sidebar rewrite

**Files:**
- Create: `web/src/components/AppShell.tsx`
- Modify: `web/src/App.tsx` (replace inline `AppLayout` function + drop
  `layoutStyles` import)
- Modify: `web/src/components/Sidebar.tsx` (full rewrite — Tailwind +
  `ScrollArea` + `Tooltip`; remove theme toggle; remove `useTheme` import)
- Modify: `web/src/components/Sidebar.test.tsx` (full rewrite — drop all
  theme-toggle assertions; drop all `className.toContain('expanded')`
  assertions; use `aria-expanded` on the toggle button instead)
- Delete: `web/src/styles/Sidebar.module.css`
- Delete: `web/src/styles/AppLayout.module.css`

**Context for the engineer (read this before starting):**

- The current `Sidebar.tsx` ([web/src/components/Sidebar.tsx](web/src/components/Sidebar.tsx))
  cycles theme via `useTheme()` → `setTheme('light'|'dark'|'system')`. The spec
  drops light mode entirely (§Goals item 5). Remove the theme toggle button
  AND the `useTheme` import from `Sidebar.tsx`. Do NOT delete
  `web/src/hooks/useTheme.tsx` itself — Task 13 sweeps it up. Leaving the file
  dangling for a few commits is intentional: keeps this commit small.
- The sidebar collapse state is driven by `localStorage['spendrop-sidebar']`
  and a `window`-level `sidebar-toggle` custom event, which `AppLayout` in
  [web/src/App.tsx](web/src/App.tsx) (lines 14–44) listens for to resize the
  main content area. The rewrite MUST preserve both the localStorage key and
  the custom event — `AppShell` will still listen for `sidebar-toggle` so
  the main column can shift its left padding when the sidebar expands.
- `AppLayout` is an inline function inside `App.tsx` — it is NOT a separate
  file. You are replacing that function's body, not deleting a file. The
  replacement is: `App.tsx` imports `AppShell` from
  `@/components/AppShell` and uses `<AppShell>` in the protected-route
  `element`. The `<Routes>` tree for nested pages moves into `AppShell`
  (same shape as current `AppLayout`).
- `AppLayout.module.css` ([web/src/styles/AppLayout.module.css](web/src/styles/AppLayout.module.css))
  encodes three rules: `display:flex` on the layout, a 64px-collapsed left
  padding on `main`, and a 240px-expanded left padding on `main`. The
  Tailwind rewrite uses `flex min-h-screen` on the layout, `pl-16`
  (=64px) or `pl-60` (=240px) on `main`, and swaps between them based on
  the expanded state. Max content width `1400px` becomes `max-w-[1400px]`.
- Existing `Sidebar.test.tsx` ([web/src/components/Sidebar.test.tsx](web/src/components/Sidebar.test.tsx))
  mocks `useTheme` and asserts `getByRole('button', { name: /theme/i })` +
  `cycles theme on toggle click`. Both tests get DELETED in the rewrite.
  It also asserts `sidebar.className).toContain('expanded')` in three
  places — swap these for `aria-expanded` queries on the toggle button
  (more robust; doesn't couple tests to Tailwind class strings).
- shadcn `ScrollArea` wraps long nav lists; shadcn `Tooltip` is used only
  when the sidebar is collapsed, to show the label of each icon-only link.
  Both primitives were already installed in Task 2.
- Lucide icons already imported today: `LayoutGrid, ArrowLeftRight,
  ChartNoAxesColumnIncreasing, Tag, Settings, ChevronLeft, ChevronRight,
  LogOut`. Keep these. Drop `Moon, Sun, Monitor` (theme toggle icons).

---

- [ ] **Step 1: Delete the stale CSS Module files up front**

The rewrite has no need for these and their presence during the rewrite
only creates noise in grep / editor search. Delete them as the first step.

```bash
rm web/src/styles/Sidebar.module.css web/src/styles/AppLayout.module.css
```

Expected: files removed. `App.tsx` and `Sidebar.tsx` will temporarily
have dangling imports — that is fine; the next three steps rewrite both
files. Do NOT run `tsc -b` at this point: it will fail on those exact
dangling imports, and the failure is not useful signal (you know the
imports are dangling — you just deleted the files). The typecheck in
Step 6 validates the success state after the rewrite.

- [ ] **Step 2: Create `web/src/components/AppShell.tsx`**

Replace the inline `AppLayout` function. Preserves the `spendrop-sidebar`
localStorage key and the `sidebar-toggle` custom-event wiring.

```tsx
import { useState, useEffect } from 'react';
import { Routes, Route } from 'react-router-dom';
import { Sidebar } from './Sidebar';
import { Dashboard } from '../pages/Dashboard';
import { Transactions } from '../pages/Transactions';
import { Categories } from '../pages/Categories';
import { Reports } from '../pages/Reports';
import { Settings } from '../pages/Settings';

export function AppShell() {
  const [sidebarExpanded, setSidebarExpanded] = useState(
    () => localStorage.getItem('spendrop-sidebar') === 'true',
  );

  useEffect(() => {
    const handler = () => {
      setSidebarExpanded(localStorage.getItem('spendrop-sidebar') === 'true');
    };
    window.addEventListener('sidebar-toggle', handler);
    return () => {
      window.removeEventListener('sidebar-toggle', handler);
    };
  }, []);

  return (
    <div className="flex min-h-screen bg-background text-foreground">
      <Sidebar />
      <main
        className={
          sidebarExpanded
            ? 'flex-1 max-w-[1640px] py-8 pr-10 pl-[calc(240px+2.5rem)]'
            : 'flex-1 max-w-[1464px] py-8 pr-10 pl-[calc(64px+2.5rem)]'
        }
      >
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
```

Note on the math: `py-8 pr-10` is `32px 40px`. The collapsed-max is
`1400 + 64 = 1464`; expanded-max is `1400 + 240 = 1640`. These match
the current `AppLayout.module.css` values exactly so the visual diff
is zero relative to today's layout at this commit.

- [ ] **Step 3: Rewrite `web/src/App.tsx` to use `<AppShell>`**

Drop the inline `AppLayout` function and the `layoutStyles` import. Drop
every page import from `App.tsx` (they now live in `AppShell`).

```tsx
import { Routes, Route } from 'react-router-dom';
import { AppShell } from './components/AppShell';
import { ProtectedRoute } from './components/ProtectedRoute';
import { Login } from './pages/Login';
import { Register } from './pages/Register';

function App() {
  return (
    <Routes>
      <Route path="/login" element={<Login />} />
      <Route path="/register" element={<Register />} />
      <Route
        path="/*"
        element={
          <ProtectedRoute>
            <AppShell />
          </ProtectedRoute>
        }
      />
    </Routes>
  );
}

export default App;
```

- [ ] **Step 4: Rewrite `web/src/components/Sidebar.tsx`**

Full file rewrite. Preserve: nav items, active NavLink styling, logout
button, collapse toggle, localStorage key `spendrop-sidebar`,
`sidebar-toggle` window event dispatch, user footer (name + username +
initial), `aria-label="Toggle sidebar"` on the toggle (kept so existing
`Sidebar.test.tsx` wiring patterns still work after rewrite). Remove:
`useTheme` import, entire theme toggle button + its state, `Moon`/`Sun`/
`Monitor` icons.

**Accessibility callout — do not remove.** The default-collapsed
sidebar shows only Lucide icons for each nav link and the logout
button. Lucide renders plain `<svg>` with no `<title>`, so without an
explicit label the icon-only buttons have **no accessible name at all**
— Testing Library's `getByRole('link', { name: /dashboard/i })` and
`getByRole('button', { name: /log\s*out/i })` will fail because the
accessible name is an empty string. The rewrite below pins the label
as a `<span>` that is always rendered, with `sr-only` applied when
collapsed so it's invisible but still in the accessibility tree. The
icon itself is `aria-hidden="true"` so AT doesn't double-announce.
**Do not "optimize" the `sr-only` spans away on the grounds that they
look redundant** — they are load-bearing for the tests and for screen
reader users.

```tsx
import { useState, useEffect } from 'react';
import { NavLink } from 'react-router-dom';
import {
  LayoutGrid,
  ArrowLeftRight,
  ChartNoAxesColumnIncreasing,
  Tag,
  Settings as SettingsIcon,
  ChevronLeft,
  ChevronRight,
  LogOut,
} from 'lucide-react';
import { useAuth } from '../hooks/useAuth';
import { ScrollArea } from '@/components/ui/scroll-area';
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from '@/components/ui/tooltip';
import { cn } from '@/lib/utils';

const menuItems = [
  { path: '/', label: 'Dashboard', icon: LayoutGrid, end: true },
  { path: '/transactions', label: 'Transactions', icon: ArrowLeftRight },
  { path: '/reports', label: 'Reports', icon: ChartNoAxesColumnIncreasing },
  { path: '/categories', label: 'Categories', icon: Tag },
];

const generalItems = [
  { path: '/settings', label: 'Settings', icon: SettingsIcon },
];

export function Sidebar() {
  const { user, logout } = useAuth();
  const [expanded, setExpanded] = useState(
    () => localStorage.getItem('spendrop-sidebar') === 'true',
  );

  useEffect(() => {
    localStorage.setItem('spendrop-sidebar', String(expanded));
    window.dispatchEvent(new Event('sidebar-toggle'));
  }, [expanded]);

  const initial = user?.display_name?.[0]?.toUpperCase() ?? '?';

  return (
    <TooltipProvider delayDuration={200}>
      <aside
        role="complementary"
        className={cn(
          'fixed left-0 top-0 flex h-screen flex-col border-r border-border bg-card transition-[width] duration-150',
          expanded ? 'w-60' : 'w-16',
        )}
      >
        <div className="flex h-14 items-center justify-between border-b border-border px-4">
          {expanded ? (
            <span className="font-semibold tracking-tight">SpenDrop</span>
          ) : (
            <span className="sr-only">SpenDrop</span>
          )}
          <button
            type="button"
            aria-label="Toggle sidebar"
            aria-expanded={expanded}
            onClick={() => setExpanded((v) => !v)}
            className="inline-flex h-8 w-8 items-center justify-center rounded-md text-muted-foreground hover:bg-muted hover:text-foreground"
          >
            {expanded ? (
              <ChevronLeft className="h-4 w-4" />
            ) : (
              <ChevronRight className="h-4 w-4" />
            )}
          </button>
        </div>

        <ScrollArea className="flex-1">
          <nav className="flex flex-col gap-6 px-2 py-4" aria-label="Primary">
            <SidebarSection
              title="Menu"
              items={menuItems}
              expanded={expanded}
            />
            <div className="flex flex-col gap-1">
              <SidebarSectionTitle expanded={expanded} title="General" />
              {generalItems.map((item) => (
                <SidebarLink key={item.path} item={item} expanded={expanded} />
              ))}
              <Tooltip>
                <TooltipTrigger asChild>
                  <button
                    type="button"
                    onClick={() => void logout()}
                    className="mx-1 flex items-center gap-3 rounded-md px-3 py-2 text-sm text-muted-foreground hover:bg-muted hover:text-foreground"
                  >
                    <LogOut className="h-4 w-4 shrink-0" aria-hidden="true" />
                    <span className={expanded ? undefined : 'sr-only'}>
                      Log out
                    </span>
                  </button>
                </TooltipTrigger>
                {!expanded && (
                  <TooltipContent side="right">Log out</TooltipContent>
                )}
              </Tooltip>
            </div>
          </nav>
        </ScrollArea>

        <div className="flex items-center gap-3 border-t border-border px-3 py-3">
          <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-muted text-sm font-medium">
            {initial}
          </div>
          {expanded && user && (
            <div className="min-w-0">
              <p className="truncate text-sm font-medium">
                {user.display_name}
              </p>
              <p className="truncate text-xs text-muted-foreground">
                @{user.username}
              </p>
            </div>
          )}
        </div>
      </aside>
    </TooltipProvider>
  );
}

function SidebarSection({
  title,
  items,
  expanded,
}: {
  title: string;
  items: typeof menuItems;
  expanded: boolean;
}) {
  return (
    <div className="flex flex-col gap-1">
      <SidebarSectionTitle expanded={expanded} title={title} />
      {items.map((item) => (
        <SidebarLink key={item.path} item={item} expanded={expanded} />
      ))}
    </div>
  );
}

function SidebarSectionTitle({
  title,
  expanded,
}: {
  title: string;
  expanded: boolean;
}) {
  if (!expanded) return null;
  return (
    <p className="px-3 pb-1 text-[10px] font-medium uppercase tracking-wider text-muted-foreground">
      {title}
    </p>
  );
}

function SidebarLink({
  item,
  expanded,
}: {
  item: { path: string; label: string; icon: React.ElementType; end?: boolean };
  expanded: boolean;
}) {
  const Icon = item.icon;
  const link = (
    <NavLink
      to={item.path}
      end={item.end}
      className={({ isActive }) =>
        cn(
          'mx-1 flex items-center gap-3 rounded-md px-3 py-2 text-sm text-muted-foreground hover:bg-muted hover:text-foreground',
          isActive && 'bg-muted text-foreground',
        )
      }
    >
      <Icon className="h-4 w-4 shrink-0" aria-hidden="true" />
      <span className={expanded ? undefined : 'sr-only'}>{item.label}</span>
    </NavLink>
  );
  if (expanded) return link;
  return (
    <Tooltip>
      <TooltipTrigger asChild>{link}</TooltipTrigger>
      <TooltipContent side="right">{item.label}</TooltipContent>
    </Tooltip>
  );
}
```

Two things to notice about this rewrite:
1. `NavLink` renders with `aria-current="page"` automatically when
   `isActive` is true. Tests query for active state via that attribute,
   not via className, so the rewrite is test-stable even if you tweak
   Tailwind classes later.
2. `aria-expanded` on the toggle button lets the test assert the
   expanded state without touching `className`.

- [ ] **Step 5: Rewrite `web/src/components/Sidebar.test.tsx`**

Full file rewrite. Drop `useTheme` mock + both theme tests. Replace
className assertions with `aria-expanded`. Use `aria-current="page"` for
the active-link test.

```tsx
import { describe, test, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';

vi.mock('../hooks/useAuth', () => ({
  useAuth: vi.fn(),
}));

import { useAuth } from '../hooks/useAuth';
import { Sidebar } from './Sidebar';

const mockedUseAuth = vi.mocked(useAuth);

const mockUser = {
  id: 1,
  username: 'alice',
  display_name: 'Alice',
  role: 'admin' as const,
  created_at: '2024-01-01',
};

function renderSidebar(currentPath = '/') {
  return render(
    <MemoryRouter initialEntries={[currentPath]}>
      <Sidebar />
    </MemoryRouter>,
  );
}

describe('Sidebar', () => {
  const mockLogout = vi.fn();

  beforeEach(() => {
    localStorage.clear();
    vi.clearAllMocks();
    mockedUseAuth.mockReturnValue({
      user: mockUser,
      loading: false,
      login: vi.fn(),
      register: vi.fn(),
      logout: mockLogout,
    });
  });

  test('renders all navigation links', () => {
    renderSidebar();
    expect(screen.getByRole('link', { name: /dashboard/i })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: /reports/i })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: /transactions/i })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: /categories/i })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: /settings/i })).toBeInTheDocument();
  });

  test('marks the current route as active via aria-current', () => {
    renderSidebar('/transactions');
    const link = screen.getByRole('link', { name: /transactions/i });
    expect(link).toHaveAttribute('aria-current', 'page');
  });

  test('displays user display name when expanded', async () => {
    const user = userEvent.setup();
    renderSidebar();
    await user.click(screen.getByLabelText('Toggle sidebar'));
    expect(screen.getByText('Alice')).toBeInTheDocument();
  });

  test('displays user avatar initial', () => {
    renderSidebar();
    expect(screen.getByText('A')).toBeInTheDocument();
  });

  test('calls logout when logout button is clicked', async () => {
    const user = userEvent.setup();
    renderSidebar();
    await user.click(screen.getByRole('button', { name: /log\s*out/i }));
    expect(mockLogout).toHaveBeenCalledTimes(1);
  });

  test('has semantic nav element', () => {
    renderSidebar();
    expect(screen.getByRole('navigation')).toBeInTheDocument();
  });

  test('renders collapsed by default', () => {
    renderSidebar();
    expect(screen.getByLabelText('Toggle sidebar')).toHaveAttribute(
      'aria-expanded',
      'false',
    );
  });

  test('expands when toggle button is clicked', async () => {
    const user = userEvent.setup();
    renderSidebar();
    await user.click(screen.getByLabelText('Toggle sidebar'));
    expect(screen.getByLabelText('Toggle sidebar')).toHaveAttribute(
      'aria-expanded',
      'true',
    );
  });

  test('persists expanded state to localStorage', async () => {
    const user = userEvent.setup();
    renderSidebar();
    await user.click(screen.getByLabelText('Toggle sidebar'));
    expect(localStorage.getItem('spendrop-sidebar')).toBe('true');
  });

  test('reads initial state from localStorage', () => {
    localStorage.setItem('spendrop-sidebar', 'true');
    renderSidebar();
    expect(screen.getByLabelText('Toggle sidebar')).toHaveAttribute(
      'aria-expanded',
      'true',
    );
  });

  test('dispatches sidebar-toggle event on toggle', async () => {
    const listener = vi.fn();
    window.addEventListener('sidebar-toggle', listener);
    const user = userEvent.setup();
    renderSidebar();
    await user.click(screen.getByLabelText('Toggle sidebar'));
    expect(listener).toHaveBeenCalled();
    window.removeEventListener('sidebar-toggle', listener);
  });
});
```

Note the removed tests: `displays SpenDrop title` (brand still rendered
but behind `sr-only` when collapsed — covered by the structure, not
worth a brittle string assertion), `does not expand on mouse hover`
(behavior preserved but untestable without real pointer events; keep
the assertion in the collapse test via `aria-expanded='false'`),
`has theme toggle button`, `cycles theme on toggle click` (both theme
tests — theme is removed entirely). The new `dispatches sidebar-toggle
event on toggle` test is net-new and locks in the AppShell contract.

- [ ] **Step 6: Run typecheck**

```bash
cd web && npx tsc -b
```

Expected: pass. No stale `AppLayout.module.css` or `Sidebar.module.css`
imports remain; no references to `useTheme` from `Sidebar.tsx`. If this
FAILS, you have a real bug in Steps 2–5 — fix it before continuing.

- [ ] **Step 7: Run the full frontend test suite**

```bash
cd web && npx vitest run
```

Expected: pass. Focus is on `Sidebar.test.tsx` (rewritten) and anything
else that rendered `<AppShell>` or `<Sidebar>`. No other page tests
should break — the `<AppShell>` preserves the routing tree shape.

- [ ] **Step 8: Visual smoke-test in the dev server**

```bash
cd web && npm run dev
```

Open `http://localhost:5173` and verify:
1. Sidebar renders at 64px collapsed by default
2. Clicking the toggle expands to 240px and shows labels
3. Main content shifts left to compensate when expanded
4. Active nav link is visually distinguishable (muted background)
5. Logout button is still clickable and sits at the bottom of the nav
6. User initial avatar shows `A` for the current user (or whichever)
7. No console warnings from React Router or shadcn primitives

Stop the dev server with Ctrl+C when satisfied.

- [ ] **Step 9: Commit**

```bash
git add web/src/App.tsx \
        web/src/components/AppShell.tsx \
        web/src/components/Sidebar.tsx \
        web/src/components/Sidebar.test.tsx
git rm web/src/styles/AppLayout.module.css web/src/styles/Sidebar.module.css
git commit -m "feat(web): rewrite AppShell + Sidebar with shadcn primitives"
```

The `git rm` is required because Step 1 deleted the files on disk but
did not stage the deletion — committing now without `git rm` would
leave the deletions unstaged.

---

### Task 5: Auth pages rewrite

**Files:**
- Modify: `web/src/pages/Login.tsx` (full rewrite — shadcn `Card` +
  `Form` + `Input` + `Button`)
- Modify: `web/src/pages/Register.tsx` (full rewrite — same pattern
  with display_name field)
- Modify: `web/src/pages/Login.test.tsx` (update imports; assertions
  mostly unchanged thanks to role-based queries)
- Modify: `web/src/pages/Register.test.tsx` (update imports; assertions
  mostly unchanged)
- Delete: `web/src/styles/Auth.module.css`

**Context for the engineer (read this before starting):**

- The spec (§3 "Auth") specifies: centered `Card` with `Form` + `Input`
  + `Label` + `Button`. No hero. Existing `useAuth().login()` and
  `useAuth().register()` hooks are unchanged.
- Both current pages use classic controlled inputs (useState per field).
  The rewrite switches to react-hook-form + Zod via the shadcn `Form`
  primitive (installed in Task 2, which also pulled in
  `@hookform/resolvers` as a transitive dep). If `@hookform/resolvers`
  did NOT come down with `form`, add it explicitly in Step 1 below.
- The existing test files ([Login.test.tsx](web/src/pages/Login.test.tsx),
  [Register.test.tsx](web/src/pages/Register.test.tsx)) query by role
  and label, not by className. That means the rewrite barely touches
  the tests — the existing assertions keep working as long as the
  heading, labels, submit button name, and error-role stay the same.
  The only reason the test files are in the modify list is to update
  imports and to stabilize the "error role" path (see Step 5).
- RHF + shadcn `Form` form fields use `FormMessage` for per-field
  validation errors, and a separate mechanism for server-side errors.
  Keep a plain `<p role="alert">` for the server error so that the
  existing `getByRole('alert')` assertion in both tests keeps passing.
- Zod schemas: username is `string().min(1)`; password is
  `string().min(1)`; display_name is `string().min(1)`. Tight schemas
  here would duplicate server-side validation for no user value, so
  keep the client-side schema to the bare minimum (non-empty strings).
  The point of RHF here is form state management, not re-validating
  the server.

---

- [ ] **Step 1: Verify RHF + resolver are installed**

```bash
cd web && npm ls react-hook-form @hookform/resolvers zod
```

Expected: all three listed. If `@hookform/resolvers` is missing (shadcn
`form` pulls it transitively on most versions, but not always), install
it explicitly:

```bash
cd web && npm install react-hook-form @hookform/resolvers zod
```

These are runtime deps — NOT dev deps.

- [ ] **Step 2: Delete the CSS Module file**

```bash
rm web/src/styles/Auth.module.css
```

Expected: file gone. `Login.tsx` and `Register.tsx` now have dangling
imports. Do NOT run `tsc -b` yet — the expected failure is not useful
signal and the final typecheck in Step 6 validates the success state.

- [ ] **Step 3: Rewrite `web/src/pages/Login.tsx`**

```tsx
import { useState } from 'react';
import { Link } from 'react-router-dom';
import { useForm } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { z } from 'zod';
import { useAuth } from '../hooks/useAuth';
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card';
import {
  Form,
  FormControl,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form';
import { Input } from '@/components/ui/input';
import { Button } from '@/components/ui/button';

const loginSchema = z.object({
  username: z.string().min(1, 'Required'),
  password: z.string().min(1, 'Required'),
});

type LoginValues = z.infer<typeof loginSchema>;

export function Login() {
  const { login } = useAuth();
  const [serverError, setServerError] = useState('');
  const form = useForm<LoginValues>({
    resolver: zodResolver(loginSchema),
    defaultValues: { username: '', password: '' },
  });

  async function onSubmit(values: LoginValues) {
    setServerError('');
    try {
      await login(values.username, values.password);
    } catch (err) {
      setServerError(err instanceof Error ? err.message : 'Login failed');
    }
  }

  return (
    <div className="flex min-h-screen items-center justify-center bg-background p-6">
      <Card className="w-full max-w-sm">
        <CardHeader>
          <CardTitle asChild>
            <h1 className="text-2xl font-semibold tracking-tight">Login</h1>
          </CardTitle>
          <CardDescription>Welcome back to SpenDrop</CardDescription>
        </CardHeader>
        <CardContent>
          <Form {...form}>
            <form
              onSubmit={(e) => void form.handleSubmit(onSubmit)(e)}
              className="flex flex-col gap-4"
            >
              {serverError && (
                <p
                  role="alert"
                  className="rounded-md border border-destructive/50 bg-destructive/10 px-3 py-2 text-sm text-destructive"
                >
                  {serverError}
                </p>
              )}
              <FormField
                control={form.control}
                name="username"
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>Username</FormLabel>
                    <FormControl>
                      <Input autoComplete="username" {...field} />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <FormField
                control={form.control}
                name="password"
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>Password</FormLabel>
                    <FormControl>
                      <Input
                        type="password"
                        autoComplete="current-password"
                        {...field}
                      />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <Button
                type="submit"
                className="w-full"
                disabled={form.formState.isSubmitting}
              >
                {form.formState.isSubmitting ? 'Logging in...' : 'Log in'}
              </Button>
              <p className="text-center text-sm text-muted-foreground">
                No account?{' '}
                <Link
                  to="/register"
                  className="font-medium text-foreground underline-offset-4 hover:underline"
                >
                  Register
                </Link>
              </p>
            </form>
          </Form>
        </CardContent>
      </Card>
    </div>
  );
}
```

Why `CardTitle asChild`: the shadcn `CardTitle` defaults to rendering
a `<div>`. The existing `Login.test.tsx` asserts
`getByRole('heading', { level: 1, name: /login/i })`. `asChild` lets
us render an `<h1>` in its place while keeping the shadcn styling.
Same pattern applies to `Register.tsx`.

- [ ] **Step 4: Rewrite `web/src/pages/Register.tsx`**

```tsx
import { useState } from 'react';
import { Link } from 'react-router-dom';
import { useForm } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { z } from 'zod';
import { useAuth } from '../hooks/useAuth';
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card';
import {
  Form,
  FormControl,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form';
import { Input } from '@/components/ui/input';
import { Button } from '@/components/ui/button';

const registerSchema = z.object({
  username: z.string().min(1, 'Required'),
  password: z.string().min(1, 'Required'),
  displayName: z.string().min(1, 'Required'),
});

type RegisterValues = z.infer<typeof registerSchema>;

export function Register() {
  const { register } = useAuth();
  const [serverError, setServerError] = useState('');
  const form = useForm<RegisterValues>({
    resolver: zodResolver(registerSchema),
    defaultValues: { username: '', password: '', displayName: '' },
  });

  async function onSubmit(values: RegisterValues) {
    setServerError('');
    try {
      await register(values.username, values.password, values.displayName);
    } catch (err) {
      setServerError(
        err instanceof Error ? err.message : 'Registration failed',
      );
    }
  }

  return (
    <div className="flex min-h-screen items-center justify-center bg-background p-6">
      <Card className="w-full max-w-sm">
        <CardHeader>
          <CardTitle asChild>
            <h1 className="text-2xl font-semibold tracking-tight">Register</h1>
          </CardTitle>
          <CardDescription>Create your SpenDrop account</CardDescription>
        </CardHeader>
        <CardContent>
          <Form {...form}>
            <form
              onSubmit={(e) => void form.handleSubmit(onSubmit)(e)}
              className="flex flex-col gap-4"
            >
              {serverError && (
                <p
                  role="alert"
                  className="rounded-md border border-destructive/50 bg-destructive/10 px-3 py-2 text-sm text-destructive"
                >
                  {serverError}
                </p>
              )}
              <FormField
                control={form.control}
                name="username"
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>Username</FormLabel>
                    <FormControl>
                      <Input autoComplete="username" {...field} />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <FormField
                control={form.control}
                name="password"
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>Password</FormLabel>
                    <FormControl>
                      <Input
                        type="password"
                        autoComplete="new-password"
                        {...field}
                      />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <FormField
                control={form.control}
                name="displayName"
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>Display name</FormLabel>
                    <FormControl>
                      <Input {...field} />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <Button
                type="submit"
                className="w-full"
                disabled={form.formState.isSubmitting}
              >
                {form.formState.isSubmitting
                  ? 'Creating account...'
                  : 'Create account'}
              </Button>
              <p className="text-center text-sm text-muted-foreground">
                Already have an account?{' '}
                <Link
                  to="/login"
                  className="font-medium text-foreground underline-offset-4 hover:underline"
                >
                  Log in
                </Link>
              </p>
            </form>
          </Form>
        </CardContent>
      </Card>
    </div>
  );
}
```

- [ ] **Step 5: Update `Login.test.tsx` and `Register.test.tsx` (RHF timing)**

Most assertions already use `getByLabelText`, `getByRole`, and
`getByRole('alert')` — these all still work against the rewritten
pages. The ONE likely source of flake is the "disables submit button
while submitting" test in each file.

The current Login test at
[web/src/pages/Login.test.tsx:92-103](web/src/pages/Login.test.tsx#L92-L103)
reads:

```ts
test('disables submit button while submitting', async () => {
  mockLogin.mockReturnValue(new Promise(() => {}));
  const user = userEvent.setup();
  renderLogin();
  await user.type(screen.getByLabelText(/username/i), 'alice');
  await user.type(screen.getByLabelText(/password/i), 'secret');
  await user.click(screen.getByRole('button', { name: /log\s*in/i }));
  expect(screen.getByRole('button', { name: /log/i })).toBeDisabled();
});
```

With RHF, `formState.isSubmitting` flips to `true` inside a microtask
after `handleSubmit` is invoked. `userEvent.click` awaits microtasks,
so the assertion should work — BUT there's a subtle re-render window.
Proactively wrap the assertion in `waitFor` so the test is stable
regardless of RHF's internal timing:

```ts
test('disables submit button while submitting', async () => {
  mockLogin.mockReturnValue(new Promise(() => {}));
  const user = userEvent.setup();
  renderLogin();
  await user.type(screen.getByLabelText(/username/i), 'alice');
  await user.type(screen.getByLabelText(/password/i), 'secret');
  await user.click(screen.getByRole('button', { name: /log\s*in/i }));
  await waitFor(() => {
    expect(screen.getByRole('button', { name: /log/i })).toBeDisabled();
  });
});
```

Apply the same `waitFor` wrap to the identical test in
[web/src/pages/Register.test.tsx:93-106](web/src/pages/Register.test.tsx#L93-L106).
Note that the Register test queries `{ name: /creating/i }`, which only
matches the `"Creating account..."` text — so the `waitFor` is
mandatory there (the initial button text is `"Create account"`, and
`/creating/i` does NOT match that since "Create" has no "ing" suffix).
Change that test's query to use a more robust selector that works in
both states:

```ts
const submit = screen.getByRole('button', { name: /create account/i });
await user.click(submit);
await waitFor(() => expect(submit).toBeDisabled());
```

`/create account/i` matches both `"Create account"` and
`"Creating account..."`, so the same element reference stays stable
across the state flip. Do NOT weaken the disabled assertion — only
the query is changing.

All other tests (heading, inputs, submit button, link, error, happy
path submit) do not need changes. Run them:

```bash
cd web && npx vitest run src/pages/Login.test.tsx src/pages/Register.test.tsx
```

Expected: all pass.

- [ ] **Step 6: Run typecheck + full test suite**

```bash
cd web && npx tsc -b && npx vitest run
```

Expected: both pass.

- [ ] **Step 7: Visual smoke-test**

```bash
cd web && npm run dev
```

Open `http://localhost:5173/login`:
1. Centered card, max-w-sm, dark background
2. Heading `Login` renders as an `h1` (inspect element → `<h1>`)
3. Username + password inputs render with shadcn styling (border,
   rounded corners, focus ring)
4. Submit button is full-width
5. Typing wrong credentials and submitting shows the destructive-styled
   error banner with `role="alert"`
6. Link to `/register` works
7. `/register` page shows three fields with the same styling

Stop the dev server with Ctrl+C.

- [ ] **Step 8: Commit**

```bash
git add web/src/pages/Login.tsx \
        web/src/pages/Register.tsx \
        web/src/pages/Login.test.tsx \
        web/src/pages/Register.test.tsx
git rm web/src/styles/Auth.module.css
git commit -m "feat(web): rewrite auth pages with shadcn Card + Form"
```

---

## Chunk 3: Dashboard rewrite

Rebuilds the Dashboard page on top of two new local components (`KpiCard` and `ChartCard`) and the shadcn `chart` primitive. This is the **first commit that stops reading `cat.color` and `tx.category_color`** — every color on Dashboard now flows through `getCategoryColorVar({ id })` (added in Task 3). The DB column is still there; we just stop touching it from this page.

**What is NOT in these tasks:**
- Touching any other page. Categories, Transactions, Reports, and Settings still render from their old CSS Modules and still read `cat.color` directly — that is expected and will be cleaned up in Chunks 4–6.
- Deleting `useChartTheme`, `useChartPatterns`, or `ChartTooltip`. Those hooks/components stay in the tree even after Dashboard stops importing them — other pages still depend on them. They are deleted wholesale in Task 13 (final cleanup).
- Touching `tailwind.config.ts`. Preflight stays **disabled** until Task 13 because six CSS Modules are still live after Chunk 3 (Categories, Transactions, Tabs, ChartTooltip, Reports, Settings).
- Changing the data layer. `useDashboard`, `/dashboard/summary`, `/dashboard/trend`, `/dashboard/categories`, and `/transactions` endpoints are untouched. Only presentation code changes in this chunk.
- Dropping the `color` field from `Dashboard.test.tsx` fixtures. Per spec §8 commit 12, the fixture cleanup lands atomically with the DB migration. For this commit the fixture keeps `color: '#818CF8'` / `category_color: '#818CF8'` — the new render code simply does not read those keys.

### Task 6: Dashboard rewrite — KpiCard + ChartCard + shadcn chart primitive

**Context:** The current `Dashboard.tsx` is 706 lines of CSS-Module-driven JSX that imports `useChartTheme`, `useChartPatterns`, `ChartPatternDefs`, and a custom `<ChartTooltip>`. The KPI row uses four hand-rolled `kpiCard` divs with `splitCurrency` for the dollar/cents split. The cash-flow chart is a Recharts `BarChart` wrapped in its own custom legend + 6M/12M toggle. The spending half-gauge is a Recharts `PieChart` (startAngle 180, endAngle 0) with per-slice fills sourced from `cat.color`. The savings progress is a full-ring Pie with percent center. Recent transactions are read from `api.get<PaginatedResponse<Transaction>>` and rendered with `tx.category_color` as the row accent.

We keep the **semantics** (four KPIs, cash flow chart with 6M/12M toggle, spending breakdown, savings progress, recent transactions, month/year selectors, loading/error states, "Welcome back, {name}" heading) but rebuild the **rendering** on shadcn primitives and route every color through `getCategoryColorVar()`. All the existing assertions in `Dashboard.test.tsx` must keep passing — section headings, toggle buttons, month/year selectors, Welcome heading, "does not render removed Monthly Budget section", etc.

**Files:**
- Create: `web/src/components/KpiCard.tsx`
- Create: `web/src/components/KpiCard.test.tsx`
- Create: `web/src/components/ChartCard.tsx`
- Create: `web/src/components/ChartCard.test.tsx`
- Modify (full rewrite): `web/src/pages/Dashboard.tsx`
- Modify (full rewrite): `web/src/pages/Dashboard.test.tsx`
- Delete: `web/src/styles/Dashboard.module.css`

**Spec references:** §2.2 (KpiCard / ChartCard), §2.3 (`getCategoryColorVar`), §3 (Dashboard zones), §4.1 (shadcn chart anatomy), §4.2 (dynamic category palette), §4.3 (per-chart treatments — cash flow, category donut, savings donut), §4.4 (axes/grids/tooltips), §4.6 (deleted), §8 commit 6

- [ ] **Step 1: Write KpiCard failing test**

Create `web/src/components/KpiCard.test.tsx`:

```tsx
import { describe, test, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { Wallet } from 'lucide-react';
import { KpiCard } from './KpiCard';

describe('KpiCard', () => {
  test('renders label, dollars, and cents', () => {
    render(
      <KpiCard
        label="Total Balance"
        icon={Wallet}
        dollars="$2,847"
        cents=".32"
      />,
    );
    expect(screen.getByText('Total Balance')).toBeInTheDocument();
    expect(screen.getByText('$2,847')).toBeInTheDocument();
    expect(screen.getByText('.32')).toBeInTheDocument();
  });

  test('renders delta with up arrow when positive', () => {
    render(
      <KpiCard
        label="Income"
        dollars="$5,200"
        cents=".00"
        delta={{ percent: 3.2, direction: 'up' }}
      />,
    );
    expect(screen.getByText(/3\.2%/)).toBeInTheDocument();
    expect(screen.getByText(/vs last month/i)).toBeInTheDocument();
  });

  test('renders delta with down arrow when negative', () => {
    render(
      <KpiCard
        label="Expenses"
        dollars="$1,200"
        cents=".00"
        delta={{ percent: 8.1, direction: 'down' }}
      />,
    );
    expect(screen.getByText(/8\.1%/)).toBeInTheDocument();
  });

  test('omits the delta row when delta is null', () => {
    render(
      <KpiCard label="Savings Rate" dollars="45" cents="%" delta={null} />,
    );
    expect(screen.queryByText(/vs last month/i)).not.toBeInTheDocument();
  });

  test('applies featured styling when featured is true', () => {
    const { container } = render(
      <KpiCard label="Total Balance" dollars="$100" cents=".00" featured />,
    );
    // Featured cards use the primary background class — verify it lands.
    const card = container.querySelector('[data-featured="true"]');
    expect(card).not.toBeNull();
  });
});
```

- [ ] **Step 2: Run KpiCard test to verify it fails**

```bash
cd web && npx vitest run src/components/KpiCard.test.tsx
```

Expected: FAIL with `Cannot find module './KpiCard'` or `KpiCard is not exported`.

- [ ] **Step 3: Implement KpiCard**

Create `web/src/components/KpiCard.tsx`:

```tsx
import type { LucideIcon } from 'lucide-react';
import { ArrowUpRight, ArrowDownRight } from 'lucide-react';
import { Card, CardContent } from '@/components/ui/card';
import { cn } from '@/lib/utils';

export interface KpiDelta {
  percent: number;
  direction: 'up' | 'down' | 'flat';
}

interface KpiCardProps {
  label: string;
  icon?: LucideIcon;
  dollars: string;
  cents: string;
  delta?: KpiDelta | null;
  featured?: boolean;
}

export function KpiCard({
  label,
  icon: Icon,
  dollars,
  cents,
  delta,
  featured = false,
}: KpiCardProps) {
  return (
    <Card
      data-featured={featured ? 'true' : 'false'}
      className={cn(
        'flex flex-col gap-3.5 p-6 transition-shadow hover:shadow-md',
        featured && 'border-transparent bg-primary text-primary-foreground',
      )}
    >
      <CardContent className="flex flex-col gap-3.5 p-0">
        <div className="flex items-center justify-between">
          <span
            className={cn(
              'text-xs font-medium uppercase tracking-wide',
              featured ? 'text-primary-foreground/80' : 'text-muted-foreground',
            )}
          >
            {label}
          </span>
          {Icon && (
            <div
              className={cn(
                'grid h-9 w-9 shrink-0 place-items-center rounded-lg',
                featured
                  ? 'bg-primary-foreground/15 text-primary-foreground'
                  : 'bg-muted text-muted-foreground',
              )}
            >
              <Icon className="h-[18px] w-[18px]" strokeWidth={1.5} aria-hidden="true" />
            </div>
          )}
        </div>
        <div className="font-mono text-3xl font-bold leading-none tracking-tight tabular-nums">
          {dollars}
          <span
            className={cn(
              'text-lg font-medium',
              featured ? 'text-primary-foreground/70' : 'text-muted-foreground',
            )}
          >
            {cents}
          </span>
        </div>
        {delta && (
          <div className="flex items-center gap-2 text-xs">
            <span
              className={cn(
                'inline-flex items-center gap-1 rounded-md px-2 py-0.5 font-semibold',
                featured && 'bg-primary-foreground/15 text-primary-foreground',
                !featured && delta.direction === 'up' && 'bg-emerald-500/10 text-emerald-500',
                !featured && delta.direction === 'down' && 'bg-rose-500/10 text-rose-500',
                !featured && delta.direction === 'flat' && 'bg-muted text-muted-foreground',
              )}
            >
              {delta.direction === 'up' && (
                <ArrowUpRight className="h-3 w-3" strokeWidth={3} aria-hidden="true" />
              )}
              {delta.direction === 'down' && (
                <ArrowDownRight className="h-3 w-3" strokeWidth={3} aria-hidden="true" />
              )}
              {delta.percent.toFixed(1)}%
            </span>
            <span
              className={cn(
                featured ? 'text-primary-foreground/60' : 'text-muted-foreground',
              )}
            >
              vs last month
            </span>
          </div>
        )}
      </CardContent>
    </Card>
  );
}
```

**Note on `emerald-500` / `rose-500`:** these are Tailwind's built-in named colors, not tokens from `globals.css`. They ship with Tailwind regardless of preflight being disabled. The delta arrows are utility pills, not chart data — using built-in Tailwind hues is consistent with the monochrome chrome + colorful data philosophy (the only chrome color is the primary accent; deltas are intentionally chromatic to signal direction). If a reviewer later wants these routed through `--chart-6` / `--chart-10`, that is a follow-up, not part of this task.

- [ ] **Step 4: Run KpiCard test to verify it passes**

```bash
cd web && npx vitest run src/components/KpiCard.test.tsx
```

Expected: all 5 tests pass.

- [ ] **Step 5: Write ChartCard failing test**

Create `web/src/components/ChartCard.test.tsx`:

```tsx
import { describe, test, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { ChartCard } from './ChartCard';

describe('ChartCard', () => {
  test('renders title and children', () => {
    render(
      <ChartCard title="Cash Flow">
        <div data-testid="chart-body">chart here</div>
      </ChartCard>,
    );
    expect(screen.getByText('Cash Flow')).toBeInTheDocument();
    expect(screen.getByTestId('chart-body')).toBeInTheDocument();
  });

  test('renders subtitle when provided', () => {
    render(
      <ChartCard title="Cash Flow" subtitle="Income vs expenses">
        <div />
      </ChartCard>,
    );
    expect(screen.getByText('Income vs expenses')).toBeInTheDocument();
  });

  test('renders action slot when provided', () => {
    render(
      <ChartCard
        title="Cash Flow"
        action={<button type="button">6M</button>}
      >
        <div />
      </ChartCard>,
    );
    expect(screen.getByRole('button', { name: '6M' })).toBeInTheDocument();
  });

  test('renders skeleton (not children) when loading', () => {
    render(
      <ChartCard title="Cash Flow" loading>
        <div data-testid="chart-body">chart here</div>
      </ChartCard>,
    );
    expect(screen.queryByTestId('chart-body')).not.toBeInTheDocument();
    expect(screen.getByTestId('chart-loading')).toBeInTheDocument();
  });
});
```

- [ ] **Step 6: Run ChartCard test to verify it fails**

```bash
cd web && npx vitest run src/components/ChartCard.test.tsx
```

Expected: FAIL with `Cannot find module './ChartCard'`.

- [ ] **Step 7: Implement ChartCard**

Create `web/src/components/ChartCard.tsx`:

```tsx
import type { ReactNode } from 'react';
import { Card, CardHeader, CardDescription, CardContent } from '@/components/ui/card';
import { Skeleton } from '@/components/ui/skeleton';
import { cn } from '@/lib/utils';

interface ChartCardProps {
  title: string;
  subtitle?: string;
  action?: ReactNode;
  loading?: boolean;
  className?: string;
  children: ReactNode;
}

export function ChartCard({
  title,
  subtitle,
  action,
  loading = false,
  className,
  children,
}: ChartCardProps) {
  return (
    <Card className={cn('flex flex-col', className)}>
      <CardHeader className="flex flex-row items-start justify-between gap-4 space-y-0 pb-4">
        <div className="flex flex-col gap-0.5">
          {/*
            Use a semantic <h2> (not shadcn's default CardTitle <div>) to keep
            the heading hierarchy consistent under the Dashboard <h1>. Classes
            mirror CardTitle's shadcn defaults so visual output is unchanged.
          */}
          <h2 className="text-base font-semibold leading-none tracking-tight">
            {title}
          </h2>
          {subtitle && (
            <CardDescription className="text-xs text-muted-foreground">
              {subtitle}
            </CardDescription>
          )}
        </div>
        {action && <div className="shrink-0">{action}</div>}
      </CardHeader>
      <CardContent className="flex flex-1 flex-col pt-0">
        {loading ? (
          <Skeleton data-testid="chart-loading" className="h-64 w-full" />
        ) : (
          children
        )}
      </CardContent>
    </Card>
  );
}
```

- [ ] **Step 8: Run ChartCard test to verify it passes**

```bash
cd web && npx vitest run src/components/ChartCard.test.tsx
```

Expected: all 4 tests pass.

- [ ] **Step 9: Delete `Dashboard.module.css`**

```bash
rm web/src/styles/Dashboard.module.css
```

**Do NOT run `tsc -b` at this point.** Deleting the stylesheet leaves `Dashboard.tsx` importing a file that no longer exists — typecheck will fail in spectacular fashion. Step 10 rewrites `Dashboard.tsx` to drop the stylesheet import entirely. The typecheck in Step 12 validates the success state.

- [ ] **Step 10: Rewrite `Dashboard.tsx`**

Full replacement. Every line of the old file is replaced. Key differences from the current file:

1. No `styles` import. No `styles.*` classes anywhere.
2. No `useChartTheme`, no `useChartPatterns`, no `ChartPatternDefs`, no custom `<ChartTooltip>`.
3. `BarChart` is wrapped in shadcn `<ChartContainer>` from `@/components/ui/chart` with `chartConfig` declaring `income` → `--chart-6` and `expense` → `--chart-10`. Recharts `<Bar>` uses `fill="var(--color-income)"` / `fill="var(--color-expense)"` — these CSS variables are scoped by `ChartContainer` at paint time (spec §4.1).
4. Category donut `PieChart` builds its `chartConfig` dynamically per render via `categories.reduce(...)` using `getCategoryColorVar({ id: cat.id })` (spec §4.2). Per-slice `<Cell fill={getCategoryColorVar({ id: cat.id })} />`.
5. Savings donut uses two slices: filled = `hsl(var(--chart-1))`, unfilled = `hsl(var(--muted))` (spec §4.3).
6. Recent transactions list sources the row dot color from `getCategoryColorVar({ id: tx.category_id })`. The `tx.category_color` field is still in the payload (backend unchanged) but it is never read.
7. 6M/12M toggle becomes a shadcn `<Tabs>` in the cash-flow card header action slot.
8. Loading state uses shadcn `<Skeleton>` inside `KpiCard`-shaped stubs + a `<Skeleton className="h-64">` in the chart area.
9. Welcome heading is `<h1>` — `screen.getByRole('heading', { level: 1 })` in the test file still resolves.
10. Month/Year selectors use native `<select>` + `<Label>` pair — `getByLabelText(/month/i)` / `getByLabelText(/year/i)` in the existing test still resolves. (shadcn `<Select>` would require mocking in tests and is intentionally not used here — native select is fine for this one place.)

Replace the entire contents of `web/src/pages/Dashboard.tsx` with:

```tsx
import { useState, useEffect, useMemo } from 'react';
import {
  ResponsiveContainer,
  BarChart,
  Bar,
  XAxis,
  YAxis,
  CartesianGrid,
  PieChart,
  Pie,
  Cell,
} from 'recharts';
import { Wallet, TrendingUp, TrendingDown, Database } from 'lucide-react';
import { Link } from 'react-router-dom';
import { useDashboard } from '../hooks/useDashboard';
import { useAuth } from '../hooks/useAuth';
import { api } from '../api/client';
import { getCategoryColorVar } from '@/lib/chart-colors';
import { KpiCard, type KpiDelta } from '@/components/KpiCard';
import { ChartCard } from '@/components/ChartCard';
import {
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
  type ChartConfig,
} from '@/components/ui/chart';
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { Skeleton } from '@/components/ui/skeleton';
import { Label } from '@/components/ui/label';
import { Button } from '@/components/ui/button';
import { Card, CardContent } from '@/components/ui/card';
import type { Transaction, PaginatedResponse } from '../api/types';

/* ── Formatters ── */

function splitCurrency(amount: number): { dollars: string; cents: string } {
  const abs = Math.abs(amount);
  const dollars = Math.floor(abs).toLocaleString('en-US');
  const cents = (abs % 1).toFixed(2).slice(1); // ".52"
  return { dollars: `$${dollars}`, cents };
}

function formatFull(amount: number): string {
  return '$' + Math.abs(amount).toLocaleString('en-US', {
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  });
}

function pctChange(current: number, previous: number): number | null {
  if (previous === 0) return null;
  return ((current - previous) / Math.abs(previous)) * 100;
}

function toDelta(value: number | null): KpiDelta | null {
  if (value == null) return null;
  return {
    percent: Math.abs(value),
    direction: value > 0 ? 'up' : value < 0 ? 'down' : 'flat',
  };
}

/* ── Constants ── */

const MONTHS = [
  'January', 'February', 'March', 'April', 'May', 'June',
  'July', 'August', 'September', 'October', 'November', 'December',
];

const SHORT_MONTHS = [
  'Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun',
  'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec',
];

type CashFlowView = '6m' | '12m';

const cashFlowConfig: ChartConfig = {
  income: { label: 'Income', color: 'hsl(var(--chart-6))' },
  expense: { label: 'Expense', color: 'hsl(var(--chart-10))' },
};

const savingsConfig: ChartConfig = {
  filled: { label: 'Saved', color: 'hsl(var(--chart-1))' },
  rest: { label: 'Remaining', color: 'hsl(var(--muted))' },
};

/* ── Component ── */

export function Dashboard() {
  const { user } = useAuth();
  const now = new Date();
  const [selectedYear, setSelectedYear] = useState(now.getFullYear());
  const [selectedMonth, setSelectedMonth] = useState(now.getMonth() + 1);
  const [cashFlowView, setCashFlowView] = useState<CashFlowView>('6m');
  const { summary, trend, categories, loading, error } = useDashboard(
    selectedYear,
    selectedMonth,
  );
  const [recentTransactions, setRecentTransactions] = useState<Transaction[]>([]);
  const [showLatest, setShowLatest] = useState(false);

  useEffect(() => {
    let cancelled = false;
    let url = 'transactions?per_page=6';
    if (!showLatest) {
      const mm = String(selectedMonth).padStart(2, '0');
      const lastDay = new Date(selectedYear, selectedMonth, 0).getDate();
      const dd = String(lastDay).padStart(2, '0');
      url += `&date_from=${selectedYear}-${mm}-01&date_to=${selectedYear}-${mm}-${dd}`;
    }
    api
      .get<PaginatedResponse<Transaction>>(url)
      .then((data) => { if (!cancelled) setRecentTransactions(data.transactions); })
      .catch(() => { /* silent — non-critical */ });
    return () => { cancelled = true; };
  }, [selectedYear, selectedMonth, showLatest]);

  const currentYear = now.getFullYear();
  const yearOptions = Array.from({ length: 5 }, (_, i) => currentYear - i);

  /* ── Derived ── */

  const totalIncome = summary?.total_income ?? 0;
  const totalExpense = summary?.total_spent ?? 0;
  const totalBalance = totalIncome - totalExpense;

  const chartData = useMemo(() => {
    const sorted = [...trend].reverse();
    const sliced = cashFlowView === '6m' ? sorted.slice(-6) : sorted;
    return sliced.map((item) => ({
      name: SHORT_MONTHS[item.month - 1],
      income: item.total_income,
      expense: item.total_spent,
    }));
  }, [trend, cashFlowView]);

  const totalCategorySpent = categories.reduce((sum, cat) => sum + cat.total, 0);

  // Category donut data — per-slice color from getCategoryColorVar.
  //
  // gaugeData is memoized on `categories` so that `categoryChartConfig`'s
  // useMemo below actually works. Without this, gaugeData would be a new
  // array reference on every render and categoryChartConfig would recompute
  // unnecessarily (defeating the memo and breaking referential equality for
  // anything consuming the chart config).
  //
  // The synthetic "Other" bucket intentionally bypasses getCategoryColorVar
  // and uses a flat muted-foreground color. Passing id: -1 to the formula
  // would yield slot `((-1 - 1) % 11) + 1 = -1` which is nonsense —
  // getCategoryColorVar expects positive ids only. Giving "Other" a neutral
  // color also signals visually that it's an aggregate, not a real category.
  const gaugeData = useMemo(() => {
    const topCats = categories.slice(0, 5);
    const otherTotal = categories
      .slice(5)
      .reduce((sum, cat) => sum + cat.total, 0);
    return [
      ...topCats.map((cat) => ({
        id: cat.id,
        name: cat.name,
        value: cat.total,
        color: getCategoryColorVar({ id: cat.id }),
      })),
      ...(otherTotal > 0
        ? [{
            id: -1,
            name: 'Other',
            value: otherTotal,
            color: 'hsl(var(--muted-foreground))',
          }]
        : []),
    ];
  }, [categories]);

  const categoryChartConfig = useMemo<ChartConfig>(() => {
    return gaugeData.reduce<ChartConfig>((acc, slice) => {
      acc[slice.name] = { label: slice.name, color: slice.color };
      return acc;
    }, {});
  }, [gaugeData]);

  const savingsRate = totalIncome > 0
    ? ((totalIncome - totalExpense) / totalIncome * 100)
    : 0;

  const prevMonthTrend = (() => {
    const prevM = selectedMonth === 1 ? 12 : selectedMonth - 1;
    const prevY = selectedMonth === 1 ? selectedYear - 1 : selectedYear;
    return trend.find(t => t.year === prevY && t.month === prevM);
  })();

  const balanceDelta = prevMonthTrend
    ? pctChange(totalBalance, prevMonthTrend.total_income - prevMonthTrend.total_spent)
    : null;
  const incomeDelta = prevMonthTrend
    ? pctChange(totalIncome, prevMonthTrend.total_income)
    : null;
  const expenseDelta = prevMonthTrend
    ? pctChange(totalExpense, prevMonthTrend.total_spent)
    : null;
  const savingsRatePrev = prevMonthTrend && prevMonthTrend.total_income > 0
    ? ((prevMonthTrend.total_income - prevMonthTrend.total_spent) / prevMonthTrend.total_income * 100)
    : null;
  const savingsDelta = savingsRatePrev !== null ? savingsRate - savingsRatePrev : null;

  const balanceSplit = splitCurrency(totalBalance);
  const incomeSplit = splitCurrency(totalIncome);
  const expenseSplit = splitCurrency(totalExpense);

  const savingsGoalPct = summary?.savings_goal_progress ?? 0;
  const savingsData = [
    { name: 'filled', value: savingsGoalPct },
    { name: 'rest', value: Math.max(0, 100 - savingsGoalPct) },
  ];

  /* ── Loading state ── */
  if (loading) {
    return (
      <div className="flex flex-col gap-6">
        <div className="flex items-center justify-between">
          <Skeleton className="h-8 w-64" />
          <Skeleton className="h-9 w-48" />
        </div>
        <div className="grid grid-cols-1 gap-3 md:grid-cols-2 xl:grid-cols-4">
          {[1, 2, 3, 4].map((n) => (
            <Card key={n} className="p-6">
              <Skeleton className="mb-3 h-3 w-1/2" />
              <Skeleton className="mb-3 h-8 w-3/4" />
              <Skeleton className="h-3 w-2/3" />
            </Card>
          ))}
        </div>
        <div className="grid grid-cols-1 gap-3 lg:grid-cols-5">
          <Card className="p-6 lg:col-span-3">
            <Skeleton className="mb-4 h-4 w-1/4" />
            <Skeleton className="h-64 w-full" />
          </Card>
          <Card className="p-6 lg:col-span-2">
            <Skeleton className="mb-4 h-4 w-1/3" />
            <Skeleton className="h-48 w-full" />
          </Card>
        </div>
      </div>
    );
  }

  /* ── Error state ── */
  if (error) {
    return (
      <div className="flex flex-col gap-6">
        <h1 className="text-2xl font-bold tracking-tight">Dashboard</h1>
        <Card className="flex flex-col items-center gap-4 p-12 text-center" role="alert">
          <p className="text-muted-foreground">{error}</p>
          <Button onClick={() => window.location.reload()}>Retry</Button>
        </Card>
      </div>
    );
  }

  /* ── Render ── */
  return (
    <div className="flex flex-col gap-6">
      {/* Header */}
      <div className="flex flex-col gap-3 md:flex-row md:items-center md:justify-between">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">
            Welcome back, {user?.display_name ?? 'there'}
          </h1>
          <p className="mt-0.5 text-sm text-muted-foreground">
            Here's what's happening with your finances.
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Label htmlFor="dash-month" className="sr-only">Month</Label>
          <select
            id="dash-month"
            aria-label="Month"
            className="h-9 rounded-md border border-input bg-background px-3 text-sm font-medium"
            value={selectedMonth}
            onChange={(e) => setSelectedMonth(Number(e.target.value))}
          >
            {MONTHS.map((m, i) => (
              <option key={m} value={i + 1}>{m}</option>
            ))}
          </select>
          <Label htmlFor="dash-year" className="sr-only">Year</Label>
          <select
            id="dash-year"
            aria-label="Year"
            className="h-9 rounded-md border border-input bg-background px-3 text-sm font-medium"
            value={selectedYear}
            onChange={(e) => setSelectedYear(Number(e.target.value))}
          >
            {yearOptions.map((y) => (
              <option key={y} value={y}>{y}</option>
            ))}
          </select>
        </div>
      </div>

      {/* KPI row */}
      {summary && (
        <div className="grid grid-cols-1 gap-3 md:grid-cols-2 xl:grid-cols-4">
          <KpiCard
            label="Total Balance"
            icon={Wallet}
            dollars={balanceSplit.dollars}
            cents={balanceSplit.cents}
            delta={toDelta(balanceDelta)}
            featured
          />
          <KpiCard
            label="Income"
            icon={TrendingUp}
            dollars={incomeSplit.dollars}
            cents={incomeSplit.cents}
            delta={toDelta(incomeDelta)}
          />
          <KpiCard
            label="Expenses"
            icon={TrendingDown}
            dollars={expenseSplit.dollars}
            cents={expenseSplit.cents}
            delta={toDelta(expenseDelta == null ? null : -expenseDelta)}
          />
          <KpiCard
            label="Savings Rate"
            icon={Database}
            dollars={savingsRate.toFixed(1)}
            cents="%"
            delta={toDelta(savingsDelta)}
          />
        </div>
      )}

      {/* Cash flow — full width */}
      <ChartCard
        title="Cash Flow"
        subtitle="Income vs expenses over time"
        action={
          <Tabs
            value={cashFlowView}
            onValueChange={(v) => setCashFlowView(v as CashFlowView)}
          >
            <TabsList className="h-8">
              <TabsTrigger value="6m" className="h-6 px-3 text-xs">6M</TabsTrigger>
              <TabsTrigger value="12m" className="h-6 px-3 text-xs">12M</TabsTrigger>
            </TabsList>
          </Tabs>
        }
      >
        <ChartContainer config={cashFlowConfig} className="h-64 w-full">
          <ResponsiveContainer width="100%" height="100%">
            <BarChart data={chartData} margin={{ top: 4, right: 0, bottom: 0, left: 0 }} barGap={4}>
              <CartesianGrid
                strokeDasharray="3 3"
                stroke="hsl(var(--border))"
                vertical={false}
              />
              <XAxis
                dataKey="name"
                tickLine={false}
                axisLine={false}
                className="text-xs"
                stroke="hsl(var(--muted-foreground))"
              />
              <YAxis
                tickLine={false}
                axisLine={false}
                className="text-xs"
                stroke="hsl(var(--muted-foreground))"
              />
              <ChartTooltip content={<ChartTooltipContent />} />
              <Bar dataKey="income" fill="var(--color-income)" radius={[4, 4, 0, 0]} />
              <Bar dataKey="expense" fill="var(--color-expense)" radius={[4, 4, 0, 0]} />
            </BarChart>
          </ResponsiveContainer>
        </ChartContainer>
      </ChartCard>

      {/* Bottom grid */}
      <div className="grid grid-cols-1 gap-3 lg:grid-cols-5">
        {/* Spending by Category */}
        <ChartCard
          title="Spending by Category"
          subtitle={formatFull(totalCategorySpent)}
          className="lg:col-span-3"
        >
          {gaugeData.length === 0 ? (
            <div className="flex flex-1 items-center justify-center p-8 text-sm text-muted-foreground">
              No spending yet this month.
            </div>
          ) : (
            <div className="flex flex-1 flex-col gap-4 md:flex-row md:items-center">
              <ChartContainer
                config={categoryChartConfig}
                className="h-48 w-full md:w-1/2"
              >
                <ResponsiveContainer width="100%" height="100%">
                  <PieChart>
                    <ChartTooltip content={<ChartTooltipContent hideLabel />} />
                    <Pie
                      data={gaugeData}
                      dataKey="value"
                      nameKey="name"
                      innerRadius={60}
                      outerRadius={90}
                      paddingAngle={2}
                      strokeWidth={0}
                    >
                      {gaugeData.map((slice) => (
                        <Cell key={slice.name} fill={slice.color} />
                      ))}
                    </Pie>
                  </PieChart>
                </ResponsiveContainer>
              </ChartContainer>
              <ul className="flex flex-1 flex-col gap-2 md:w-1/2">
                {gaugeData.map((slice) => {
                  const pct = totalCategorySpent > 0
                    ? (slice.value / totalCategorySpent) * 100
                    : 0;
                  return (
                    <li key={slice.name} className="flex items-center gap-3 text-sm">
                      <span
                        className="h-2.5 w-2.5 shrink-0 rounded-sm"
                        style={{ backgroundColor: slice.color }}
                        aria-hidden="true"
                      />
                      <span className="flex-1 truncate font-medium">{slice.name}</span>
                      <span className="font-mono tabular-nums text-muted-foreground">
                        {pct.toFixed(0)}%
                      </span>
                      <span className="min-w-20 text-right font-mono font-semibold tabular-nums">
                        {formatFull(slice.value)}
                      </span>
                    </li>
                  );
                })}
              </ul>
            </div>
          )}
        </ChartCard>

        {/* Recent Transactions */}
        <Card className="flex flex-col p-6 lg:col-span-2">
          <div className="mb-4 flex items-center justify-between">
            <div>
              <h2 className="text-base font-semibold tracking-tight">Recent Transactions</h2>
              <button
                type="button"
                className="mt-0.5 text-xs font-medium text-primary hover:underline"
                onClick={() => setShowLatest((v) => !v)}
              >
                {showLatest ? 'Show this month' : 'Show latest'}
              </button>
            </div>
            <Link
              to="/transactions"
              className="text-xs font-medium text-primary hover:underline"
            >
              View all →
            </Link>
          </div>
          {recentTransactions.length === 0 ? (
            <div className="flex flex-1 items-center justify-center p-8 text-sm text-muted-foreground">
              No transactions yet.
            </div>
          ) : (
            <ul className="flex flex-col divide-y divide-border">
              {recentTransactions.map((tx) => {
                const color = getCategoryColorVar({ id: tx.category_id });
                return (
                  <li key={tx.id} className="flex items-center gap-3 py-2.5">
                    <span
                      className="h-9 w-9 shrink-0 rounded-full"
                      style={{
                        backgroundColor: `color-mix(in srgb, ${color} 15%, transparent)`,
                      }}
                      aria-hidden="true"
                    />
                    <div className="min-w-0 flex-1">
                      <div className="truncate text-sm font-medium">{tx.description}</div>
                      <div className="text-xs text-muted-foreground">{tx.category_name}</div>
                    </div>
                    <div className="text-right">
                      <div
                        className={
                          tx.category_type === 'income'
                            ? 'font-mono text-sm font-semibold tabular-nums text-emerald-500'
                            : 'font-mono text-sm font-semibold tabular-nums'
                        }
                      >
                        {tx.category_type === 'income' ? '+' : '-'}
                        {formatFull(tx.amount)}
                      </div>
                      <div className="text-xs text-muted-foreground">{tx.date}</div>
                    </div>
                  </li>
                );
              })}
            </ul>
          )}
        </Card>
      </div>

      {/* Savings Progress — full width */}
      <ChartCard
        title="Savings Progress"
        subtitle={`${savingsGoalPct.toFixed(0)}% of goal`}
      >
        <div className="flex flex-col items-center gap-6 md:flex-row md:justify-around">
          <ChartContainer config={savingsConfig} className="h-48 w-48">
            <ResponsiveContainer width="100%" height="100%">
              <PieChart>
                <Pie
                  data={savingsData}
                  dataKey="value"
                  nameKey="name"
                  innerRadius={60}
                  outerRadius={88}
                  strokeWidth={0}
                  startAngle={90}
                  endAngle={-270}
                >
                  <Cell fill="hsl(var(--chart-1))" />
                  <Cell fill="hsl(var(--muted))" />
                </Pie>
              </PieChart>
            </ResponsiveContainer>
          </ChartContainer>
          <div className="flex flex-col items-center gap-1 text-center">
            <div className="font-mono text-3xl font-bold tracking-tight tabular-nums">
              {savingsGoalPct.toFixed(0)}%
            </div>
            <div className="text-xs text-muted-foreground">of goal</div>
          </div>
          <div className="flex gap-8">
            <div className="text-center">
              <div className="font-mono text-base font-semibold tabular-nums">
                {formatFull(summary?.savings_ytd ?? 0)}
              </div>
              <div className="mt-0.5 text-xs text-muted-foreground">Saved YTD</div>
            </div>
            <div className="text-center">
              <div className="font-mono text-base font-semibold tabular-nums">
                {formatFull(summary?.savings_goal ?? 0)}
              </div>
              <div className="mt-0.5 text-xs text-muted-foreground">Annual Goal</div>
            </div>
          </div>
        </div>
      </ChartCard>
    </div>
  );
}
```

**Why the expense delta is inverted** (`toDelta(expenseDelta == null ? null : -expenseDelta)`): a positive `expenseDelta` means expenses went up, which is *bad*. `toDelta` renders positive as the green up-arrow, so we negate the sign before passing it in — positive expense growth shows as a red down-arrow badge. The current CSS-Module implementation achieves the same effect with `kpiBadgeNegative` when `expenseDelta >= 0`.

**Why `formatCompact` is gone:** it was only used once in the original file for a chart axis tick formatter that we replaced with Recharts default. If a reviewer wants compact axis labels later, re-add as `tickFormatter`.

- [ ] **Step 11: Rewrite `Dashboard.test.tsx`**

Replace the entire contents of `web/src/pages/Dashboard.test.tsx` with:

```tsx
import { describe, test, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import React from 'react';

// Recharts mock — Recharts ships as ES modules that don't render in jsdom
// without a ResizeObserver, so every component is stubbed to a passthrough.
//
// IMPORTANT: shadcn's `src/components/ui/chart.tsx` does:
//
//     import * as RechartsPrimitive from "recharts"
//     const ChartLegend = RechartsPrimitive.Legend
//     // and ChartStyle reads RechartsPrimitive.* directly
//
// Because it's a namespace import, any name referenced at module-eval time
// that's missing from this mock becomes `undefined` and React crashes with
// "Element type is invalid: expected a string or a class/function but got:
// undefined" as soon as ChartContainer mounts. The mock below therefore
// declares *every* Recharts surface shadcn's chart helper can touch, not
// just the ones Dashboard.tsx uses directly. Add new stubs here whenever a
// future chart primitive starts pulling in more Recharts exports.
vi.mock('recharts', () => ({
  // Used by Dashboard.tsx
  ResponsiveContainer: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  BarChart: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  Bar: () => <div />,
  XAxis: () => <div />,
  YAxis: () => <div />,
  CartesianGrid: () => <div />,
  PieChart: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  Pie: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  Cell: () => <div />,
  // Referenced by shadcn's chart.tsx namespace import (ChartLegend, ChartStyle, tooltip plumbing)
  Tooltip: () => <div />,
  Legend: () => <div />,
  Surface: () => <div />,
  Layer: () => <div />,
  Sector: () => <div />,
  LabelList: () => <div />,
  Customized: () => <div />,
  ReferenceLine: () => <div />,
}));

vi.mock('../hooks/useDashboard', () => ({
  useDashboard: () => ({
    summary: {
      budget: 5000,
      total_spent: 3200,
      total_income: 4500,
      remaining: 1300,
      savings_this_month: 500,
      savings_goal: 10000,
      savings_ytd: 7500,
      savings_goal_progress: 75,
    },
    trend: [
      { year: 2026, month: 3, total_spent: 2800, total_income: 4200 },
      { year: 2026, month: 4, total_spent: 3200, total_income: 4500 },
    ],
    categories: [
      { id: 1, name: 'Food', color: '#818CF8', total: 1200 },
      { id: 2, name: 'Transport', color: '#7EC89B', total: 800 },
    ],
    loading: false,
    error: '',
  }),
}));

vi.mock('../api/client', () => ({
  api: {
    get: vi.fn().mockResolvedValue({
      transactions: [
        {
          id: 1,
          user_id: 1,
          date: '2026-04-01',
          amount: 42.50,
          original_amount: null,
          original_currency: null,
          description: 'Groceries',
          category_id: 1,
          category_name: 'Food',
          category_type: 'expense',
          category_color: '#818CF8',
          tags: null,
          notes: null,
          created_at: '2026-04-01T00:00:00Z',
          updated_at: '2026-04-01T00:00:00Z',
        },
      ],
      total: 1,
      page: 1,
      per_page: 6,
      total_pages: 1,
    }),
  },
}));

vi.mock('../hooks/useAuth', () => ({
  useAuth: () => ({
    user: { id: 1, username: 'elie', display_name: 'Elie' },
    isAuthenticated: true,
    loading: false,
    login: vi.fn(),
    register: vi.fn(),
    logout: vi.fn(),
  }),
}));

import { Dashboard } from './Dashboard';

describe('Dashboard', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  test('renders welcome heading with user name', () => {
    render(<MemoryRouter><Dashboard /></MemoryRouter>);
    expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent(/Welcome back, Elie/);
  });

  test('renders month/year selectors', () => {
    render(<MemoryRouter><Dashboard /></MemoryRouter>);
    expect(screen.getByLabelText(/month/i)).toBeInTheDocument();
    expect(screen.getByLabelText(/year/i)).toBeInTheDocument();
  });

  test('renders KPI cards with Total Balance', async () => {
    render(<MemoryRouter><Dashboard /></MemoryRouter>);
    await waitFor(() => {
      expect(screen.getByText('Total Balance')).toBeInTheDocument();
      expect(screen.getAllByText('Income').length).toBeGreaterThanOrEqual(1);
      expect(screen.getAllByText('Expenses').length).toBeGreaterThanOrEqual(1);
      expect(screen.getByText('Savings Rate')).toBeInTheDocument();
    });
  });

  test('renders Cash Flow section', async () => {
    render(<MemoryRouter><Dashboard /></MemoryRouter>);
    await waitFor(() => {
      expect(screen.getByText('Cash Flow')).toBeInTheDocument();
    });
  });

  test('renders Spending and Recent Transactions sections', async () => {
    render(<MemoryRouter><Dashboard /></MemoryRouter>);
    await waitFor(() => {
      expect(screen.getByText('Spending by Category')).toBeInTheDocument();
      expect(screen.getByText('Recent Transactions')).toBeInTheDocument();
    });
  });

  test('renders Savings Progress section', async () => {
    render(<MemoryRouter><Dashboard /></MemoryRouter>);
    await waitFor(() => {
      expect(screen.getByText('Savings Progress')).toBeInTheDocument();
      expect(screen.getByText('Saved YTD')).toBeInTheDocument();
      expect(screen.getByText('Annual Goal')).toBeInTheDocument();
    });
  });

  test('does not render removed Monthly Budget section', () => {
    render(<MemoryRouter><Dashboard /></MemoryRouter>);
    expect(screen.queryByText('Monthly Budget')).not.toBeInTheDocument();
  });

  test('renders 6M and 12M toggle buttons', () => {
    render(<MemoryRouter><Dashboard /></MemoryRouter>);
    expect(screen.getByRole('tab', { name: '6M' })).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: '12M' })).toBeInTheDocument();
  });
});
```

**What changed from the old test file:**
1. Deleted `vi.mock('../hooks/useChartTheme', ...)` — hook is no longer imported by Dashboard.
2. Deleted `vi.mock('../hooks/useChartPatterns', ...)` — ditto.
3. Deleted `vi.mock('../components/ChartTooltip', ...)` — ditto.
4. Dropped the `transaction_count` field from the category fixture (it was never part of `CategoryBreakdownItem` — the old fixture had an extra untyped field that was silently discarded).
5. Expanded the transaction fixture to match the full `Transaction` shape (added `user_id`, `original_amount`, `original_currency`, `tags`, `notes`, `created_at`, `updated_at`) so TypeScript's structural check passes against the imported type even though the mock factory is loose.
6. Kept `color: '#818CF8'` / `category_color: '#818CF8'` in the fixtures — the new render code ignores them but the fixture shape still needs to satisfy `CategoryBreakdownItem.color` and `Transaction.category_color` (those types still have the field until Task 12). Task 12 drops both keys from both fixtures atomically with the DB migration.
7. "6M"/"12M" assertions now use `getByRole('tab', ...)` since they render inside shadcn `<Tabs>` (which creates `role="tab"` elements). The old `getByText('6M')` would still match the visible text, but the role query is more stable and self-documenting.
8. Dropped the `expect(screen.getByText('of goal')).toBeInTheDocument()` assertion from the Savings Progress test. The new layout shows `X% of goal` as a subtitle plus a separate "of goal" label under the donut center, but if we assert both we get a duplicate-text failure. Keeping the two unambiguous assertions (`Saved YTD`, `Annual Goal`) is enough to prove the section rendered.

- [ ] **Step 12: Typecheck**

```bash
cd web && npx tsc -b
```

Expected: PASS (no errors). If `tsc` complains about `ChartConfig` not being exported, verify `src/components/ui/chart.tsx` was installed by `shadcn add chart` in Task 2 — it should export `ChartConfig` as a type alias. If missing, run `npx shadcn@latest add chart` again.

- [ ] **Step 13: Run full test suite**

```bash
cd web && npx vitest run
```

Expected: all existing tests + the new `KpiCard.test.tsx` + `ChartCard.test.tsx` + rewritten `Dashboard.test.tsx` pass. Pay specific attention to:
- `Dashboard.test.tsx` — all 8 test cases
- `KpiCard.test.tsx` — 5 cases
- `ChartCard.test.tsx` — 4 cases
- `chart-colors.test.ts` (from Task 3) — still passes
- `Sidebar.test.tsx` / `Login.test.tsx` / `Register.test.tsx` (from Chunk 2) — still passes

If a Chunk 2 test breaks, a Chunk 3 change probably leaked into its dependencies — investigate before committing.

- [ ] **Step 14: Visual smoke-test**

```bash
cd web && npm run dev
```

Open the dev server URL, log in (or register), and verify:
1. Dashboard renders without a blank page or a hydration error in the console
2. "Welcome back, {name}" heading at top-left
3. Month and Year selectors at top-right work (changing the month re-triggers `useDashboard`)
4. Four KPI cards render in a row (desktop) — Total Balance in the featured primary-accent style, Income / Expenses / Savings Rate in neutral style
5. Cash Flow chart renders below the KPI row; 6M/12M tabs switch the bar count between 6 and 12 bars; income bars are green (`--chart-6`), expense bars are crimson (`--chart-10`)
6. Spending by Category card shows a donut + a legend list; donut slice colors come from the `--chart-N` palette (violet → indigo → blue → cyan → teal → green → lime → amber → orange → crimson → orchid for category IDs 1–11)
7. Recent Transactions card shows up to 6 rows with category colors flowing from the same `--chart-N` slots; "Show latest" toggles the date filter
8. Savings Progress card shows a mini donut with the percentage + YTD + Annual Goal numbers
9. No stray references to the old `styles.*` classes in the DOM inspector — every element uses Tailwind utility classes or shadcn's `data-*` attributes

Stop the dev server with Ctrl+C.

- [ ] **Step 15: Commit**

```bash
git add web/src/components/KpiCard.tsx \
        web/src/components/KpiCard.test.tsx \
        web/src/components/ChartCard.tsx \
        web/src/components/ChartCard.test.tsx \
        web/src/pages/Dashboard.tsx \
        web/src/pages/Dashboard.test.tsx
git rm web/src/styles/Dashboard.module.css
git commit -m "feat(web): rewrite dashboard with shadcn chart primitive + KpiCard/ChartCard"
```

---

## Chunk 4: Categories page

This chunk covers commit 7 of the migration order (spec §8). One commit — the Categories page rewrite. No category-color rendering happens here (the existing page showed a color swatch; the new page doesn't), so `getCategoryColorVar()` isn't used on this page. The `color` field on the `Category` TypeScript interface stays (it's dropped in Chunk 8, commit 12); this commit only stops *writing* a color value from the Add/Edit form.

### Task 7: Categories page rewrite (commit 7)

**What this commit does:** Replace `Categories.tsx`'s current grid-of-cards-with-color-swatches layout with a single shadcn `Card` containing a `Table` (name, type `Badge`, transaction count, kebab actions). The color picker is removed. Adding and editing categories both open a right-side `Sheet` with a `Form` containing name + type + icon inputs only — no color field. The edit form stops submitting a `color` value; the backend silently keeps the old stored color until commit 12 drops the column.

**Scope trade-off — drag-and-drop reorder UX is removed.** The current page supports drag-drop reordering of categories via `POST /categories/reorder`. The new Table-with-kebab layout from spec §3 has no column for a drag handle, and the spec does not list a reorder kebab action. The `POST /categories/reorder` API endpoint on the Go backend stays intact (untouched in this commit), so the feature can be reintroduced later via a "Reorder categories" dialog if needed. In this commit the drag-drop `describe` block in `Categories.test.tsx` is removed along with the drag-drop handlers in `Categories.tsx`. Call this out in the commit body so the trade-off is visible in git log.

**Scope trade-off — transaction-count column is a placeholder.** Spec §3 line 307 lists "transaction count" as a column, but the `Category` API shape does not currently include a count field. Adding one would widen the scope of this commit into backend work. Render a placeholder em-dash (`—`) in the count column with a `// TODO: transaction count requires backend API change (spec §3)` comment above the cell. No backend change in this commit.

**Files:**
- Modify: `web/src/pages/Categories.tsx` (346 lines → rewritten in full, target ~350 lines including the inline `CategoryEditorSheet` sub-component)
- Modify: `web/src/pages/Categories.test.tsx` (229 lines → rewritten in full, drag-drop describe block removed, target ~250 lines)
- Read (reference): `web/src/components/ui/card.tsx`, `sheet.tsx`, `table.tsx`, `form.tsx`, `input.tsx`, `select.tsx`, `badge.tsx`, `dropdown-menu.tsx`, `button.tsx`, `label.tsx`
- Read (reference): `web/src/lib/chart-colors.ts` — **not used on this page**, but familiarity helps when Task 8 lands
- Read (reference): `web/src/api/types.ts` `Category` interface — unchanged in this commit
- Do NOT delete: `web/src/styles/Categories.module.css` — the final-cleanup commit (Chunk 8) sweeps it alongside the other remaining `*.module.css` files

**Spec references:** §2.2 (CategoryEditor row), §3 (Categories layout), §8 commit 7

- [ ] **Step 1: Read the current Categories implementation and test**

Run:

```bash
cd web && wc -l src/pages/Categories.tsx src/pages/Categories.test.tsx
```

Expected: `346 src/pages/Categories.tsx`, `229 src/pages/Categories.test.tsx`.

Open both files in your editor. Note the current contract you must preserve:
1. Admin-only: add + edit buttons visible only when `useAuth().user.role === 'admin'`
2. Two sections: expense categories and income categories, rendered under clear headings
3. Category names are fetched from `GET /categories?include_inactive=true`
4. Creating a category calls `POST /categories` with `{ name, icon, type }` — **no `color` field submitted**
5. Updating calls `PUT /categories/:id` with `{ name, icon }` — **no `color` field submitted**
6. Toggling active state calls `PATCH /categories/:id` with `{ is_active: boolean }`
7. Errors from the API display in an alert banner

Dropped in this commit:
- Drag-drop reorder UX (handlers + describe block)
- Color swatch `<div>` next to each category name
- Color picker `<input type="color">` in the edit form

- [ ] **Step 2: Rewrite the test file (test-first for the new component shape)**

Replace the contents of `web/src/pages/Categories.test.tsx` with:

```tsx
import { describe, test, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';

vi.mock('../hooks/useAuth', () => ({
  useAuth: vi.fn(),
}));

vi.mock('../api/client', () => ({
  api: {
    get: vi.fn(),
    post: vi.fn(),
    put: vi.fn(),
    patch: vi.fn(),
    del: vi.fn(),
  },
}));

import { useAuth } from '../hooks/useAuth';
import { api } from '../api/client';
import { Categories } from './Categories';

const mockedUseAuth = vi.mocked(useAuth);
const mockedApi = vi.mocked(api);

// Fixtures still carry `color` — the backend still returns it until commit 12.
// These tests no longer assert anything about `color` visually.
const mockCategories = [
  {
    id: 1,
    name: 'Food',
    type: 'expense' as const,
    color: '#ff0000',
    icon: null,
    sort_order: 0,
    is_active: true,
    created_at: '2024-01-01',
  },
  {
    id: 2,
    name: 'Salary',
    type: 'income' as const,
    color: '#00ff00',
    icon: null,
    sort_order: 0,
    is_active: true,
    created_at: '2024-01-01',
  },
  {
    id: 3,
    name: 'Transport',
    type: 'expense' as const,
    color: '#0000ff',
    icon: null,
    sort_order: 1,
    is_active: false,
    created_at: '2024-01-01',
  },
];

function renderCategories() {
  return render(
    <MemoryRouter>
      <Categories />
    </MemoryRouter>,
  );
}

function asAdmin() {
  mockedUseAuth.mockReturnValue({
    user: {
      id: 1,
      username: 'alice',
      display_name: 'Alice',
      role: 'admin',
      created_at: '2024-01-01',
    },
    loading: false,
    login: vi.fn(),
    register: vi.fn(),
    logout: vi.fn(),
  });
}

function asMember() {
  mockedUseAuth.mockReturnValue({
    user: {
      id: 2,
      username: 'bob',
      display_name: 'Bob',
      role: 'member',
      created_at: '2024-01-01',
    },
    loading: false,
    login: vi.fn(),
    register: vi.fn(),
    logout: vi.fn(),
  });
}

describe('Categories', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockedApi.get.mockResolvedValue(mockCategories);
  });

  describe('as admin', () => {
    beforeEach(asAdmin);

    test('renders the Categories heading', async () => {
      renderCategories();
      expect(
        screen.getByRole('heading', { level: 1, name: /categories/i }),
      ).toBeInTheDocument();
    });

    test('renders category rows from API', async () => {
      renderCategories();
      await waitFor(() => {
        expect(screen.getByText('Food')).toBeInTheDocument();
        expect(screen.getByText('Salary')).toBeInTheDocument();
        expect(screen.getByText('Transport')).toBeInTheDocument();
      });
    });

    test('renders a type badge for each category', async () => {
      renderCategories();
      await waitFor(() => {
        // Expense and Income labels appear as badges in rows
        expect(screen.getAllByText(/expense/i).length).toBeGreaterThan(0);
        expect(screen.getAllByText(/income/i).length).toBeGreaterThan(0);
      });
    });

    test('renders Add category button for admin', async () => {
      renderCategories();
      await waitFor(() => {
        expect(
          screen.getByRole('button', { name: /add category/i }),
        ).toBeInTheDocument();
      });
    });

    test('clicking Add category opens a Sheet with a name input and type select', async () => {
      const user = userEvent.setup();
      renderCategories();
      await waitFor(() => {
        expect(screen.getByText('Food')).toBeInTheDocument();
      });

      await user.click(screen.getByRole('button', { name: /add category/i }));

      // Sheet dialog with form fields
      expect(screen.getByRole('dialog')).toBeInTheDocument();
      expect(screen.getByLabelText(/name/i)).toBeInTheDocument();
      expect(screen.getByLabelText(/type/i)).toBeInTheDocument();
      // Icon input is optional
      expect(screen.getByLabelText(/icon/i)).toBeInTheDocument();
      // No color input anywhere in the form
      expect(screen.queryByLabelText(/color/i)).not.toBeInTheDocument();
    });

    test('submitting the Add Sheet posts to categories without a color field', async () => {
      const user = userEvent.setup();
      mockedApi.post.mockResolvedValue({});
      renderCategories();
      await waitFor(() => {
        expect(screen.getByText('Food')).toBeInTheDocument();
      });

      await user.click(screen.getByRole('button', { name: /add category/i }));
      await user.type(screen.getByLabelText(/name/i), 'Rent');
      // Type defaults to 'expense' — leave as-is
      await user.click(screen.getByRole('button', { name: /save/i }));

      await waitFor(() => {
        expect(mockedApi.post).toHaveBeenCalledWith(
          'categories',
          expect.objectContaining({ name: 'Rent', type: 'expense' }),
        );
      });
      // Explicit: no color field in the payload
      const payload = mockedApi.post.mock.calls[0][1] as Record<string, unknown>;
      expect(payload).not.toHaveProperty('color');
    });

    test('row kebab menu opens Edit and Activate/Deactivate actions', async () => {
      const user = userEvent.setup();
      renderCategories();
      await waitFor(() => {
        expect(screen.getByText('Food')).toBeInTheDocument();
      });

      // Each row has an "Actions for {name}" trigger
      await user.click(
        screen.getByRole('button', { name: /actions for food/i }),
      );
      expect(screen.getByRole('menuitem', { name: /edit/i })).toBeInTheDocument();
      expect(
        screen.getByRole('menuitem', { name: /deactivate/i }),
      ).toBeInTheDocument();
    });

    test('inactive category kebab shows Activate', async () => {
      const user = userEvent.setup();
      renderCategories();
      await waitFor(() => {
        expect(screen.getByText('Transport')).toBeInTheDocument();
      });

      await user.click(
        screen.getByRole('button', { name: /actions for transport/i }),
      );
      expect(
        screen.getByRole('menuitem', { name: /activate/i }),
      ).toBeInTheDocument();
    });

    test('Edit menu item opens a Sheet with existing category values', async () => {
      const user = userEvent.setup();
      renderCategories();
      await waitFor(() => {
        expect(screen.getByText('Food')).toBeInTheDocument();
      });

      await user.click(
        screen.getByRole('button', { name: /actions for food/i }),
      );
      await user.click(screen.getByRole('menuitem', { name: /edit/i }));

      expect(screen.getByRole('dialog')).toBeInTheDocument();
      expect(screen.getByLabelText(/name/i)).toHaveValue('Food');
      // No color input in edit form either
      expect(screen.queryByLabelText(/color/i)).not.toBeInTheDocument();
      // Type is immutable after creation — no Type select in edit mode
      expect(screen.queryByLabelText(/type/i)).not.toBeInTheDocument();
    });

    test('saving edits posts PUT without color or type fields', async () => {
      const user = userEvent.setup();
      mockedApi.put.mockResolvedValue({});
      renderCategories();
      await waitFor(() => {
        expect(screen.getByText('Food')).toBeInTheDocument();
      });

      await user.click(
        screen.getByRole('button', { name: /actions for food/i }),
      );
      await user.click(screen.getByRole('menuitem', { name: /edit/i }));

      const nameInput = screen.getByLabelText(/name/i);
      await user.clear(nameInput);
      await user.type(nameInput, 'Groceries');
      await user.click(screen.getByRole('button', { name: /save/i }));

      await waitFor(() => {
        expect(mockedApi.put).toHaveBeenCalledWith(
          'categories/1',
          expect.objectContaining({ name: 'Groceries' }),
        );
      });
      const payload = mockedApi.put.mock.calls[0][1] as Record<string, unknown>;
      // Backend only accepts {name, color, icon}; color is intentionally
      // omitted until commit 12 drops the column, and type is immutable.
      expect(payload).not.toHaveProperty('color');
      expect(payload).not.toHaveProperty('type');
    });

    test('Deactivate menu item PATCHes is_active=false', async () => {
      const user = userEvent.setup();
      mockedApi.patch.mockResolvedValue({});
      renderCategories();
      await waitFor(() => {
        expect(screen.getByText('Food')).toBeInTheDocument();
      });

      await user.click(
        screen.getByRole('button', { name: /actions for food/i }),
      );
      await user.click(screen.getByRole('menuitem', { name: /deactivate/i }));

      await waitFor(() => {
        expect(mockedApi.patch).toHaveBeenCalledWith('categories/1', {
          is_active: false,
        });
      });
    });
  });

  describe('as member', () => {
    beforeEach(asMember);

    test('hides Add category button for non-admin', async () => {
      renderCategories();
      await waitFor(() => {
        expect(screen.getByText('Food')).toBeInTheDocument();
      });
      expect(
        screen.queryByRole('button', { name: /add category/i }),
      ).not.toBeInTheDocument();
    });

    test('hides row kebab menus for non-admin', async () => {
      renderCategories();
      await waitFor(() => {
        expect(screen.getByText('Food')).toBeInTheDocument();
      });
      expect(
        screen.queryByRole('button', { name: /actions for food/i }),
      ).not.toBeInTheDocument();
    });
  });
});
```

- [ ] **Step 3: Run the tests to verify they fail**

```bash
cd web && npx vitest run src/pages/Categories.test.tsx
```

Expected: FAIL. The existing `Categories.tsx` exports a component that passes the old tests but doesn't satisfy the new ones — in particular the new `/add category/i` label, the kebab menu trigger ("Actions for {name}"), and the no-color-field assertions will all fail. Some tests may incidentally pass (e.g. the heading test).

- [ ] **Step 4: Rewrite `Categories.tsx`**

Replace the contents of `web/src/pages/Categories.tsx` with:

```tsx
import { useState, useEffect, useCallback } from 'react';
import { MoreHorizontal, Plus } from 'lucide-react';
import { api } from '../api/client';
import { useAuth } from '../hooks/useAuth';
import type { Category } from '../api/types';
import { Button } from '@/components/ui/button';
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
} from '@/components/ui/card';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import { Badge } from '@/components/ui/badge';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';

type CategoryType = 'expense' | 'income';

interface CategoryFormData {
  name: string;
  type: CategoryType;
  icon: string;
}

interface CategoryEditorState {
  mode: 'create' | 'edit';
  category?: Category;
}

export function Categories() {
  const { user } = useAuth();
  const isAdmin = user?.role === 'admin';

  const [categories, setCategories] = useState<Category[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [editor, setEditor] = useState<CategoryEditorState | null>(null);

  const fetchCategories = useCallback(() => {
    setLoading(true);
    api
      .get<Category[]>('categories?include_inactive=true')
      .then((cats) => {
        setCategories(cats);
        setError('');
      })
      .catch((err) => {
        setError(
          err instanceof Error ? err.message : 'Failed to load categories',
        );
      })
      .finally(() => setLoading(false));
  }, []);

  useEffect(() => {
    fetchCategories();
  }, [fetchCategories]);

  async function handleSave(data: CategoryFormData) {
    try {
      if (editor?.mode === 'edit' && editor.category) {
        // PUT only sends `name` and `icon` — the backend's
        // `handleUpdateCategory` accepts only {name, color, icon}, and a
        // category's `type` is immutable after creation. `color` is
        // intentionally omitted; the backend keeps the stored value until
        // commit 12 drops the column.
        await api.put(`categories/${editor.category.id}`, {
          name: data.name,
          icon: data.icon,
        });
      } else {
        await api.post('categories', {
          name: data.name,
          type: data.type,
          icon: data.icon,
        });
      }
      setEditor(null);
      fetchCategories();
    } catch (err) {
      setError(
        err instanceof Error ? err.message : 'Failed to save category',
      );
    }
  }

  async function handleToggleActive(cat: Category) {
    try {
      await api.patch(`categories/${cat.id}`, { is_active: !cat.is_active });
      fetchCategories();
    } catch (err) {
      setError(
        err instanceof Error ? err.message : 'Failed to update category',
      );
    }
  }

  // Sort: expense first, then income; within each group, by sort_order
  const sortedCategories = [...categories].sort((a, b) => {
    if (a.type !== b.type) return a.type === 'expense' ? -1 : 1;
    return a.sort_order - b.sort_order;
  });

  return (
    <div className="container mx-auto max-w-4xl space-y-6 p-6">
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-semibold tracking-tight">Categories</h1>
        {isAdmin && (
          <Button
            onClick={() => setEditor({ mode: 'create' })}
            aria-label="Add category"
          >
            <Plus className="mr-2 h-4 w-4" />
            Add category
          </Button>
        )}
      </div>

      {error && (
        <div
          role="alert"
          className="rounded-md border border-destructive/50 bg-destructive/10 px-4 py-3 text-sm text-destructive"
        >
          {error}
        </div>
      )}

      <Card>
        <CardHeader>
          <CardDescription>
            Manage expense and income categories. Deactivated categories stay
            attached to past transactions but no longer appear in the entry row.
          </CardDescription>
        </CardHeader>
        <CardContent>
          {loading ? (
            <p className="text-sm text-muted-foreground">
              Loading categories…
            </p>
          ) : sortedCategories.length === 0 ? (
            <p className="text-sm text-muted-foreground">
              No categories yet.
            </p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Name</TableHead>
                  <TableHead>Type</TableHead>
                  <TableHead className="text-right">Transactions</TableHead>
                  <TableHead className="w-12" />
                </TableRow>
              </TableHeader>
              <TableBody>
                {sortedCategories.map((cat) => (
                  <TableRow
                    key={cat.id}
                    className={!cat.is_active ? 'opacity-60' : undefined}
                  >
                    <TableCell className="font-medium">
                      {cat.name}
                      {!cat.is_active && (
                        <span className="ml-2 text-xs text-muted-foreground">
                          (inactive)
                        </span>
                      )}
                    </TableCell>
                    <TableCell>
                      <Badge
                        variant={cat.type === 'expense' ? 'outline' : 'secondary'}
                      >
                        {cat.type === 'expense' ? 'Expense' : 'Income'}
                      </Badge>
                    </TableCell>
                    {/* TODO: transaction count requires backend API change (spec §3) */}
                    <TableCell className="text-right font-mono tabular-nums text-muted-foreground">
                      —
                    </TableCell>
                    <TableCell>
                      {isAdmin && (
                        <DropdownMenu>
                          <DropdownMenuTrigger asChild>
                            <Button
                              variant="ghost"
                              size="icon"
                              className="h-8 w-8"
                              aria-label={`Actions for ${cat.name}`}
                            >
                              <MoreHorizontal className="h-4 w-4" />
                            </Button>
                          </DropdownMenuTrigger>
                          <DropdownMenuContent align="end">
                            <DropdownMenuItem
                              onClick={() =>
                                setEditor({ mode: 'edit', category: cat })
                              }
                            >
                              Edit
                            </DropdownMenuItem>
                            <DropdownMenuItem
                              onClick={() => void handleToggleActive(cat)}
                            >
                              {cat.is_active ? 'Deactivate' : 'Activate'}
                            </DropdownMenuItem>
                          </DropdownMenuContent>
                        </DropdownMenu>
                      )}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>

      <CategoryEditorSheet
        state={editor}
        onClose={() => setEditor(null)}
        onSave={(data) => void handleSave(data)}
      />
    </div>
  );
}

function CategoryEditorSheet({
  state,
  onClose,
  onSave,
}: {
  state: CategoryEditorState | null;
  onClose: () => void;
  onSave: (data: CategoryFormData) => void;
}) {
  const [name, setName] = useState('');
  const [type, setType] = useState<CategoryType>('expense');
  const [icon, setIcon] = useState('');

  // Seed the form whenever the editor is (re)opened
  useEffect(() => {
    if (!state) return;
    if (state.mode === 'edit' && state.category) {
      setName(state.category.name);
      setType(state.category.type);
      setIcon(state.category.icon ?? '');
    } else {
      setName('');
      setType('expense');
      setIcon('');
    }
  }, [state]);

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    if (!name.trim()) return;
    onSave({ name: name.trim(), type, icon: icon.trim() });
  }

  return (
    <Sheet
      open={state !== null}
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
    >
      <SheetContent side="right" className="sm:max-w-md">
        <SheetHeader>
          <SheetTitle>
            {state?.mode === 'edit' ? 'Edit category' : 'Add category'}
          </SheetTitle>
          <SheetDescription>
            {state?.mode === 'edit'
              ? "Update this category's name or icon. Type can't be changed after creation."
              : 'Create a new expense or income category.'}
          </SheetDescription>
        </SheetHeader>

        <form onSubmit={handleSubmit} className="mt-6 space-y-4">
          <div className="space-y-2">
            <Label htmlFor="category-name">Name</Label>
            <Input
              id="category-name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="e.g. Groceries"
              required
            />
          </div>

          {state?.mode === 'create' && (
            <div className="space-y-2">
              <Label htmlFor="category-type">Type</Label>
              <Select
                value={type}
                onValueChange={(v) => setType(v as CategoryType)}
              >
                <SelectTrigger id="category-type">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="expense">Expense</SelectItem>
                  <SelectItem value="income">Income</SelectItem>
                </SelectContent>
              </Select>
            </div>
          )}

          <div className="space-y-2">
            <Label htmlFor="category-icon">Icon (optional)</Label>
            <Input
              id="category-icon"
              value={icon}
              onChange={(e) => setIcon(e.target.value)}
              placeholder="e.g. 🛒"
            />
          </div>

          <SheetFooter className="mt-6 gap-2">
            <Button type="button" variant="outline" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit">Save</Button>
          </SheetFooter>
        </form>
      </SheetContent>
    </Sheet>
  );
}
```

Notes:
- The file drops the CSS-Modules import entirely. Existing `Categories.module.css` stays on disk (deleted by the final-cleanup commit in Chunk 8).
- `CategoryEditorSheet` is a plain component in the same file — this matches the pattern used in the rewritten Dashboard and keeps the editor's state local.
- The kebab trigger uses `aria-label="Actions for {name}"` so tests can query it by role.
- The `color` field is never submitted; the backend still accepts the old value from the DB because the column still exists.
- `useEffect` seeds the form whenever `state` changes, which handles the Sheet re-opening for a different category without stale values.

- [ ] **Step 5: Run the tests to verify they pass**

```bash
cd web && npx vitest run src/pages/Categories.test.tsx
```

Expected: all tests pass.

If `Select` complains about uncontrolled `value` in tests, verify the initial state seeding runs — the `useEffect([state])` hook in `CategoryEditorSheet` must run before any `user.type` interaction. If a test fires on the seed being missing, ensure `renderCategories()` has `await waitFor(() => expect(screen.getByText('Food'))…)` *before* the Add-button click.

- [ ] **Step 6: Type check the whole app**

```bash
cd web && npx tsc -b
```

Expected: passes. The `Category` interface in `api/types.ts` still carries `color: string` — this commit doesn't touch that field. If `tsc` complains about an unused import in `Categories.tsx`, drop it.

- [ ] **Step 7: Run the full test suite**

```bash
cd web && npx vitest run
```

Expected: every existing test still passes. The Dashboard and Transactions fixture files (with `color`) still satisfy the `Category` interface — this commit doesn't remove the field.

- [ ] **Step 8: Dev-server smoke check**

```bash
cd web && npm run dev
```

Expected: Vite dev server starts on its default port with no build errors. Open the app, log in as an admin user, and navigate to `/categories`. Verify:
1. The page renders a single card with a table listing every category (expense then income)
2. Each expense row shows an outline `Badge` reading "Expense"; income rows show a secondary `Badge` reading "Income"
3. The transactions column shows an em-dash (`—`) placeholder
4. Clicking "Add category" opens a right-side `Sheet` with Name, Type, and Icon inputs — **no color picker**
5. Submitting Add creates a new category and the Sheet closes
6. Kebab → Edit opens the Sheet with the existing values; visually confirm the Type select is **not** rendered in edit mode (type is immutable — you should see only Name and Icon); submit PUTs with only `{name, icon}` in the body (check the Network tab request body)
7. Kebab → Deactivate greys out the row; kebab re-opens and now says "Activate"
8. Logging in as a non-admin user hides the Add button and the kebab menus

Stop the dev server with Ctrl+C.

- [ ] **Step 9: Commit**

```bash
git add web/src/pages/Categories.tsx web/src/pages/Categories.test.tsx
git commit -m "feat(web): rewrite categories page with shadcn Table + Sheet editor

- Remove color picker from UI (Category.color field still exists server-side
  until commit 12 drops the column)
- Replace grid-of-cards with a single Card + Table + kebab DropdownMenu
- Type select is no longer rendered in edit mode — the backend's
  handleUpdateCategory ignores \`type\` on PUT, and a category's type is
  immutable after creation. PUT body is now {name, icon} only.
- Remove drag-drop reorder UX; POST /categories/reorder backend endpoint
  stays for a future reintroduction
- Transaction count column is a placeholder (—) until a backend API change
- Categories.module.css is NOT deleted in this commit — it will be swept by
  the final-cleanup commit alongside the other remaining *.module.css files"
```

---

## Chunk 5: Transactions page non-entry rewrite (commit 8)

This chunk converts the Transactions page chrome — page shell, toolbar, filter Sheet, table, and row actions — to Tailwind + shadcn primitives. The entry form **is not** rewritten here: the old `TransactionEntry` component stays wired in via a Tailwind `hidden` toggle. Commit 9 (Chunk 6) rewrites it from scratch under TDD.

**Deliberate deviations from spec §8 commit 8:**

The spec §8 bullet for commit 8 literally instructs us to "delete `Transactions.module.css`, `Tabs.module.css`, `ChartTooltip.module.css`". We cannot follow that instruction in this commit because doing so would break the build. Every deletion is deferred to Chunk 8 / commit 13 (final cleanup):

| File | Still imported by (after commit 8) | Swept in |
|---|---|---|
| `web/src/styles/Transactions.module.css` | `components/TagInput.tsx` (kept as-is per spec §5), `components/TransactionEntry.tsx` (deleted in commit 9, not here) | commit 13 |
| `web/src/styles/Tabs.module.css` | `components/Tabs.tsx` (used by `pages/Settings.tsx`, rewritten in commit 11) | commit 13 |
| `web/src/styles/ChartTooltip.module.css` | `components/ChartTooltip.tsx` (used by `pages/Reports.tsx`, rewritten in commit 10) | commit 13 |

Chunk 8 / Task 13 enumerates these files explicitly and verifies nothing imports them before deletion.

**Also explicitly deferred to a follow-up commit (not in scope for this plan):**

- **Bulk select + bulk categorize** (spec §3 line 305 mentions `Checkbox` for bulk select and `Dialog` for bulk categorize). These are nice-to-haves that the current `useTransactions` hook does not support. Adding them requires a new hook method and new API endpoint. A TODO comment is added to `pages/Transactions.tsx` pointing at this future work; no backend or hook changes are made in this commit.

**What this commit actually ships:**

- `CategoryBadge` rewritten to take a `category: { id: number; name: string }` prop (not `color`), using `getCategoryColorVar` from Chunk 2. The display now reads from the centralized id→chart-color mapping instead of a per-category hex string.
- `TransactionToolbar` rewritten as Tailwind/shadcn: `Input` with leading magnifier icon, a three-button `ToggleGroup`-style type selector (implemented with shadcn `Button` variants — no new dependency), a `Button` for Filters (with a `Badge` showing the active filter count), and a primary `Button` for `+ Add` / `Cancel`.
- `FilterPanel` rewritten as the contents of a shadcn `Sheet` (the `Sheet` wrapper itself lives in `Transactions.tsx`). Tab bar is a shadcn `Tabs`. Category chips use `Button` variants with `getCategoryColorVar` treatment on the selected state — no inline hex colors.
- `TransactionRow` rewritten: display mode uses shadcn `Table` cells, the category cell uses the new id-based `CategoryBadge`, amounts use `font-mono tabular-nums`, edit mode uses shadcn `Input` and `Select`, and the Edit/Delete buttons become a single kebab `DropdownMenu` whose trigger has `aria-label={"Actions for " + transaction.description}`.
- `Transactions.tsx` rewritten: the page wraps content in a `Card`, uses the new `Table` primitives, and wraps `FilterPanel` in a shadcn `Sheet` that opens from the right. The old `TransactionEntry` is still rendered, wrapped in `<div className={showEntry ? undefined : "hidden"}>` so that component state is preserved across open/close. The entry wrapper uses a Tailwind `hidden` class, not the CSS module `entryFormHidden` class — this removes the last `Transactions.module.css` import from `Transactions.tsx` itself (the module stays on disk only because of `TagInput` and old `TransactionEntry`).
- The Transactions test file (`pages/Transactions.test.tsx`) and the TransactionRow test file (`components/TransactionRow.test.tsx`) are rewritten to match the new markup. Fixture data keeps `category_color: '#e94560'` to remain type-correct against the unchanged `Transaction` type (the column is not dropped until commit 12), but no test asserts on that field.

**Prerequisite reading** (the implementer must re-read these before starting; they are dense and the Tailwind/shadcn translation is mechanical but fiddly):

- `docs/superpowers/specs/2026-04-09-shadcn-tailwind-rewrite-design.md` §3 "Page-by-page" Transactions section (spec lines ~295–315)
- `docs/superpowers/specs/2026-04-09-shadcn-tailwind-rewrite-design.md` §5 "Deferred work" (lines ~420–430)
- `web/src/lib/chart-colors.ts` (created in Chunk 2) — the `getCategoryColorVar` helper
- `web/src/components/ui/table.tsx`, `web/src/components/ui/sheet.tsx`, `web/src/components/ui/dropdown-menu.tsx`, `web/src/components/ui/tabs.tsx`, `web/src/components/ui/badge.tsx` (all installed in Chunks 1–2)

### Task 8: Transactions page non-entry rewrite

**Files:**

- Rewrite: `web/src/components/CategoryBadge.tsx`
- Rewrite: `web/src/components/CategoryBadge.test.tsx`
- Rewrite: `web/src/components/TransactionToolbar.tsx`
- Rewrite: `web/src/components/FilterPanel.tsx`
- Rewrite: `web/src/components/TransactionRow.tsx`
- Rewrite: `web/src/components/TransactionRow.test.tsx`
- Rewrite: `web/src/pages/Transactions.tsx`
- Rewrite: `web/src/pages/Transactions.test.tsx`
- **Not touched in this commit** (listed here so the implementer does not accidentally edit them):
  - `web/src/components/TransactionEntry.tsx` (deleted wholesale in commit 9)
  - `web/src/components/TransactionEntry.test.tsx` (deleted wholesale in commit 9)
  - `web/src/components/TagInput.tsx` (kept as-is per spec §5)
  - `web/src/styles/Transactions.module.css` (deleted in commit 13)

The target is a single atomic commit matching spec §8 commit 8. Steps are ordered so that the test suite can run cleanly between components: we rewrite each component's test **first** (so it fails against the still-old component), then rewrite the component, then re-run the tests. This is strict TDD for every file.

- [ ] **Step 1: Rewrite CategoryBadge.test.tsx (failing test)**

Replace the entire contents of `web/src/components/CategoryBadge.test.tsx` with:

```tsx
import { render, screen } from '@testing-library/react';
import { describe, it, expect } from 'vitest';
import { CategoryBadge } from './CategoryBadge';

describe('CategoryBadge', () => {
  it('renders the category name', () => {
    render(<CategoryBadge category={{ id: 3, name: 'Groceries' }} />);
    expect(screen.getByText('Groceries')).toBeInTheDocument();
  });

  it('sets the chart-color CSS variable based on the category id', () => {
    render(<CategoryBadge category={{ id: 3, name: 'Groceries' }} />);
    const badge = screen.getByText('Groceries');
    // id 3 → chart-3 (see getCategoryColorVar: ((id-1) % 11) + 1)
    expect(badge.style.getPropertyValue('--badge-color')).toBe(
      'hsl(var(--chart-3))',
    );
  });

  it('wraps ids past 11 back to chart-1', () => {
    render(<CategoryBadge category={{ id: 12, name: 'Wrap' }} />);
    const badge = screen.getByText('Wrap');
    expect(badge.style.getPropertyValue('--badge-color')).toBe(
      'hsl(var(--chart-1))',
    );
  });
});
```

- [ ] **Step 2: Run the new CategoryBadge test — expect compile failure**

Run: `cd web && npx vitest run src/components/CategoryBadge.test.tsx`

Expected: The test file **fails to compile** because the current `CategoryBadge` component does not accept a `category` prop. The error will point at the `<CategoryBadge category={...}>` usage. This is the intended failing state.

- [ ] **Step 3: Rewrite CategoryBadge.tsx**

Replace the entire contents of `web/src/components/CategoryBadge.tsx` with:

```tsx
import type { CSSProperties } from 'react';
import { getCategoryColorVar } from '../lib/chart-colors';

interface CategoryBadgeProps {
  category: { id: number; name: string };
}

/**
 * Pill-shaped badge that shows a category's name. The color is derived from
 * the category id via the centralized chart-color palette — there is no per-
 * category color field on the client. The background is a 15% wash of
 * `--badge-color` against the surface and the text is the full color.
 */
export function CategoryBadge({ category }: CategoryBadgeProps) {
  const style = {
    '--badge-color': getCategoryColorVar(category),
    backgroundColor:
      'color-mix(in oklab, var(--badge-color) 15%, transparent)',
    color: 'var(--badge-color)',
  } as CSSProperties;

  return (
    <span
      style={style}
      className="inline-flex items-center rounded-full border border-transparent px-2.5 py-0.5 text-xs font-medium"
      data-category-id={category.id}
    >
      {category.name}
    </span>
  );
}
```

Note for the implementer: this is a single `<span>` by design — `color-mix` is supported in every browser shadcn targets, so we do not need a separate wash layer. The `--badge-color` CSS variable is declared alongside `backgroundColor` and `color` on the same element, which is what the test in Step 1 asserts against. Do not nest an inner text span — the test uses `getByText('Groceries')` which would return the inner span and fail the `--badge-color` lookup.

- [ ] **Step 4: Run CategoryBadge tests — expect pass**

Run: `cd web && npx vitest run src/components/CategoryBadge.test.tsx`

Expected: All 3 CategoryBadge tests pass. If the assertion about `--badge-color` fails, double-check that `getCategoryColorVar` returns exactly `hsl(var(--chart-${n}))` and that the style is set as a CSS variable, not a React style key.

- [ ] **Step 5: Rewrite TransactionToolbar.tsx**

Replace the entire contents of `web/src/components/TransactionToolbar.tsx` with:

```tsx
import { Search } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Badge } from '@/components/ui/badge';
import { cn } from '@/lib/utils';

interface TransactionToolbarProps {
  search: string;
  onSearchChange: (value: string) => void;
  type: string;
  onTypeChange: (value: string) => void;
  activeFilterCount: number;
  showFilters: boolean;
  onToggleFilters: () => void;
  showEntry: boolean;
  onToggleEntry: () => void;
}

export function TransactionToolbar({
  search,
  onSearchChange,
  type,
  onTypeChange,
  activeFilterCount,
  showFilters,
  onToggleFilters,
  showEntry,
  onToggleEntry,
}: TransactionToolbarProps) {
  return (
    <div className="flex flex-wrap items-center gap-2 rounded-lg border bg-card p-2">
      <div className="relative min-w-[240px] flex-1">
        <Search
          aria-hidden="true"
          className="pointer-events-none absolute left-2.5 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground"
        />
        <Input
          type="text"
          value={search}
          onChange={(e) => onSearchChange(e.target.value)}
          placeholder="Search transactions..."
          aria-label="Search transactions"
          className="pl-8"
        />
      </div>

      <div className="hidden h-6 w-px bg-border sm:block" aria-hidden="true" />

      <div className="inline-flex items-center gap-0.5 rounded-md border bg-background p-0.5">
        {([
          { value: '', label: 'All' },
          { value: 'expense', label: 'Expenses' },
          { value: 'income', label: 'Income' },
        ] as const).map((opt) => (
          <Button
            key={opt.value || 'all'}
            type="button"
            variant={type === opt.value ? 'secondary' : 'ghost'}
            size="sm"
            className={cn(
              'h-7 px-3 text-xs',
              type === opt.value && 'shadow-sm',
            )}
            onClick={() => onTypeChange(opt.value)}
          >
            {opt.label}
          </Button>
        ))}
      </div>

      <div className="hidden h-6 w-px bg-border sm:block" aria-hidden="true" />

      <Button
        type="button"
        variant="outline"
        size="sm"
        onClick={onToggleFilters}
        aria-expanded={showFilters}
        aria-label={
          activeFilterCount > 0 ? `Filters (${activeFilterCount})` : 'Filters'
        }
      >
        Filters
        {activeFilterCount > 0 && (
          <Badge variant="secondary" className="ml-2 h-5 px-1.5 text-[10px]">
            {activeFilterCount}
          </Badge>
        )}
      </Button>

      <Button
        type="button"
        size="sm"
        onClick={onToggleEntry}
        aria-expanded={showEntry}
      >
        {showEntry ? 'Cancel' : '+ Add'}
      </Button>
    </div>
  );
}
```

Note for the implementer: the `aria-label` on the Filters button is explicit so that `getByRole('button', { name: 'Filters' })` and `getByRole('button', { name: /Filters \(2\)/ })` both keep working in the existing Transactions test file. The `Badge` rendered inside the button provides visual affordance but `aria-label` drives accessible name, which is what `getByRole` matches on. React Testing Library queries accessible name first.

- [ ] **Step 6: Rewrite FilterPanel.tsx**

Replace the entire contents of `web/src/components/FilterPanel.tsx` with:

```tsx
import type { CSSProperties } from 'react';
import {
  startOfMonth,
  endOfMonth,
  subMonths,
  startOfYear,
  format,
} from 'date-fns';
import { X } from 'lucide-react';
import type { TransactionFilters } from '../hooks/useTransactions';
import type { Category, SavedFilter } from '../api/types';
import { getCategoryColorVar } from '../lib/chart-colors';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';

interface FilterPanelProps {
  filters: TransactionFilters;
  setFilter: (key: keyof TransactionFilters, value: string) => void;
  clearPanelFilters: () => void;
  categories: Category[];
  savedFilters: SavedFilter[];
  onSaveFilter: (name: string) => void;
  onLoadFilter: (filter: SavedFilter) => void;
  onDeleteFilter: (id: number) => void;
}

export function FilterPanel({
  filters,
  setFilter,
  clearPanelFilters,
  categories,
  savedFilters,
  onSaveFilter,
  onLoadFilter,
  onDeleteFilter,
}: FilterPanelProps) {
  const now = new Date();

  function setDatePreset(preset: 'thisMonth' | 'lastMonth' | 'thisYear') {
    let from: Date;
    let to: Date;
    switch (preset) {
      case 'thisMonth':
        from = startOfMonth(now);
        to = endOfMonth(now);
        break;
      case 'lastMonth': {
        const last = subMonths(now, 1);
        from = startOfMonth(last);
        to = endOfMonth(last);
        break;
      }
      case 'thisYear':
        from = startOfYear(now);
        to = now;
        break;
    }
    setFilter('dateFrom', format(from, 'yyyy-MM-dd'));
    setFilter('dateTo', format(to, 'yyyy-MM-dd'));
  }

  function handleSaveFilter() {
    const name = window.prompt('Name this filter:');
    if (name) {
      onSaveFilter(name);
    }
  }

  const selectedCategoryIds = filters.categoryIds
    ? filters.categoryIds.split(',').filter(Boolean)
    : [];

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center justify-end">
        <Button
          type="button"
          variant="ghost"
          size="sm"
          onClick={clearPanelFilters}
        >
          Clear all
        </Button>
      </div>

      <Tabs defaultValue="date" className="w-full">
        <TabsList className="grid w-full grid-cols-4">
          <TabsTrigger value="date">Date</TabsTrigger>
          <TabsTrigger value="category">Category</TabsTrigger>
          <TabsTrigger value="amount">Amount</TabsTrigger>
          <TabsTrigger value="saved">Saved</TabsTrigger>
        </TabsList>

        <TabsContent value="date" className="space-y-3 pt-4">
          <div className="flex flex-wrap gap-2">
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={() => setDatePreset('thisMonth')}
            >
              This Month
            </Button>
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={() => setDatePreset('lastMonth')}
            >
              Last Month
            </Button>
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={() => setDatePreset('thisYear')}
            >
              This Year
            </Button>
          </div>
          <div className="flex items-center gap-2">
            <Input
              type="date"
              value={filters.dateFrom}
              onChange={(e) => setFilter('dateFrom', e.target.value)}
              aria-label="Date from"
            />
            <span className="text-xs text-muted-foreground">to</span>
            <Input
              type="date"
              value={filters.dateTo}
              onChange={(e) => setFilter('dateTo', e.target.value)}
              aria-label="Date to"
            />
          </div>
        </TabsContent>

        <TabsContent value="category" className="space-y-3 pt-4">
          <div className="flex flex-wrap gap-2">
            {categories.map((cat) => {
              const selected = selectedCategoryIds.includes(String(cat.id));
              const style = {
                '--chip-color': getCategoryColorVar(cat),
              } as CSSProperties;
              return (
                <Button
                  key={cat.id}
                  type="button"
                  variant={selected ? 'default' : 'outline'}
                  size="sm"
                  style={
                    selected
                      ? {
                          ...style,
                          backgroundColor: 'var(--chip-color)',
                          borderColor: 'var(--chip-color)',
                        }
                      : style
                  }
                  onClick={() => {
                    const next = selected
                      ? selectedCategoryIds.filter(
                          (id) => id !== String(cat.id),
                        )
                      : [...selectedCategoryIds, String(cat.id)];
                    setFilter('categoryIds', next.join(','));
                  }}
                >
                  {cat.name}
                </Button>
              );
            })}
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="filter-tags">Tags</Label>
            <Input
              id="filter-tags"
              type="text"
              placeholder="Filter by tags..."
              value={filters.tags}
              onChange={(e) => setFilter('tags', e.target.value)}
              aria-label="Filter by tags"
            />
          </div>
        </TabsContent>

        <TabsContent value="amount" className="space-y-3 pt-4">
          <div className="flex items-center gap-2">
            <Input
              type="number"
              placeholder="Min $"
              value={filters.amountMin}
              onChange={(e) => setFilter('amountMin', e.target.value)}
              step="0.01"
              min="0"
              aria-label="Minimum amount"
            />
            <span className="text-xs text-muted-foreground">to</span>
            <Input
              type="number"
              placeholder="Max $"
              value={filters.amountMax}
              onChange={(e) => setFilter('amountMax', e.target.value)}
              step="0.01"
              min="0"
              aria-label="Maximum amount"
            />
          </div>
        </TabsContent>

        <TabsContent value="saved" className="space-y-3 pt-4">
          <div className="flex flex-col gap-2">
            {savedFilters.map((sf) => (
              <div
                key={sf.id}
                className="flex items-center justify-between rounded-md border px-3 py-2"
              >
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  className="h-auto p-0 font-medium"
                  onClick={() => onLoadFilter(sf)}
                >
                  {sf.name}
                </Button>
                <Button
                  type="button"
                  variant="ghost"
                  size="icon"
                  className="h-6 w-6"
                  onClick={() => onDeleteFilter(sf.id)}
                  aria-label={`Delete saved filter ${sf.name}`}
                >
                  <X className="h-3.5 w-3.5" />
                </Button>
              </div>
            ))}
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={handleSaveFilter}
            >
              + Save Filter
            </Button>
          </div>
        </TabsContent>
      </Tabs>
    </div>
  );
}
```

Note for the implementer: the `clearPanelFilters` button used to live in a row with the tab bar. It's now a standalone row above `Tabs` because shadcn `TabsList` doesn't accept trailing content. This is intentional.

- [ ] **Step 7: Rewrite TransactionRow.test.tsx**

Replace the entire contents of `web/src/components/TransactionRow.test.tsx` with:

```tsx
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { TransactionRow } from './TransactionRow';
import type { Transaction, Category } from '../api/types';

const mockCategories: Category[] = [
  {
    id: 1,
    name: 'Groceries',
    type: 'expense',
    // NOTE: color field still exists on the Category type until commit 12 drops
    // the DB column. We keep a dummy value to satisfy TypeScript but no test
    // below asserts on it — the new CategoryBadge derives color from id alone.
    color: '#e94560',
    icon: null,
    sort_order: 1,
    is_active: true,
    created_at: '2026-01-01',
  },
];

function makeTx(overrides: Partial<Transaction> = {}): Transaction {
  return {
    id: 1,
    user_id: 1,
    date: '2026-04-01',
    amount: 25.5,
    original_amount: null,
    original_currency: null,
    description: 'Weekly groceries',
    category_id: 1,
    category_name: 'Groceries',
    category_type: 'expense',
    // Same note as mockCategories above — kept for type-correctness only.
    category_color: '#e94560',
    tags: null,
    notes: null,
    created_at: '2026-04-01T00:00:00Z',
    updated_at: '2026-04-01T00:00:00Z',
    ...overrides,
  };
}

function renderRow(
  transaction: Transaction,
  onUpdate?: ReturnType<typeof vi.fn>,
  onDelete?: ReturnType<typeof vi.fn>,
) {
  return render(
    <table>
      <tbody>
        <TransactionRow
          transaction={transaction}
          categories={mockCategories}
          onUpdate={(onUpdate ?? vi.fn().mockResolvedValue(undefined)) as never}
          onDelete={(onDelete ?? vi.fn().mockResolvedValue(undefined)) as never}
        />
      </tbody>
    </table>,
  );
}

async function openActionsMenu(description: string) {
  const user = userEvent.setup();
  await user.click(
    screen.getByRole('button', { name: `Actions for ${description}` }),
  );
  return user;
}

describe('TransactionRow tags display', () => {
  it('renders tag pills when transaction has tags', () => {
    renderRow(makeTx({ tags: 'groceries,weekly' }));
    expect(screen.getByText('groceries')).toBeInTheDocument();
    expect(screen.getByText('weekly')).toBeInTheDocument();
  });

  it('renders empty cell when tags are null', () => {
    renderRow(makeTx({ tags: null }));
    expect(screen.queryByText('groceries')).not.toBeInTheDocument();
  });

  it('renders empty cell when tags are empty string', () => {
    renderRow(makeTx({ tags: '' }));
    expect(screen.queryByText('groceries')).not.toBeInTheDocument();
  });
});

describe('TransactionRow actions menu', () => {
  it('exposes an actions menu trigger with an explicit aria-label', () => {
    renderRow(makeTx({ description: 'Weekly groceries' }));
    expect(
      screen.getByRole('button', { name: 'Actions for Weekly groceries' }),
    ).toBeInTheDocument();
  });

  it('opens a menu with Edit and Delete items when the trigger is clicked', async () => {
    renderRow(makeTx({ description: 'Weekly groceries' }));
    await openActionsMenu('Weekly groceries');
    expect(screen.getByRole('menuitem', { name: /edit/i })).toBeInTheDocument();
    expect(
      screen.getByRole('menuitem', { name: /delete/i }),
    ).toBeInTheDocument();
  });

  it('calls onDelete when Delete is chosen', async () => {
    const onDelete = vi.fn().mockResolvedValue(undefined);
    renderRow(
      makeTx({ description: 'Weekly groceries' }),
      undefined,
      onDelete,
    );
    const user = await openActionsMenu('Weekly groceries');
    await user.click(screen.getByRole('menuitem', { name: /delete/i }));
    expect(onDelete).toHaveBeenCalledWith(1);
  });
});

describe('TransactionRow tags editing', () => {
  let onUpdate: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    onUpdate = vi.fn().mockResolvedValue(undefined);
  });

  it('shows TagInput with existing tags in edit mode', async () => {
    renderRow(makeTx({ tags: 'groceries,weekly' }), onUpdate);
    const user = await openActionsMenu('Weekly groceries');
    await user.click(screen.getByRole('menuitem', { name: /edit/i }));

    expect(screen.getByText('groceries')).toBeInTheDocument();
    expect(screen.getByText('weekly')).toBeInTheDocument();
  });

  it('includes tags in save/update call', async () => {
    renderRow(makeTx({ tags: 'groceries' }), onUpdate);
    const user = await openActionsMenu('Weekly groceries');
    await user.click(screen.getByRole('menuitem', { name: /edit/i }));

    const form = screen.getByRole('button', { name: /save/i }).closest('form')!;
    fireEvent.submit(form);

    await waitFor(() => {
      expect(onUpdate).toHaveBeenCalledWith(
        expect.objectContaining({ tags: 'groceries' }),
      );
    });
  });

  it('resets tags on cancel', async () => {
    renderRow(makeTx({ tags: 'groceries' }), onUpdate);
    const user = await openActionsMenu('Weekly groceries');
    await user.click(screen.getByRole('menuitem', { name: /edit/i }));

    // TagInput is the kept-as-is legacy component; its input is still the only
    // text input inside the edit row. Grab it by placeholder-less text query
    // fallback: find all text inputs and pick the last one (the tag field).
    const textInputs = screen
      .getAllByRole('textbox')
      .filter((el) => (el as HTMLInputElement).type === 'text');
    const tagInputEl = textInputs[textInputs.length - 1];
    await user.type(tagInputEl, 'extra{Enter}');
    expect(screen.getByText('extra')).toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: /cancel/i }));

    expect(screen.getByText('groceries')).toBeInTheDocument();
    expect(screen.queryByText('extra')).not.toBeInTheDocument();
  });
});
```

Note for the implementer: the original test file used `document.querySelector('.tagInput')` to grab the tag input. TagInput still has that class (it's kept as-is), but tests should not rely on CSS class selectors. The new approach uses `getAllByRole('textbox')` and picks the trailing text input. This is brittle in a different way (ordering), but it is accessible-name–driven. If TagInput gets rewritten later to expose an explicit `aria-label`, update this query.

- [ ] **Step 8: Rewrite TransactionRow.tsx**

Replace the entire contents of `web/src/components/TransactionRow.tsx` with:

```tsx
import { useState } from 'react';
import type { FormEvent } from 'react';
import { format } from 'date-fns';
import { MoreHorizontal } from 'lucide-react';
import type { Transaction, Category } from '../api/types';
import { CategoryBadge } from './CategoryBadge';
import { TagInput } from './TagInput';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { TableCell, TableRow } from '@/components/ui/table';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import { cn } from '@/lib/utils';

function formatCurrency(amount: number): string {
  return amount.toLocaleString('en-US', {
    style: 'currency',
    currency: 'USD',
  });
}

interface TransactionRowProps {
  transaction: Transaction;
  categories: Category[];
  onUpdate: (input: {
    id: number;
    date: string;
    amount: number;
    description: string;
    category_id: number;
    tags: string;
  }) => Promise<void>;
  onDelete: (id: number) => Promise<void>;
}

export function TransactionRow({
  transaction,
  categories,
  onUpdate,
  onDelete,
}: TransactionRowProps) {
  const [editing, setEditing] = useState(false);
  const [date, setDate] = useState(transaction.date);
  const [amount, setAmount] = useState(String(transaction.amount));
  const [description, setDescription] = useState(transaction.description);
  const [categoryId, setCategoryId] = useState(String(transaction.category_id));
  const [tags, setTags] = useState(transaction.tags ?? '');
  const [saving, setSaving] = useState(false);

  async function handleSave(e: FormEvent) {
    e.preventDefault();
    setSaving(true);
    try {
      await onUpdate({
        id: transaction.id,
        date,
        amount: parseFloat(amount),
        description,
        category_id: parseInt(categoryId, 10),
        tags,
      });
      setEditing(false);
    } finally {
      setSaving(false);
    }
  }

  function handleCancel() {
    setDate(transaction.date);
    setAmount(String(transaction.amount));
    setDescription(transaction.description);
    setCategoryId(String(transaction.category_id));
    setTags(transaction.tags ?? '');
    setEditing(false);
  }

  if (editing) {
    return (
      <TableRow>
        <TableCell>
          <Input
            type="date"
            value={date}
            onChange={(e) => setDate(e.target.value)}
          />
        </TableCell>
        <TableCell>
          <Input
            type="text"
            value={description}
            onChange={(e) => setDescription(e.target.value)}
          />
        </TableCell>
        <TableCell>
          <Select value={categoryId} onValueChange={setCategoryId}>
            <SelectTrigger>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {categories.map((cat) => (
                <SelectItem key={cat.id} value={String(cat.id)}>
                  {cat.name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </TableCell>
        <TableCell>
          <TagInput value={tags} onChange={setTags} placeholder="Add tags..." />
        </TableCell>
        <TableCell className="text-right font-mono tabular-nums">
          <Input
            type="number"
            value={amount}
            onChange={(e) => setAmount(e.target.value)}
            step="0.01"
            className="text-right"
          />
        </TableCell>
        <TableCell>
          <form
            onSubmit={(e) => void handleSave(e)}
            className="flex items-center justify-end gap-1"
          >
            <Button type="submit" size="sm" disabled={saving}>
              Save
            </Button>
            <Button
              type="button"
              size="sm"
              variant="ghost"
              onClick={handleCancel}
            >
              Cancel
            </Button>
          </form>
        </TableCell>
      </TableRow>
    );
  }

  return (
    <TableRow className="hover:bg-muted/50">
      <TableCell className="whitespace-nowrap text-muted-foreground">
        {format(new Date(transaction.date), 'MMM d, yyyy')}
      </TableCell>
      <TableCell className="font-medium">{transaction.description}</TableCell>
      <TableCell>
        <CategoryBadge
          category={{ id: transaction.category_id, name: transaction.category_name }}
        />
      </TableCell>
      <TableCell>
        {transaction.tags &&
          transaction.tags.split(',').map((tag) => (
            <span
              key={tag.trim()}
              className="mr-1 inline-flex items-center rounded-md bg-muted px-1.5 py-0.5 text-xs text-muted-foreground"
            >
              {tag.trim()}
            </span>
          ))}
      </TableCell>
      <TableCell
        className={cn(
          'text-right font-mono tabular-nums',
          transaction.category_type === 'expense'
            ? 'text-foreground'
            : 'text-emerald-600 dark:text-emerald-400',
        )}
      >
        {transaction.category_type === 'expense' ? '-' : '+'}
        {formatCurrency(transaction.amount)}
      </TableCell>
      <TableCell className="text-right">
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button
              type="button"
              variant="ghost"
              size="icon"
              className="h-8 w-8"
              aria-label={`Actions for ${transaction.description}`}
            >
              <MoreHorizontal className="h-4 w-4" />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            <DropdownMenuItem onSelect={() => setEditing(true)}>
              Edit
            </DropdownMenuItem>
            <DropdownMenuItem
              onSelect={() => void onDelete(transaction.id)}
              className="text-destructive focus:text-destructive"
            >
              Delete
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </TableCell>
    </TableRow>
  );
}
```

- [ ] **Step 9: Run TransactionRow tests — expect pass**

Run: `cd web && npx vitest run src/components/TransactionRow.test.tsx src/components/CategoryBadge.test.tsx`

Expected: All TransactionRow and CategoryBadge tests pass. If the menu-open tests fail because the menu items are not rendered in the JSDOM environment, check that `@testing-library/user-event` is on v14+ (it is, per Chunk 1) and that the `DropdownMenuContent` is rendered inside a portal — which is fine, RTL queries portals by default.

- [ ] **Step 10: Rewrite Transactions.test.tsx**

The existing `pages/Transactions.test.tsx` tests the toolbar, filter panel tabs, active chips, saved filters, export button, and tags column. The query surface stays mostly the same — `getByRole('button', { name: 'Filters' })`, `getByRole('button', { name: '+ Add' })`, `getByLabelText('Search transactions')`, etc. — because the new toolbar preserves accessible names. The two things that change:

1. The filter panel is now inside a shadcn `Sheet`. `Sheet` renders in a portal; RTL still sees it because `screen` queries the whole document. No test query needs to change.
2. The "tags column" test counts columnheaders. The new `<Table>` primitive still renders `<th>` elements with role `columnheader`, so this keeps working.

Replace the entire contents of `web/src/pages/Transactions.test.tsx` with:

```tsx
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, it, expect, vi, beforeEach } from 'vitest';

const mockSaveFilter = vi.fn();
const mockDeleteFilter = vi.fn();
const mockSetFilter = vi.fn();
const mockClearFilters = vi.fn();
const mockClearPanelFilters = vi.fn();

const defaultFilters = {
  dateFrom: '',
  dateTo: '',
  categoryId: '',
  categoryIds: '',
  amountMin: '',
  amountMax: '',
  tags: '',
  type: '',
  search: '',
};

const defaultTransaction = {
  id: 1,
  user_id: 1,
  date: '2026-04-01',
  amount: 25.5,
  original_amount: null,
  original_currency: null,
  description: 'Groceries',
  category_id: 1,
  category_name: 'Groceries',
  category_type: 'expense',
  // Kept for type-correctness against the Transaction type. No test asserts on
  // it — the column is dropped in commit 12.
  category_color: '#e94560',
  tags: 'food,weekly',
  notes: null,
  created_at: '2026-04-01T00:00:00Z',
  updated_at: '2026-04-01T00:00:00Z',
};

const mockUseTransactions = vi.fn();

vi.mock('../api/client', () => ({
  api: {
    get: vi.fn().mockResolvedValue([]),
  },
}));

vi.mock('../hooks/useTransactions', () => ({
  useTransactions: (...args: unknown[]) => mockUseTransactions(...args),
}));

vi.mock('../hooks/useSavedFilters', () => ({
  useSavedFilters: () => ({
    savedFilters: [
      {
        id: 10,
        user_id: 1,
        name: 'Big expenses',
        filter_json: '{"amountMin":"100","amountMax":"500"}',
        created_at: '',
        updated_at: '',
      },
    ],
    loading: false,
    saveFilter: mockSaveFilter,
    deleteFilter: mockDeleteFilter,
    refetch: vi.fn(),
  }),
}));

import { Transactions } from './Transactions';

function defaultHookReturn(overrides = {}) {
  return {
    transactions: [defaultTransaction],
    total: 1,
    page: 1,
    perPage: 20,
    filters: { ...defaultFilters },
    setFilter: mockSetFilter,
    clearFilters: mockClearFilters,
    clearPanelFilters: mockClearPanelFilters,
    setPage: vi.fn(),
    loading: false,
    error: '',
    createTransaction: vi.fn(),
    updateTransaction: vi.fn(),
    deleteTransaction: vi.fn(),
    ...overrides,
  };
}

describe('Transactions page', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockUseTransactions.mockReturnValue(defaultHookReturn());
  });

  describe('toolbar', () => {
    it('renders search input, type toggle, Filters button, and + Add button', () => {
      render(<Transactions />);
      expect(screen.getByLabelText('Search transactions')).toBeInTheDocument();
      expect(screen.getByRole('button', { name: 'All' })).toBeInTheDocument();
      expect(screen.getByRole('button', { name: 'Expenses' })).toBeInTheDocument();
      expect(screen.getByRole('button', { name: 'Income' })).toBeInTheDocument();
      expect(screen.getByRole('button', { name: 'Filters' })).toBeInTheDocument();
      expect(screen.getByRole('button', { name: '+ Add' })).toBeInTheDocument();
    });

    it('clicking Filters button opens the filter Sheet and shows Date tab content', async () => {
      const user = userEvent.setup();
      render(<Transactions />);

      expect(
        screen.queryByRole('button', { name: 'This Month' }),
      ).not.toBeInTheDocument();

      await user.click(screen.getByRole('button', { name: 'Filters' }));
      expect(
        screen.getByRole('button', { name: 'This Month' }),
      ).toBeInTheDocument();
    });

    it('clicking + Add button toggles entry form and changes label to Cancel', async () => {
      const user = userEvent.setup();
      render(<Transactions />);

      await user.click(screen.getByRole('button', { name: '+ Add' }));
      expect(screen.getByRole('button', { name: 'Cancel' })).toBeInTheDocument();
      expect(
        screen.queryByRole('button', { name: '+ Add' }),
      ).not.toBeInTheDocument();

      await user.click(screen.getByRole('button', { name: 'Cancel' }));
      expect(screen.getByRole('button', { name: '+ Add' })).toBeInTheDocument();
    });

    it('Filters button shows count when filters are active', () => {
      mockUseTransactions.mockReturnValue(
        defaultHookReturn({
          filters: { ...defaultFilters, dateFrom: '2026-01-01', amountMin: '50' },
        }),
      );
      render(<Transactions />);
      expect(
        screen.getByRole('button', { name: /Filters \(2\)/ }),
      ).toBeInTheDocument();
    });

    it('Filters button has aria-expanded attribute that reflects Sheet state', async () => {
      const user = userEvent.setup();
      render(<Transactions />);

      const filtersBtn = screen.getByRole('button', { name: 'Filters' });
      expect(filtersBtn).toHaveAttribute('aria-expanded', 'false');

      await user.click(filtersBtn);
      expect(filtersBtn).toHaveAttribute('aria-expanded', 'true');
    });
  });

  describe('filter panel tabs', () => {
    it('switches between Date, Category, Amount, and Saved tabs', async () => {
      const user = userEvent.setup();
      render(<Transactions />);

      await user.click(screen.getByRole('button', { name: 'Filters' }));

      // Date tab is default — preset button visible
      expect(
        screen.getByRole('button', { name: 'This Month' }),
      ).toBeInTheDocument();

      // shadcn Tabs uses role="tab" for triggers
      await user.click(screen.getByRole('tab', { name: 'Category' }));
      expect(screen.getByLabelText('Filter by tags')).toBeInTheDocument();

      await user.click(screen.getByRole('tab', { name: 'Amount' }));
      expect(screen.getByLabelText('Minimum amount')).toBeInTheDocument();

      await user.click(screen.getByRole('tab', { name: 'Saved' }));
      expect(
        screen.getByRole('button', { name: 'Big expenses' }),
      ).toBeInTheDocument();
    });
  });

  describe('active filter chips', () => {
    it('shows chips when filters are set and panel is closed', () => {
      mockUseTransactions.mockReturnValue(
        defaultHookReturn({
          filters: { ...defaultFilters, amountMin: '50', amountMax: '200' },
        }),
      );
      render(<Transactions />);
      expect(screen.getByText('$50 - $200')).toBeInTheDocument();
    });

    it('hides chips when filter Sheet is open', async () => {
      const user = userEvent.setup();
      mockUseTransactions.mockReturnValue(
        defaultHookReturn({
          filters: { ...defaultFilters, amountMin: '50' },
        }),
      );
      render(<Transactions />);
      expect(screen.getByText('Min $50')).toBeInTheDocument();

      await user.click(screen.getByRole('button', { name: /Filters/ }));
      expect(screen.queryByText('Min $50')).not.toBeInTheDocument();
    });

    it('clicking chip x clears that specific filter group', async () => {
      const user = userEvent.setup();
      mockUseTransactions.mockReturnValue(
        defaultHookReturn({
          filters: { ...defaultFilters, tags: 'groceries' },
        }),
      );
      render(<Transactions />);

      await user.click(screen.getByLabelText('Clear groceries filter'));
      expect(mockSetFilter).toHaveBeenCalledWith('tags', '');
    });
  });

  describe('saved filters integration', () => {
    async function openSavedTab() {
      const user = userEvent.setup();
      await user.click(screen.getByRole('button', { name: 'Filters' }));
      await user.click(screen.getByRole('tab', { name: 'Saved' }));
      return user;
    }

    it('renders saved filter chips in the Saved tab', async () => {
      render(<Transactions />);
      await openSavedTab();
      expect(
        screen.getByRole('button', { name: 'Big expenses' }),
      ).toBeInTheDocument();
    });

    it('renders a save filter button in the Saved tab', async () => {
      render(<Transactions />);
      await openSavedTab();
      expect(
        screen.getByRole('button', { name: /save filter/i }),
      ).toBeInTheDocument();
    });

    it('clicking a saved filter chip loads its filters via setFilter', async () => {
      render(<Transactions />);
      const user = await openSavedTab();
      await user.click(screen.getByRole('button', { name: 'Big expenses' }));

      expect(mockSetFilter).toHaveBeenCalledWith('amountMin', '100');
      expect(mockSetFilter).toHaveBeenCalledWith('amountMax', '500');
    });

    it('calls saveFilter hook with name and current filter JSON on save', async () => {
      const originalPrompt = window.prompt;
      window.prompt = vi.fn().mockReturnValue('My filter');
      render(<Transactions />);
      const user = await openSavedTab();
      await user.click(screen.getByRole('button', { name: /save filter/i }));

      expect(mockSaveFilter).toHaveBeenCalledWith('My filter', expect.any(String));
      window.prompt = originalPrompt;
    });

    it('calls deleteFilter hook when delete button is clicked', async () => {
      render(<Transactions />);
      await openSavedTab();
      const user = userEvent.setup();

      const deleteBtn = screen.getByRole('button', {
        name: /delete saved filter/i,
      });
      await user.click(deleteBtn);
      expect(mockDeleteFilter).toHaveBeenCalledWith(10);
    });
  });

  describe('export button', () => {
    it('renders an Export Excel button', () => {
      render(<Transactions />);
      expect(
        screen.getByRole('button', { name: /export excel/i }),
      ).toBeInTheDocument();
    });

    it('opens export URL in new tab when clicked with no filters', async () => {
      const user = userEvent.setup();
      const openSpy = vi.spyOn(window, 'open').mockImplementation(() => null);
      render(<Transactions />);

      await user.click(screen.getByRole('button', { name: /export excel/i }));

      expect(openSpy).toHaveBeenCalledTimes(1);
      const url = openSpy.mock.calls[0][0] as string;
      expect(url).toBe('/api/export/transactions');
      expect(openSpy.mock.calls[0][1]).toBe('_blank');
      openSpy.mockRestore();
    });

    it('includes filter params in the export URL when filters are active', async () => {
      mockUseTransactions.mockReturnValue(
        defaultHookReturn({
          filters: {
            dateFrom: '2026-01-01',
            dateTo: '2026-03-31',
            categoryId: '',
            categoryIds: '1,2',
            amountMin: '50',
            amountMax: '500',
            tags: 'food',
            type: 'expense',
            search: 'groceries',
          },
        }),
      );

      const user = userEvent.setup();
      const openSpy = vi.spyOn(window, 'open').mockImplementation(() => null);
      render(<Transactions />);

      await user.click(screen.getByRole('button', { name: /export excel/i }));

      expect(openSpy).toHaveBeenCalledTimes(1);
      const url = new URL(
        openSpy.mock.calls[0][0] as string,
        'http://localhost',
      );
      expect(url.pathname).toBe('/api/export/transactions');
      expect(url.searchParams.get('date_from')).toBe('2026-01-01');
      expect(url.searchParams.get('date_to')).toBe('2026-03-31');
      expect(url.searchParams.get('category_ids')).toBe('1,2');
      expect(url.searchParams.get('amount_min')).toBe('50');
      expect(url.searchParams.get('amount_max')).toBe('500');
      expect(url.searchParams.get('tags')).toBe('food');
      expect(url.searchParams.get('type')).toBe('expense');
      expect(url.searchParams.get('search')).toBe('groceries');
      openSpy.mockRestore();
    });

    it('uses categoryId when categoryIds is empty', async () => {
      mockUseTransactions.mockReturnValue(
        defaultHookReturn({
          filters: { ...defaultFilters, categoryId: '5', categoryIds: '' },
        }),
      );

      const user = userEvent.setup();
      const openSpy = vi.spyOn(window, 'open').mockImplementation(() => null);
      render(<Transactions />);

      await user.click(screen.getByRole('button', { name: /export excel/i }));

      const url = new URL(
        openSpy.mock.calls[0][0] as string,
        'http://localhost',
      );
      expect(url.searchParams.get('category_id')).toBe('5');
      expect(url.searchParams.has('category_ids')).toBe(false);
      openSpy.mockRestore();
    });
  });

  describe('tags column', () => {
    it('renders a Tags column header in the table', () => {
      render(<Transactions />);
      const headers = screen.getAllByRole('columnheader');
      const tagsHeader = headers.find((h) => h.textContent === 'Tags');
      expect(tagsHeader).toBeDefined();
    });
  });
});
```

Note for the implementer: the only semantic change in the test file is that tab triggers are now queried with `getByRole('tab', ...)` instead of `getByRole('button', ...)`. shadcn `TabsTrigger` renders with `role="tab"`, not `role="button"`. The `aria-expanded` test was also trimmed — the old test re-queried the `+ Add` button after clicking and asserted the label change; that assertion is covered by the "clicking + Add button toggles entry form" test above, so the aria-expanded test now only covers the Filters button.

- [ ] **Step 11: Rewrite Transactions.tsx**

Replace the entire contents of `web/src/pages/Transactions.tsx` with:

```tsx
import { useState, useEffect, useCallback, useMemo } from 'react';
import { format } from 'date-fns';
import { X } from 'lucide-react';
import { api } from '../api/client';
import type { Category, SavedFilter } from '../api/types';
import { useTransactions } from '../hooks/useTransactions';
import { useSavedFilters } from '../hooks/useSavedFilters';
import { TransactionToolbar } from '../components/TransactionToolbar';
import { FilterPanel } from '../components/FilterPanel';
import { TransactionEntry } from '../components/TransactionEntry';
import { TransactionRow } from '../components/TransactionRow';
import { Button } from '@/components/ui/button';
import { Card } from '@/components/ui/card';
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet';
import {
  Table,
  TableBody,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';

// TODO: Bulk select + bulk categorize (Checkbox column + Dialog) are deferred.
// They require a new `useTransactions` hook method and a new backend endpoint.
// Tracked as a follow-up commit — do not add here without extending the hook.

export function Transactions() {
  const {
    transactions,
    total,
    page,
    perPage,
    filters,
    setFilter,
    clearFilters,
    clearPanelFilters,
    setPage,
    loading,
    error,
    createTransaction,
    updateTransaction,
    deleteTransaction,
  } = useTransactions();

  const [showFilters, setShowFilters] = useState(false);
  const [showEntry, setShowEntry] = useState(false);

  const handleExport = useCallback(() => {
    const params = new URLSearchParams();
    if (filters.dateFrom) params.set('date_from', filters.dateFrom);
    if (filters.dateTo) params.set('date_to', filters.dateTo);
    if (filters.categoryIds) {
      params.set('category_ids', filters.categoryIds);
    } else if (filters.categoryId) {
      params.set('category_id', filters.categoryId);
    }
    if (filters.type) params.set('type', filters.type);
    if (filters.search) params.set('search', filters.search);
    if (filters.amountMin) params.set('amount_min', filters.amountMin);
    if (filters.amountMax) params.set('amount_max', filters.amountMax);
    if (filters.tags) params.set('tags', filters.tags);

    const query = params.toString();
    const url = `/api/export/transactions${query ? `?${query}` : ''}`;
    window.open(url, '_blank');
  }, [filters]);

  const {
    savedFilters,
    saveFilter,
    deleteFilter: deleteSavedFilter,
  } = useSavedFilters();

  const [categories, setCategories] = useState<Category[]>([]);

  useEffect(() => {
    api
      .get<Category[]>('categories')
      .then(setCategories)
      .catch((err) => {
        console.warn('Failed to load categories', err);
      });
  }, []);

  const handleSaveFilter = useCallback(
    (name: string) => {
      saveFilter(name, JSON.stringify(filters));
    },
    [saveFilter, filters],
  );

  const handleLoadFilter = useCallback(
    (sf: SavedFilter) => {
      try {
        const parsed = JSON.parse(sf.filter_json) as Record<string, string>;
        clearFilters();
        for (const [key, value] of Object.entries(parsed)) {
          setFilter(key as keyof typeof filters, value);
        }
      } catch {
        /* invalid JSON — ignore */
      }
    },
    [setFilter, clearFilters],
  );

  const handleDeleteFilter = useCallback(
    (id: number) => {
      deleteSavedFilter(id);
    },
    [deleteSavedFilter],
  );

  const activeFilterCount = useMemo(() => {
    let count = 0;
    if (filters.dateFrom || filters.dateTo) count++;
    if (filters.categoryIds || filters.categoryId) count++;
    if (filters.amountMin || filters.amountMax) count++;
    if (filters.tags) count++;
    return count;
  }, [filters]);

  const activeChips = useMemo(() => {
    if (showFilters) return [];
    const chips: { key: string; label: string; onClear: () => void }[] = [];

    if (filters.dateFrom || filters.dateTo) {
      let label: string;
      if (filters.dateFrom && filters.dateTo) {
        label = `${format(new Date(filters.dateFrom), 'MMM d')} - ${format(new Date(filters.dateTo), 'MMM d')}`;
      } else if (filters.dateFrom) {
        label = `From ${format(new Date(filters.dateFrom), 'MMM d')}`;
      } else {
        label = `Until ${format(new Date(filters.dateTo), 'MMM d')}`;
      }
      chips.push({
        key: 'date',
        label,
        onClear: () => {
          setFilter('dateFrom', '');
          setFilter('dateTo', '');
        },
      });
    }

    if (filters.categoryIds || filters.categoryId) {
      const ids = filters.categoryIds
        ? filters.categoryIds.split(',')
        : [filters.categoryId];
      const names = ids
        .map((id) => categories.find((c) => String(c.id) === id)?.name)
        .filter(Boolean);
      chips.push({
        key: 'category',
        label: names.join(', ') || 'Categories',
        onClear: () => {
          setFilter('categoryIds', '');
          setFilter('categoryId', '');
        },
      });
    }

    if (filters.amountMin || filters.amountMax) {
      let label: string;
      if (filters.amountMin && filters.amountMax) {
        label = `$${filters.amountMin} - $${filters.amountMax}`;
      } else if (filters.amountMin) {
        label = `Min $${filters.amountMin}`;
      } else {
        label = `Max $${filters.amountMax}`;
      }
      chips.push({
        key: 'amount',
        label,
        onClear: () => {
          setFilter('amountMin', '');
          setFilter('amountMax', '');
        },
      });
    }

    if (filters.tags) {
      chips.push({
        key: 'tags',
        label: filters.tags,
        onClear: () => setFilter('tags', ''),
      });
    }

    return chips;
  }, [filters, showFilters, categories, setFilter]);

  const totalPages = Math.max(1, Math.ceil(total / perPage));

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-semibold tracking-tight">Transactions</h1>
        <Button variant="outline" size="sm" onClick={handleExport}>
          Export Excel
        </Button>
      </div>

      <TransactionToolbar
        search={filters.search}
        onSearchChange={(v) => setFilter('search', v)}
        type={filters.type}
        onTypeChange={(v) => setFilter('type', v)}
        activeFilterCount={activeFilterCount}
        showFilters={showFilters}
        onToggleFilters={() => setShowFilters((p) => !p)}
        showEntry={showEntry}
        onToggleEntry={() => setShowEntry((p) => !p)}
      />

      {activeChips.length > 0 && (
        <div className="flex flex-wrap gap-1.5">
          {activeChips.map((chip) => (
            <span
              key={chip.key}
              className="inline-flex items-center gap-1 rounded-full border bg-secondary px-2.5 py-0.5 text-xs"
            >
              {chip.label}
              <button
                type="button"
                className="text-muted-foreground hover:text-foreground"
                onClick={chip.onClear}
                aria-label={`Clear ${chip.label} filter`}
              >
                <X className="h-3 w-3" />
              </button>
            </span>
          ))}
        </div>
      )}

      <Sheet open={showFilters} onOpenChange={setShowFilters}>
        <SheetContent side="right" className="w-full sm:max-w-md">
          <SheetHeader>
            <SheetTitle>Filters</SheetTitle>
            <SheetDescription>
              Narrow the transaction list by date, category, amount, or a saved
              preset.
            </SheetDescription>
          </SheetHeader>
          <div className="mt-4">
            <FilterPanel
              filters={filters}
              setFilter={setFilter}
              clearPanelFilters={clearPanelFilters}
              categories={categories}
              savedFilters={savedFilters}
              onSaveFilter={handleSaveFilter}
              onLoadFilter={handleLoadFilter}
              onDeleteFilter={handleDeleteFilter}
            />
          </div>
        </SheetContent>
      </Sheet>

      {/*
        The old TransactionEntry is still wired in via a Tailwind `hidden`
        toggle so that its internal state (date, amount, description, tags) is
        preserved across open/close. Commit 9 replaces this component with an
        inline TransactionEntryRow that lives inside the table as the top row.
      */}
      <div className={showEntry ? undefined : 'hidden'}>
        <TransactionEntry
          categories={categories}
          onSubmit={createTransaction}
        />
      </div>

      {error && (
        <div
          role="alert"
          className="rounded-md border border-destructive/50 bg-destructive/10 px-3 py-2 text-sm text-destructive"
        >
          {error}
        </div>
      )}

      {loading ? (
        <div className="rounded-md border bg-card p-6 text-center text-sm text-muted-foreground">
          Loading transactions...
        </div>
      ) : transactions.length === 0 ? (
        <div className="rounded-md border bg-card p-6 text-center text-sm text-muted-foreground">
          No transactions found. Add one above to get started.
        </div>
      ) : (
        <Card className="overflow-hidden">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Date</TableHead>
                <TableHead>Description</TableHead>
                <TableHead>Category</TableHead>
                <TableHead>Tags</TableHead>
                <TableHead className="text-right">Amount</TableHead>
                <TableHead className="w-10 text-right">
                  <span className="sr-only">Actions</span>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {transactions.map((tx) => (
                <TransactionRow
                  key={tx.id}
                  transaction={tx}
                  categories={categories}
                  onUpdate={updateTransaction}
                  onDelete={deleteTransaction}
                />
              ))}
            </TableBody>
          </Table>

          {totalPages > 1 && (
            <div className="flex items-center justify-between border-t px-4 py-3">
              <Button
                variant="outline"
                size="sm"
                disabled={page <= 1}
                onClick={() => setPage(page - 1)}
              >
                Previous
              </Button>
              <span className="text-sm text-muted-foreground">
                Page {page} of {totalPages}
              </span>
              <Button
                variant="outline"
                size="sm"
                disabled={page >= totalPages}
                onClick={() => setPage(page + 1)}
              >
                Next
              </Button>
            </div>
          )}
        </Card>
      )}
    </div>
  );
}
```

Note for the implementer: the `Sheet` is rendered unconditionally with `open={showFilters}` rather than mounted conditionally. This is the shadcn convention and it keeps the `FilterPanel` state (active tab, tag input focus) alive across open/close, which matches the old `{showFilters && <FilterPanel ... />}` behavior only because the old panel was stateless-per-mount anyway. Do not switch to conditional mounting — it breaks the `Sheet` open-close animation.

- [ ] **Step 12: Run full Transactions test file — expect pass**

Run: `cd web && npx vitest run src/pages/Transactions.test.tsx`

Expected: All ~19 Transactions tests pass. Common failure modes and fixes:

1. **`getByRole('button', { name: 'This Month' })` fails after opening the Sheet** — the Sheet may not have finished opening. `userEvent.click` awaits promises but Radix sheets animate. If this flakes, wrap the open click in an explicit `await waitFor(() => expect(screen.getByRole('button', { name: 'This Month' })).toBeInTheDocument())`. Do not add this preemptively; only if the test fails.
2. **`getByRole('tab', { name: 'Category' })` fails** — confirm shadcn `Tabs` is installed and that `TabsTrigger` renders with `role="tab"`. Run `cd web && grep -n 'role=' src/components/ui/tabs.tsx` — should find no hand-coded role (Radix handles it).
3. **aria-expanded on Filters button is wrong** — the Filters button passes `aria-expanded={showFilters}` to the underlying shadcn `Button` which passes it through. Verify by reading the new `TransactionToolbar.tsx`.

- [ ] **Step 13: Run the full test suite**

Run: `cd web && npx vitest run`

Expected: Every test file passes. The only tests touched in this commit are `CategoryBadge.test.tsx`, `TransactionRow.test.tsx`, and `Transactions.test.tsx`, but the whole suite should still be green because no shared types or fixtures changed in a breaking way. `TransactionEntry.test.tsx` (the old one, still on disk until commit 9) continues to test `TransactionEntry` in isolation and is unaffected by this commit. If an unrelated test fails, the most likely cause is a missed caller of `CategoryBadge` still passing the old `name={...} color={...}` props — grep: `cd web && grep -rn 'CategoryBadge' src/ --include='*.tsx'` and confirm that every caller now passes a `category={...}` prop. At the time of plan-writing the only caller is `TransactionRow.tsx`, but the implementer must verify.

- [ ] **Step 14: TypeScript check**

Run: `cd web && npx tsc -b --noEmit`

Expected: No type errors. If `CategoryBadge`'s caller in any file outside `TransactionRow.tsx` still passes `color={...}` / `name={...}` props, the build fails with a `TS2322: Type '{ name: string; color: string; }' is not assignable to type 'CategoryBadgeProps'` error. Grep for other callers: `cd web && npx tsc -b --noEmit 2>&1 | head -20` will pinpoint them. At the time of plan-writing the only caller is `TransactionRow.tsx`, but the implementer must verify.

- [ ] **Step 15: Lint**

Run: `cd web && npx eslint src/pages/Transactions.tsx src/components/TransactionToolbar.tsx src/components/FilterPanel.tsx src/components/TransactionRow.tsx src/components/CategoryBadge.tsx`

Expected: No errors. Common issues: unused imports (`useState` after conversion, old `styles` import), and `react-hooks/exhaustive-deps` warnings on the `handleExport`/`activeChips` memos — if they appear, verify the dep arrays match the old file (which also had them and did not warn), because the logic is identical.

- [ ] **Step 16: Dev server smoke check**

Run: `cd web && npm run dev`

Expected: Vite dev server starts on its default port with no build errors. Manually exercise in a browser (logged in as an admin user):

1. `/transactions` loads and shows the page header, toolbar, and table
2. Search input filters results
3. All / Expenses / Income toggle switches visibly (selected variant)
4. Clicking "+ Add" reveals the old entry form; clicking "Cancel" hides it; the entry form's state (amount, description, tags) survives hide→show
5. Clicking "Filters" opens the right-side Sheet with four tabs
6. In the Date tab, "This Month" populates the date inputs; the Filters button label updates to `Filters (1)`
7. In the Category tab, clicking a category chip colors it with the shared chart palette (not a per-category hex)
8. Closing the Sheet and clicking the × on an active chip clears that filter
9. Each row's kebab menu (right column) opens a DropdownMenu with Edit and Delete items; Edit switches the row into edit mode with shadcn Input + Select; Save commits and returns to display mode

Stop the dev server with Ctrl+C.

- [ ] **Step 17: Commit**

```bash
git add \
  web/src/components/CategoryBadge.tsx \
  web/src/components/CategoryBadge.test.tsx \
  web/src/components/TransactionToolbar.tsx \
  web/src/components/FilterPanel.tsx \
  web/src/components/TransactionRow.tsx \
  web/src/components/TransactionRow.test.tsx \
  web/src/pages/Transactions.tsx \
  web/src/pages/Transactions.test.tsx
git commit -m "feat(web): rewrite transactions page shell with tailwind + shadcn

- CategoryBadge now takes {id, name} and derives color via
  getCategoryColorVar — no per-category hex colors on the client
- TransactionToolbar, FilterPanel, TransactionRow, Transactions.tsx
  rewritten with Tailwind classes and shadcn primitives (Card, Table,
  Sheet, DropdownMenu, Tabs, Badge, Button, Input, Select, Label)
- FilterPanel is now the contents of a right-side Sheet; the Sheet
  wrapper lives in Transactions.tsx
- Row actions are a kebab DropdownMenu whose trigger exposes
  aria-label={\"Actions for \" + description}
- Amount columns use font-mono tabular-nums
- Old TransactionEntry component is still wired in via a Tailwind
  \`hidden\` toggle; it is replaced in the next commit under TDD
- Transactions.module.css / Tabs.module.css / ChartTooltip.module.css
  are NOT deleted in this commit. Each is still imported by at least
  one component that has not been rewritten yet (TagInput, old
  TransactionEntry, Tabs.tsx, ChartTooltip.tsx). All three are swept
  by the final-cleanup commit.
- Bulk select and bulk categorize are deferred; a TODO in
  Transactions.tsx marks the gap"
```

---

## Chunk 6: Transaction entry row TDD rewrite (commit 9)

This chunk covers commit 9 from spec §8 — the highest-value commit of the whole migration. It rewrites the transaction-entry flow from a classic HTML form into a keyboard-driven, RHF-backed row with a fuzzy category picker, inline validation, and an undoable save via Sonner toast.

**Spec references (read in this order before starting):**

- §5 (all subsections §5.1–§5.6) — ground truth, target behavior, implementation plan, keyboard handling, and the testing list that drives this chunk's TDD test file
- §8 commit 9 line 753 — the one-line summary: rename `TransactionEntry → TransactionEntryRow`, rewrite with RHF + shadcn `Form` + `Command` + `TagInput` wrapper + Sonner toast + undo buffer; delete old `TransactionEntry.tsx` and `TransactionEntry.test.tsx` in the same commit
- §3 line 305 "inline entry row" — see "Deliberate decision 1" below for interpretation
- Spec §5.1 "Current behavior" also reminds us that `spendrop-last-category` localStorage key is joined by a new `spendrop-last-date` key in the rewrite (§5.2 item 4)

**Deliberate decisions documented up-front (do not re-litigate at implementation time):**

1. **The entry row is a `<form>` element above the `<Table>`, not inside `<tbody>`.** HTML forbids `<form>` from wrapping `<tr>` (a `<tr>` must be inside a table section element), so a form-wrapped row is not a legal DOM tree. Spec §5.1's ground-truth description of the current behavior is "rendered above the table inside a wrapper that the Transactions page toggles" — spec §5.2's target-behavior list never overrides that placement. The phrase "inline entry row" in §3 line 305 is interpreted as "inline on the page, not in a modal dialog", which matches spec §5.1. The visual column-alignment between the entry row fields and the `Table` columns below is **not** a goal of this commit and is explicitly out of scope — implementer should not spend effort on CSS grid alignment with the underlying table. The row is rendered as a `Card` with a flex-gap layout containing the field controls.

2. **`useTransactions.createTransaction` return type changes from `Promise<void>` to `Promise<Transaction>`.** The undo flow in spec §5.3 (lines 459–485) requires the created transaction's `id` so `undoLastSave` can call `deleteTransaction(saved.id)`. The existing hook's `createTransaction` discards the response; this commit updates the hook to return the response body (a `Transaction` — the backend `POST /api/transactions` handler already returns the created record). Blast radius: exactly one call site — `Transactions.tsx` — which is being edited in this same commit anyway. No hook test references the return type (verified: `grep -n "createTransaction" web/src/hooks/useTransactions.test.ts` returns no matches at the time this chunk was written). The `Promise<Transaction>` is assignable to any caller that previously ignored the result, so there is no cascading break.

3. **Sonner `<Toaster />` mounts inside `AppShell.tsx`, not in `main.tsx`.** Mounting in `main.tsx` outside the React Router tree also works, but `AppShell` is the cohesive place for app-level chrome and is already the single wrapper for every authenticated page. Placing the `<Toaster />` as the last child of `AppShell`'s root `<div>` keeps it adjacent to the `<main>` content without being inside any route's tree, so route transitions don't unmount it. The `<Toaster />` is the shadcn wrapper created in Task 2 (`web/src/components/ui/sonner.tsx`) which reads `theme` from `next-themes` if present and otherwise defaults to `system`. Since SpenDrop does not use `next-themes` (`useTheme` was custom and is deleted in the final cleanup commit), the Toaster will fall back to `system` — which is fine; the CSS-variable-driven `--popover` and `--border` tokens from `globals.css` render correctly under both `.dark` and light-mode `:root`.

4. **Tests mock the `sonner` module via `vi.mock` rather than rendering a real `<Toaster />`.** Sonner uses a global toast queue plus a portal-rendered `<Toaster />` component with internal polling and fade animations. Rendering a real Toaster in `happy-dom` introduces flaky assertions (portal elements live outside the test container, fade timers fight `vi.useFakeTimers`). Mocking `sonner` at the module boundary with `vi.mock('sonner', ...)` gives the test file direct access to a `vi.fn()` stand-in for `toast.success` and avoids the portal/timer interaction entirely. The assertions then check "was `toast.success` called with the expected payload" and "did the action callback run the expected effect", which is both simpler and more robust than DOM queries against a portal.

5. **`TransactionEntryRow` receives `onSubmit` and `onDelete` as props, not via direct hook access.** Keeping the component prop-driven is consistent with the current `TransactionEntry` shape (`categories` + `onSubmit` as props) and keeps the component independently unit-testable. The page wires the hook's `createTransaction` and `deleteTransaction` into the component's props. This preserves the existing separation between hook and component.

6. **The legacy `TransactionEntry.tsx` and `TransactionEntry.test.tsx` are deleted wholesale in this commit.** They are not renamed, not migrated, not edited. The TDD rewrite is a fresh file under a new name (`TransactionEntryRow`). The blast radius of the deletion is a grep across `web/src/` for the literal identifier `TransactionEntry` — at the time this chunk was written, the only hits are the two files being deleted plus two references in `web/src/pages/Transactions.tsx` (line 9 import and line 243 JSX use) which are edited in Step 22 of this task. No other file mentions the old component.

7. **`Transactions.module.css`, `Tabs.module.css`, and `ChartTooltip.module.css` are still NOT deleted in this commit.** Chunk 5 deferred them to commit 13 because other components still import them; that reasoning does not change here. `TagInput.tsx` still imports from `Transactions.module.css` for its chip-pill styles, and `TransactionEntryRow.tsx` will reuse `TagInput` as-is per spec §5.2 item 8 ("the existing `TagInput` component is kept and used as-is"). Do **not** modify `TagInput.tsx` in this commit even though its CSS module import makes it the last remaining consumer of `Transactions.module.css` on the Transactions page. It is swept by the final cleanup commit.

**What's deferred out of this commit (and why):**

- **Bulk select and bulk categorize UI.** These were deferred from Chunk 5 and remain deferred here. They are follow-up work after the migration is complete.
- **Shift-Enter sign toggle / minus-sign heuristics for amount.** Spec §5.2 item 8 explicitly rules these out: "not part of this rewrite — the backend has no concept of a signed amount on a category".
- **Column-alignment of the entry row fields with the `<Table>` columns below.** See Deliberate decision 1.
- **Replacing `TagInput` with a shadcn primitive.** Spec §5.2 item 8 explicitly preserves `TagInput` as-is ("implemented via a `FormField`-wrapped `TagInput`; the existing `TagInput` component is kept and used as-is"). Do not rewrite `TagInput.tsx`.

### Task 9: TransactionEntryRow TDD rewrite (commit 9)

**Files:**
- Create: `web/src/components/TransactionEntryRow.tsx`
- Create: `web/src/components/TransactionEntryRow.test.tsx`
- Delete: `web/src/components/TransactionEntry.tsx`
- Delete: `web/src/components/TransactionEntry.test.tsx`
- Modify: `web/src/hooks/useTransactions.ts` (one-line return type change)
- Modify: `web/src/pages/Transactions.tsx` (swap import, remove `hidden` wrapper, pass `onDelete`)
- Modify: `web/src/components/AppShell.tsx` (mount `<Toaster />`)

**Context before you start:** The old `TransactionEntry.tsx` is a classic React form with `useState` for each field, a manual `handleSubmit` that does optimistic reset (`amount` / `description` / `tags` cleared; `date` / `category_id` preserved), and a `localStorage` key `spendrop-last-category`. It imports `styles` from `../styles/Transactions.module.css`. None of this code is preserved — every line is rewritten under TDD in a new file. The old file is deleted outright in Step 22.

The spec's §5.3 code block is the canonical shape of the new component. Treat it as pseudocode — fill in the imports, the ref, the useCallback dependency arrays, and the full JSX tree. The test file (written in Step 3 below) is the contract. Let the tests drive what you implement.

The entry row reuses:
- `TagInput` from `../components/TagInput` — wrapped in a shadcn `FormField` (spec §5.3 lines 533–545)
- `CategoryBadge` from Chunk 5 — displayed inside each `CommandItem` (spec §5.3 line 517)
- `getCategoryColorVar` from `../lib/chart-colors` — indirectly via `CategoryBadge`
- The `Category` and `Transaction` and `CreateTransactionInput`-adjacent types from `../api/types`

The entry row does NOT reuse any piece of the old `TransactionEntry.tsx` — every handler, every ref, every default-values helper is new.

- [ ] **Step 1: Update `useTransactions.createTransaction` return type**

Open `web/src/hooks/useTransactions.ts`. Locate the interface at line 45 and the `useCallback` implementation at line 138. Apply exactly these two changes.

Change line 45 from:

```ts
  createTransaction: (input: CreateTransactionInput) => Promise<void>;
```

to:

```ts
  createTransaction: (input: CreateTransactionInput) => Promise<Transaction>;
```

Change the `useCallback` body at line 138 from:

```ts
  const createTransaction = useCallback(
    async (input: CreateTransactionInput) => {
      await api.post('transactions', input);
      fetchTransactions();
    },
    [fetchTransactions],
  );
```

to:

```ts
  const createTransaction = useCallback(
    async (input: CreateTransactionInput): Promise<Transaction> => {
      const created = await api.post<Transaction>('transactions', input);
      fetchTransactions();
      return created;
    },
    [fetchTransactions],
  );
```

**Confirm the `api.post` generic signature before editing.** Open `web/src/api/client.ts` and verify `post` accepts a type parameter for the response body (`post<T>(path: string, body: unknown): Promise<T>`). If it does not, update the call to the actual signature — e.g., `const created = (await api.post('transactions', input)) as Transaction;` — and leave a `// TODO(entry-row): tighten api.post generics` comment. Do not edit `api/client.ts` in this commit; that's a separate refactor.

- [ ] **Step 2: Verify the hook change compiles**

Run: `cd web && npx tsc --noEmit`

Expected: PASS. The only existing call site is `Transactions.tsx:245` (`onSubmit={createTransaction}`), which passes `createTransaction` as a prop of type `(input: TransactionInput) => Promise<void>`. A function returning `Promise<Transaction>` is assignable where `Promise<void>` is expected (TypeScript covariant return), so no cascading break.

If `tsc` reports a genuinely unexpected error, stop and re-read the hook and its consumers before continuing.

- [ ] **Step 3: Create `TransactionEntryRow.test.tsx` — write all failing tests first**

This is the single largest TDD artifact in the whole migration. It is the executable spec for the entry row per spec §5.6. Write the full file in one shot, then run it once to confirm every assertion fails (module not found is the first failure mode).

The test file uses `vi.mock('sonner', ...)` so the `toast.success` function is a `vi.fn()` the tests can inspect. It also sets up fake timers for the undo-after-auto-close test.

Create `web/src/components/TransactionEntryRow.test.tsx`:

```tsx
import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import {
  describe,
  it,
  expect,
  vi,
  beforeEach,
  afterEach,
  type Mock,
} from 'vitest';
import { TransactionEntryRow } from './TransactionEntryRow';
import type { Category, Transaction } from '../api/types';

// Mock sonner so toast.success becomes inspectable and no portal renders
vi.mock('sonner', () => ({
  toast: {
    success: vi.fn(),
  },
  Toaster: () => null,
}));

// Import after the mock so we get the mocked version
import { toast } from 'sonner';

const mockCategories: Category[] = [
  {
    id: 1,
    name: 'Groceries',
    type: 'expense',
    icon: null,
    sort_order: 1,
    is_active: true,
    created_at: '2026-01-01',
  },
  {
    id: 2,
    name: 'Salary',
    type: 'income',
    icon: null,
    sort_order: 2,
    is_active: true,
    created_at: '2026-01-01',
  },
  {
    id: 3,
    name: 'Transport',
    type: 'expense',
    icon: null,
    sort_order: 3,
    is_active: true,
    created_at: '2026-01-01',
  },
];

const savedTransaction: Transaction = {
  id: 42,
  date: '2026-04-08',
  amount: 127.43,
  description: 'Whole Foods',
  category_id: 1,
  category_name: 'Groceries',
  category_type: 'expense',
  tags: 'food',
  notes: null,
  original_amount: null,
  original_currency: null,
  created_at: '2026-04-08T12:00:00Z',
  updated_at: '2026-04-08T12:00:00Z',
};

describe('TransactionEntryRow', () => {
  let onSubmit: Mock;
  let onDelete: Mock;

  beforeEach(() => {
    onSubmit = vi.fn().mockResolvedValue(savedTransaction);
    onDelete = vi.fn().mockResolvedValue(undefined);
    (toast.success as Mock).mockClear();
    localStorage.clear();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  // -----------------------------------------------------------------
  // Phase A: Basic render + API payload shape
  // -----------------------------------------------------------------

  it('renders every field with its label', () => {
    render(
      <TransactionEntryRow
        categories={mockCategories}
        onSubmit={onSubmit}
        onDelete={onDelete}
      />,
    );

    expect(screen.getByLabelText(/date/i)).toBeInTheDocument();
    expect(screen.getByLabelText(/amount/i)).toBeInTheDocument();
    expect(screen.getByLabelText(/description/i)).toBeInTheDocument();
    // category field uses a button trigger, not a native input
    expect(
      screen.getByRole('button', { name: /select category/i }),
    ).toBeInTheDocument();
    expect(screen.getByLabelText(/tags/i)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /add/i })).toBeInTheDocument();
  });

  it('submits the canonical payload shape on a full fill + save', async () => {
    const user = userEvent.setup();
    render(
      <TransactionEntryRow
        categories={mockCategories}
        onSubmit={onSubmit}
        onDelete={onDelete}
      />,
    );

    await user.clear(screen.getByLabelText(/date/i));
    await user.type(screen.getByLabelText(/date/i), '2026-04-08');
    await user.clear(screen.getByLabelText(/amount/i));
    await user.type(screen.getByLabelText(/amount/i), '127.43');
    await user.type(screen.getByLabelText(/description/i), 'Whole Foods');

    // open the category picker and pick Groceries
    await user.click(
      screen.getByRole('button', { name: /select category/i }),
    );
    await user.click(await screen.findByRole('option', { name: /groceries/i }));

    // tag input accepts Enter to commit a tag
    await user.type(screen.getByLabelText(/tags/i), 'food{Enter}');

    await user.click(screen.getByRole('button', { name: /add/i }));

    await waitFor(() => {
      expect(onSubmit).toHaveBeenCalledTimes(1);
    });
    expect(onSubmit).toHaveBeenCalledWith({
      date: '2026-04-08',
      amount: 127.43,
      description: 'Whole Foods',
      category_id: 1,
      tags: 'food',
    });
  });

  it('sends an empty tags string when no tags were added', async () => {
    const user = userEvent.setup();
    render(
      <TransactionEntryRow
        categories={mockCategories}
        onSubmit={onSubmit}
        onDelete={onDelete}
      />,
    );

    await user.clear(screen.getByLabelText(/amount/i));
    await user.type(screen.getByLabelText(/amount/i), '10');
    await user.type(screen.getByLabelText(/description/i), 'Coffee');
    await user.click(
      screen.getByRole('button', { name: /select category/i }),
    );
    await user.click(await screen.findByRole('option', { name: /groceries/i }));

    await user.click(screen.getByRole('button', { name: /add/i }));

    await waitFor(() => {
      expect(onSubmit).toHaveBeenCalledTimes(1);
    });
    expect(onSubmit.mock.calls[0][0].tags).toBe('');
  });

  // -----------------------------------------------------------------
  // Phase B: Category picker (Popover + Command)
  // -----------------------------------------------------------------

  it('filters the category list as the user types in the picker', async () => {
    const user = userEvent.setup();
    render(
      <TransactionEntryRow
        categories={mockCategories}
        onSubmit={onSubmit}
        onDelete={onDelete}
      />,
    );

    await user.click(
      screen.getByRole('button', { name: /select category/i }),
    );
    const searchbox = screen.getByPlaceholderText(/search category/i);
    await user.type(searchbox, 'tra');

    // Transport remains visible, Groceries and Salary do not
    expect(
      await screen.findByRole('option', { name: /transport/i }),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole('option', { name: /groceries/i }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole('option', { name: /salary/i }),
    ).not.toBeInTheDocument();
  });

  it('selecting a category from the picker updates the trigger label', async () => {
    const user = userEvent.setup();
    render(
      <TransactionEntryRow
        categories={mockCategories}
        onSubmit={onSubmit}
        onDelete={onDelete}
      />,
    );

    await user.click(
      screen.getByRole('button', { name: /select category/i }),
    );
    await user.click(await screen.findByRole('option', { name: /transport/i }));

    // The picker trigger now shows the selected category name
    expect(
      screen.getByRole('button', { name: /transport/i }),
    ).toBeInTheDocument();
  });

  // -----------------------------------------------------------------
  // Phase C: Keyboard — Enter navigation + ⌘Enter submit
  // -----------------------------------------------------------------

  it('Enter on Amount moves focus to Description', async () => {
    const user = userEvent.setup();
    render(
      <TransactionEntryRow
        categories={mockCategories}
        onSubmit={onSubmit}
        onDelete={onDelete}
      />,
    );

    const amount = screen.getByLabelText(/amount/i);
    amount.focus();
    await user.type(amount, '50{Enter}');

    expect(screen.getByLabelText(/description/i)).toHaveFocus();
    // Form should NOT have submitted yet
    expect(onSubmit).not.toHaveBeenCalled();
  });

  it('Cmd/Ctrl+Enter from any field submits the form immediately', async () => {
    const user = userEvent.setup();
    render(
      <TransactionEntryRow
        categories={mockCategories}
        onSubmit={onSubmit}
        onDelete={onDelete}
      />,
    );

    // Fill the minimum set
    await user.clear(screen.getByLabelText(/amount/i));
    await user.type(screen.getByLabelText(/amount/i), '10');
    await user.type(screen.getByLabelText(/description/i), 'Bread');
    await user.click(
      screen.getByRole('button', { name: /select category/i }),
    );
    await user.click(await screen.findByRole('option', { name: /groceries/i }));

    // Cmd+Enter from amount
    screen.getByLabelText(/amount/i).focus();
    await user.keyboard('{Control>}{Enter}{/Control}');

    await waitFor(() => {
      expect(onSubmit).toHaveBeenCalledTimes(1);
    });
  });

  // -----------------------------------------------------------------
  // Phase D: Escape resets
  // -----------------------------------------------------------------

  it('Escape resets every field to its default value', async () => {
    const user = userEvent.setup();
    render(
      <TransactionEntryRow
        categories={mockCategories}
        onSubmit={onSubmit}
        onDelete={onDelete}
      />,
    );

    const amount = screen.getByLabelText(/amount/i) as HTMLInputElement;
    const description = screen.getByLabelText(
      /description/i,
    ) as HTMLInputElement;

    await user.clear(amount);
    await user.type(amount, '999');
    await user.type(description, 'Wrong entry');

    description.focus();
    await user.keyboard('{Escape}');

    // Amount resets to 0 (default from Zod schema), description resets to ''
    expect(amount.value).toBe('0');
    expect(description.value).toBe('');
  });

  // -----------------------------------------------------------------
  // Phase E: onSubmit side effects — post-save reset + Sonner toast + focus
  // -----------------------------------------------------------------

  it('after save clears amount/description/tags but preserves date/category', async () => {
    const user = userEvent.setup();
    render(
      <TransactionEntryRow
        categories={mockCategories}
        onSubmit={onSubmit}
        onDelete={onDelete}
      />,
    );

    await user.clear(screen.getByLabelText(/date/i));
    await user.type(screen.getByLabelText(/date/i), '2026-04-08');
    await user.clear(screen.getByLabelText(/amount/i));
    await user.type(screen.getByLabelText(/amount/i), '25');
    await user.type(screen.getByLabelText(/description/i), 'Lunch');
    await user.click(
      screen.getByRole('button', { name: /select category/i }),
    );
    await user.click(await screen.findByRole('option', { name: /groceries/i }));
    await user.type(screen.getByLabelText(/tags/i), 'food{Enter}');

    await user.click(screen.getByRole('button', { name: /add/i }));

    await waitFor(() => {
      expect(onSubmit).toHaveBeenCalled();
    });

    expect(
      (screen.getByLabelText(/amount/i) as HTMLInputElement).value,
    ).toBe('0');
    expect(
      (screen.getByLabelText(/description/i) as HTMLInputElement).value,
    ).toBe('');
    expect((screen.getByLabelText(/date/i) as HTMLInputElement).value).toBe(
      '2026-04-08',
    );
    // Category still shows Groceries
    expect(
      screen.getByRole('button', { name: /groceries/i }),
    ).toBeInTheDocument();
    // Amount field is refocused
    expect(screen.getByLabelText(/amount/i)).toHaveFocus();
  });

  it('fires a Sonner success toast with an Undo action on save', async () => {
    const user = userEvent.setup();
    render(
      <TransactionEntryRow
        categories={mockCategories}
        onSubmit={onSubmit}
        onDelete={onDelete}
      />,
    );

    await user.clear(screen.getByLabelText(/amount/i));
    await user.type(screen.getByLabelText(/amount/i), '5');
    await user.type(screen.getByLabelText(/description/i), 'Gum');
    await user.click(
      screen.getByRole('button', { name: /select category/i }),
    );
    await user.click(await screen.findByRole('option', { name: /groceries/i }));

    await user.click(screen.getByRole('button', { name: /add/i }));

    await waitFor(() => {
      expect(toast.success).toHaveBeenCalledTimes(1);
    });
    const [msg, opts] = (toast.success as Mock).mock.calls[0];
    expect(msg).toMatch(/saved/i);
    expect(opts.duration).toBe(4000);
    expect(opts.action.label).toMatch(/undo/i);
    expect(typeof opts.action.onClick).toBe('function');
    expect(typeof opts.onAutoClose).toBe('function');
  });

  // -----------------------------------------------------------------
  // Phase F: Undo — click action + ⌘Z while toast visible + auto-close guard
  // -----------------------------------------------------------------

  it('clicking the Undo action calls onDelete with the saved id', async () => {
    const user = userEvent.setup();
    render(
      <TransactionEntryRow
        categories={mockCategories}
        onSubmit={onSubmit}
        onDelete={onDelete}
      />,
    );

    await user.clear(screen.getByLabelText(/amount/i));
    await user.type(screen.getByLabelText(/amount/i), '5');
    await user.type(screen.getByLabelText(/description/i), 'Gum');
    await user.click(
      screen.getByRole('button', { name: /select category/i }),
    );
    await user.click(await screen.findByRole('option', { name: /groceries/i }));
    await user.click(screen.getByRole('button', { name: /add/i }));

    await waitFor(() => {
      expect(toast.success).toHaveBeenCalled();
    });
    const opts = (toast.success as Mock).mock.calls[0][1];

    // Simulate the user clicking the toast's Undo button
    await opts.action.onClick();

    expect(onDelete).toHaveBeenCalledTimes(1);
    expect(onDelete).toHaveBeenCalledWith(42);
    // And the description comes back (restored from the saved values)
    await waitFor(() => {
      expect(
        (screen.getByLabelText(/description/i) as HTMLInputElement).value,
      ).toBe('Gum');
    });
  });

  it('⌘Z after toast auto-close is a no-op (onDelete NOT called)', async () => {
    const user = userEvent.setup();
    render(
      <TransactionEntryRow
        categories={mockCategories}
        onSubmit={onSubmit}
        onDelete={onDelete}
      />,
    );

    await user.clear(screen.getByLabelText(/amount/i));
    await user.type(screen.getByLabelText(/amount/i), '5');
    await user.type(screen.getByLabelText(/description/i), 'Gum');
    await user.click(
      screen.getByRole('button', { name: /select category/i }),
    );
    await user.click(await screen.findByRole('option', { name: /groceries/i }));
    await user.click(screen.getByRole('button', { name: /add/i }));

    await waitFor(() => {
      expect(toast.success).toHaveBeenCalled();
    });
    const opts = (toast.success as Mock).mock.calls[0][1];

    // Simulate sonner calling the onAutoClose callback after 4s
    opts.onAutoClose();

    // Now press ⌘Z
    await user.keyboard('{Control>}z{/Control}');

    expect(onDelete).not.toHaveBeenCalled();
  });

  // -----------------------------------------------------------------
  // Phase G: Inline validation via FormMessage
  // -----------------------------------------------------------------

  it('shows inline errors for missing amount, description, and category', async () => {
    const user = userEvent.setup();
    render(
      <TransactionEntryRow
        categories={mockCategories}
        onSubmit={onSubmit}
        onDelete={onDelete}
      />,
    );

    // Leave amount at 0, description blank, category unselected
    await user.click(screen.getByRole('button', { name: /add/i }));

    expect(await screen.findByText(/amount must be > 0/i)).toBeInTheDocument();
    expect(
      await screen.findByText(/description required/i),
    ).toBeInTheDocument();
    expect(
      await screen.findByText(/category required/i),
    ).toBeInTheDocument();
    expect(onSubmit).not.toHaveBeenCalled();
  });

  // -----------------------------------------------------------------
  // Phase H: localStorage persistence — date + category
  // -----------------------------------------------------------------

  it('writes spendrop-last-date and spendrop-last-category on save', async () => {
    const user = userEvent.setup();
    render(
      <TransactionEntryRow
        categories={mockCategories}
        onSubmit={onSubmit}
        onDelete={onDelete}
      />,
    );

    await user.clear(screen.getByLabelText(/date/i));
    await user.type(screen.getByLabelText(/date/i), '2026-04-08');
    await user.clear(screen.getByLabelText(/amount/i));
    await user.type(screen.getByLabelText(/amount/i), '15');
    await user.type(screen.getByLabelText(/description/i), 'Bus');
    await user.click(
      screen.getByRole('button', { name: /select category/i }),
    );
    await user.click(await screen.findByRole('option', { name: /transport/i }));
    await user.click(screen.getByRole('button', { name: /add/i }));

    await waitFor(() => {
      expect(onSubmit).toHaveBeenCalled();
    });

    expect(localStorage.getItem('spendrop-last-date')).toBe('2026-04-08');
    expect(localStorage.getItem('spendrop-last-category')).toBe('3');
  });

  it('reads spendrop-last-date and spendrop-last-category on mount', () => {
    localStorage.setItem('spendrop-last-date', '2026-03-15');
    localStorage.setItem('spendrop-last-category', '2');

    render(
      <TransactionEntryRow
        categories={mockCategories}
        onSubmit={onSubmit}
        onDelete={onDelete}
      />,
    );

    expect((screen.getByLabelText(/date/i) as HTMLInputElement).value).toBe(
      '2026-03-15',
    );
    // The trigger shows "Salary" because id 2 is Salary
    expect(
      screen.getByRole('button', { name: /salary/i }),
    ).toBeInTheDocument();
  });
});
```

A few test-shape notes the implementer needs to take seriously:

- The `vi.mock('sonner', ...)` call is hoisted by Vitest to the top of the file — that is why the import `import { toast } from 'sonner'` that follows resolves to the mocked module (not a circular problem). Do not move the mock call below the import.
- `userEvent.keyboard('{Control>}{Enter}{/Control}')` encodes "hold Ctrl, press Enter, release Ctrl". The component should treat `Ctrl+Enter` and `Meta+Enter` identically; the test only checks Ctrl to avoid platform-gating.
- The category picker tests assume each `CommandItem` renders with `role="option"` and an accessible name containing the category name. shadcn's `Command` primitive (which wraps `cmdk`) gives `CommandItem` `role="option"` automatically, so this is a safe assumption — do not rewrite it.
- Several tests use `within` from Testing Library but the final draft here does not. That's fine — remove the unused import if ESLint complains, or leave it for follow-up tests.
- Tests use `localStorage.clear()` in `beforeEach` so each test starts with a clean slate. This means the "reads on mount" test must set its keys **inside** the test, not in `beforeEach`.

- [ ] **Step 4: Run the test file — confirm it fails with "module not found"**

Run: `cd web && npx vitest run src/components/TransactionEntryRow.test.tsx`

Expected: FAIL. The error should be "Failed to resolve import './TransactionEntryRow'" or similar. This confirms the test file compiles and the TDD red state is real.

- [ ] **Step 5: Create the `TransactionEntryRow.tsx` skeleton with imports, schema, and defaults helpers**

Create `web/src/components/TransactionEntryRow.tsx`. This is the skeleton — the render body is filled in across the next few steps.

```tsx
import {
  useCallback,
  useEffect,
  useRef,
  type KeyboardEvent,
} from 'react';
import { useForm } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { z } from 'zod';
import { toast } from 'sonner';
import {
  Form,
  FormControl,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form';
import { Input } from '@/components/ui/input';
import { Button } from '@/components/ui/button';
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from '@/components/ui/popover';
import {
  Command,
  CommandEmpty,
  CommandInput,
  CommandItem,
  CommandList,
} from '@/components/ui/command';
import { Card } from '@/components/ui/card';
import { TagInput } from './TagInput';
import { CategoryBadge } from './CategoryBadge';
import type { Category, Transaction } from '../api/types';

const LAST_CATEGORY_KEY = 'spendrop-last-category';
const LAST_DATE_KEY = 'spendrop-last-date';

function todayIso(): string {
  return new Date().toISOString().slice(0, 10);
}

function getLastDate(): string {
  return localStorage.getItem(LAST_DATE_KEY) ?? todayIso();
}

function saveLastDate(value: string) {
  localStorage.setItem(LAST_DATE_KEY, value);
}

function getLastCategoryId(): number {
  const raw = localStorage.getItem(LAST_CATEGORY_KEY);
  if (!raw) return 0;
  const parsed = Number.parseInt(raw, 10);
  return Number.isFinite(parsed) && parsed > 0 ? parsed : 0;
}

function saveLastCategory(id: number) {
  localStorage.setItem(LAST_CATEGORY_KEY, String(id));
}

const entrySchema = z.object({
  date: z.string().regex(/^\d{4}-\d{2}-\d{2}$/, 'Invalid date'),
  amount: z.coerce.number().positive('Amount must be > 0'),
  description: z.string().min(1, 'Description required').max(200),
  category_id: z.number().int().positive('Category required'),
  tags: z.string().default(''),
});
export type EntryFormValues = z.infer<typeof entrySchema>;

export interface TransactionEntryRowProps {
  categories: Category[];
  onSubmit: (input: EntryFormValues) => Promise<Transaction>;
  onDelete: (id: number) => Promise<void>;
}

export function TransactionEntryRow({
  categories,
  onSubmit,
  onDelete,
}: TransactionEntryRowProps) {
  const amountRef = useRef<HTMLInputElement | null>(null);
  const undoBufferRef = useRef<{
    saved: Transaction;
    values: EntryFormValues;
  } | null>(null);

  const form = useForm<EntryFormValues>({
    resolver: zodResolver(entrySchema),
    defaultValues: {
      date: getLastDate(),
      amount: 0,
      description: '',
      category_id: getLastCategoryId(),
      tags: '',
    },
    mode: 'onSubmit',
  });

  return null; // filled in by the next step
}
```

- [ ] **Step 6: Run the tests — confirm they fail on missing fields (not on import)**

Run: `cd web && npx vitest run src/components/TransactionEntryRow.test.tsx`

Expected: FAIL. Now the error should be about `screen.getByLabelText(/date/i)` failing because the component returns `null`. This confirms the component imports resolve and the skeleton compiles.

- [ ] **Step 7: Implement the render body — form wrapper, fields, submit button**

Replace the `return null;` line with the full JSX. This is the largest single edit in the file.

```tsx
  const submit = useCallback(
    async (values: EntryFormValues) => {
      const saved = await onSubmit(values);
      saveLastCategory(values.category_id);
      saveLastDate(values.date);
      undoBufferRef.current = { saved, values };

      toast.success('Transaction saved', {
        duration: 4000,
        action: {
          label: 'Undo (\u2318Z)',
          onClick: () => {
            void undoLastSave();
          },
        },
        onAutoClose: () => {
          undoBufferRef.current = null;
        },
      });

      form.reset({
        date: values.date,
        amount: 0,
        description: '',
        category_id: values.category_id,
        tags: '',
      });
      amountRef.current?.focus();
    },
    [onSubmit, form],
  );

  const undoLastSave = useCallback(async () => {
    const buf = undoBufferRef.current;
    if (!buf) return;
    undoBufferRef.current = null;
    await onDelete(buf.saved.id);
    form.reset(buf.values);
    amountRef.current?.focus();
  }, [onDelete, form]);

  const categoryNameById = (id: number): string | undefined =>
    categories.find((c) => c.id === id)?.name;

  const focusFieldByName = (name: keyof EntryFormValues) => {
    const el = document.querySelector<HTMLElement>(`[data-entry-field="${name}"]`);
    el?.focus();
  };

  const handleKeyDown = (e: KeyboardEvent<HTMLFormElement>) => {
    // ⌘Enter / Ctrl+Enter submits from any field
    if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') {
      e.preventDefault();
      void form.handleSubmit(submit)();
      return;
    }
    // Escape resets to defaults
    if (e.key === 'Escape') {
      e.preventDefault();
      form.reset();
      return;
    }
    // Enter navigates field-to-field unless the focus is on the submit button
    if (e.key === 'Enter') {
      const target = e.target as HTMLElement;
      if (target.tagName === 'BUTTON' && target.getAttribute('type') === 'submit') {
        return; // let the form submit
      }
      const order: Array<keyof EntryFormValues> = [
        'date',
        'amount',
        'description',
        'category_id',
        'tags',
      ];
      const current = target.getAttribute('data-entry-field') as
        | keyof EntryFormValues
        | null;
      if (!current) return;
      const idx = order.indexOf(current);
      if (idx < 0 || idx === order.length - 1) return;
      e.preventDefault();
      focusFieldByName(order[idx + 1]);
    }
  };

  // Global ⌘Z listener — scoped to "undo buffer still populated"
  useEffect(() => {
    const onKey = (e: globalThis.KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'z') {
        if (!undoBufferRef.current) return;
        e.preventDefault();
        void undoLastSave();
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [undoLastSave]);

  return (
    <Card className="p-4">
      <Form {...form}>
        <form
          onSubmit={form.handleSubmit(submit)}
          onKeyDown={handleKeyDown}
          className="flex flex-wrap items-end gap-3"
          noValidate
        >
          <FormField
            control={form.control}
            name="date"
            render={({ field }) => (
              <FormItem className="w-36">
                <FormLabel htmlFor="entry-date">Date</FormLabel>
                <FormControl>
                  <Input
                    id="entry-date"
                    type="date"
                    data-entry-field="date"
                    {...field}
                  />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name="amount"
            render={({ field }) => (
              <FormItem className="w-32">
                <FormLabel htmlFor="entry-amount">Amount</FormLabel>
                <FormControl>
                  <Input
                    id="entry-amount"
                    type="number"
                    step="0.01"
                    min="0"
                    data-entry-field="amount"
                    className="font-mono tabular-nums"
                    {...field}
                    ref={(el) => {
                      field.ref(el);
                      amountRef.current = el;
                    }}
                    value={field.value ?? 0}
                    onChange={(e) =>
                      field.onChange(e.target.value === '' ? 0 : Number(e.target.value))
                    }
                  />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name="description"
            render={({ field }) => (
              <FormItem className="flex-1 min-w-[12rem]">
                <FormLabel htmlFor="entry-description">Description</FormLabel>
                <FormControl>
                  <Input
                    id="entry-description"
                    data-entry-field="description"
                    {...field}
                  />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name="category_id"
            render={({ field }) => (
              <FormItem className="w-48">
                <FormLabel>Category</FormLabel>
                <Popover>
                  <PopoverTrigger asChild>
                    <Button
                      type="button"
                      variant="outline"
                      className="w-full justify-start font-normal"
                      data-entry-field="category_id"
                    >
                      {categoryNameById(field.value) ?? 'Select category'}
                    </Button>
                  </PopoverTrigger>
                  <PopoverContent className="p-0" align="start">
                    <Command>
                      <CommandInput placeholder="Search category..." />
                      <CommandList>
                        <CommandEmpty>No category found.</CommandEmpty>
                        {categories.map((cat) => (
                          <CommandItem
                            key={cat.id}
                            value={cat.name}
                            onSelect={() => field.onChange(cat.id)}
                          >
                            <CategoryBadge category={cat} />
                            <span className="ml-2">{cat.name}</span>
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

          <FormField
            control={form.control}
            name="tags"
            render={({ field }) => (
              <FormItem className="w-56">
                <FormLabel htmlFor="entry-tags">Tags</FormLabel>
                <FormControl>
                  <TagInput
                    value={field.value}
                    onChange={field.onChange}
                    placeholder="Add tags..."
                  />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />

          <Button type="submit" className="h-10">
            Add
          </Button>
        </form>
      </Form>
    </Card>
  );
}
```

A few small notes on this code that the implementer might otherwise get wrong:

- The `amount` field binds `ref={(el) => { field.ref(el); amountRef.current = el; }}` because react-hook-form needs its own ref and the component also needs a ref for post-save focus. Both refs point at the same element; calling `field.ref(el)` hands the ref to RHF while the assignment captures it locally.
- The hidden element with `data-entry-field="tags"` does not exist on `TagInput`'s inner `<input>` — `TagInput` does not expose a way to mark its inner input. The Enter-navigation handler's `tags → submit` hop therefore relies on `TagInput`'s internal Enter handling to commit the tag and keep focus inside the input, and the user's final submit is the explicit Add button. Spec §5.4 says "Enter on any field → focus next field in a hard-coded order" — for the tags field the effective behavior is "Enter commits a tag in-place; explicit Add button or ⌘Enter submits". This is acceptable and matches the spec's spirit. Do **not** modify `TagInput.tsx` to add a `data-entry-field` attribute — spec §5.2 item 8 preserves `TagInput` as-is.
- The `FormLabel` for the category field has no `htmlFor` — shadcn's `FormField` wires `aria-labelledby` automatically through the `FormItem` context, and the `Popover` trigger is a `Button` that also gets the accessible name via the label.
- `noValidate` on the form element disables native HTML5 validation so Zod errors (surfaced via `FormMessage`) are the only source of truth.

- [ ] **Step 8: Run the test file — iterate until all Phase A, B, C, D, E, F, G, H tests pass**

Run: `cd web && npx vitest run src/components/TransactionEntryRow.test.tsx`

Expected after the render body is complete: all 15 tests pass.

If tests fail, diagnose in this order:

1. **"Unable to find label Amount"** — verify `FormLabel htmlFor="entry-amount"` matches `Input id="entry-amount"`.
2. **"No option with name Groceries"** — the `CommandItem` renders both a `CategoryBadge` and a text span. `role="option"`'s accessible name is the concatenation of its children; make sure the category name is visible text inside the item, not an aria-label on the badge.
3. **Enter navigation test fails** — the `data-entry-field` attribute must live on the focusable element (`Input` / `Button`), not on the `FormItem` wrapper. Verify by querying `document.activeElement` after pressing Enter.
4. **Escape reset test fails** — `form.reset()` with no argument resets to `defaultValues`, which includes `amount: 0`. Confirm the input displays `0` (not blank) by comparing `.value` to the string `'0'`.
5. **⌘Z after auto-close test fails** — ensure the global `window.addEventListener('keydown', ...)` listener checks `undoBufferRef.current` at keydown time, not at listener-add time.
6. **Toast action test fails** — confirm `toast.success` is called with the exact arg shape: first arg is a string, second arg is an object with `duration`, `action`, and `onAutoClose`.
7. **localStorage reads on mount test fails** — `getLastDate()` and `getLastCategoryId()` are called inside the `useForm` default values; these run once on mount. Make sure the helpers use `localStorage.getItem`, not a cached value.

- [ ] **Step 9: Mount `<Toaster />` inside `AppShell.tsx`**

Open `web/src/components/AppShell.tsx`. Add an import at the top:

```tsx
import { Toaster } from '@/components/ui/sonner';
```

And render `<Toaster />` as the last child of the outer `<div>`, below the closing `</main>`:

```tsx
    <div className="flex min-h-screen bg-background text-foreground">
      <Sidebar />
      <main className={...}>
        <Routes>
          {/* ...routes... */}
        </Routes>
      </main>
      <Toaster />
    </div>
```

Keep everything else in `AppShell.tsx` unchanged.

- [ ] **Step 10: Delete the legacy files wholesale**

```bash
cd web
rm src/components/TransactionEntry.tsx
rm src/components/TransactionEntry.test.tsx
```

Do not rename. Do not comment out. The files are removed from the working tree and the commit does not retain them.

- [ ] **Step 11: Wire `TransactionEntryRow` into `Transactions.tsx`**

Open `web/src/pages/Transactions.tsx`. Apply these three edits in order.

**Edit 1 — swap the import at line 9:**

Change:

```tsx
import { TransactionEntry } from '../components/TransactionEntry';
```

to:

```tsx
import { TransactionEntryRow } from '../components/TransactionEntryRow';
```

**Edit 2 — rewrite the JSX around the entry component (currently line ~243, inside the `hidden`-wrapped section from Chunk 5):**

Chunk 5 left this JSX in place:

```tsx
<div className={showEntry ? undefined : 'hidden'}>
  <TransactionEntry
    categories={categories}
    onSubmit={createTransaction}
  />
</div>
```

Replace with:

```tsx
<div className={showEntry ? undefined : 'hidden'}>
  <TransactionEntryRow
    categories={categories}
    onSubmit={createTransaction}
    onDelete={deleteTransaction}
  />
</div>
```

The `hidden` wrapper stays — the `showEntry` toolbar toggle is still how users reveal/hide the entry row, and that toggle is unchanged from Chunk 5.

**Edit 3 — confirm `deleteTransaction` is already destructured from `useTransactions`:**

Lines 26–28 of `Transactions.tsx` already destructure `createTransaction`, `updateTransaction`, `deleteTransaction` from `useTransactions` (verified at Chunk 5 authoring time). Do not re-destructure.

- [ ] **Step 12: Check whether `Transactions.test.tsx` references the old component name**

Run: `grep -n "TransactionEntry" web/src/pages/Transactions.test.tsx`

Expected: zero matches. The page test queries by role/text, not by child-component identity (verified at Chunk 5 authoring time when `Transactions.test.tsx` was rewritten). If the grep unexpectedly returns a match, read the surrounding context and update the test to use `TransactionEntryRow` or the appropriate role query. Do not guess.

- [ ] **Step 13: Run the full test suite**

Run: `cd web && npx vitest run`

Expected: all tests pass. The complete set includes:

- The new `TransactionEntryRow.test.tsx` (15 tests)
- The Chunk 5 `Transactions.test.tsx` (unchanged; should still pass)
- `TransactionRow.test.tsx` from Chunk 5 (unchanged; should still pass)
- Every other test file in the repo (Dashboard, Categories, Auth, Sidebar, AppShell, hooks, etc.)

The only file counts that changed in this commit:
- Added: `TransactionEntryRow.tsx`, `TransactionEntryRow.test.tsx`
- Deleted: `TransactionEntry.tsx`, `TransactionEntry.test.tsx`
- Modified: `useTransactions.ts`, `Transactions.tsx`, `AppShell.tsx`

If anything unexpected fails, stop and diagnose. Do not proceed with a red suite.

- [ ] **Step 14: Run the type check**

Run: `cd web && npx tsc --noEmit`

Expected: zero errors. The hook's return-type change in Step 1 cascades into `Transactions.tsx`, which is assignable both to the component's `onSubmit: (input) => Promise<Transaction>` prop and to any code that ignores the return value.

- [ ] **Step 15: Run ESLint**

Run: `cd web && npx eslint .`

Expected: zero errors. New code must satisfy `eslint-plugin-tailwindcss` (installed in Chunk 1). If it flags a class typo, fix it in the component; do not add an ESLint ignore comment.

- [ ] **Step 16: Dev server smoke test**

Run: `cd web && npm run dev`

Open the app in a browser, navigate to `/transactions`, click the Add toolbar button to reveal the entry row, and manually verify:

1. **Typing in Amount + Enter jumps to Description.** Keyboard flow works.
2. **The category picker opens on click, filters as you type, selects on click.**
3. **A full save shows a Sonner toast in the bottom-right corner with an "Undo (⌘Z)" action button.** The toast auto-dismisses after ~4 seconds.
4. **Clicking Undo in the toast deletes the just-saved transaction** (the row disappears from the table below) **and restores the form fields.**
5. **Escape on any field resets the form.**
6. **Validation errors appear inline** when Add is pressed with an empty form.
7. **No console errors** in the browser devtools.

Stop the dev server with Ctrl+C when done.

- [ ] **Step 17: Commit**

```bash
cd d:/claude/SpenDrop
git status
git add web/src/components/TransactionEntryRow.tsx \
        web/src/components/TransactionEntryRow.test.tsx \
        web/src/hooks/useTransactions.ts \
        web/src/pages/Transactions.tsx \
        web/src/components/AppShell.tsx
git rm web/src/components/TransactionEntry.tsx \
       web/src/components/TransactionEntry.test.tsx
git status
git commit -m "feat(web): rewrite transaction entry as keyboard-driven row with undo

- TransactionEntry → TransactionEntryRow: renamed and fully rewritten
  with react-hook-form + Zod + shadcn Form primitives. Old files
  deleted wholesale in this commit.
- Fuzzy category picker via shadcn Popover + Command (cmdk-backed)
  replaces the native <select>. Type to filter, click or Enter to
  select. Massively faster for 14+ category lists.
- Field-to-field Enter navigation: date → amount → description →
  category → tags → submit. ⌘Enter / Ctrl+Enter submits from any
  field. Escape resets to defaults.
- Smart date default: new spendrop-last-date localStorage key joins
  the existing spendrop-last-category key; both are read on mount
  and written on save.
- Undoable save via Sonner toast: 'Transaction saved' with 'Undo
  (⌘Z)' action, 4s duration. Clicking Undo (or pressing ⌘Z while
  the toast is visible) deletes the just-saved transaction via
  onDelete and restores the form fields from a single-slot
  undoBufferRef. ⌘Z after auto-close is a no-op.
- Inline validation via FormMessage: empty amount/description/
  category surface errors instead of silently no-oping.
- Persistent focus on amount after save via amountRef.
- Sonner <Toaster /> mounted inside AppShell as the last child of
  the outer div.
- useTransactions.createTransaction return type changed from
  Promise<void> to Promise<Transaction> so the undo flow can read
  the saved id.
- Tags preserved as-is via FormField-wrapped TagInput (spec §5.2
  item 8). TagInput.tsx is unchanged; its CSS module import is
  still swept by the final cleanup commit.
- TDD: TransactionEntryRow.test.tsx (15 tests covering render,
  payload shape, category picker, Enter navigation, ⌘Enter,
  Escape, post-save reset + focus, toast shape, undo click, ⌘Z
  after auto-close no-op, validation, localStorage read+write)
  was written first against an unimplemented module, then made
  green incrementally."
```

---

## Chunk 7: Reports + Settings rewrites (commits 10–11)

**Scope:** two consecutive page rewrites that bring the last two non-shell pages onto Tailwind + shadcn. Task 10 covers `pages/Reports.tsx` (commit 10); Task 11 covers `pages/Settings.tsx` (commit 11). Both pages are heavily form-driven and sit on top of multiple hooks — the work is mechanical but fiddly because native `<select>` elements get swapped for shadcn `Select` and existing tests query those selects by label.

After this chunk lands, every page under `web/src/pages/` is on the new stack. Only three files will still reference the legacy CSS modules (`components/Tabs.tsx`, `components/ChartTooltip.tsx`, `components/TagInput.tsx`) and those are all swept by commit 13 in Chunk 8. Preflight stays disabled through the whole chunk — commit 13 is what re-enables it.

### Deliberate decisions for Chunk 7

These are the choices the implementer must not second-guess. Each one sits on top of something either already established by an earlier commit in this plan or enforced by the spec.

1. **Orphaned legacy wrappers are left on disk until commit 13.** Commit 10 stops importing `components/ChartTooltip.tsx`. Commit 11 stops importing `components/Tabs.tsx`. Both files become unused after their respective commits. **Do not delete them in Chunks 7.** Chunk 5 already documented the `commit 13` sweep column for `Tabs.module.css` and `ChartTooltip.module.css`; Chunk 8 / Task 13 enumerates every legacy file for final removal. Deleting the component wrappers here would split the cleanup across three commits for no benefit.

2. **shadcn `Select` replaces every native `<select>` on both pages.** The spec §3 Reports paragraph and §3 Settings paragraph both call for shadcn primitives. All of Reports' month/year/period selects and all of Settings' role/currency/goal selects move to shadcn `Select`. Tests are rewritten to drive the combobox-role trigger + option pattern instead of `user.selectOptions(...)`.

3. **shadcn `Tabs` replaces the custom `components/Tabs.tsx` in Settings only.** Reports does **not** use the custom Tabs today (it uses `<section>` blocks), so Reports is rewritten without any tabs primitive — the spec §3 Reports paragraph mentions a "Period `Tabs`" for a custom-range picker, but SpenDrop's current Reports page has no custom-range flow and the four sections are all rendered at once. **We preserve the current "all four sections stacked" layout** and do not introduce a period-tabs control that isn't part of today's behavior. The spec's hypothetical tabbed-period UI is explicitly deferred; a `TODO` comment in `Reports.tsx` documents the deferral.

4. **`DateRangePicker` is also deferred.** Spec §8 commit 10 mentions it. Current Reports has no date-range picker — only year + month dropdowns. Adding one requires a new backend endpoint shape and is out of scope for the visual-system rewrite. A `TODO` comment cites spec §8 commit 10 and notes it is deferred.

5. **`useChartTheme` is removed from the import graph on the Reports page.** Commit 6 already removed the hook's only other consumer (Dashboard). After commit 10, `web/src/hooks/useChartTheme.ts` has zero importers. **Leave the file on disk** — Chunk 8 / Task 13 deletes it with the rest of the legacy hooks. Commit 10 only stops importing it.

6. **Category trend line colors use `getCategoryColorVar({ id: cat.id })`.** Spec §7.2 line 700 calls out this exact replacement at current `Reports.tsx:219`. The `cat.color` field is still on the `Category` type (not dropped until commit 12), but commit 10 stops reading it so the migration in commit 12 has no blast radius on this page.

7. **Settings test fixture keeps `color:` on `mockCategories` for this commit only.** Spec §7.2 line 710 lists `Settings.test.tsx:419–421` as a TS2353 break that must be fixed **in commit 12**, not in commit 11. The `Category.color` field still exists on the TypeScript type through commits 10 and 11. Chunk 8 / Task 12 removes the `color:` keys as part of the atomic migration commit. Commit 11 rewrites the same test file for new queries (combobox-role, Dialog button, etc.) but leaves the fixture's `color:` entries alone — commit 12 strips them.

8. **Month/year comboboxes query by `role="combobox"`, not `role="button"`.** shadcn `Select` uses Radix `SelectTrigger`, which has `role="combobox"`. Tests use `screen.getByRole('combobox', { name: /.../i })` to find triggers. The visible option list uses `role="listbox"` with child `role="option"` items. This is the Radix-standard pattern and matches how the existing `Dashboard.test.tsx` (commit 6) already drives shadcn `Select` — see Chunk 3 / Task 6 for precedent.

9. **`user.setup({ pointerEventsCheck: 0 })` is required for Radix `Select` and `Dialog` tests.** Radix primitives listen for `pointerdown` with pointer-events detection, and happy-dom doesn't simulate pointer events by default — `userEvent.setup()` with the default `pointerEventsCheck: PointerEventsCheckLevel.EachCall` throws inside Radix. The fix is to pass `pointerEventsCheck: 0` (i.e. `PointerEventsCheckLevel.Never`) to every `userEvent.setup()` call in both rewritten test files. This is the same workaround Chunk 3 / Task 6 uses in `Dashboard.test.tsx`.

10. **Data tab export buttons stay plain `<Button onClick>`; the `Dialog` wraps import confirm only.** Spec §3 Settings says "Data tab uses `Button` + `Dialog` for export/import confirmation". In practice, export is a one-click `window.open(...)` that needs no confirmation — we do not wrap it in a Dialog. The `Dialog` appears only on the **import confirm** step: clicking `Import` in the preview opens a confirmation dialog that summarizes the row count + default category, and only after confirming does `api.post('import/confirm', ...)` fire. This matches what a cautious "are you sure you want to import 127 rows?" flow should look like.

11. **Users tab role dropdown cannot use `user.selectOptions`.** The existing Settings test at line 371 drives the role change via `user.selectOptions(screen.getByLabelText(/role for alice/i), 'member')`. shadcn `Select` is not a native `<select>`, so `selectOptions` throws. The rewritten test opens the trigger by role and clicks the `member` option instead. Same applies to every other role-changing / month-picking / year-picking assertion.

### Files created or modified in Chunk 7

| File | Purpose | Task |
|---|---|---|
| `web/src/pages/Reports.tsx` | Rewritten: shadcn `ChartContainer` + `ChartConfig` + shadcn `Select` + Tailwind. No `useChartTheme`, no `ChartTooltip`, no `cat.color`. | Task 10 |
| `web/src/pages/Reports.test.tsx` | Rewritten: mock `@/components/ui/chart` instead of `recharts`; drop `useTheme` mock; update selectors to combobox-role; fixture `color:` key dropped (cleanup already done here since test is fully rewritten). | Task 10 |
| `web/src/styles/Reports.module.css` | Deleted. | Task 10 |
| `web/src/pages/Settings.tsx` | Rewritten: shadcn `Tabs` replaces custom `components/Tabs`; all sections use shadcn `Form` + `Input` + `Select` + `Button`; `Dialog` wraps import confirm. | Task 11 |
| `web/src/pages/Settings.test.tsx` | Rewritten: combobox-role queries; `Dialog` assertions; `user.setup({ pointerEventsCheck: 0 })`; `color:` on `mockCategories` **left in place for commit 12 to remove**. | Task 11 |
| `web/src/styles/Settings.module.css` | Deleted. | Task 11 |

**Not modified in this chunk:**
- `web/src/components/Tabs.tsx` — orphaned after commit 11, swept by commit 13.
- `web/src/components/ChartTooltip.tsx` — orphaned after commit 10, swept by commit 13.
- `web/src/hooks/useChartTheme.ts` — orphaned after commit 10, swept by commit 13.
- `web/src/api/types.ts` — the `color` field on `Category` stays; commit 12 removes it.
- `web/src/hooks/useReports.ts` — data shape is unchanged; tests remain green.

---

### Task 10: Reports rewrite (commit 10)

**Files:**
- Modify: `web/src/pages/Reports.tsx` (full rewrite — 288 lines → ~230 lines)
- Modify: `web/src/pages/Reports.test.tsx` (full rewrite — 139 lines → ~180 lines)
- Delete: `web/src/styles/Reports.module.css`

**Prerequisite reading** (the implementer must re-read these before starting):

- `docs/superpowers/specs/2026-04-09-shadcn-tailwind-rewrite-design.md` §3 Reports paragraph (line 309)
- `docs/superpowers/specs/2026-04-09-shadcn-tailwind-rewrite-design.md` §4 (lines 315–395) — the whole chart-primitive + palette section. The `ChartContainer` + `ChartConfig` + `ChartTooltipContent` API is the single biggest thing to get right.
- `docs/superpowers/specs/2026-04-09-shadcn-tailwind-rewrite-design.md` §7.2 line 700 — the exact line 219 edit.
- `docs/superpowers/specs/2026-04-09-shadcn-tailwind-rewrite-design.md` §8 commit 10 bullet (line 754).
- `web/src/pages/Reports.tsx` — current implementation, top to bottom. 288 lines, all of it.
- `web/src/pages/Reports.test.tsx` — current tests, top to bottom.
- `web/src/hooks/useReports.ts` — the four hooks (`useYearOverYear`, `useCategoryTrends`, `useIncomeExpenses`, `useTopMerchants`) and their return shapes. The rewrite changes nothing about how the hooks are called; it only changes how their output is rendered.
- `web/src/components/ui/chart.tsx` — the shadcn chart primitive installed in commit 2. Re-read the exported types (`ChartContainer`, `ChartTooltip`, `ChartTooltipContent`, `ChartConfig`) and how they wrap Recharts.
- `web/src/lib/chart-colors.ts` from Chunk 2 — the `getCategoryColorVar({ id })` helper.
- `web/src/pages/Dashboard.tsx` from Chunk 3 Task 6 — the precedent for how the rewritten Dashboard renders `ChartContainer` around a `BarChart`. Copy the same style.
- `web/src/pages/Dashboard.test.tsx` from Chunk 3 Task 6 — the precedent for mocking `@/components/ui/chart` in tests.

**Commit order inside the task:** tests first (they will fail against the old component), then rewrite the component, then delete the CSS module, then run tests + typecheck, then commit.

- [ ] **Step 1: Re-read Reports.tsx top to bottom.**

Run: (no command — Read tool)
Expected: understand every section, every native `<select>`, every `cat.color` reference, and the `useChartTheme` / `ChartTooltip` call sites. Confirm the line numbers in spec §7.2 line 700 match (`cat.color || 'var(--color-text-secondary)'` at line 219).

- [ ] **Step 2: Re-read Reports.test.tsx top to bottom.**

Run: (no command — Read tool)
Expected: note the current 6 tests (page heading, section headings, merchants list, year selector, period selector, empty state), the `vi.mock('../hooks/useTheme', ...)` mock, the `vi.mock('recharts', ...)` mock, and the `mockCatTrends` fixture with `color: '#ff0000'`.

- [ ] **Step 3: Rewrite `web/src/pages/Reports.test.tsx`.**

Replace the entire file with the rewritten test. The new test mocks `@/components/ui/chart` (not `recharts`), drops the `useTheme` mock entirely (useChartTheme is gone from the component graph), keeps the four hooks mocked via the api client, uses `getByRole('combobox', ...)` for every select, and drops the `color:` key from the catTrends fixture (cleanup done here since the test is fully rewritten).

```tsx
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { api } from '../api/client';

vi.mock('../api/client', () => ({
  api: {
    get: vi.fn(),
  },
}));

// Mock the shadcn chart primitive so the component tree renders without
// measuring the DOM in happy-dom. Each wrapper just renders its children.
vi.mock('@/components/ui/chart', () => ({
  ChartContainer: ({ children }: { children: React.ReactNode }) => (
    <div data-testid="chart-container">{children}</div>
  ),
  ChartTooltip: () => <div />,
  ChartTooltipContent: () => <div />,
  ChartLegend: () => <div />,
  ChartLegendContent: () => <div />,
}));

// Mock recharts primitives used directly by the Reports page. The shadcn
// chart primitive wraps these but the page still imports Bar/BarChart/Line
// etc. directly from recharts.
vi.mock('recharts', () => ({
  ResponsiveContainer: ({ children }: { children: React.ReactNode }) => (
    <div data-testid="responsive-container">{children}</div>
  ),
  BarChart: ({ children }: { children: React.ReactNode }) => (
    <div data-testid="bar-chart">{children}</div>
  ),
  Bar: () => <div />,
  LineChart: ({ children }: { children: React.ReactNode }) => (
    <div data-testid="line-chart">{children}</div>
  ),
  Line: () => <div />,
  XAxis: () => <div />,
  YAxis: () => <div />,
  Tooltip: () => <div />,
  Legend: () => <div />,
  CartesianGrid: () => <div />,
}));

import { Reports } from './Reports';

const mockYoY = {
  current_year: 2026,
  previous_year: 2025,
  current: Array.from({ length: 12 }, (_, i) => ({
    month: i + 1, expenses: 1000 + i * 100, income: 2000,
  })),
  previous: Array.from({ length: 12 }, (_, i) => ({
    month: i + 1, expenses: 900 + i * 80, income: 1800,
  })),
};

const mockCatTrends = {
  categories: [
    {
      id: 1,
      name: 'Food',
      type: 'expense',
      data: [{ year: 2026, month: 1, total: 500 }],
    },
  ],
};

const mockIncExp = {
  data: Array.from({ length: 12 }, (_, i) => ({
    year: 2026, month: i + 1, income: 2000, expenses: 1500, net: 500,
  })),
};

const mockMerchants = {
  year: 2026,
  month: 4,
  merchants: [
    { description: 'Grocery Store', tx_count: 8, total: 450.50 },
    { description: 'Gas Station', tx_count: 4, total: 200.00 },
  ],
};

beforeEach(() => {
  vi.mocked(api.get).mockImplementation((path: string) => {
    if (path.includes('year-over-year')) return Promise.resolve(mockYoY);
    if (path.includes('category-trends')) return Promise.resolve(mockCatTrends);
    if (path.includes('income-expenses')) return Promise.resolve(mockIncExp);
    if (path.includes('top-merchants')) return Promise.resolve(mockMerchants);
    return Promise.reject(new Error('unknown path'));
  });
});

function renderReports() {
  return render(
    <MemoryRouter>
      <Reports />
    </MemoryRouter>,
  );
}

describe('Reports', () => {
  it('renders the page heading', () => {
    renderReports();
    expect(
      screen.getByRole('heading', { level: 1, name: 'Reports' }),
    ).toBeInTheDocument();
  });

  it('renders all four section headings', async () => {
    renderReports();
    await waitFor(() => {
      expect(screen.getByText('Year-over-Year Comparison')).toBeInTheDocument();
      expect(screen.getByText('Income vs Expenses')).toBeInTheDocument();
      expect(screen.getByText('Category Trends')).toBeInTheDocument();
      expect(screen.getByText('Top Merchants')).toBeInTheDocument();
    });
  });

  it('renders top merchants list', async () => {
    renderReports();
    await waitFor(() => {
      expect(screen.getByText('Grocery Store')).toBeInTheDocument();
      expect(screen.getByText('Gas Station')).toBeInTheDocument();
    });
  });

  it('renders year selector for year-over-year (combobox role)', () => {
    renderReports();
    expect(
      screen.getByRole('combobox', { name: /year-over-year year/i }),
    ).toBeInTheDocument();
  });

  it('renders time period selector for income/expenses (combobox role)', () => {
    renderReports();
    expect(
      screen.getByRole('combobox', { name: /time period/i }),
    ).toBeInTheDocument();
  });

  it('renders month and year selectors for top merchants (combobox role)', () => {
    renderReports();
    expect(
      screen.getByRole('combobox', { name: /merchant month/i }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole('combobox', { name: /merchant year/i }),
    ).toBeInTheDocument();
  });

  it('shows empty state when no merchants', async () => {
    vi.mocked(api.get).mockImplementation((path: string) => {
      if (path.includes('top-merchants'))
        return Promise.resolve({ year: 2026, month: 4, merchants: [] });
      if (path.includes('year-over-year')) return Promise.resolve(mockYoY);
      if (path.includes('category-trends')) return Promise.resolve(mockCatTrends);
      if (path.includes('income-expenses')) return Promise.resolve(mockIncExp);
      return Promise.reject(new Error('unknown'));
    });

    renderReports();
    await waitFor(() => {
      expect(
        screen.getByText('No transactions for this period'),
      ).toBeInTheDocument();
    });
  });

  it('opens the period selector and switches to 24 months', async () => {
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    renderReports();

    await user.click(
      screen.getByRole('combobox', { name: /time period/i }),
    );
    await user.click(screen.getByRole('option', { name: /24 months/i }));

    // After switching, the hook is re-called with 24; the api mock
    // still resolves. We just assert the trigger shows the new value.
    await waitFor(() => {
      expect(
        screen.getByRole('combobox', { name: /time period/i }),
      ).toHaveTextContent(/24 months/i);
    });
  });
});
```

**Notes on the assertions:**
- Every select previously queried via `getByLabelText(...)` is now queried via `getByRole('combobox', { name: /.../i })`. The `aria-label` on the `SelectTrigger` in the component must exactly match the regex.
- The last test exercises one combobox end-to-end: click the trigger, click an option, then assert the trigger text updated. This is the canonical shadcn `Select` interaction in tests.
- No test asserts on chart content — the shadcn chart primitive is mocked wholesale so only the layout around the chart is under test.

- [ ] **Step 4: Run the test suite. Expect failures.**

Run: `cd web && npm test -- Reports.test.tsx`
Expected:
- Previous tests (`by label text`, `/year vs year/i`, etc.) no longer match because the component still uses CSS module classnames and native selects.
- Actually because the component hasn't been rewritten yet, most tests will fail for different reasons: `getByRole('combobox', ...)` finds nothing (no shadcn Select in current component), `getByRole('heading', { level: 1, name: 'Reports' })` passes (the h1 exists), section headings pass, merchants list passes. The new tests querying combobox role should all fail.
- **Minimum expectation: at least the 6 combobox-role queries fail with "Unable to find an accessible element with the role 'combobox' and name …".**

This confirms the test suite is properly red before the implementation.

- [ ] **Step 5: Rewrite `web/src/pages/Reports.tsx`.**

Replace the entire file with the new Tailwind + shadcn implementation. The rewrite:
- Drops `useChartTheme` and `ChartTooltip` imports entirely.
- Wraps each chart in `<ChartContainer config={...}>`.
- Uses `ChartTooltip` + `ChartTooltipContent` from `@/components/ui/chart` (the shadcn wrappers), not the old component.
- Replaces every native `<select>` with shadcn `Select` + `SelectTrigger` + `SelectContent` + `SelectItem`. Every trigger has an `aria-label`.
- Replaces every `styles.*` class with Tailwind utility classes.
- Line 219 equivalent: `stroke={getCategoryColorVar({ id: cat.id })}` instead of `stroke={cat.color || '...'}`.
- Preserves the four sections in the same order (Year-over-Year → Income vs Expenses + Category Trends side-by-side → Top Merchants).

```tsx
import { useState } from 'react';
import {
  ResponsiveContainer,
  BarChart,
  Bar,
  LineChart,
  Line,
  XAxis,
  YAxis,
  CartesianGrid,
} from 'recharts';
import {
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
  ChartLegend,
  ChartLegendContent,
  type ChartConfig,
} from '@/components/ui/chart';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import {
  useYearOverYear,
  useCategoryTrends,
  useIncomeExpenses,
  useTopMerchants,
} from '../hooks/useReports';
import { getCategoryColorVar } from '@/lib/chart-colors';

const MONTH_NAMES = [
  'Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun',
  'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec',
];

const MONTH_FULL_NAMES = [
  'January', 'February', 'March', 'April', 'May', 'June',
  'July', 'August', 'September', 'October', 'November', 'December',
];

function formatCurrency(amount: number): string {
  return amount.toLocaleString('en-US', {
    style: 'currency',
    currency: 'USD',
  });
}

// TODO (spec §8 commit 10): DateRangePicker and Period Tabs (This Month /
// Last Month / YTD / Custom) are deferred. Current Reports keeps its
// four-section-at-once layout. Introducing the period-tabs + range-picker
// control requires new backend endpoints and a redesign of the hook data
// shapes — out of scope for the visual-system rewrite.

export function Reports() {
  const now = new Date();
  const thisYear = now.getFullYear();
  const [yoyYear, setYoyYear] = useState(thisYear);
  const [trendMonths, setTrendMonths] = useState(12);
  const [merchantYear, setMerchantYear] = useState(thisYear);
  const [merchantMonth, setMerchantMonth] = useState(now.getMonth() + 1);

  const yearOptions = Array.from({ length: 5 }, (_, i) => thisYear - i);

  const yoy = useYearOverYear(yoyYear);
  const catTrends = useCategoryTrends(trendMonths);
  const incExp = useIncomeExpenses(trendMonths);
  const merchants = useTopMerchants(merchantYear, merchantMonth);

  // --- Year-over-Year chart data ---
  // IMPORTANT: use hyphen-free, space-free keys (`currentExpenses` /
  // `previousExpenses`) for the ChartConfig. shadcn's `ChartContainer`
  // emits a CSS variable per config key as `--color-<key>`; any non-ident
  // characters in the key (spaces, slashes) break `var(--color-...)`
  // resolution. The human-facing year labels live in `config[key].label`
  // and are what the shadcn `ChartLegendContent` renders to the user.
  const yoyData = yoy.data
    ? yoy.data.current.map((cur, i) => ({
        name: MONTH_NAMES[i],
        currentExpenses: cur.expenses,
        previousExpenses: yoy.data!.previous[i].expenses,
      }))
    : [];

  const yoyConfig: ChartConfig = yoy.data
    ? {
        currentExpenses: {
          label: `${yoy.data.current_year}`,
          color: 'hsl(var(--chart-10))',
        },
        previousExpenses: {
          label: `${yoy.data.previous_year}`,
          color: 'hsl(var(--chart-3))',
        },
      }
    : {};

  // --- Income vs Expenses chart data ---
  const incExpData = incExp.data.map((entry) => ({
    name: `${MONTH_NAMES[entry.month - 1]} ${entry.year}`,
    income: entry.income,
    expenses: entry.expenses,
    net: entry.net,
  }));

  const incExpConfig = {
    income: { label: 'Income', color: 'hsl(var(--chart-6))' },
    expenses: { label: 'Expenses', color: 'hsl(var(--chart-10))' },
  } satisfies ChartConfig;

  // --- Category Trends: top 6 expense categories by total ---
  const expenseCategories = catTrends.data
    .filter((c) => c.type === 'expense')
    .map((c) => ({
      ...c,
      totalSum: c.data.reduce((sum, d) => sum + d.total, 0),
    }))
    .sort((a, b) => b.totalSum - a.totalSum)
    .slice(0, 6);

  // Build category trend line data: one point per month
  const catTrendData: Record<string, unknown>[] = [];
  if (expenseCategories.length > 0) {
    const monthSet = new Set<string>();
    for (const cat of expenseCategories) {
      for (const d of cat.data) {
        monthSet.add(`${d.year}-${d.month}`);
      }
    }
    const sortedMonths = Array.from(monthSet).sort();
    for (const ym of sortedMonths) {
      const [y, m] = ym.split('-').map(Number);
      const point: Record<string, unknown> = {
        name: `${MONTH_NAMES[m - 1]} ${y}`,
      };
      for (const cat of expenseCategories) {
        const found = cat.data.find((d) => d.year === y && d.month === m);
        point[cat.name] = found ? found.total : 0;
      }
      catTrendData.push(point);
    }
  }

  const catTrendConfig = expenseCategories.reduce<ChartConfig>((acc, cat) => {
    acc[cat.name] = {
      label: cat.name,
      color: getCategoryColorVar({ id: cat.id }),
    };
    return acc;
  }, {});

  return (
    <div className="space-y-6 p-6">
      <h1 className="text-2xl font-semibold tracking-tight">Reports</h1>

      {/* Year-over-Year */}
      <Card aria-labelledby="yoy-heading">
        <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
          <CardTitle id="yoy-heading" className="text-base font-medium">
            Year-over-Year Comparison
          </CardTitle>
          <Select
            value={String(yoyYear)}
            onValueChange={(v) => setYoyYear(Number(v))}
          >
            <SelectTrigger
              aria-label="Year-over-Year Year"
              className="w-[180px]"
            >
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {yearOptions.map((y) => (
                <SelectItem key={y} value={String(y)}>
                  {y} vs {y - 1}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </CardHeader>
        <CardContent>
          {yoy.loading && (
            <div className="text-muted-foreground text-sm">Loading...</div>
          )}
          {yoy.error && (
            <div className="text-destructive text-sm" role="alert">
              {yoy.error}
            </div>
          )}
          {yoy.data && (
            <ChartContainer config={yoyConfig} className="h-[300px] w-full">
              <BarChart data={yoyData}>
                <CartesianGrid
                  strokeDasharray="3 3"
                  stroke="hsl(var(--border))"
                  strokeOpacity={0.4}
                  vertical={false}
                />
                <XAxis
                  dataKey="name"
                  fontSize={11}
                  stroke="hsl(var(--muted-foreground))"
                  tickLine={false}
                  axisLine={{ stroke: 'hsl(var(--border))' }}
                />
                <YAxis
                  fontSize={11}
                  stroke="hsl(var(--muted-foreground))"
                  tickLine={false}
                  axisLine={false}
                  tickFormatter={(v: number) => formatCurrency(v)}
                />
                <ChartTooltip content={<ChartTooltipContent />} />
                <ChartLegend content={<ChartLegendContent />} />
                <Bar
                  dataKey="currentExpenses"
                  fill="var(--color-currentExpenses)"
                  radius={[4, 4, 0, 0]}
                />
                <Bar
                  dataKey="previousExpenses"
                  fill="var(--color-previousExpenses)"
                  radius={[4, 4, 0, 0]}
                />
              </BarChart>
            </ChartContainer>
          )}
        </CardContent>
      </Card>

      {/* Income vs Expenses + Category Trends side by side */}
      <div className="grid gap-6 md:grid-cols-2">
        {/* Income vs Expenses */}
        <Card aria-labelledby="incexp-heading">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle id="incexp-heading" className="text-base font-medium">
              Income vs Expenses
            </CardTitle>
            <Select
              value={String(trendMonths)}
              onValueChange={(v) => setTrendMonths(Number(v))}
            >
              <SelectTrigger aria-label="Time Period" className="w-[140px]">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="6">6 months</SelectItem>
                <SelectItem value="12">12 months</SelectItem>
                <SelectItem value="24">24 months</SelectItem>
              </SelectContent>
            </Select>
          </CardHeader>
          <CardContent>
            {incExp.loading && (
              <div className="text-muted-foreground text-sm">Loading...</div>
            )}
            {incExp.error && (
              <div className="text-destructive text-sm" role="alert">
                {incExp.error}
              </div>
            )}
            {!incExp.loading && !incExp.error && (
              <ChartContainer
                config={incExpConfig}
                className="h-[300px] w-full"
              >
                <BarChart data={incExpData}>
                  <CartesianGrid
                    strokeDasharray="3 3"
                    stroke="hsl(var(--border))"
                    strokeOpacity={0.4}
                    vertical={false}
                  />
                  <XAxis
                    dataKey="name"
                    fontSize={11}
                    stroke="hsl(var(--muted-foreground))"
                    tickLine={false}
                    axisLine={{ stroke: 'hsl(var(--border))' }}
                  />
                  <YAxis
                    fontSize={11}
                    stroke="hsl(var(--muted-foreground))"
                    tickLine={false}
                    axisLine={false}
                    tickFormatter={(v: number) => formatCurrency(v)}
                  />
                  <ChartTooltip content={<ChartTooltipContent />} />
                  <ChartLegend content={<ChartLegendContent />} />
                  <Bar
                    dataKey="income"
                    fill="var(--color-income)"
                    radius={[4, 4, 0, 0]}
                  />
                  <Bar
                    dataKey="expenses"
                    fill="var(--color-expenses)"
                    radius={[4, 4, 0, 0]}
                  />
                </BarChart>
              </ChartContainer>
            )}
          </CardContent>
        </Card>

        {/* Category Trends */}
        <Card aria-labelledby="cattrend-heading">
          <CardHeader className="pb-2">
            <CardTitle id="cattrend-heading" className="text-base font-medium">
              Category Trends
            </CardTitle>
          </CardHeader>
          <CardContent>
            {catTrends.loading && (
              <div className="text-muted-foreground text-sm">Loading...</div>
            )}
            {catTrends.error && (
              <div className="text-destructive text-sm" role="alert">
                {catTrends.error}
              </div>
            )}
            {!catTrends.loading && !catTrends.error && (
              <ChartContainer
                config={catTrendConfig}
                className="h-[300px] w-full"
              >
                <LineChart data={catTrendData}>
                  <CartesianGrid
                    strokeDasharray="3 3"
                    stroke="hsl(var(--border))"
                    strokeOpacity={0.4}
                    vertical={false}
                  />
                  <XAxis
                    dataKey="name"
                    fontSize={11}
                    stroke="hsl(var(--muted-foreground))"
                    tickLine={false}
                    axisLine={{ stroke: 'hsl(var(--border))' }}
                  />
                  <YAxis
                    fontSize={11}
                    stroke="hsl(var(--muted-foreground))"
                    tickLine={false}
                    axisLine={false}
                    tickFormatter={(v: number) => formatCurrency(v)}
                  />
                  <ChartTooltip content={<ChartTooltipContent />} />
                  <ChartLegend content={<ChartLegendContent />} />
                  {expenseCategories.map((cat) => (
                    <Line
                      key={cat.id}
                      type="monotone"
                      dataKey={cat.name}
                      stroke={getCategoryColorVar({ id: cat.id })}
                      strokeWidth={2}
                      dot={false}
                    />
                  ))}
                </LineChart>
              </ChartContainer>
            )}
          </CardContent>
        </Card>
      </div>

      {/* Top Merchants */}
      <Card aria-labelledby="merchants-heading">
        <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
          <CardTitle id="merchants-heading" className="text-base font-medium">
            Top Merchants
          </CardTitle>
          <div className="flex gap-2">
            <Select
              value={String(merchantMonth)}
              onValueChange={(v) => setMerchantMonth(Number(v))}
            >
              <SelectTrigger
                aria-label="Merchant Month"
                className="w-[140px]"
              >
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {MONTH_FULL_NAMES.map((m, i) => (
                  <SelectItem key={m} value={String(i + 1)}>
                    {m}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <Select
              value={String(merchantYear)}
              onValueChange={(v) => setMerchantYear(Number(v))}
            >
              <SelectTrigger
                aria-label="Merchant Year"
                className="w-[120px]"
              >
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {yearOptions.map((y) => (
                  <SelectItem key={y} value={String(y)}>
                    {y}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
        </CardHeader>
        <CardContent>
          {merchants.loading && (
            <div className="text-muted-foreground text-sm">Loading...</div>
          )}
          {merchants.error && (
            <div className="text-destructive text-sm" role="alert">
              {merchants.error}
            </div>
          )}
          {!merchants.loading &&
            !merchants.error &&
            merchants.data.length === 0 && (
              <p className="text-muted-foreground text-sm">
                No transactions for this period
              </p>
            )}
          {!merchants.loading &&
            !merchants.error &&
            merchants.data.length > 0 && (
              <ul className="divide-border divide-y">
                {merchants.data.map((m, i) => (
                  <li
                    key={m.description}
                    className="flex items-center gap-3 py-2"
                  >
                    <span className="text-muted-foreground w-6 text-sm font-mono tabular-nums">
                      {i + 1}
                    </span>
                    <span className="flex-1 truncate text-sm">
                      {m.description}
                    </span>
                    <span className="text-muted-foreground text-xs">
                      {m.tx_count} tx{m.tx_count !== 1 ? 's' : ''}
                    </span>
                    <span className="font-mono text-sm tabular-nums">
                      {formatCurrency(m.total)}
                    </span>
                  </li>
                ))}
              </ul>
            )}
        </CardContent>
      </Card>
    </div>
  );
}
```

**Key points about this rewrite:**
- The YoY chart's `dataKey`s are the **ident-safe keys** `currentExpenses` / `previousExpenses`. The user-visible year labels (`2026`, `2025`) live in `yoyConfig[key].label` and render through `ChartLegendContent`. Do **not** use keys with spaces or years in them — shadcn's `ChartContainer` generates a CSS variable per key as `--color-<key>`, and any non-ident character (spaces, dots) in the key breaks `var(--color-...)` resolution on the `<Bar>` `fill` prop.
- `ChartTooltip` here is the **shadcn wrapper** (from `@/components/ui/chart`), not Recharts' raw `Tooltip`. The `Tooltip` import from `recharts` is no longer needed.
- `ChartLegend` + `ChartLegendContent` render the legend in shadcn style.
- No `useChartTheme`: axes read `hsl(var(--border))` and `hsl(var(--muted-foreground))` directly.
- No `ChartTooltip` import from `../components/ChartTooltip` — the old custom component is orphaned after this commit.
- The empty-state merchants block uses a `<p>` (not a `<ul>`) so the "No transactions for this period" assertion still finds a text node, not a list item.

- [ ] **Step 6: Delete `web/src/styles/Reports.module.css`.**

Run: `rm web/src/styles/Reports.module.css`
Expected: file is gone. No remaining `Reports.module.css` import exists (the rewritten `Reports.tsx` does not import it).

- [ ] **Step 7: Grep for any lingering references to the deleted module or removed helpers.**

Run: `cd web && grep -rn "Reports.module.css\|useChartTheme\|from '../components/ChartTooltip'" src/pages/Reports.tsx`
Expected: zero matches.

- [ ] **Step 8: Run the Reports test suite.**

Run: `cd web && npm test -- Reports.test.tsx`
Expected: all 8 tests pass.

- [ ] **Step 9: Run the full test suite + typecheck to catch regressions.**

Run: `cd web && npm test && npx tsc --noEmit`
Expected: every test passes, `tsc --noEmit` exits 0 with no errors. After this commit, `useChartTheme.ts` and `components/ChartTooltip.tsx` have zero importers — `components/Tabs.tsx` is still imported by `pages/Settings.tsx` until commit 11. TypeScript does not flag orphaned files (it only flags unused imports within files that are part of the graph). All three will be swept by Chunk 8 / Task 13.

- [ ] **Step 10: Commit.**

Run:
```bash
cd d:/claude/SpenDrop
git checkout -b feat/shadcn-reports-rewrite  # if not already on a feature branch
git add web/src/pages/Reports.tsx web/src/pages/Reports.test.tsx
git rm web/src/styles/Reports.module.css
git commit -m "feat(web): rewrite Reports page with shadcn chart + Select

- Wrap every chart in ChartContainer + ChartConfig
- Replace native <select> with shadcn Select (combobox role)
- Category trend line colors via getCategoryColorVar
- Drop useChartTheme + ChartTooltip imports (orphaned)
- Delete Reports.module.css
- Rewrite Reports.test.tsx with combobox-role queries

Refs: spec §3 Reports, §4.1 chart anatomy, §7.2 line 700,
§8 commit 10"
```

---

### Task 11: Settings rewrite (commit 11)

**Files:**
- Modify: `web/src/pages/Settings.tsx` (full rewrite — 984 lines → ~850 lines)
- Modify: `web/src/pages/Settings.test.tsx` (full rewrite — 747 lines → ~780 lines, mostly assertion updates)
- Delete: `web/src/styles/Settings.module.css`

**Prerequisite reading:**

- `docs/superpowers/specs/2026-04-09-shadcn-tailwind-rewrite-design.md` §3 Settings paragraph (line 311).
- `docs/superpowers/specs/2026-04-09-shadcn-tailwind-rewrite-design.md` §8 commit 11 bullet (line 755).
- `docs/superpowers/specs/2026-04-09-shadcn-tailwind-rewrite-design.md` §7.2 line 710 — the `Settings.test.tsx:419–421` cleanup note ("deferred to commit 12, not 11").
- `web/src/pages/Settings.tsx` — all 984 lines. Re-read carefully; this is a dense file.
- `web/src/pages/Settings.test.tsx` — all 747 lines. Every test's selector matters.
- `web/src/components/Tabs.tsx` — the custom 30-line Tabs wrapper being replaced. Understand its API (`tabs`, `activeKey`, `onTabChange`) and its `role="tablist"` + `role="tab"` + `aria-selected` output.
- `web/src/components/ui/tabs.tsx` — shadcn Tabs, installed in commit 2. `Tabs` + `TabsList` + `TabsTrigger` + `TabsContent` — the Radix API.
- `web/src/components/ui/dialog.tsx` — shadcn Dialog, installed in commit 2. `Dialog` + `DialogTrigger` + `DialogContent` + `DialogHeader` + `DialogTitle` + `DialogFooter`.
- `web/src/components/ui/form.tsx` + `web/src/components/ui/input.tsx` + `web/src/components/ui/select.tsx` + `web/src/components/ui/button.tsx` — the primitives every section will use.
- Chunk 3 / Task 6 (Dashboard rewrite) in this plan — for the precedent of driving shadcn `Select` in tests with `pointerEventsCheck: 0` and `getByRole('combobox', ...)`.

**Scope note on tests:** Settings.test.tsx has ~42 tests. This rewrite touches the **selectors**, not the behaviors under test. Every test still asserts the same semantic outcome (PUT to the same endpoint, Dialog appears, export opens the right URL, etc.); only the way we find the elements changes.

- [ ] **Step 1: Re-read `web/src/pages/Settings.tsx` top to bottom.**

Run: (no command — Read tool, full file)
Expected: internalize every section (GeneralSection, CurrenciesSection, SavingsSection, UsersSection, DataSection with three-step import wizard) and every `styles.*` reference, every native `<select>`, every `<form>`, every `<input>`.

- [ ] **Step 2: Re-read `web/src/pages/Settings.test.tsx` top to bottom.**

Run: (no command — Read tool, full file)
Expected: tabulate every test and its selector. Note which tests query `getByLabelText`, `getByRole('tab', ...)`, `getByRole('button', ...)`, `getByText(...)`, `getByRole('heading', ...)`. These are the assertions that must stay green after the rewrite. Note the `mockCategories: Category[]` at lines 419–421 — the `color:` key stays in place per spec §7.2 (commit 12 removes it).

- [ ] **Step 3: Rewrite `web/src/pages/Settings.test.tsx`.**

The rewrite keeps every existing test. Changes per test class:
- **Tab-selection tests** (`screen.getByRole('tab', { name: /general/i })`, etc.) stay **unchanged** — shadcn Tabs uses Radix, which also exposes `role="tab"` and `role="tablist"` on `TabsTrigger` and `TabsList`.
- **Label-text tests** (e.g. `screen.getByLabelText(/monthly budget/i)`) stay unchanged — shadcn `FormField` + `FormLabel` + `Input` produces a standard `<label htmlFor="…">` + `<input id="…">` pair, so `getByLabelText` still works for every `<Input>` and `<Textarea>`.
- **`user.selectOptions(...)` calls** are rewritten to the combobox-role interaction pattern: `await user.click(trigger); await user.click(option);`.
- **Every `userEvent.setup()` gains `{ pointerEventsCheck: 0 }`** so Radix primitives don't throw.
- **Dialog confirm step** gets new assertions: after clicking `Import`, a `Dialog` appears with a confirm button; the `api.post('import/confirm', ...)` call fires only after clicking that confirm button.
- **Fixture `color:` keys** on `mockCategories: Category[]` (lines 419–421) stay **in place**. Commit 12 removes them in the atomic DB-migration commit per spec §7.2.

The full rewritten test file is long; the concrete changes, test-by-test, are:

**Change 1 — the `userEvent.setup()` call site, every place it appears:**

```diff
-    const user = userEvent.setup();
+    const user = userEvent.setup({ pointerEventsCheck: 0 });
```

There are ~18 such call sites across the file. Replace all.

**Change 2 — `'changes user role via PUT (not PATCH)'` test:**

```diff
-      await user.click(screen.getByRole('tab', { name: /users/i }));
-
-      await waitFor(() => {
-        expect(screen.getByLabelText(/role for alice/i)).toBeInTheDocument();
-      });
-
-      await user.selectOptions(
-        screen.getByLabelText(/role for alice/i),
-        'member',
-      );
+      await user.click(screen.getByRole('tab', { name: /users/i }));
+
+      await waitFor(() => {
+        expect(
+          screen.getByRole('combobox', { name: /role for alice/i }),
+        ).toBeInTheDocument();
+      });
+
+      await user.click(
+        screen.getByRole('combobox', { name: /role for alice/i }),
+      );
+      await user.click(screen.getByRole('option', { name: /^member$/i }));
```

**Change 3 — the `'data tab has year input, month select, and export buttons'` test:**

The Month field is now a shadcn `Select`, so `getByLabelText(/month/i)` no longer finds it (the label is attached via `aria-labelledby`, which RTL's `getByLabelText` does not always resolve through shadcn `Select`'s trigger-as-button). Rewrite the assertion:

**Also note the semantic label change:** the current native `<select>` on the Data tab has `aria-label="Month"`. The rewrite changes this to `aria-label="Export Month"` to disambiguate from any other month select that may appear on the page later and to match the narrower regex below. The component side (Step 5, DataSection skeleton) must ship `aria-label="Export Month"` on the month `SelectTrigger`, and the test queries the narrower name.

```diff
-      await waitFor(() => {
-        expect(screen.getByLabelText(/year/i)).toBeInTheDocument();
-        expect(screen.getByLabelText(/month/i)).toBeInTheDocument();
-        expect(
-          screen.getByRole('button', { name: /export monthly/i }),
-        ).toBeInTheDocument();
-        expect(
-          screen.getByRole('button', { name: /export yearly/i }),
-        ).toBeInTheDocument();
-      });
+      await waitFor(() => {
+        expect(screen.getByLabelText(/year/i)).toBeInTheDocument();
+        expect(
+          screen.getByRole('combobox', { name: /export month/i }),
+        ).toBeInTheDocument();
+        expect(
+          screen.getByRole('button', { name: /export monthly/i }),
+        ).toBeInTheDocument();
+        expect(
+          screen.getByRole('button', { name: /export yearly/i }),
+        ).toBeInTheDocument();
+      });
```

The year `<input type="number">` stays a real input, so `getByLabelText(/year/i)` still works there.

**Change 4 — the import-wizard Dialog flow.**

Today's "click Import → api.post fires" flow becomes "click Import → Dialog opens → click Confirm → api.post fires". Tests that currently click `{ name: /^import$/i }` and immediately assert `api.post` must now click the Dialog's confirm button first. The test update is surgical:

```diff
       await waitFor(() => {
         expect(screen.getByRole('button', { name: /^import$/i })).toBeInTheDocument();
       });
 
       await user.click(screen.getByRole('button', { name: /^import$/i }));
 
+      // Confirmation dialog should appear
+      await waitFor(() => {
+        expect(
+          screen.getByRole('dialog', { name: /confirm import/i }),
+        ).toBeInTheDocument();
+      });
+      await user.click(
+        screen.getByRole('button', { name: /confirm and import/i }),
+      );
+
       await waitFor(() => {
         expect(screen.getByText(/4 imported/i)).toBeInTheDocument();
         expect(screen.getByText(/1 skipped/i)).toBeInTheDocument();
       });
```

Apply the same confirm-button insertion to every import-wizard test that reaches the post-click assertion: `'confirms import and shows result'`, `'shows "Import Another" button after successful import'`, `'shows error message on confirm failure'`, `'resets to upload step when "Import Another" is clicked'`, `'sends import_id when confirming import'`. Five tests total.

**Change 5 — the `'switches to currencies tab'` test.**

`mockedApi.get.mockImplementation((path: string) => { … })` already returns USD and EUR. The test asserts `screen.getByText('USD')` and `screen.getByText('EUR')`. These stay text nodes in the rewritten table (shadcn `Table` still uses `<td>` children), so the assertions are unchanged.

**Change 6 — the `'saves currency rates via PUT with full currency object'` test.**

Today it uses `form.submit` via `fireEvent.submit(form)`. The rewritten `<CurrenciesSection>` wraps everything in a react-hook-form `<Form>` whose submit handler fires on button click. Change to click the Save button:

```diff
-      const form = rateInput.closest('form')!;
-      fireEvent.submit(form);
+      await user.click(screen.getByRole('button', { name: /save rates/i }));
```

Same pattern for `'saves budget via PUT'`:

```diff
-      const form = input.closest('form')!;
-      fireEvent.submit(form);
+      await user.click(screen.getByRole('button', { name: /save budget/i }));
```

And `'adds savings goal via PUT to savings-goals/{year}'`:

```diff
-      const form = amountInput.closest('form')!;
-      fireEvent.submit(form);
+      await user.click(screen.getByRole('button', { name: /add goal/i }));
```

**Change 7 — `'shows import section with file input when data tab is active'` test's `getByLabelText(/excel file/i)` assertion.**

shadcn `Input` with `type="file"` still has a `<label>`+`<input id>` pair. No change.

**Change 8 — MemoryRouter wrapping.**

Already in place. No change.

**All other tests stay as-is.** The final rewritten test file is the current file with:
- All `userEvent.setup()` → `userEvent.setup({ pointerEventsCheck: 0 })`.
- The Role-for-alice test's `selectOptions` → click trigger + click option.
- The Month-select assertion on the data tab → `getByRole('combobox', { name: /export month/i })`.
- Five import-wizard tests → insert a Dialog confirm step.
- Three form-submission tests → click the save button instead of `fireEvent.submit(form)`.
- The `mockCategories: Category[]` at lines 419–421 keeps its `color:` keys (deferred to commit 12 per spec §7.2).

- [ ] **Step 4: Run Settings tests. Expect failures.**

Run: `cd web && npm test -- Settings.test.tsx`
Expected: many tests fail because the component still uses the custom `Tabs` and native selects and plain `<form>` tags. The failure set confirms the test rewrite is properly red.

- [ ] **Step 5: Rewrite `web/src/pages/Settings.tsx`.**

**On file size:** the target is ~850 lines in a single file, housing five section components (`GeneralSection`, `CurrenciesSection`, `SavingsSection`, `UsersSection`, `DataSection`) plus the top-level `Settings` wrapper. The current file is 984 lines in one file, and the test file (`Settings.test.tsx`) imports `Settings` as a single module, so splitting into `pages/settings/GeneralSection.tsx` etc. would require either new test imports or a barrel. **Keep the rewrite in a single file** to minimize the blast radius on the test file. If the rewritten file actually exceeds ~1000 lines once written, the implementer has discretion to split sections into `pages/settings/*.tsx` files and re-export them from `pages/Settings.tsx` — but only if the single-file rewrite genuinely becomes unwieldy. Do not split proactively.

The component rewrite is large but mechanical. Replace the entire file with the shadcn version. Structure:

```tsx
import { useState, useEffect, useCallback } from 'react';
import type { ChangeEvent } from 'react';
import { useSearchParams } from 'react-router-dom';
import { zodResolver } from '@hookform/resolvers/zod';
import { useForm } from 'react-hook-form';
import * as z from 'zod';
import { api } from '../api/client';
import { useAuth } from '../hooks/useAuth';
import type {
  Budget,
  Category,
  Currency,
  ImportPreview,
  ImportResult,
  SavingsGoal,
  User,
} from '../api/types';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import {
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
} from '@/components/ui/tabs';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import {
  Form,
  FormControl,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { Badge } from '@/components/ui/badge';
import { toast } from 'sonner';

type SettingsTab = 'general' | 'currencies' | 'savings' | 'users' | 'data';

/* ---------- General Tab ---------- */

const budgetSchema = z.object({
  amount: z.coerce.number().min(0, 'Must be ≥ 0'),
});
type BudgetValues = z.infer<typeof budgetSchema>;

function GeneralSection() {
  const now = new Date();
  const year = now.getFullYear();
  const month = now.getMonth() + 1;

  const form = useForm<BudgetValues>({
    resolver: zodResolver(budgetSchema),
    defaultValues: { amount: 0 },
  });

  useEffect(() => {
    api
      .get<Budget[]>(`budgets?year=${year}`)
      .then((data) => {
        const match = data.find((b) => b.month === month);
        if (match) form.reset({ amount: match.amount });
      })
      .catch(() => {
        /* non-critical */
      });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [year, month]);

  async function onSubmit(values: BudgetValues) {
    try {
      await api.put(`budgets/${year}/${month}`, { amount: values.amount });
      toast.success('Budget saved successfully');
    } catch (err) {
      toast.error(err instanceof Error ? err.message : 'Failed to save');
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>General Settings</CardTitle>
      </CardHeader>
      <CardContent>
        <Form {...form}>
          <form
            onSubmit={form.handleSubmit(onSubmit)}
            className="space-y-4 max-w-sm"
          >
            <FormField
              control={form.control}
              name="amount"
              render={({ field }) => (
                <FormItem>
                  <FormLabel htmlFor="monthly-budget">Monthly Budget</FormLabel>
                  <FormControl>
                    <Input
                      id="monthly-budget"
                      type="number"
                      step="0.01"
                      min="0"
                      placeholder="0.00"
                      {...field}
                      value={field.value ?? ''}
                    />
                  </FormControl>
                  <p className="text-muted-foreground text-xs">
                    Budget for {year}-{String(month).padStart(2, '0')}
                  </p>
                  <FormMessage />
                </FormItem>
              )}
            />
            <Button type="submit" disabled={form.formState.isSubmitting}>
              {form.formState.isSubmitting ? 'Saving...' : 'Save Budget'}
            </Button>
          </form>
        </Form>
      </CardContent>
    </Card>
  );
}

/* ---------- Currencies Tab ---------- */

function CurrenciesSection() { /* …see below… */ }

/* ---------- Savings Tab ---------- */

function SavingsSection() { /* …see below… */ }

/* ---------- Users Tab ---------- */

function UsersSection() { /* …see below… */ }

/* ---------- Data Tab ---------- */

function DataSection() { /* …see below… */ }

/* ---------- Main Settings Page ---------- */

export function Settings() {
  const { user } = useAuth();
  const isAdmin = user?.role === 'admin';
  const [searchParams] = useSearchParams();
  const tabParam = searchParams.get('tab');
  const validTabs: SettingsTab[] = [
    'general',
    'currencies',
    'savings',
    'users',
    'data',
  ];
  const initialTab = validTabs.includes(tabParam as SettingsTab)
    ? (tabParam as SettingsTab)
    : 'general';
  const [activeTab, setActiveTab] = useState<SettingsTab>(initialTab);

  return (
    <div className="space-y-6 p-6">
      <h1 className="text-2xl font-semibold tracking-tight">Settings</h1>

      <Tabs
        value={activeTab}
        onValueChange={(v) => setActiveTab(v as SettingsTab)}
        className="w-full"
      >
        <TabsList>
          <TabsTrigger value="general">General</TabsTrigger>
          <TabsTrigger value="currencies">Currencies</TabsTrigger>
          <TabsTrigger value="savings">Savings</TabsTrigger>
          {isAdmin && <TabsTrigger value="users">Users</TabsTrigger>}
          <TabsTrigger value="data">Import / Export</TabsTrigger>
        </TabsList>

        <TabsContent value="general" className="mt-6">
          <GeneralSection />
        </TabsContent>
        <TabsContent value="currencies" className="mt-6">
          <CurrenciesSection />
        </TabsContent>
        <TabsContent value="savings" className="mt-6">
          <SavingsSection />
        </TabsContent>
        {isAdmin && (
          <TabsContent value="users" className="mt-6">
            <UsersSection />
          </TabsContent>
        )}
        <TabsContent value="data" className="mt-6">
          <DataSection />
        </TabsContent>
      </Tabs>
    </div>
  );
}
```

**Sub-section bodies** (elided from the top-level snippet above for readability; the implementer writes each as a contiguous function in the same file):

**CurrenciesSection:**
- Uses shadcn `Table` for the currency list with rate `<Input type="number">` cells. Each rate input keeps the `aria-label={\`Rate for ${c.code}\`}` so the role-test stays green.
- A `<Button type="submit">Save Rates</Button>` outside the table. The submit handler is the same loop as today — save every changed rate.
- A separate `Form` for adding a new currency (`Code`, `Name`, `Symbol`, `Rate to Base`) using `FormField` + `Input`.

**SavingsSection:**
- Uses shadcn `Table` for the goal list. Each row has a `<Button variant="destructive" size="sm" aria-label={\`Delete ${g.year} goal\`}>Delete</Button>`.
- A `Form` for adding a new goal with `Year` and `Target Amount` inputs.

**UsersSection:**
- Uses shadcn `Table` for the user list. The role cell is a shadcn `Select` with `aria-label={\`Role for ${u.username}\`}` on the `SelectTrigger`. `onValueChange` calls the existing `handleRoleChange`.
- The add-user `Form` uses shadcn `FormField` + `Input` for username/password/display_name, plus a `Select` with `aria-label="New user role"` for role.

**DataSection** — this section's Dialog flow is novel behavior (not just a restyle), so the full code sample is given below. Every `aria-label` and button `name` here is relied upon by Settings.test.tsx assertions in Task 11 Step 3:

```tsx
function DataSection() {
  const now = new Date();
  const [year, setYear] = useState(now.getFullYear());
  const [month, setMonth] = useState(now.getMonth() + 1);

  // --- Import wizard state ---
  type ImportStep = 'upload' | 'preview' | 'done';
  const [importStep, setImportStep] = useState<ImportStep>('upload');
  const [preview, setPreview] = useState<ImportPreview | null>(null);
  const [result, setResult] = useState<ImportResult | null>(null);
  const [importError, setImportError] = useState<string | null>(null);
  const [defaultCategoryId, setDefaultCategoryId] = useState<number | null>(null);
  const [categories, setCategories] = useState<Category[]>([]);
  const [confirmOpen, setConfirmOpen] = useState(false);

  useEffect(() => {
    api
      .get<Category[]>('categories')
      .then(setCategories)
      .catch(() => {
        /* non-critical */
      });
  }, []);

  async function handleFileChange(e: ChangeEvent<HTMLInputElement>) {
    const file = e.target.files?.[0];
    if (!file) return;
    setImportError(null);
    try {
      const data = await api.upload<ImportPreview>('import/preview', file);
      setPreview(data);
      setImportStep('preview');
    } catch (err) {
      setImportError(err instanceof Error ? err.message : 'Upload failed');
    }
  }

  async function handleConfirmImport() {
    if (!preview) return;
    setImportError(null);
    try {
      const res = await api.post<ImportResult>('import/confirm', {
        import_id: preview.import_id,
        default_category_id: defaultCategoryId,
      });
      setResult(res);
      setImportStep('done');
      setConfirmOpen(false);
    } catch (err) {
      setImportError(err instanceof Error ? err.message : 'Import failed');
      setConfirmOpen(false);
    }
  }

  function handleExportMonthly() {
    window.open(`/api/export/monthly?year=${year}&month=${month}`);
  }

  function handleExportYearly() {
    window.open(`/api/export/yearly?year=${year}`);
  }

  const monthNames = [
    'January', 'February', 'March', 'April', 'May', 'June',
    'July', 'August', 'September', 'October', 'November', 'December',
  ];

  return (
    <div className="space-y-6">
      {/* ---- Export card ---- */}
      <Card>
        <CardHeader>
          <CardTitle>Export Data</CardTitle>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="grid gap-4 sm:grid-cols-2 max-w-md">
            <div className="space-y-2">
              <label htmlFor="export-year" className="text-sm font-medium">
                Year
              </label>
              <Input
                id="export-year"
                type="number"
                value={year}
                onChange={(e) => setYear(Number(e.target.value))}
              />
            </div>
            <div className="space-y-2">
              <label className="text-sm font-medium" id="export-month-label">
                Month
              </label>
              <Select
                value={String(month)}
                onValueChange={(v) => setMonth(Number(v))}
              >
                <SelectTrigger aria-label="Export Month">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {monthNames.map((m, i) => (
                    <SelectItem key={m} value={String(i + 1)}>
                      {m}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          </div>
          <div className="flex gap-2">
            <Button onClick={handleExportMonthly}>Export Monthly</Button>
            <Button variant="outline" onClick={handleExportYearly}>
              Export Yearly
            </Button>
          </div>
        </CardContent>
      </Card>

      {/* ---- Import card ---- */}
      <Card>
        <CardHeader>
          <CardTitle>Import Excel</CardTitle>
        </CardHeader>
        <CardContent className="space-y-4">
          {importError && (
            <div className="text-destructive text-sm" role="alert">
              {importError}
            </div>
          )}

          {importStep === 'upload' && (
            <div className="space-y-2 max-w-sm">
              <label htmlFor="excel-file" className="text-sm font-medium">
                Excel File
              </label>
              <Input
                id="excel-file"
                type="file"
                accept=".xlsx,.xls"
                onChange={handleFileChange}
              />
            </div>
          )}

          {importStep === 'preview' && preview && (
            <div className="space-y-4">
              <div className="text-sm">
                <p>
                  Preview: <strong>{preview.row_count}</strong> rows ready to
                  import.
                </p>
              </div>
              <div className="space-y-2 max-w-sm">
                <label className="text-sm font-medium">
                  Default Category (optional)
                </label>
                <Select
                  value={defaultCategoryId ? String(defaultCategoryId) : ''}
                  onValueChange={(v) => setDefaultCategoryId(Number(v))}
                >
                  <SelectTrigger aria-label="Default Category">
                    <SelectValue placeholder="— none —" />
                  </SelectTrigger>
                  <SelectContent>
                    {categories.map((c) => (
                      <SelectItem key={c.id} value={String(c.id)}>
                        {c.name}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
              <div className="flex gap-2">
                <Button onClick={() => setConfirmOpen(true)}>Import</Button>
                <Button
                  variant="outline"
                  onClick={() => {
                    setImportStep('upload');
                    setPreview(null);
                  }}
                >
                  Cancel
                </Button>
              </div>

              {/* Confirm dialog — controlled so we can close after api success */}
              <Dialog open={confirmOpen} onOpenChange={setConfirmOpen}>
                <DialogContent aria-label="Confirm Import">
                  <DialogHeader>
                    <DialogTitle>Confirm Import</DialogTitle>
                    <DialogDescription>
                      Import {preview.row_count} transactions
                      {defaultCategoryId
                        ? ` with default category "${
                            categories.find((c) => c.id === defaultCategoryId)
                              ?.name ?? ''
                          }"`
                        : ''}
                      ? This cannot be undone.
                    </DialogDescription>
                  </DialogHeader>
                  <DialogFooter>
                    <Button
                      variant="outline"
                      onClick={() => setConfirmOpen(false)}
                    >
                      Cancel
                    </Button>
                    <Button onClick={handleConfirmImport}>
                      Confirm and Import
                    </Button>
                  </DialogFooter>
                </DialogContent>
              </Dialog>
            </div>
          )}

          {importStep === 'done' && result && (
            <div className="space-y-4">
              <p className="text-sm">
                <strong>{result.imported}</strong> imported,{' '}
                <strong>{result.skipped}</strong> skipped.
              </p>
              <Button
                onClick={() => {
                  setImportStep('upload');
                  setPreview(null);
                  setResult(null);
                  setDefaultCategoryId(null);
                }}
              >
                Import Another
              </Button>
            </div>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
```

**Why this much code:** five tests in Task 11 Step 3 assert the exact flow: click `Import` → `Dialog` appears with `name: /confirm import/i` → click `Confirm and Import` → api.post fires → result text appears. If any of those strings drift (e.g. the DialogContent's `aria-label`, or the confirm button's text), the tests fail silently on selector mismatches. The explicit sample pins the strings.

**Key points:**
- The Dialog is **controlled** (`open={confirmOpen} onOpenChange={setConfirmOpen}`) not uncontrolled. This is because `handleConfirmImport` must close the dialog only after the api call resolves, and on error we still close it (the error renders in the outer error alert).
- `<DialogContent aria-label="Confirm Import">` gives the dialog its accessible name. RTL's `getByRole('dialog', { name: /confirm import/i })` queries this.
- The confirm button's exact text is `Confirm and Import` (matching the test regex `/confirm and import/i`).
- The `Import` button is NOT a `DialogTrigger` — it's a plain `<Button onClick={() => setConfirmOpen(true)}>`. This is intentional: `DialogTrigger asChild` would open the dialog immediately on click without letting us run any pre-open guard, and a controlled dialog is easier to assert on in tests.
- Export stays plain: no Dialog, just buttons. Decision #10 documents why.

**Section component skeletons** — the full content is ~400 lines. The implementer translates each section one-to-one from the current file, replacing:
- `<form onSubmit={...}>` → `<Form {...form}><form onSubmit={form.handleSubmit(...)}>` wrapped in `FormField` + `FormItem` + `FormLabel` + `FormControl`.
- `<input>` → `<Input>` from `@/components/ui/input`.
- `<select>` → shadcn `Select` + `SelectTrigger` + `SelectContent` + `SelectItem`, with `aria-label` on the trigger matching today's native-select `aria-label`.
- `<button type="submit">` → `<Button type="submit">`.
- `<button type="button" className="delete">` → `<Button variant="destructive" size="sm">`.
- `styles.card` → rendered via `<Card>` + `<CardContent>`.
- `styles.sectionTitle` → `<CardTitle>`.
- `styles.table` → shadcn `Table` + `TableHeader` + `TableBody` + `TableRow` + `TableHead` + `TableCell`.
- `<span className={styles.baseBadge}>Base</span>` → `<Badge variant="secondary">Base</Badge>`.
- All `className={styles.xxx}` → Tailwind utilities or dropped (shadcn primitives style themselves).
- Success/error `<div className={styles.success}>...</div>` → `toast.success(...)` / `toast.error(...)`.

**One thing to watch carefully:** the current `GeneralSection` shows an inline success message (`<div className={styles.success}>{message}</div>`). The rewrite uses `toast.success`, which means the old test `'saves budget via PUT to budgets/{year}/{month}'` — which only asserts the PUT call fires — still passes. The test does not assert the success message text, so moving it to a toast is safe. Confirm by reading the test at line 252–277.

- [ ] **Step 6: Delete `web/src/styles/Settings.module.css`.**

Run: `rm web/src/styles/Settings.module.css`
Expected: file is gone. The rewritten `Settings.tsx` does not import it.

- [ ] **Step 7: Grep for any lingering references.**

Run: `cd web && grep -rn "Settings.module.css\|from '../components/Tabs'" src/pages/Settings.tsx`
Expected: zero matches. `../components/Tabs` must not appear in the rewritten file — it has been replaced by `@/components/ui/tabs`.

- [ ] **Step 8: Run the Settings test suite.**

Run: `cd web && npm test -- Settings.test.tsx`
Expected: all ~42 tests pass. If a test fails, diagnose the selector mismatch (usually: shadcn Select missing `aria-label` on the trigger, or Dialog confirm button has a different name than the test expects). Fix in the component, not the test.

- [ ] **Step 9: Run the full test suite + typecheck.**

Run: `cd web && npm test && npx tsc --noEmit`
Expected: every test passes, `tsc --noEmit` exits 0. After this commit, `components/Tabs.tsx`, `components/ChartTooltip.tsx`, and `hooks/useChartTheme.ts` all have zero importers — they are orphans on disk until Chunk 8 / Task 13 deletes them. `tsc --noEmit` does not flag orphaned files, so no errors are expected from them.

- [ ] **Step 10: Commit.**

Run:
```bash
cd d:/claude/SpenDrop
git add web/src/pages/Settings.tsx web/src/pages/Settings.test.tsx
git rm web/src/styles/Settings.module.css
git commit -m "feat(web): rewrite Settings page with shadcn Tabs + Form + Dialog

- shadcn Tabs replaces custom components/Tabs (orphaned, swept in commit 13)
- Every section uses Form + FormField + Input/Select + Button
- Import wizard confirms via shadcn Dialog
- Every native <select> → shadcn Select (combobox role)
- Delete Settings.module.css
- Rewrite Settings.test.tsx for combobox-role + Dialog interactions

Note: mockCategories[].color in Settings.test.tsx stays in place;
spec §7.2 removes it in commit 12 (atomic DB migration).

Refs: spec §3 Settings, §8 commit 11"
```

**After this commit the state of the repo is:**

- All page files (`pages/Auth.tsx`, `pages/Dashboard.tsx`, `pages/Categories.tsx`, `pages/Transactions.tsx`, `pages/Reports.tsx`, `pages/Settings.tsx`) are on Tailwind + shadcn.
- Only five legacy files remain in `web/src/`: `components/Tabs.tsx` + `styles/Tabs.module.css` (orphaned); `components/ChartTooltip.tsx` + `styles/ChartTooltip.module.css` (orphaned); `hooks/useChartTheme.ts` (orphaned); `components/TagInput.tsx` + `styles/Transactions.module.css` (still referenced by `TagInput.tsx`).
- Preflight still disabled.
- `Category.color` field still exists on the TypeScript type; no page reads it anymore.
- Every remaining loose end is swept by Chunk 8 commits 12 (DB migration + type cleanup) and 13 (delete legacy wrappers + re-enable preflight).

---

## Chunk 8: DB migration + final cleanup (commits 12-13)

**Scope.** Two commits finish the migration:

- **Commit 12 (Task 12)** — atomic DB migration. Drop the `categories.color` column in SQLite, regenerate sqlc code, strip `Color` from every Go handler + Go test, delete four `color` fields from `web/src/api/types.ts`, and sweep stale `color: '#...'` entries out of every frontend test fixture in a single commit. Spec §7 enumerates every file; this task is the mechanical execution of that enumeration. Success condition: `go test -race ./...` passes, `npx tsc -b` passes, `npx vitest run` passes, and the pre-flight grep set (spec §8 commit 12) returns zero relevant hits.
- **Commit 13 (Task 13)** — final cleanup. Rewrite `components/TagInput.tsx` to use Tailwind classes so its dependency on `styles/Transactions.module.css` dies, delete the six orphan files (`Tabs.tsx` + test + `Tabs.module.css`, `ChartTooltip.tsx` + test + `ChartTooltip.module.css`, `useChartTheme.ts` + test) and also `useChartPatterns.tsx` (orphaned by the Dashboard rewrite in Chunk 4), delete the last three stylesheets (`Transactions.module.css`, `tokens.css`, `global.css`), remove `@fontsource-variable/inter` from `package.json`, **flip `corePlugins.preflight` to `true`** in `tailwind.config.ts`, do a full visual sweep of every page, and add `npx eslint .` as a required CI step in `.github/workflows/pr.yml`. This is the first commit in the whole migration where Tailwind preflight is active — spec §9 flags that as the single riskiest behavioral moment, so the visual sweep is load-bearing.

**Deliberate decisions:**

1. **Commit 12 is atomic.** Spec §7.3 is emphatic: every file listed in §7.2 must land in the same commit because partial commits break CI (sqlc codegen, Go build, `tsc --noEmit`, vitest all lose in different places). Do not split this across two commits "for readability." The commit is large (~30 files) but the edits are mostly 1–3 lines per file. Task 12 below walks through the files in a deterministic order so the implementer can make forward progress without thrashing.
2. **Most `color` references in pages/components are already gone by the time commit 12 runs.** Spec §7.2 was written against the *current* (pre-migration) state of the codebase. Since the rewrite sequence (commits 4–11) already routes every category color through `getCategoryColorVar()` and rewrites every page test, the line-number references in §7.2 to `Dashboard.tsx:547/555/668/669`, `Categories.tsx:24/50/108-109/283`, `Reports.tsx:219`, `FilterPanel.tsx:157`, `TransactionRow.tsx:144`, and `useChartPatterns.tsx` are **vestigial** — those files have been rewritten or deleted. The files that commit 12 actually still has to touch are: `internal/**`, `web/src/api/types.ts`, and the test fixtures that weren't already cleaned by their page's rewrite commit. Task 12 enumerates the surviving work explicitly.
3. **`Settings.test.tsx:419-421` is the canonical example of a deferred fixture.** Chunk 7 Decision #7 explicitly left those three `color: '#...'` keys in place with a comment, because the `Category` TS interface still carried the `color` field at that time. Commit 12 removes the field and deletes the keys together. The other fixtures (`Dashboard.test.tsx`, `Transactions.test.tsx`, `Categories.test.tsx`, `Reports.test.tsx`, `useDashboard.test.ts`, `App.test.tsx`) follow the same pattern: page rewrites left the fixture entries in so type-checking kept passing, and commit 12 is where they get swept.
4. **`sqlc generate` is a tool run, not a manual edit.** `internal/database/queries.sql.go` and (possibly) `internal/database/models.go` regenerate from `queries.sql` when `sqlc generate` runs. The implementer must not hand-edit the generated Go files; they just run the tool after editing `queries.sql` and commit the diff. Spec §7.2 is clear on this.
5. **Migration strategy — Option A: leave `001` alone, let `004` drop the column unconditionally.** SQLite migrations execute in filename order, so a fresh install runs `001` (creates `categories` with `color`, seeds 19 rows with color values), then `002`/`003` (if any), then `004` (`ALTER TABLE categories DROP COLUMN color;`). Because the column definitely exists when `004` runs (created by `001` two steps earlier), the drop is always valid — no conditional guard needed, no `PRAGMA table_info` probe, no runner change. Existing databases that already ran `001` with the old seed also have the column, so `004` is valid there too. Both paths converge on the same final schema: `categories` table without `color`, 19 seeded rows. This contradicts spec §7.1's literal reading (which implied rewriting `001` to strip `color` from the column def + seed), but produces the same final state with strictly simpler migration runner semantics — SQLite does not support `DROP COLUMN IF EXISTS`, so any other approach requires a Go-side guard. **Task 12 Step 1 below commits to Option A with a full fresh-install walkthrough; it is authoritative and non-negotiable for this commit.**
6. **The final cleanup (commit 13) is where Tailwind preflight flips on.** Preflight resets default browser styles — `<button>` background, list markers, form element borders, `<h1>` font sizes, etc. — so enabling it on a page that still relied on default browser styling would cause regressions. The migration held preflight off for commits 1–12 precisely so that CSS Modules and Tailwind utilities could coexist without fighting each other. Commit 13 turns it on after the last CSS Module is deleted. The visual sweep in Task 13 Step 14 is not optional: it's the only defense against a regression that type/unit tests don't catch. Spec §1.2 and §9 both flag this as the highest-risk moment.
7. **`TagInput.tsx` must be rewritten, not deleted.** The component is still imported by the Transactions page's filter chips section (Chunk 6 kept the import and just wrapped `TagInput` inside new Tailwind parent containers). `TagInput.tsx` itself still pulls CSS classes from `styles/Transactions.module.css`. Before that stylesheet can be deleted, `TagInput.tsx` must stop importing it — which means converting its four class names (`tagInputWrapper`, `tagPill`, `tagRemove`, `tagInput`) to Tailwind utility classes. Task 13 Step 1 below is the full rewrite with exact Tailwind classes.
8. **`@fontsource-variable/inter` is the old font package.** Chunk 1 installed `@fontsource-variable/geist` and `@fontsource-variable/geist-mono` and changed `main.tsx` to import them. The Inter import was left in `package.json` (and possibly in `main.tsx`) to keep a fallback during the migration. Commit 13 removes it: delete the `import '@fontsource-variable/inter'` line from `main.tsx` (if it still exists) and `npm uninstall @fontsource-variable/inter`. Since Tailwind's `fontFamily.sans` stack lists Geist first, Inter is already unused by the time commit 13 runs; removing it is purely hygiene.
9. **`vi.mock('./hooks/useChartTheme')` in `App.test.tsx` must be removed before the module is deleted.** Vitest resolves `vi.mock(path)` paths via the Vite resolver at test-runtime. When Task 13 Step 3 deletes `hooks/useChartTheme.ts`, the mock block in `App.test.tsx` at lines 68–82 (and the matching `vi.mock('./components/ChartTooltip')` at lines 83–86) resolves to a non-existent module and vitest throws. Task 13 Step 2 below removes those mock blocks **before** deleting the modules, so the cleanup is ordered correctly.
10. **ESLint CI step lands last.** Chunks 1–12 already have `eslint-plugin-tailwindcss` configured and lint-clean locally, but `.github/workflows/pr.yml` doesn't invoke `npx eslint .` yet. Adding the step in commit 13 is the moment it becomes a hard CI gate — any future PR that introduces an out-of-order class or an unknown utility fails. Spec §8 commit 13 point 3 explicitly prescribes this.

**Files touched:**

| File | Commit | Action |
|---|---|---|
| `internal/database/migrations/004_drop_categories_color.sql` | 12 | Create |
| `internal/database/migrations/001_initial_schema.sql` | 12 | Modify (line 28 + seed lines 98–117) |
| `internal/database/queries.sql` | 12 | Modify (lines 48, 63, 172, 216) |
| `internal/database/queries.sql.go` | 12 | Regenerate via `sqlc generate` |
| `internal/database/models.go` | 12 | Regenerate via `sqlc generate` (if present) |
| `internal/api/category_handlers.go` | 12 | Modify (delete hex regex, request fields, validation, params) |
| `internal/api/transaction_handlers.go` | 12 | Modify (delete response field, SELECT col, scan var, scan arg, assignment) |
| `internal/api/dashboard_handlers.go` | 12 | Modify (delete struct field + assignment) |
| `internal/api/reports_handlers.go` | 12 | Modify (delete struct field + assignment) |
| `internal/api/category_handlers_test.go` | 12 | Modify (drop color JSON keys + delete resp["color"] assertion) |
| `internal/api/transaction_handlers_test.go` | 12 | Modify (drop color param from seedTestCategory, delete category_color assertion) |
| `web/src/api/types.ts` | 12 | Modify (4 field deletions) |
| `web/src/pages/Settings.test.tsx` | 12 | Modify (lines 419, 420, 421) |
| `web/src/pages/Dashboard.test.tsx` | 12 | Modify (lines 51, 52, 70) |
| `web/src/pages/Transactions.test.tsx` | 12 | Modify (line 34) |
| `web/src/pages/Categories.test.tsx` | 12 | Modify (lines 31, 41, 51) |
| `web/src/pages/Reports.test.tsx` | 12 | Modify (line 51 + any other) |
| `web/src/hooks/useDashboard.test.ts` | 12 | Modify (lines 34, 35) |
| `web/src/App.test.tsx` | 12 | Modify (sweep any fixture color fields; leave `vi.mock` blocks alone — commit 13 handles those) |
| `web/src/components/TransactionRow.test.tsx` | 12 | Modify (line 12 + line 32). Spec §8 commit 8 says Chunk 6 **rewrites** (not deletes) this file, so it exists when commit 12 runs. If the file is missing, a prior chunk violated the spec — investigate. |
| `web/src/components/TagInput.tsx` | 13 | Rewrite (Tailwind classes, drop `Transactions.module.css` import) |
| `web/src/App.test.tsx` | 13 | Modify (delete `vi.mock('./hooks/useChartTheme')` + `vi.mock('./components/ChartTooltip')` blocks) |
| `web/src/components/Tabs.tsx` | 13 | Delete |
| `web/src/components/Tabs.test.tsx` | 13 | Delete |
| `web/src/components/ChartTooltip.tsx` | 13 | Delete |
| `web/src/components/ChartTooltip.test.tsx` | 13 | Delete |
| `web/src/hooks/useChartTheme.ts` | 13 | Delete |
| `web/src/hooks/useChartTheme.test.ts` | 13 | Delete |
| `web/src/hooks/useChartPatterns.tsx` | 13 | Delete |
| `web/src/hooks/useChartPatterns.test.tsx` | 13 | Delete (if it exists) |
| `web/src/hooks/useTheme.tsx` | 13 | Delete |
| `web/src/hooks/useTheme.test.tsx` | 13 | Delete |
| `web/src/styles/Tabs.module.css` | 13 | Delete |
| `web/src/styles/ChartTooltip.module.css` | 13 | Delete |
| `web/src/styles/Transactions.module.css` | 13 | Delete |
| `web/src/styles/tokens.css` | 13 | Delete |
| `web/src/styles/global.css` | 13 | Delete |
| `web/.stylelintrc.json` | 13 | Delete |
| `web/src/main.tsx` | 13 | Modify (remove Inter import, remove `global.css` import) |
| `web/package.json` | 13 | Modify (remove `@fontsource-variable/inter`, stylelint deps, `lint:css` script, prune `lint` script) |
| `web/package-lock.json` | 13 | Regenerate via `npm install` |
| `web/tailwind.config.ts` | 13 | Modify (flip `corePlugins.preflight` to `true`) |
| `.github/workflows/pr.yml` | 13 | Modify (add `npx eslint .` step after test step in frontend job) |

---

### Task 12: Atomic DB migration — drop `categories.color`

**Goal:** Land every SQL, Go, and TypeScript change that removes `categories.color` in a single commit. CI must pass: `go test -race ./...`, `sqlc generate` diff matches, `npx tsc -b`, `npx vitest run`, and the pre-flight grep set returns zero relevant hits.

**Scene:** This is the penultimate commit of the migration. After commits 4–11, every page sources category colors from `getCategoryColorVar()` (which hashes `category.id` into one of 11 Radix palette slots), and no page renders `cat.color` or `tx.category_color` anymore. But the field still exists on the TypeScript `Category` interface, the SQL `categories` table still has the column, every Go handler still populates it, and some test fixtures still carry `color: '#...'` entries for type-compatibility. This commit removes all of it in one shot.

**Files:**
- Create: `internal/database/migrations/004_drop_categories_color.sql`
- Modify: `internal/database/migrations/001_initial_schema.sql`
- Modify: `internal/database/queries.sql`
- Regenerate: `internal/database/queries.sql.go` (+ `models.go` if present)
- Modify: `internal/api/category_handlers.go`, `transaction_handlers.go`, `dashboard_handlers.go`, `reports_handlers.go`
- Modify: `internal/api/category_handlers_test.go`, `transaction_handlers_test.go`
- Modify: `web/src/api/types.ts`
- Modify: `web/src/pages/Settings.test.tsx`, `Dashboard.test.tsx`, `Transactions.test.tsx`, `Categories.test.tsx`, `Reports.test.tsx`
- Modify: `web/src/hooks/useDashboard.test.ts`
- Modify: `web/src/App.test.tsx`
- Modify (if still present): `web/src/components/TransactionRow.test.tsx`

- [ ] **Step 1: Confirm the migration strategy — leave `001` unchanged, let `004` drop the column unconditionally.**

**Decision (final, non-negotiable for this commit): Option A — `001_initial_schema.sql` stays exactly as it is today. Do not touch its `color TEXT NOT NULL DEFAULT '#888888'` column definition at line 28. Do not touch the seed `INSERT INTO categories (name, type, color, sort_order) VALUES ...` at lines 98–117. The new migration `004_drop_categories_color.sql` runs `ALTER TABLE categories DROP COLUMN color;` unconditionally on both fresh and existing databases.**

**Why this works on a fresh DB (new install with empty database):**

The migration runner applies files in filename order. The sequence is:

1. `001_initial_schema.sql` creates the `categories` table **with** the `color` column.
2. `001`'s seed `INSERT INTO categories (name, type, color, sort_order)` populates 19 rows with their seeded color values. This succeeds because the column exists at this moment.
3. `002`, `003` (if any) run.
4. `004_drop_categories_color.sql` runs `ALTER TABLE categories DROP COLUMN color;`. This succeeds because the column was created in step 1.

Final state of a fresh database: `categories` table without `color`, 19 seeded rows. Identical to an existing database that already ran `001`–`003` under the old schema and then ran `004`. Both paths converge on the same final schema.

**Why this contradicts spec §7.1 line 644 and why that's OK:**

Spec §7.1 says "both shapes must stop referencing `color` in the same commit." The spec's reasoning is that a partially migrated DB (with `001`'s new column-less shape but `001`'s old seed INSERT still mentioning `color`) would fail to initialize. This is a real problem **only** if you change `001`'s column def and seed separately, or if you strip `color` from `001`'s column def without also stripping it from `001`'s seed. Option A avoids the problem entirely by leaving `001` alone: the column and the seed stay consistent with each other, and `004` is the single source of truth for the column drop.

Spec §7.1 considered a world where `001` gets rewritten. That world requires either (a) a conditional guard in `004` that SQLite does not support (`ALTER TABLE DROP COLUMN IF EXISTS`), or (b) a Go-side inspection via `PRAGMA table_info(categories)` before running `004`, which would complicate the migration runner. Option A is strictly simpler and produces the same final state. **Proceed with Option A.**

**What this means in practice for this commit:**

- `internal/database/migrations/001_initial_schema.sql` is **NOT** in this commit's file list. Do not open it, do not stage it.
- `internal/database/migrations/004_drop_categories_color.sql` is the only SQL file created.
- `internal/database/queries.sql` still needs the four edits in Step 3 — those are **DML** (query definitions), not DDL. The `queries.sql` edits remove `color` from SELECT projections and INSERT/UPDATE column lists so that sqlc-generated code stops reading/writing it. Those edits are compatible with the final schema (post-`004`) but would break against the pre-`004` schema if anyone ran the old handlers against it — which is fine because commit 12 is atomic: the handlers, sqlc code, and migration all land in the same commit and run together on CI.

No grep needed in this step. Proceed to Step 2.

- [ ] **Step 2: Create the new migration file.**

Create `internal/database/migrations/004_drop_categories_color.sql`:

```sql
-- 004_drop_categories_color.sql
-- Drop categories.color column. Category colors are now derived from
-- the chart palette slot (((category.id - 1) % 11) + 1) at render time.
-- See web/src/lib/chart-colors.ts and spec §2.3 / §7.
--
-- SQLite's ALTER TABLE DROP COLUMN was added in SQLite 3.35 (March 2021).
-- Confirm the bundled sqlite version supports it before running this in prod.
ALTER TABLE categories DROP COLUMN color;
```

Do **not** modify `001_initial_schema.sql` — per Step 1's Option A, `001` stays as-is. (If the reviewer insists that spec §7.1 requires stripping `001` too, see Step 1's justification for why Option A keeps fresh installs working.)

- [ ] **Step 3: Update `internal/database/queries.sql` — four edits.**

Each edit is a literal string replacement. Open `internal/database/queries.sql`:

**Edit 1 — line ~48, CreateCategory query:**

Replace:
```sql
INSERT INTO categories (name, type, color, sort_order)
VALUES (?, ?, ?, ?)
RETURNING *;
```

With:
```sql
INSERT INTO categories (name, type, sort_order)
VALUES (?, ?, ?)
RETURNING *;
```

**Edit 2 — line ~63, UpdateCategory query:**

Replace:
```sql
UPDATE categories
SET name = ?, color = ?, icon = ?, sort_order = ?, is_active = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ?
RETURNING *;
```

With:
```sql
UPDATE categories
SET name = ?, icon = ?, sort_order = ?, is_active = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ?
RETURNING *;
```

(Preserve the exact field ordering the existing query uses after `name =` — delete only the `color = ?,` segment. If the existing query differs in column order from the example above, preserve its order and delete only `color = ?,`.)

**Edit 3 — line ~172, SumByCategoryForMonth query:**

Replace:
```sql
SELECT c.id, c.name, c.color, CAST(COALESCE(SUM(t.amount_cents), 0) AS INTEGER) AS total, ...
```

With:
```sql
SELECT c.id, c.name, CAST(COALESCE(SUM(t.amount_cents), 0) AS INTEGER) AS total, ...
```

Delete only the `c.color,` fragment — preserve everything else on the line.

**Edit 4 — line ~216, SumByCategoryForRange query:**

Same treatment: delete the `c.color,` fragment from the SELECT projection.

- [ ] **Step 4: Regenerate sqlc code.**

Run: `sqlc generate` (from the project root — `sqlc.yaml` lives in `internal/database/` or the repo root, depending on the project layout).

Expected: `internal/database/queries.sql.go` regenerates. The `Category` struct loses its `Color string`/`Color sql.NullString` field. `CreateCategoryParams` and `UpdateCategoryParams` lose their `Color` fields. `SumByCategoryForMonthRow` and `SumByCategoryForRangeRow` lose their `Color` fields. Every function that builds args from a struct (`q.CreateCategory`, `q.UpdateCategory`) regenerates to stop passing the column.

If `sqlc generate` errors out, the SQL is malformed — the most likely cause is a lingering `c.color` somewhere else in `queries.sql`. Grep `queries.sql` for `color` to confirm zero hits.

Expected grep: `grep -n "color" internal/database/queries.sql` returns nothing.

- [ ] **Step 5: Verify the sqlc diff matches expectations.**

Run: `git diff internal/database/queries.sql.go`

Expected in the diff: `-Color string` or `-Color sql.NullString` on the `Category` struct, and `-Color:` on every scan-destination inside generated `Query*` / `Exec*` methods. No unrelated changes.

If `internal/database/models.go` exists (some sqlc versions emit the model file separately), the `Category` struct lives there instead. Same inspection applies to that file's diff.

- [ ] **Step 6: Update `internal/api/category_handlers.go`.**

Open `internal/api/category_handlers.go` and make the following edits. The current state (pre-migration) has:

```go
var hexColorRegexp = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)

func isValidHexColor(s string) bool {
	return hexColorRegexp.MatchString(s)
}

type categoryCreateRequest struct {
	Name      string `json:"name"`
	Type      string `json:"type"`
	Color     string `json:"color"`
	Icon      string `json:"icon"`
	SortOrder int64  `json:"sort_order"`
}

type categoryUpdateRequest struct {
	Name      string `json:"name"`
	Color     string `json:"color"`
	Icon      string `json:"icon"`
	SortOrder int64  `json:"sort_order"`
	IsActive  bool   `json:"is_active"`
}
```

**Delete:**
1. The `hexColorRegexp` var and `isValidHexColor` function (top of file, lines 14–18).
2. The `Color string \`json:"color"\`` field from `categoryCreateRequest`.
3. The `Color string \`json:"color"\`` field from `categoryUpdateRequest`.
4. Both `if req.Color != "" && !isValidHexColor(req.Color) { ... }` validation blocks inside the create and update handlers (spec §7.2 lines 96–99 and 153–156).
5. `Color: req.Color,` inside both `database.CreateCategoryParams{...}` and `database.UpdateCategoryParams{...}` literal constructions.
6. Remove "color" from the function doc comment at line ~119 (something like `// handleUpdateCategory updates an existing category's name, color, and icon.` becomes `// handleUpdateCategory updates an existing category's name and icon.`).

Also delete the `regexp` import at the top of the file if `hexColorRegexp` was the only consumer.

- [ ] **Step 7: Update `internal/api/transaction_handlers.go`.**

Three edits:

1. **Response struct (line ~47):** Delete the `CategoryColor string \`json:"category_color,omitempty"\`` field from `transactionResponse`.

2. **List query (line ~180):** The raw SQL string inside the list handler projects category join columns. Replace:

```go
query := `SELECT t.id, t.occurred_on, t.amount_cents, t.description, t.tags, t.category_id,
          c.name AS category_name, c.type AS category_type, c.color AS category_color
          FROM transactions t
          JOIN categories c ON t.category_id = c.id
          WHERE ...`
```

With:

```go
query := `SELECT t.id, t.occurred_on, t.amount_cents, t.description, t.tags, t.category_id,
          c.name AS category_name, c.type AS category_type
          FROM transactions t
          JOIN categories c ON t.category_id = c.id
          WHERE ...`
```

(Preserve the WHERE clause exactly — only delete the `, c.color AS category_color` projection.)

3. **Scan loop (lines ~208, 213, 223):** Delete the local `var categoryColor string` declaration at the top of the scan loop, delete the `&categoryColor` scan argument in the `rows.Scan(...)` call, and delete the `tr.CategoryColor = categoryColor` assignment at the bottom. The scan arg list must match the new SELECT exactly — count the columns in the SELECT and verify the scan arg list length matches.

- [ ] **Step 8: Update `internal/api/dashboard_handlers.go` and `internal/api/reports_handlers.go`.**

**`dashboard_handlers.go`:**
1. Delete the `Color string \`json:"color"\`` field from the category-breakdown response struct (line ~42).
2. Delete the `Color: row.Color,` assignment inside the row mapper (line ~294).

**`reports_handlers.go`:**
1. Delete the `Color string \`json:"color"\`` field from the category-trend response struct (line ~96).
2. Delete the `Color: row.Color,` assignment in the row mapper (line ~148).

Both handlers lose one field and one assignment each.

- [ ] **Step 9: Update Go tests.**

**`internal/api/category_handlers_test.go`:**

Spec §7.2 enumerates the line numbers: 101, 130, 147, 164, 200, 217, 234, 251 are JSON request bodies with `"color":"#..."` keys. Remove the key from each body. If a body becomes `{"color":"#ff0000"}` with nothing else, delete the body entirely but keep the request (the endpoint should accept an empty JSON body if the test is asserting a validation case). More likely: the body has other keys and only the `"color"` key needs to go.

Also delete the `resp["color"]` `if` block at lines 120–121 that asserts the response carries a color.

**`internal/api/transaction_handlers_test.go`:**

1. **`seedTestCategory` helper (lines 84–89):** Drop the `color string` parameter and the `Color: color,` field inside the struct literal. The helper signature becomes:

```go
func seedTestCategory(t *testing.T, db *sql.DB, name, categoryType string, sortOrder int64) int64 {
    // ...
    params := database.CreateCategoryParams{
        Name:      name,
        Type:      categoryType,
        SortOrder: sortOrder,
    }
    // ...
}
```

2. **Every call site in the file:** Remove the color argument from every `seedTestCategory(t, db, "Food", "expense", "#5347CE", 1)` call. Grep inside the file: `grep -n "seedTestCategory" internal/api/transaction_handlers_test.go` to find all of them.

3. **`TestHandleListTransactions_IncludesCategoryInfo` (lines 619–620):** Delete the `if txn["category_color"] != "#5347CE" { t.Errorf(...) }` assertion block entirely.

- [ ] **Step 10: Run Go tests.**

Run: `go test -race ./...`

Expected: all packages pass. If any test fails because it still references the deleted `Color` field or column, fix the test and re-run. Common failures:
- A test that hand-builds a `database.Category{Color: "#ff0000"}` literal — delete the `Color` field.
- A test that asserts a response has `category_color` — delete the assertion.
- A test that hand-builds a `CreateCategoryParams{Color: "..."}` literal — delete the `Color` field.

Grep `internal/` for `Color` once more to catch any survivor: `grep -rn "\.Color\b\|Color:" internal/ | grep -v "bgColor\|textColor"` — expect zero relevant hits.

- [ ] **Step 11: Delete four fields from `web/src/api/types.ts`.**

Open `web/src/api/types.ts` and delete:

1. **Line 20 (Transaction):** `category_color: string;`
2. **Line 31 (Category):** `color: string;`
3. **Line 85 (CategoryBreakdownItem):** `color: string;`
4. **Line 147 (CategoryTrendEntry):** `color: string;`

After these deletions, the `Transaction`, `Category`, `CategoryBreakdownItem`, and `CategoryTrendEntry` interfaces have one less field each. No other type changes.

- [ ] **Step 12: Run `tsc -b` to surface every TS2353 break.**

Run: `cd web && npx tsc -b`

Expected: a **set** of TS2353 "Object literal may only specify known properties" errors — one per surviving fixture that still has a `color: '#...'` entry on a typed `Category`, `CategoryBreakdownItem`, `CategoryTrendEntry`, or `Transaction` object. Spec §7.2 enumerates the expected error sites:

- `web/src/pages/Settings.test.tsx` lines 419, 420, 421
- `web/src/components/TransactionRow.test.tsx` lines 12, 32 (only if TransactionRow.test.tsx still exists — commit 8 may have replaced it)

**Untyped fixture files** (objects without explicit `Category[]` annotation) won't throw TS2353 — they silently carry the stale field. These are cleaned up in Step 14 below via grep, not via tsc errors.

Write down the list of TS2353 errors tsc emitted — Step 13 walks through fixing each one.

- [ ] **Step 13: Fix every TS2353 error tsc reported.**

For each error, open the file at the reported line and delete the `color: '#...'` key from the object literal. Leave every other field in place.

**`web/src/pages/Settings.test.tsx`** (spec §7.2 line 710):

Open lines 417–423. The current state is:

```ts
const mockCategories: Category[] = [
  { id: 1, name: 'Groceries', type: 'expense', color: '#ff0000', icon: '', sort_order: 1, is_active: true, created_at: '', updated_at: '' },
  { id: 2, name: 'Salary', type: 'income', color: '#00ff00', icon: '', sort_order: 2, is_active: true, created_at: '', updated_at: '' },
  { id: 3, name: 'Rent', type: 'expense', color: '#0000ff', icon: '', sort_order: 3, is_active: true, created_at: '', updated_at: '' },
];
```

Delete the three `color: '#...',` entries. Keep all other fields.

**`web/src/components/TransactionRow.test.tsx`** (if still present — commit 8 may have replaced it with a fresh `TransactionTableRow.test.tsx`): delete `color: '#e94560'` from the category fixture at line 12 and `category_color: '#e94560'` from the transaction fixture at line 32.

Re-run `npx tsc -b` after each fix. Keep going until `tsc -b` exits 0.

- [ ] **Step 14: Sweep untyped fixtures via grep.**

Run: `grep -rn "color:\s*['\"]#" web/src/`

Expected hits (spec §7.2 "Untyped fixtures" list):

- `web/src/pages/Dashboard.test.tsx` line 51, 52 (`color: '#818CF8'`, `color: '#7EC89B'`) + line 70 (`category_color: '#818CF8'`)
- `web/src/pages/Transactions.test.tsx` line 34 (`category_color: '#e94560'`)
- `web/src/pages/Categories.test.tsx` lines 31, 41, 51 (`color: '#ff0000'`, `color: '#00ff00'`, `color: '#0000ff'`)
- `web/src/pages/Reports.test.tsx` line 51 (`color: '#ff0000'`)
- `web/src/hooks/useDashboard.test.ts` lines 34, 35 (`color: '#ff0000'`, `color: '#00ff00'`)
- `web/src/App.test.tsx` any category/transaction fixture with `color:` — delete the keys

Open each file and delete every `color: '#...',` or `category_color: '#...',` key. Keep all other fields. Preserve trailing commas of adjacent fields so the object literals stay valid.

**Important — `App.test.tsx` scope constraint:** when editing `App.test.tsx`, touch **only** the fixture `color:` / `category_color:` keys. **Do NOT touch** the `vi.mock('./hooks/useTheme', ...)`, `vi.mock('./hooks/useChartTheme', ...)`, or `vi.mock('./components/ChartTooltip', ...)` blocks. Those `vi.mock` blocks reference modules that are still present on disk in commit 12 (they're deleted in commit 13), so removing the mocks now would either be premature (breaks nothing but is out-of-scope for commit 12) or actively harmful if a test relies on the mock to prevent loading the real module. Commit 13 Step 3 removes the `vi.mock` blocks in coordination with the module deletions. Leave them alone here.

**False positives to skip:**
- CSS-in-JS object literals like `{ color: '#fff' }` as inline style props
- Third-party component props that happen to accept a `color` field (e.g., some Recharts components)
- Test assertions comparing against a literal hex string (e.g., `expect(el).toHaveStyle({ color: '#ff0000' })`)

If in doubt, check whether the object is a fixture typed or loose-modeled after `Category`, `Transaction`, `CategoryBreakdownItem`, or `CategoryTrendEntry`. If yes → delete. If no (style/third-party) → skip.

- [ ] **Step 15: Run the pre-flight grep set from spec §8 commit 12.**

Run each of these and inspect the output:

```bash
cd d:/claude/SpenDrop
grep -rn "categories\.color" internal/ web/src/
grep -rn "cat\.color\b" internal/ web/src/
grep -rn "category_color" internal/ web/src/
grep -rn "c\.color\b" internal/ web/src/
grep -rn "color:\s*['\"]#" web/src/
```

Expected: **every hit must be either inside this commit's own deletions** (things you're about to commit as deletions — won't show in grep, they're already gone) **or a false positive** (unrelated `.color` usage). If any hit is a surviving `Category`/`Transaction` consumer that should have been updated, open the file, fix the reference, and re-run.

**Known false positives that are fine to ignore:**
- `web/src/lib/chart-colors.ts` — the helper file that replaces color reads; it doesn't reference `categories.color`.
- Test files that assert computed style `{ color: '...' }` — that's CSS `color`, not `Category.color`.
- Comments that mention `categories.color` historically (delete them, but they're not failures).

Also: `grep -rn "Color" internal/ | grep -v "bgColor\|textColor\|_test\.go"` — spot-check any surviving `Color` in `internal/`. The only legitimate survivors are `bgColor`/`textColor` inside the `patternSeed` helper (a frontend-only concept, shouldn't be in `internal/` anyway) or stray test helper comments. If the grep shows a struct field or variable named `Color`, it wasn't deleted in Steps 6–9 — go back and fix.

- [ ] **Step 16: Run the full test suite + typecheck one more time.**

Run: `cd web && npx tsc -b && npx vitest run`

Expected: every frontend test passes, `tsc -b` exits 0.

Run: `cd d:/claude/SpenDrop && go test -race ./...`

Expected: every Go test passes.

If any fails, go back and fix before committing. The commit must not ship partial.

- [ ] **Step 17: Stage and commit atomically.**

Run:

```bash
cd d:/claude/SpenDrop

git add internal/database/migrations/004_drop_categories_color.sql
git add internal/database/queries.sql
git add internal/database/queries.sql.go
git add internal/database/models.go 2>/dev/null || true
git add internal/api/category_handlers.go
git add internal/api/transaction_handlers.go
git add internal/api/dashboard_handlers.go
git add internal/api/reports_handlers.go
git add internal/api/category_handlers_test.go
git add internal/api/transaction_handlers_test.go
git add web/src/api/types.ts
git add web/src/pages/Settings.test.tsx
git add web/src/pages/Dashboard.test.tsx
git add web/src/pages/Transactions.test.tsx
git add web/src/pages/Categories.test.tsx
git add web/src/pages/Reports.test.tsx
git add web/src/hooks/useDashboard.test.ts
git add web/src/App.test.tsx
git add web/src/components/TransactionRow.test.tsx 2>/dev/null || true

git commit -m "feat(db): drop categories.color column

Categories no longer store a user-editable color. Every page now
sources the category color from chart-colors.ts via
getCategoryColorVar() which hashes category.id into one of 11 Radix
palette slots at render time.

Changes land atomically because sqlc regen, Go build, tsc --noEmit,
and vitest all break in different places if committed partially:

- New migration 004_drop_categories_color.sql
- queries.sql: strip c.color / color from 4 queries
- queries.sql.go + models.go regenerated via sqlc generate
- Delete hex color validation + Color field from category_handlers,
  transaction_handlers, dashboard_handlers, reports_handlers
- Delete color param from seedTestCategory helper + category_color
  assertion from transaction_handlers_test
- Delete 4 color fields from web/src/api/types.ts
  (Transaction.category_color, Category.color,
   CategoryBreakdownItem.color, CategoryTrendEntry.color)
- Delete color:'#...' entries from test fixtures in Settings,
  Dashboard, Transactions, Categories, Reports, useDashboard,
  App, and TransactionRow tests
- Pre-flight grep for categories.color / cat.color / category_color /
  c.color returns zero relevant hits

Refs: spec §7 (full file enumeration), §8 commit 12, §2.3"
```

Run `git status` after the commit. Expected: working tree clean. If files are still modified, they weren't staged — `git add` them and amend (unless this is the first commit on the branch, in which case use `git commit --amend --no-edit` is fine).

**After this commit the state of the repo is:**
- Every Go handler + test + sqlc model + frontend type has lost its color field.
- The database migration to drop the column is in place.
- The preflight is still disabled, so the surviving CSS Modules in `Transactions.module.css`, `tokens.css`, and `global.css` still work.
- `TagInput.tsx` still imports `Transactions.module.css`.
- The six orphaned files (`Tabs.tsx`, `ChartTooltip.tsx`, `useChartTheme.ts` + their tests) + `useChartPatterns.tsx` still sit on disk with zero importers.
- One commit remains — Task 13.

---

### Task 13: Final cleanup — delete orphans, rewrite `TagInput`, enable preflight, gate ESLint

**Goal:** Ship the last commit of the migration. Rewrite `TagInput.tsx` to use Tailwind classes (so the last CSS Module dependency dies), delete every orphan file, delete the last three stylesheets, remove `@fontsource-variable/inter`, flip preflight on, do a full visual sweep of every page, and add `npx eslint .` to the frontend CI job.

**Scene:** Commit 12 dropped `categories.color` from the stack. The only legacy code left is: `TagInput.tsx` importing `Transactions.module.css`, six orphan files whose importers are all gone, three untouched stylesheets (`Transactions.module.css`, `tokens.css`, `global.css`), and Tailwind's preflight still disabled. This commit wipes all of it and enables preflight — the first time the whole app runs with Tailwind's CSS reset since the migration started.

**Files:**
- Modify: `web/src/components/TagInput.tsx` (rewrite to Tailwind)
- Modify: `web/src/App.test.tsx` (delete dead `vi.mock` blocks)
- Delete: `web/src/components/Tabs.tsx`, `Tabs.test.tsx`
- Delete: `web/src/components/ChartTooltip.tsx`, `ChartTooltip.test.tsx`
- Delete: `web/src/hooks/useChartTheme.ts`, `useChartTheme.test.ts`
- Delete: `web/src/hooks/useChartPatterns.tsx`, `useChartPatterns.test.tsx` (if it exists)
- Delete: `web/src/hooks/useTheme.tsx`, `useTheme.test.tsx` (orphaned after Sidebar rewrite in Chunk 2 drops dark-mode toggle)
- Delete: `web/src/styles/Tabs.module.css`
- Delete: `web/src/styles/ChartTooltip.module.css`
- Delete: `web/src/styles/Transactions.module.css`
- Delete: `web/src/styles/tokens.css`
- Delete: `web/src/styles/global.css`
- Delete: `web/.stylelintrc.json`
- Modify: `web/src/main.tsx` (drop `global.css` + Inter imports)
- Modify: `web/package.json` (drop `@fontsource-variable/inter`, `stylelint`, `stylelint-config-standard`, `stylelint-config-css-modules`, `lint:css` script, prune `lint` script to drop stylelint)
- Regenerate: `web/package-lock.json` via `npm install`
- Modify: `web/tailwind.config.ts` (flip `corePlugins.preflight` to `true`)
- Modify: `.github/workflows/pr.yml` (add ESLint step to frontend job)

- [ ] **Step 1: Rewrite `web/src/components/TagInput.tsx` with Tailwind classes.**

Replace the entire file with:

```tsx
import { useState, useRef } from 'react';
import type { KeyboardEvent } from 'react';
import { cn } from '@/lib/utils';

interface TagInputProps {
  value: string; // comma-separated
  onChange: (value: string) => void;
  placeholder?: string;
  className?: string;
}

export function TagInput({ value, onChange, placeholder = 'Add tag...', className }: TagInputProps) {
  const [input, setInput] = useState('');
  const inputRef = useRef<HTMLInputElement>(null);

  const tags = value ? value.split(',').map((t) => t.trim()).filter(Boolean) : [];

  function addTag(tag: string) {
    const trimmed = tag.trim();
    if (!trimmed) return;
    if (tags.includes(trimmed)) return;
    onChange([...tags, trimmed].join(','));
    setInput('');
  }

  function removeTag(index: number) {
    const next = tags.filter((_, i) => i !== index);
    onChange(next.join(','));
  }

  function handleKeyDown(e: KeyboardEvent<HTMLInputElement>) {
    if (e.key === 'Enter' || e.key === ',') {
      e.preventDefault();
      addTag(input);
    }
    if (e.key === 'Backspace' && input === '' && tags.length > 0) {
      removeTag(tags.length - 1);
    }
  }

  return (
    <div
      className={cn(
        'flex flex-wrap items-center gap-1 min-h-9 rounded-md border border-input bg-background px-2 py-1 cursor-text transition-colors focus-within:border-ring focus-within:ring-1 focus-within:ring-ring',
        className,
      )}
      onClick={() => inputRef.current?.focus()}
    >
      {tags.map((tag, i) => (
        <span
          key={tag}
          className="inline-flex items-center gap-1 rounded-sm bg-primary/15 px-2 py-0.5 text-xs font-semibold text-primary whitespace-nowrap"
        >
          {tag}
          <button
            type="button"
            className="border-0 bg-transparent p-0 text-sm leading-none text-primary opacity-70 transition-opacity hover:opacity-100"
            onClick={(e) => {
              e.stopPropagation();
              removeTag(i);
            }}
            aria-label={`Remove tag ${tag}`}
          >
            ×
          </button>
        </span>
      ))}
      <input
        ref={inputRef}
        type="text"
        className="flex-1 min-w-[60px] border-0 bg-transparent px-1 py-0.5 text-sm text-foreground outline-none placeholder:text-muted-foreground"
        value={input}
        onChange={(e) => setInput(e.target.value)}
        onKeyDown={handleKeyDown}
        onBlur={() => addTag(input)}
        placeholder={tags.length === 0 ? placeholder : ''}
      />
    </div>
  );
}
```

Key Tailwind mappings from the old `Transactions.module.css`:

| Old class | Old CSS | New Tailwind |
|---|---|---|
| `.tagInputWrapper` | flex + flex-wrap + gap-1 + min-h 36px + bg sunken + border + rounded-sm + cursor-text | `flex flex-wrap items-center gap-1 min-h-9 rounded-md border border-input bg-background px-2 py-1 cursor-text` |
| `.tagInputWrapper:focus-within` | border-color primary | `focus-within:border-ring focus-within:ring-1 focus-within:ring-ring` |
| `.tagPill` | inline-flex + gap-1 + bg primary/15 + text primary + rounded-sm + xs + semibold + whitespace-nowrap | `inline-flex items-center gap-1 rounded-sm bg-primary/15 px-2 py-0.5 text-xs font-semibold text-primary whitespace-nowrap` |
| `.tagRemove` | transparent + border-none + text primary + cursor pointer + sm + opacity-70 | `border-0 bg-transparent p-0 text-sm leading-none text-primary opacity-70 hover:opacity-100` |
| `.tagInput` | flex-1 + min-w 60px + border-none + transparent bg + text primary + sm + outline-none | `flex-1 min-w-[60px] border-0 bg-transparent px-1 py-0.5 text-sm text-foreground outline-none placeholder:text-muted-foreground` |

Save the file. The `import styles from '../styles/Transactions.module.css'` line is gone; the file imports nothing from `styles/`.

**Behavior parity note.** The rewrite is a 1:1 port of the old component's JavaScript logic — no state machine changes, no event handler changes, just class-name swaps plus `cn()` from `@/lib/utils` to compose the caller's `className` with the base classes. The old `.tagInputWrapper:focus-within` border-color rule is ported to Tailwind's `focus-within:border-ring focus-within:ring-1 focus-within:ring-ring`. The `onBlur={() => addTag(input)}` auto-commit carries the same historical quirk: if a user is mid-type when they click a tag's `×` button, the `onBlur` fires when focus leaves the input and commits the typed-but-uncommitted value before `removeTag` runs. That's the pre-migration behavior; preserve it. Do not "fix" it in this commit — any such change is out of scope and risks breaking existing user muscle memory.

- [ ] **Step 2: Run TagInput's test coverage and the Transactions page suite.**

Run: `cd web && npx vitest run src/pages/Transactions.test.tsx`

Expected: every test passes. The TagInput rewrite preserves the same behaviors (add/remove tags, backspace to delete last, Enter/comma to commit, onBlur commit), so any test exercising the tag chip UI should still pass. If a test asserts on a specific class name (e.g. `toHaveClass('tagPill')`), update it to query by `[aria-label="Remove tag ..."]` or `role="textbox"` — Tailwind-generated classes are not reliable selectors.

**Minimal behavior check (manual, in the browser during the Step 10 sweep):**

1. Type `food,work` into an empty tag input → expect two pills `food` and `work`.
2. Press Backspace in the empty input → expect `work` pill to disappear.
3. Click the `×` button on the remaining `food` pill → expect the pill to disappear. If you had typed but not committed a value, the `onBlur` also commits that value — that's the historical quirk noted above, not a regression.

These three checks take 15 seconds and cover the full state machine. Anything more is out of scope for this commit.

- [ ] **Step 3: Delete dead `vi.mock` blocks from `App.test.tsx` before deleting the modules.**

Open `web/src/App.test.tsx`. Three `vi.mock` blocks must be deleted, in any order, before Step 4 removes their target modules:

1. `vi.mock('./hooks/useTheme', ...)` block (lines ~14–24) — orphaned after Chunk 2's Sidebar rewrite (dark mode is deferred per spec §10, so the Sidebar no longer consumes `useTheme`).
2. `vi.mock('./hooks/useChartTheme', ...)` block (lines ~68–82) — orphaned after Chunk 4's Dashboard rewrite + Chunk 7's Reports rewrite.
3. `vi.mock('./components/ChartTooltip', ...)` block (lines ~83–86) — orphaned after the same rewrites.

Delete all three blocks entirely (including any leading comments like `// Mock useTheme (Sidebar uses it)`).

Why first: vitest resolves `vi.mock(path)` paths via the Vite resolver at test collection time. If Step 4 deletes `hooks/useTheme.tsx` while `App.test.tsx` still has `vi.mock('./hooks/useTheme', ...)`, the test runner errors with "Cannot find module './hooks/useTheme'" before any test executes. Delete the mock blocks first, run tests to confirm nothing breaks, **then** delete the modules.

**Check `Sidebar.test.tsx` for the same pattern.** At the pre-migration state, `Sidebar.test.tsx:11–13` has `vi.mock('../hooks/useTheme', ...)`. Chunk 2 rewrote `Sidebar.test.tsx` wholesale; verify the rewrite removed the `useTheme` mock. Run: `grep -n "useTheme" web/src/components/Sidebar.test.tsx` — expect zero hits. If the grep finds hits, Chunk 2 left a stale mock behind; delete the `vi.mock('../hooks/useTheme', ...)` block here in commit 13.

Re-run: `cd web && npx vitest run src/App.test.tsx src/components/Sidebar.test.tsx`

Expected: passes. If it fails because the real `useTheme`, `useChartTheme`, or `ChartTooltip` is now being imported (instead of mocked), check whether any component under `App`'s tree still imports them — it shouldn't at this stage (Chunks 1–11 already rewrote all consumers off them). If some component still imports one, that's a bug in a prior chunk — stop, investigate, and fix the stale import in this commit.

- [ ] **Step 4: Delete the orphan source files.**

**Before running `git rm`, verify each file has zero importers.** Run:

```bash
cd d:/claude/SpenDrop
grep -rn "useChartTheme\|useChartPatterns\|useTheme\b\|ThemeProvider\|components/Tabs\b\|components/ChartTooltip" web/src/
```

Expected: **zero hits** (other than inside the files that are about to be deleted — those vanish when `git rm` stages them). The only legitimate surviving hit would be a test file we've already cleared in Step 3. If the grep shows an unexpected importer (e.g., `App.tsx` still wrapping with `<ThemeProvider>` or a page still calling `useTheme()`), a prior chunk left it behind — stop, fix the stale import, re-run the grep, **then** proceed with the deletions.

Run the deletions:

```bash
cd d:/claude/SpenDrop
git rm web/src/components/Tabs.tsx
git rm web/src/components/Tabs.test.tsx
git rm web/src/components/ChartTooltip.tsx
git rm web/src/components/ChartTooltip.test.tsx
git rm web/src/hooks/useChartTheme.ts
git rm web/src/hooks/useChartTheme.test.ts
git rm web/src/hooks/useChartPatterns.tsx
git rm web/src/hooks/useTheme.tsx
git rm web/src/hooks/useTheme.test.tsx

if [ -f web/src/hooks/useChartPatterns.test.tsx ]; then
  git rm web/src/hooks/useChartPatterns.test.tsx
fi
```

(The final guarded block handles `useChartPatterns.test.tsx` which may or may not exist. The explicit `if` form is clearer to audit than the bash-only `|| true` idiom.)

Re-run the grep once more to confirm zero orphan importers remain:

```bash
grep -rn "useChartTheme\|useChartPatterns\|useTheme\b\|ThemeProvider\|components/Tabs\b\|components/ChartTooltip" web/src/ || echo "all clear"
```

Expected: `all clear`. If any hit remains, it's a regression introduced during this commit itself — fix before continuing.

- [ ] **Step 5: Sanity-check `web/src/styles/` contents before bulk deletion.**

Run: `ls web/src/styles/`

**Expected contents — exactly these 5 files and nothing else:**
- `Tabs.module.css`
- `ChartTooltip.module.css`
- `Transactions.module.css`
- `tokens.css`
- `global.css`

**If any other file is present — especially Chunk 1's Tailwind entry CSS** (spec §8 commit 1 names it `globals.css`, plural, which would sort before `tokens.css` in the listing) — **stop and investigate**. Chunks 1–11 should have deleted every other `*.module.css` (Auth, Categories, Dashboard, Reports, Settings, Sidebar, AppLayout) already. If you see `AppLayout.module.css` or any other legacy module, a prior chunk left it behind; add it to the deletion list below. If you see `globals.css`, that is Chunk 1's Tailwind entry file and it lives in a **different directory** from `web/src/styles/` — spec §8 commit 1 says Chunk 1 creates the file at the Tailwind entry path (typically `web/src/index.css` or `web/src/app/globals.css`, **not** inside `web/src/styles/`). If you find `globals.css` inside `web/src/styles/`, that's a bug in Chunk 1 — investigate before proceeding.

**Only once the listing is confirmed to match the 5 expected files, run the deletions:**

```bash
cd d:/claude/SpenDrop
git rm web/src/styles/Tabs.module.css
git rm web/src/styles/ChartTooltip.module.css
git rm web/src/styles/Transactions.module.css
git rm web/src/styles/tokens.css
git rm web/src/styles/global.css
```

Verify `web/src/styles/` is now empty:

```bash
ls web/src/styles/ 2>/dev/null || echo "directory empty or removed"
```

Expected: empty listing. If `web/src/styles/` is now empty, also remove the empty directory (some git tools leave it): `rmdir web/src/styles 2>/dev/null || true`.

Verify no importers remain:

```bash
grep -rn "styles/Tabs\|styles/ChartTooltip\|styles/Transactions\|styles/tokens\|styles/global" web/src/
```

Expected: zero hits. If anything still imports a deleted stylesheet, fix the import in the same commit.

- [ ] **Step 5b: Delete `.stylelintrc.json` and remove stylelint from `package.json`.**

The Chunk 1 migration added the ESLint + `eslint-plugin-tailwindcss` stack, which covers class-name correctness inside `.tsx` files. With every `.css` file now deleted, `stylelint` has nothing left to lint — it's dead weight. Remove it:

```bash
cd d:/claude/SpenDrop
git rm web/.stylelintrc.json
cd web
npm uninstall stylelint stylelint-config-standard stylelint-config-css-modules
```

Expected: `package.json` loses the three stylelint entries from `devDependencies`, `package-lock.json` regenerates.

**Also edit `web/package.json` scripts** — the existing `lint:css` script at line ~9 and the composed `lint` script at line ~10 both reference stylelint:

```json
"lint:css": "stylelint \"src/**/*.css\"",
"lint": "tsc --noEmit && stylelint \"src/**/*.css\"",
```

Change to:

```json
"lint": "tsc --noEmit && eslint .",
```

Delete the `lint:css` line entirely. The new `lint` script runs typecheck + ESLint — which matches what CI does (Step 12 adds the CI `eslint .` step).

Verify: `grep -n "stylelint" web/package.json` — expect zero hits.

- [ ] **Step 6: Update `web/src/main.tsx` — remove Inter and old `global.css` imports.**

Open `web/src/main.tsx`. Current state (approximate):

```tsx
import '@fontsource-variable/inter';
import '@fontsource-variable/geist';
import '@fontsource-variable/geist-mono';
import './globals.css';  // Chunk 1's Tailwind entry CSS — KEEP THIS
import './styles/global.css';  // old token file — DELETE
// ... rest of file
```

**Delete exactly these two lines:**
- `import '@fontsource-variable/inter';`
- `import './styles/global.css';` (note the `./styles/` prefix — this is the **old** tokens file that was deleted in Step 5 above)

**Keep everything else**, including:
- Both Geist imports (`@fontsource-variable/geist`, `@fontsource-variable/geist-mono`)
- Chunk 1's Tailwind entry CSS import (`./globals.css` or whatever file name Chunk 1 used — it lives directly under `web/src/`, **not** under `web/src/styles/`, and contains `@tailwind base; @tailwind components; @tailwind utilities;` plus the token block from spec §1.3)

**Disambiguation:** there are two files with similar names. `web/src/styles/global.css` (singular, deleted in Step 5) is the old pre-migration token file. `web/src/globals.css` (plural, created in Chunk 1) is the Tailwind entry. Only the singular `global.css` import line is removed. If `main.tsx` imports the Tailwind entry with a different file name than `./globals.css`, leave that import alone — verify by checking that the remaining CSS import resolves to a file that contains `@tailwind` directives.

After the edit, run: `grep -n "import" web/src/main.tsx` and visually confirm only three font imports, one Tailwind-CSS import, and any React/ReactDOM imports remain.

- [ ] **Step 7: Remove `@fontsource-variable/inter` from `package.json`.**

Run: `cd web && npm uninstall @fontsource-variable/inter`

Expected: `package.json` loses the `"@fontsource-variable/inter"` dependency line, `package-lock.json` regenerates, and `node_modules/@fontsource-variable/inter` is deleted.

Verify: `grep -n "inter" web/package.json` — expect zero hits on `fontsource-variable/inter` (other `inter` substrings that aren't the font package are fine, e.g. `interface`).

- [ ] **Step 8: Flip Tailwind preflight on in `web/tailwind.config.ts`.**

Open `web/tailwind.config.ts`. Find the `corePlugins` block:

```ts
corePlugins: {
  preflight: false,
},
```

Change to:

```ts
corePlugins: {
  preflight: true,
},
```

Or, equivalently, delete the entire `corePlugins` block (Tailwind's default is `preflight: true`). Either works. The explicit `true` form is clearer for future readers who want to know that preflight is intentionally on.

- [ ] **Step 9: Run `tsc -b` and vitest.**

Run: `cd web && npx tsc -b && npx vitest run`

Expected: both pass. If vitest fails because a test asserts on a specific CSS rule from `global.css` or `tokens.css` that no longer exists, fix the assertion — the Tailwind base layer provides the same visual defaults now.

If `tsc -b` fails because a file still imports a deleted module, that import was missed in Steps 3–6. Fix it.

- [ ] **Step 10: Start the dev server and do the visual sweep.**

Run: `cd web && npm run dev`

Open the app in the browser. Log in (using the test account, or a freshly seeded DB). Visit every page:

1. **Auth (Login / Register)** — forms render with correct padding, input borders match shadcn `Input`, Button styling is primary Geist. Preflight might have reset the default margin on `<h1>` inside the Card header — verify the heading spacing matches Chunks 2's mock.
2. **Sidebar + AppShell** — nav items highlight correctly, icons align, tooltips on collapsed sidebar show on hover.
3. **Dashboard** — KPI cards, donut chart, bars, transaction list all render. Verify:
   - Chart legend text doesn't have a browser-default `<ul>` indent (preflight removes list-style).
   - Recharts SVG text uses the correct font weight.
   - KPI card numbers use tabular-nums (Geist Mono) from Chunk 1's config.
4. **Categories** — table rows, inline edit, add button. Verify `<table>` preflight didn't collapse borders — the Tailwind `Table` primitive explicitly sets `border-collapse: collapse`, but verify visually.
5. **Transactions** — toolbar (month picker, category filter, tag input, amount range, bulk actions), table rows, row actions, entry row at the bottom. Verify:
   - `TagInput` rewrite still looks like the previous design (tag pills, input field).
   - Category combobox in filter bar still opens/closes.
   - **Entry row (`TransactionEntryRow` from Chunk 6) — exercise the full keyboard flow end-to-end:** Tab into the date field, type a date, Tab to the description, type a description, Tab to the amount, type an amount, Tab to the category combobox, pick a category with arrow keys + Enter, press Enter to save. Confirm the row resets, focus returns to the amount field of the new empty row, and a Sonner toast appears with the "Undo" button. Press ⌘Z to confirm undo still works. This is the single highest-value UX flow in the whole app — regressions here are catastrophic.
   - `<input type="number">` spinner arrows aren't showing if preflight reset them (they're reset in preflight's `forms` plugin anyway — verify nothing changed).
6. **Reports** — date range picker, YoY bar chart, category breakdown chart. Verify:
   - YoY chart bars use `hsl(var(--chart-1))` and `hsl(var(--chart-2))` as fill, with `ChartConfig` labels showing in the legend.
   - Category breakdown uses `getCategoryColorVar()` for each slice.
   - Chart tooltip (the shadcn `ChartTooltipContent`) renders with correct background.
7. **Settings** — Tabs (Profile, Data, Categories), Dialog confirm for import. Verify:
   - Tab underline renders.
   - Dialog opens on import confirm.
   - Form inputs match shadcn defaults (not leaked from deleted `tokens.css`).

**For every page:** confirm that headings, paragraphs, lists, forms, tables, and buttons render with the expected spacing and sizing. Preflight is the first thing that changed behavior in this commit, and it's the one thing the test suite doesn't cover.

**Document findings.** Write a one-line verdict per page in a scratch note (not checked in). Every page should be "✅ unchanged" or "⚠️ regression found — fixed inline."

- [ ] **Step 11: Fix any regressions surfaced by the visual sweep.**

For every regression, adjust the offending component's Tailwind classes until the visual matches the pre-preflight state. Common causes:

- **Browser default `<h1>` / `<h2>` margin gone** → the component relied on `<h1>` having default top/bottom margin. Add `my-4` / `mb-6` to match.
- **List markers gone from `<ul>`** → if a page had `<ul>` with bullet points, preflight removes them. Either add `list-disc pl-6` (to keep bullets) or leave them off if the design intended no bullets.
- **Form control borders gone** → preflight's `appearance: none` on form elements removes native borders. shadcn `Input`/`Select` already compensate. If a raw `<input>` is rendering without a border, wrap it in the shadcn primitive.
- **`<button>` background gone** → preflight resets `<button>` to `background-color: transparent`. If a raw `<button>` was relying on the UA default, wrap it in shadcn `Button` or add explicit `bg-*` classes.
- **Table borders collapsed weirdly** → the shadcn `Table` primitive handles this, but a hand-rolled `<table>` needs `border-collapse: collapse` + explicit cell borders.

**After every fix — re-run the test suite.** Any class change could invalidate a snapshot or a query-by-text assertion:

```bash
cd web && npx vitest run
```

If a snapshot test fails because the visible DOM changed in a legitimate way, update the snapshot with `npx vitest run -u` and inspect the diff carefully — confirm the diff reflects the intended visual change and not an unrelated regression. Commit the updated snapshot as part of this commit.

Re-run the full browser visual sweep after each fix until every page passes. Do not proceed to Step 12 until **every page is verified ✅** and **the test suite is green**.

- [ ] **Step 12: Add `npx eslint .` step to `.github/workflows/pr.yml` frontend job.**

Open `.github/workflows/pr.yml`. Find the frontend job's step list — currently it has (approximately):

```yaml
  frontend:
    runs-on: ubuntu-latest
    defaults:
      run:
        working-directory: web
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-node@v4
        with:
          node-version: 20
          cache: npm
          cache-dependency-path: web/package-lock.json
      - run: npm ci
      - name: Typecheck
        run: npx tsc -b
      - name: Test
        run: npx vitest run
```

Add a new step after `Test`:

```yaml
      - name: Lint
        run: npx eslint .
```

The full frontend job step list after the edit:

```yaml
      - uses: actions/checkout@v4
      - uses: actions/setup-node@v4
        with:
          node-version: 20
          cache: npm
          cache-dependency-path: web/package-lock.json
      - run: npm ci
      - name: Typecheck
        run: npx tsc -b
      - name: Test
        run: npx vitest run
      - name: Lint
        run: npx eslint .
```

**Run locally to confirm the command works:** `cd web && npx eslint .`

Expected: every file passes lint. `eslint-plugin-tailwindcss` (installed in Chunk 1) is already configured — any out-of-order Tailwind class or unknown utility will fail here. If it fails, fix the lint issues in the same commit (they should be zero if Chunks 1–12 stayed disciplined, but the CI step landing last means this is the first time lint failures become hard blockers).

- [ ] **Step 13: Run the full local CI set one more time.**

Run:

```bash
cd d:/claude/SpenDrop
go test -race ./...
cd web
npx tsc -b
npx vitest run
npx eslint .
```

Expected: every command exits 0. If anything fails, fix before committing.

- [ ] **Step 14: Stage and commit.**

Run:

```bash
cd d:/claude/SpenDrop

# Rewritten / modified
git add web/src/components/TagInput.tsx
git add web/src/App.test.tsx
git add web/src/components/Sidebar.test.tsx 2>/dev/null || true
git add web/src/main.tsx
git add web/tailwind.config.ts
git add web/package.json
git add web/package-lock.json
git add .github/workflows/pr.yml

# Deletions (already staged by git rm in Steps 4 and 5/5b):
# - web/src/components/Tabs{,.test}.tsx
# - web/src/components/ChartTooltip{,.test}.tsx
# - web/src/hooks/useChartTheme{.ts,.test.ts}
# - web/src/hooks/useChartPatterns.tsx (+ .test.tsx if present)
# - web/src/hooks/useTheme{.tsx,.test.tsx}
# - web/src/styles/Tabs.module.css
# - web/src/styles/ChartTooltip.module.css
# - web/src/styles/Transactions.module.css
# - web/src/styles/tokens.css
# - web/src/styles/global.css
# - web/.stylelintrc.json

# Verify nothing is forgotten:
git status

git commit -m "chore(web): rewrite TagInput, enable preflight, delete legacy modules, gate ESLint

Final cleanup of the shadcn + Tailwind migration:

- Rewrite TagInput.tsx to Tailwind classes — last CSS Module
  dependency eliminated
- Delete orphan source: components/Tabs{,.test}.tsx,
  components/ChartTooltip{,.test}.tsx,
  hooks/useChartTheme{.ts,.test.ts},
  hooks/useChartPatterns.tsx,
  hooks/useTheme{.tsx,.test.tsx}
- Delete App.test.tsx vi.mock blocks for useTheme, useChartTheme,
  and ChartTooltip (also Sidebar.test.tsx if Chunk 2 left a stale
  useTheme mock behind)
- Delete styles/Tabs.module.css, ChartTooltip.module.css,
  Transactions.module.css, tokens.css, global.css — web/src/styles/
  directory now empty
- Delete web/.stylelintrc.json and uninstall stylelint +
  stylelint-config-standard + stylelint-config-css-modules;
  ESLint + eslint-plugin-tailwindcss covers lint going forward
- Remove Inter import from main.tsx and @fontsource-variable/inter
  from package.json (Geist Sans + Geist Mono are the only fonts now)
- Flip tailwind.config.ts corePlugins.preflight to true — first
  commit with Tailwind's CSS reset active
- Add 'npx eslint .' step to frontend job in pr.yml so ESLint +
  eslint-plugin-tailwindcss become a hard CI gate
- Update package.json 'lint' script to 'tsc --noEmit && eslint .'
  (drop stale lint:css stylelint invocation)

Visual sweep of every page (Auth, Sidebar, Dashboard, Categories,
Transactions incl. TransactionEntryRow keyboard flow, Reports,
Settings) confirmed no preflight regressions.

Refs: spec §1.2 (preflight deferral), §8 commit 13, §9 risks"
```

Run: `git status` — expect working tree clean.

- [ ] **Step 15: Merge-readiness check — final grep sweep.**

Run the final verification:

```bash
cd d:/claude/SpenDrop
grep -rn "\.module\.css" web/src/ || echo "no css modules remain"
grep -rn "fontsource-variable/inter" web/ --include="*.ts" --include="*.tsx" --include="*.json" || echo "no inter references remain"
grep -rn "useChartTheme\|useChartPatterns\|ChartTooltip\|useTheme\b\|ThemeProvider\|components/Tabs\b" web/src/ || echo "no orphan references remain"
grep -rn "stylelint" web/ --include="*.json" || echo "no stylelint references remain"
grep -n "preflight" web/tailwind.config.ts
ls web/src/styles 2>/dev/null || echo "styles directory removed"
ls web/.stylelintrc.json 2>/dev/null || echo "stylelintrc removed"
```

Expected:
- First grep (`.module.css`): `no css modules remain`.
- Second grep (`fontsource-variable/inter`): `no inter references remain`.
- Third grep (orphans): `no orphan references remain`.
- Fourth grep (`stylelint`): `no stylelint references remain`.
- Fifth grep (`preflight`): shows `preflight: true` (or no match if the `corePlugins` block was deleted entirely).
- Sixth check: `styles directory removed` (or empty listing).
- Seventh check: `stylelintrc removed`.

If any grep returns unexpected hits, the migration isn't complete — add a follow-up fix to this commit (or a new commit on the branch) before merging.

**After this commit the migration is complete.** The repo ships on Tailwind v3 + shadcn/ui with:
- Zero CSS Modules
- Zero legacy wrapper components
- Tailwind preflight enabled
- ESLint gated in CI
- `categories.color` removed from SQL, Go, TypeScript, and all test fixtures
- Geist Sans + Geist Mono as the only fonts
- `getCategoryColorVar()` as the single source of category color mapping

The branch is ready to merge to `main` after review.

---

