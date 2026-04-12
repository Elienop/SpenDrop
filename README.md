# SpenDrop

A self-hosted household expense tracker built for families who want full control over their financial data. Track transactions, set budgets, monitor spending trends, and manage multiple users -- all from a single Docker container running on your own hardware.

![Dashboard](docs/screenshots/01-dashboard.png)

## Why SpenDrop?

Most budgeting apps require cloud accounts, charge subscriptions, or mine your financial data. SpenDrop is different:

- **Self-hosted** -- your data stays on your server, always
- **No subscriptions** -- deploy once, use forever
- **Household-ready** -- admin and member roles for shared family tracking
- **Excel-native workflow** -- import your existing spreadsheets, export back to Excel anytime
- **Fast data entry** -- spreadsheet-like keyboard flow (Enter to save, auto-focus next row)

## Features

### Dashboard

Real-time financial overview with KPI cards (total balance, income, expenses, savings rate), a 6/12-month cash flow chart, spending breakdown by category, and recent transactions -- all filterable by month and year.

![Dashboard](docs/screenshots/01-dashboard.png)

### Transaction Management

Full CRUD for transactions with sortable columns (date, description, category, amount), search with find-and-replace, bulk selection with batch delete, inline editing, category badges, tag support, and pagination. Export to Excel at any time.

![Transactions](docs/screenshots/02-transactions.png)

### Reports

Four report tabs covering different angles of your finances:

- **Overview** -- Income vs Expenses bar chart, Net Cash Flow line chart, Budget vs Actual comparison
- **Spending** -- Category breakdown (horizontal bars), category trends over time (multi-line), top merchants table
- **Savings** -- Savings goals and progress tracking
- **Patterns** -- Expense velocity and spending pattern analysis

![Reports Overview](docs/screenshots/03-reports-overview.png)
![Reports Spending](docs/screenshots/04-reports-spending.png)
![Reports Savings](docs/screenshots/05-reports-savings.png)
![Reports Patterns](docs/screenshots/06-reports-patterns.png)

### Categories

Manage expense and income categories with color-coded badges, type labels (Expense/Income), and per-row action menus (edit, deactivate, delete). Deactivated categories stay attached to past transactions but no longer appear in the entry dropdown.

![Categories](docs/screenshots/07-categories.png)

### Settings

Tabbed settings page with five sections:

- **General** -- Monthly budget target
- **Currencies** -- Manage currencies with exchange rates (LBP, EUR to USD base)
- **Savings** -- Yearly savings goals
- **Users** -- Admin user management (create, edit roles, delete)
- **Import / Export** -- Upload Excel files, preview rows, confirm import; export transactions or monthly/yearly reports

![Settings](docs/screenshots/08-settings.png)

### Authentication

Simple username/password auth with bcrypt hashing and HTTP-only session cookies. The first registered user automatically becomes admin. Supports admin and member roles.

![Login](docs/screenshots/09-login.png)
![Register](docs/screenshots/10-register.png)

### Additional Features

- **Dark and light themes** with system preference detection, toggle in sidebar
- **Collapsible sidebar** with pin toggle, state persisted in localStorage
- **Responsive layout** with max-width 1400px for wide-screen readability
- **Saved filters** -- save and recall transaction filter presets
- **Bulk operations** -- select multiple transactions with checkboxes for batch delete
- **Find and replace** -- search transactions and replace descriptions in bulk
- **Excel export** -- export all transactions, or by month/year, as `.xlsx` files

## Tech Stack

| Layer | Technology |
|-------|-----------|
| Backend | Go 1.26 (chi router, sqlc) |
| Frontend | React 19 + TypeScript (Vite) |
| Database | SQLite (WAL mode) |
| UI Components | shadcn/ui + Tailwind CSS |
| Charts | Recharts |
| Icons | Lucide React |
| Auth | bcrypt + HTTP-only cookies |
| Deploy | Docker (multi-stage build) |

## Quick Start with Docker

The fastest way to run SpenDrop:

```bash
# Clone the repo
git clone https://github.com/elienop/spendrop.git
cd spendrop

# Start the container
docker compose up -d
```

SpenDrop is now running at **http://localhost:3535**. Data is persisted in a Docker volume (`spendrop-data`).

### First Login

1. Open http://localhost:3535
2. Click **Register** to create your account
3. The first user automatically becomes **admin** with full access
4. Additional users can be created from Settings > Users as members

## Development Setup

### Prerequisites

- Go 1.26+
- Node.js 20+
- GCC (required for go-sqlite3 CGO compilation)

### Backend

```bash
go run ./cmd/spendrop
```

The server starts on `http://localhost:8080`. On first run it creates `spendrop.db` and applies all migrations automatically.

### Frontend

```bash
cd web
npm install
npm run dev
```

The Vite dev server starts on `http://localhost:5173` and proxies `/api` requests to the Go backend.

### Running Tests

```bash
# Backend tests
go test ./...

# Frontend tests
cd web
npm test
```

### Linting

```bash
cd web
npm run lint        # TypeScript + ESLint
```

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `PUID` | `911` | User ID for file ownership |
| `PGID` | `911` | Group ID for file ownership |
| `TZ` | `UTC` | Container timezone (e.g. `Asia/Beirut`, `Europe/Berlin`) |
| `PORT` | `8080` | Server listen port |
| `DB_PATH` | `spendrop.db` | SQLite database file path |
| `CORS_ORIGIN` | `http://localhost:5173` | Allowed CORS origin (set to your domain in production) |

## Docker Configuration

The Docker setup uses a multi-stage build (Go builder, Node builder, Alpine runtime) for a minimal final image.

```yaml
# docker-compose.yml
services:
  spendrop:
    image: ghcr.io/elienop/spendrop:latest
    container_name: spendrop
    ports:
      - "3535:8080"     # Change 3535 to your preferred port
    volumes:
      - spendrop-data:/app/data
    environment:
      - PUID=1000
      - PGID=1000
      - TZ=UTC              # e.g. Asia/Beirut, Europe/Berlin
      - PORT=8080
      - DB_PATH=/app/data/spendrop.db
    restart: unless-stopped

volumes:
  spendrop-data:        # Persistent storage for SQLite database
```

### Backup

Your database lives in the `spendrop-data` Docker volume. To back it up:

```bash
# Find the volume path
docker volume inspect spendrop-data

# Or copy it out directly
docker cp spendrop:/app/data/spendrop.db ./backup-spendrop.db
```

## API Reference

SpenDrop exposes a RESTful JSON API. All endpoints (except auth and health) require authentication via session cookie.

### Auth
| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | `/api/auth/register` | Register a new user |
| POST | `/api/auth/login` | Log in |
| POST | `/api/auth/logout` | Log out |
| GET | `/api/auth/me` | Get current user info |

### Transactions
| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/api/transactions` | List transactions (supports search, pagination, sorting, date/category filters) |
| POST | `/api/transactions` | Create a transaction |
| POST | `/api/transactions/batch` | Batch create transactions |
| POST | `/api/transactions/batch-delete` | Batch delete transactions |
| PUT | `/api/transactions/{id}` | Update a transaction |
| DELETE | `/api/transactions/{id}` | Delete a transaction |

### Categories
| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/api/categories` | List all categories |
| POST | `/api/categories` | Create a category |
| PUT | `/api/categories/{id}` | Update a category |
| PATCH | `/api/categories/{id}` | Partially update (e.g., deactivate) |
| DELETE | `/api/categories/{id}` | Delete a category |
| POST | `/api/categories/reorder` | Reorder categories |

### Budgets & Savings
| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/api/budgets` | Get budgets |
| PUT | `/api/budgets/{year}/{month}` | Set monthly budget |
| GET | `/api/savings-goals` | Get savings goals |
| PUT | `/api/savings-goals/{year}` | Set yearly savings goal |

### Dashboard & Reports
| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/api/dashboard/summary` | KPI summary for a month |
| GET | `/api/dashboard/trend` | Cash flow trend data |
| GET | `/api/dashboard/categories` | Category spending breakdown |
| GET | `/api/reports/year-over-year` | Year-over-year comparison |
| GET | `/api/reports/category-trends` | Category spending trends |
| GET | `/api/reports/income-expenses` | Income vs expenses report |
| GET | `/api/reports/top-merchants` | Top merchants by spend |
| GET | `/api/reports/budget-vs-actual` | Budget vs actual spending |
| GET | `/api/reports/expense-velocity` | Cumulative spending pace |
| GET | `/api/reports/spending-heatmap` | Daily spending heatmap |
| GET | `/api/reports/recurring` | Detected recurring expenses |
| POST | `/api/reports/recurring/dismiss` | Dismiss a recurring expense |
| GET | `/api/reports/tag-breakdown` | Spending breakdown by tag |

### Export
| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/api/export/transactions` | Export all transactions as Excel |
| GET | `/api/export/monthly/{year}/{month}` | Export monthly report |
| GET | `/api/export/yearly/{year}` | Export yearly report |

### Import
| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | `/api/import/upload` | Upload Excel file for preview |
| POST | `/api/import/confirm` | Confirm and import previewed rows |

## Project Structure

```
cmd/spendrop/              Go entrypoint (main.go)
internal/
  api/                     HTTP handlers, router, middleware
  auth/                    Password hashing, session management, middleware
  database/
    migrations/            SQL migration files (auto-applied on startup)
    queries/               sqlc SQL query definitions
    *.go                   Generated sqlc code + migration runner
web/
  src/
    api/                   API client (fetch wrapper) and TypeScript types
    components/            Reusable UI components (CategoryBadge, TagInput, etc.)
    components/ui/         shadcn/ui primitives (Button, Input, Table, etc.)
    hooks/                 React hooks (useAuth, useTransactions, useDashboard, useTheme)
    lib/                   Utility functions
    pages/                 Page components (Dashboard, Transactions, Reports, etc.)
docker-compose.yml         Docker Compose configuration
Dockerfile                 Multi-stage Docker build
```

## Roadmap

- [ ] Recurring transactions (auto-generate monthly bills)
- [ ] Multi-currency dashboard (show totals in multiple currencies)
- [ ] Receipt photo attachments
- [ ] Mobile-optimized responsive views
- [ ] Spending alerts and notifications
- [ ] Data visualization improvements (pie charts, heatmaps)
- [ ] Shared household budgets (per-category budgets)
- [ ] API key authentication for external integrations

## License

Private project. All rights reserved.
