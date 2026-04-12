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

- **General** -- Editable table of monthly budgets for any year (one row per month)
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
- **Bulk operations** -- select transactions on the current page, or select every row matching the current filter across pages, for batch delete
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

Most deployments only need the first handful of variables. Everything below is a runtime knob that defaults to a sensible production value — set them only when you have a specific reason.

#### Container / host

| Variable | Default | Description |
|----------|---------|-------------|
| `PUID` | `911` | User ID for file ownership |
| `PGID` | `911` | Group ID for file ownership |
| `TZ` | `UTC` | Container timezone (e.g. `Asia/Beirut`, `Europe/Berlin`) |
| `PORT` | `8080` | Server listen port |
| `DB_PATH` | `spendrop.db` | SQLite database file path. The Docker image overrides this to `/app/data/spendrop.db` so the file lands in the mounted volume |

#### Cookies, proxies, and CORS

| Variable | Default | Description |
|----------|---------|-------------|
| `COOKIE_SECURE` | `auto` | Session cookie `Secure` flag. `auto` detects from request scheme; set `true` when behind an HTTPS reverse proxy, `false` for plain-HTTP LAN deployments |
| `TRUST_PROXY` | _(unset)_ | When `true`, honor `X-Forwarded-Proto` for HTTPS detection. Only enable behind a trusted reverse proxy (Caddy, nginx, Traefik) |
| `CORS_ORIGIN` | _(unset)_ | Allowed CORS origin for split-origin deployments. Leave unset for same-origin (default). Example: `https://spendrop.example.com` |
| `SPENDROP_INSECURE` | _(unset)_ | **Deprecated** — alias for `COOKIE_SECURE=false`. Use `COOKIE_SECURE=false` instead |

#### HTTP server tuning

| Variable | Default | Description |
|----------|---------|-------------|
| `HTTP_READ_HEADER_TIMEOUT` | `5s` | Max time allowed to read request headers. Accepts any Go `time.Duration` (e.g. `500ms`, `2s`) |
| `HTTP_READ_TIMEOUT` | `15s` | Max time to read the entire request |
| `HTTP_WRITE_TIMEOUT` | `60s` | Max time to write the response. Raise this if you return large exports |
| `HTTP_IDLE_TIMEOUT` | `120s` | Max keep-alive idle time |
| `SHUTDOWN_GRACE` | `10s` | How long the server waits for in-flight requests during graceful shutdown |

#### Sessions and passwords

| Variable | Default | Description |
|----------|---------|-------------|
| `SESSION_TTL` | `720h` (30 days) | Session cookie lifetime |
| `SESSION_CLEANUP_INTERVAL` | `1h` | How often the background job purges expired sessions |
| `SESSION_TOKEN_BYTES` | `32` | Bytes of entropy per session token (must be ≥ 16) |
| `BCRYPT_COST` | `12` | bcrypt work factor (4-31). Higher is slower but harder to brute force |
| `PASSWORD_MIN_LENGTH` | `8` | Minimum password length |
| `PASSWORD_MAX_LENGTH` | `72` | Maximum password length. Must be ≤ 72 (bcrypt's input limit) |

#### Rate limiting

| Variable | Default | Description |
|----------|---------|-------------|
| `RATE_LIMIT_MAX` | `10` | Attempts allowed per client IP per window before login/register return 429 |
| `RATE_LIMIT_WINDOW` | `1m` | How often attempt counters are reset |

#### Uploads and database

| Variable | Default | Description |
|----------|---------|-------------|
| `MAX_JSON_BYTES` | `1048576` (1 MiB) | Maximum size of a JSON request body |
| `MAX_UPLOAD_BYTES` | `10485760` (10 MiB) | Maximum size of a multipart file upload (xlsx import) |
| `SQLITE_BUSY_TIMEOUT` | `5s` | SQLite busy timeout. Raise this if you see `database is locked` errors under heavy concurrent writes |

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
      # Cookie security mode. Default "auto" detects from the request scheme.
      # Set to "true" when behind an HTTPS reverse proxy (with TRUST_PROXY=true).
      # Set to "false" for plain-HTTP LAN deployments.
      # - COOKIE_SECURE=auto
      # - TRUST_PROXY=true
      # CORS origin for split-origin deployments. Leave unset for same-origin.
      # - CORS_ORIGIN=https://spendrop.example.com
    restart: unless-stopped

volumes:
  spendrop-data:        # Persistent storage for SQLite database
```

### Upgrading from an earlier version

Before this release SpenDrop marked the session cookie `Secure` by default unless you set the old `SPENDROP_INSECURE=true`. The new default `COOKIE_SECURE=auto` detects the scheme from the incoming request instead.

If you run behind an HTTPS reverse proxy, the backend sees plain HTTP on its internal port, so `auto` will emit a non-`Secure` cookie unless you tell it the proxy is trusted. To keep the old (stronger) behavior, add both:

```yaml
environment:
  - COOKIE_SECURE=true
  - TRUST_PROXY=true
```

Plain-HTTP LAN deployments that previously worked with `SPENDROP_INSECURE=true` will keep working — `SPENDROP_INSECURE` is now a deprecated alias for `COOKIE_SECURE=false`, but `COOKIE_SECURE=false` is preferred in new configs.

### Deployment Scenarios

**Plain HTTP on your LAN** (e.g. `http://192.168.1.10:3535`):

```yaml
environment:
  - COOKIE_SECURE=false
```

Without this, browsers drop the session cookie because it would otherwise be marked `Secure` on a non-HTTPS origin, causing a 401 on every request after login.

**Behind an HTTPS reverse proxy** (Caddy, nginx, Traefik):

```yaml
environment:
  - COOKIE_SECURE=true
  - TRUST_PROXY=true
```

`TRUST_PROXY=true` is required so the Go backend honors `X-Forwarded-Proto` from the upstream proxy. Only enable it when the container is not directly reachable from untrusted networks — a spoofed header would otherwise bypass the HTTPS check.

### Caddy Reverse Proxy

[Caddy](https://caddyserver.com) is the simplest way to put SpenDrop behind a real domain with automatic TLS from Let's Encrypt. Point an `A`/`AAAA` record at your server, then:

```yaml
# docker-compose.yml
services:
  caddy:
    image: caddy:2-alpine
    container_name: caddy
    ports:
      - "80:80"
      - "443:443"
    volumes:
      - ./Caddyfile:/etc/caddy/Caddyfile:ro
      - caddy-data:/data
      - caddy-config:/config
    restart: unless-stopped

  spendrop:
    image: ghcr.io/elienop/spendrop:latest
    container_name: spendrop
    # No host port needed — Caddy reaches it over the internal network
    expose:
      - "8080"
    volumes:
      - spendrop-data:/app/data
    environment:
      - PUID=1000
      - PGID=1000
      - TZ=UTC
      - COOKIE_SECURE=true
      - TRUST_PROXY=true
    restart: unless-stopped

volumes:
  spendrop-data:
  caddy-data:
  caddy-config:
```

```caddy
# Caddyfile
spendrop.example.com {
    reverse_proxy spendrop:8080
}
```

Caddy automatically provisions and renews a TLS certificate for `spendrop.example.com` on first start.

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
| POST | `/api/transactions/batch-delete` | Batch delete transactions by ID list |
| POST | `/api/transactions/delete-by-filter` | Delete every transaction matching the current filter (atomic, single query) |
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
