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
- Dark theme UI

## Tech Stack

- **Backend:** Go (chi router, sqlc)
- **Frontend:** React 19 + TypeScript (Vite)
- **Database:** SQLite (WAL mode)
- **Charts:** Recharts
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

## Project Structure

```
cmd/spendrop/         Go entrypoint
internal/
  api/                HTTP handlers and router
  auth/               Password hashing and session middleware
  database/           SQLite migrations, sqlc queries, generated code
web/src/
  api/                API client and TypeScript types
  components/         Reusable UI components
  hooks/              React hooks (auth, transactions, dashboard)
  pages/              Page components (Dashboard, Transactions, etc.)
  styles/             CSS Modules and global styles
```
