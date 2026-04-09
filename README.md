# SpenDrop

Self-hosted household expense tracker. Track monthly and yearly expenses with budgets, currency conversion, charts, and multi-user support.

## Features

- Rapid transaction entry with spreadsheet-like speed
- Currency conversion (LBP, EUR to USD base)
- Monthly budgets with default fallback
- Yearly savings goals with progress tracking
- Dashboard with KPI cards, spending trends, and category breakdowns
- Category management with drag-and-drop reorder
- Multi-user household access (admin + member roles)
- Dark and light theme with system preference support
- Sidebar with toggle pin (click to expand/collapse), state persisted in localStorage
- Max-width 1400px centered layout for wide-screen readability
- Dark-themed charts (Recharts) with a custom `ChartTooltip` and `useChartTheme()` hook
- Token-driven design system with stylelint enforcement

## Tech Stack

- **Backend:** Go (chi router, sqlc)
- **Frontend:** React 19 + TypeScript (Vite)
- **Database:** SQLite (WAL mode)
- **Charts:** Recharts
- **Styling:** CSS Modules + CSS custom properties (design tokens)
- **Typography:** Inter Variable (self-hosted)
- **Icons:** Lucide React
- **Linting:** stylelint (enforces token-only colors)
- **Deploy:** Docker

## Development

### Prerequisites

- Go 1.26+
- Node.js 20+
- GCC (required for go-sqlite3 CGO)

### Backend

```bash
go run ./cmd/spendrop
```

The server starts on `http://localhost:8080`. On first run, it creates `spendrop.db` and applies migrations automatically.

### Frontend

```bash
cd web
npm install
npm run dev
```

The Vite dev server starts on `http://localhost:5173` and proxies `/api` requests to the Go backend.

### First Run

1. Start the backend and frontend dev servers
2. Open `http://localhost:5173`
3. Register a new account — the first user automatically becomes admin
4. Start adding transactions

### Linting

```bash
cd web
npm run lint:css    # stylelint only
npm run lint        # TypeScript + stylelint
```

Stylelint enforces that all CSS files use design tokens (`var(--token-name)`) instead of raw color values. Only `tokens.css` is allowed to contain hex values.

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `PORT` | `8080` | Server port |
| `DB_PATH` | `spendrop.db` | SQLite database file path |

## Docker

```bash
docker compose up -d
```

Access at `http://localhost:8080`. Data is persisted in a Docker volume (`spendrop-data`).

## Design System

SpenDrop uses a token-driven design system with the Graphite Indigo palette. See [docs/DESIGN_GUIDE.md](docs/DESIGN_GUIDE.md) for the full reference.

**Quick rules:**
- Never use raw hex/rgb/hsl in `.module.css` files — use `var(--token-name)`
- Only `tokens.css` may define color values
- Use semantic tokens (`--surface-raised`) not primitives (`--gray-900`)
- Three theme modes: dark (default), light, system
- Cards use a border-only pattern: `transparent` background + `1px solid var(--border-muted)` border (not surface-raised + shadow)
- Use the shared `<Tabs>` component for tab navigation and `useChartTheme()` for Recharts theming

## Project Structure

```
cmd/spendrop/         Go entrypoint
internal/
  api/                HTTP handlers and router
  auth/               Password hashing and session middleware
  database/           SQLite migrations, sqlc queries, generated code
web/
  src/
    api/              API client and TypeScript types
    components/       Reusable UI components
    hooks/            React hooks (auth, transactions, dashboard, theme)
    pages/            Page components (Dashboard, Transactions, etc.)
    styles/
      tokens.css      Design tokens (primitives + semantics + light overrides)
      global.css      CSS reset and base element styles
      *.module.css    Component/page scoped styles
  .stylelintrc.json   Stylelint config (token enforcement)
```
