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

### Multi-currency transactions

The transaction entry row has an inline currency selector. Pick a currency other than your household base (configured in Settings -> Currencies) to record the original-currency amount; SpenDrop divides by the configured `rate_to_base` and stores the base-currency value as the authoritative ledger amount. The list view shows both: the canonical base amount on top, and the original-currency amount as a muted secondary line.

Caveats:

- Every non-base currency must have a configured `rate_to_base` in Settings. If the rate is missing or zero, Save is blocked.
- The `~=` preview shown while typing is frontend-approximate; the persisted value is the backend's recomputed amount (they round identically so they agree to the cent).
- Inactive currencies don't appear in the entry-row picker but remain selectable on edit so historical rows round-trip.

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
- **API tokens** -- Mint, list, and revoke long-lived bearer tokens scoped to your user account. Tokens are show-once on creation (you will never see the plaintext again) and are revoked automatically when you change your password. Used to authorize external integrations like the Homepage dashboard widget — see the [Homepage integration](#homepage-integration) section below for the end-to-end setup.
- **Import / Export** -- Upload Excel files, preview and edit rows inline (date / description / amount), mark rows to skip, resolve duplicate-content collisions before confirming; export transactions or monthly/yearly reports. Sessions persist for 60 minutes and survive browser reloads.

![Settings](docs/screenshots/08-settings.png)

### Authentication

Simple username/password auth with bcrypt hashing and HTTP-only session cookies. API routes additionally accept `Authorization: Bearer <token>` for programmatic callers — issue a token from **Settings → API tokens** and paste it into your client's config. Bearer requests skip CSRF (session cookies are only attached to browser requests) and are rate-limited per source IP on authentication failures. The first registered user automatically becomes admin. Supports admin and member roles.

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

### Documentation

The committed [`docs/SCHEMA.md`](docs/SCHEMA.md) is a concatenation of every SQL migration under `internal/database/migrations/`, generated by `cmd/schema-doc` so the schema stays readable even if the Go toolchain stops building in some future year. Regenerate it after adding a migration:

```bash
make docs
```

On Windows (or any host without `make`), run the underlying command directly:

```bash
go run ./cmd/schema-doc > docs/SCHEMA.md
```

CI re-runs `make docs` on every PR and fails if `docs/SCHEMA.md` would change — a forgotten regen can't land on `main` silently.

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

#### Uploads, database, and backups

| Variable | Default | Description |
|----------|---------|-------------|
| `MAX_JSON_BYTES` | `1048576` (1 MiB) | Maximum size of a JSON request body |
| `MAX_UPLOAD_BYTES` | `10485760` (10 MiB) | Maximum size of a multipart file upload (xlsx import) |
| `SQLITE_BUSY_TIMEOUT` | `5s` | SQLite busy timeout. Raise this if you see `database is locked` errors under heavy concurrent writes |
| `BACKUP_ENABLED` | `true` | Enable the in-process scheduled backup loop. Set `false` to disable it entirely; no other `BACKUP_*` variables are validated when disabled |
| `BACKUP_INTERVAL` | `24h` | How often the scheduler runs a backup. Must be at least `1h` |
| `BACKUP_DIR` | `backups` | Where backups are written. The Docker image overrides this to `/app/data/backups` so the files land in the mounted volume |
| `BACKUP_KEEP_DAILY` | `7` | Most-recent daily backups retained |
| `BACKUP_KEEP_WEEKLY` | `4` | Distinct ISO weeks retained |
| `BACKUP_KEEP_MONTHLY` | `12` | Distinct calendar months retained. The sum of the three `BACKUP_KEEP_*` counts must be ≥ 1 — setting all three to `0` is rejected at startup because the current tick's own backup would be pruned on the same tick |

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

### Backup and Restore

SpenDrop takes a consistent, WAL-aware backup of your database every 24 hours by default. Backups land in `/app/data/backups/` inside the `spendrop-data` volume as timestamped files like `spendrop-2026-04-13T0300Z.db` (ISO-8601, UTC, minute precision, no colons so they work on every filesystem). Each backup is accompanied by a `.sha256` sidecar that is **only** written after the file passes three checks:

1. Size sanity — at least one SQLite page, and at most 10× the previous successful backup (the cheap check runs first so an obviously broken file never even opens SQLite)
2. `PRAGMA integrity_check` returns `ok`
3. Row-count parity against the live `transactions` table (tolerates a single in-flight write that landed between the count and the snapshot)

If any check fails, the file is renamed to `<name>.db.corrupt`, no sidecar is written, and the scheduler loop survives so the next tick still fires. The **presence of a `.sha256` sidecar is the "this file is trusted" marker** — the restore drill below relies on it, and so does the prune logic that trims old backups (it ignores `.corrupt` files entirely, leaving them for you to inspect).

Old backups are pruned on a grandfather-father-son schedule: by default, 7 daily, 4 weekly, 12 monthly — roughly 115 MB of backup history for a typical household database.

Every successful backup emits a single log line you can grep for in `docker logs spendrop`:

```
backup ok: spendrop-2026-04-13T0300Z.db (4.8 MB, 9842 rows, sha256 a1b2c3d4e5f6…) in 87ms
```

> **Do not** use `docker cp spendrop:/app/data/spendrop.db` on a running container. SQLite in WAL mode keeps uncommitted writes in `spendrop.db-wal` and `spendrop.db-shm`. A naive copy of just `spendrop.db` can be silently inconsistent or lose recent transactions on restore. Use the scheduled backup or the `spendrop backup` subcommand below instead.

#### Take an immediate backup

```bash
docker exec spendrop ./spendrop backup /app/data/backups/manual-$(date -u +%Y%m%dT%H%MZ).db
```

The subcommand refuses to overwrite an existing file and writes both the `.db` and its `.sha256` sidecar atomically. Running it a second time with the same target exits non-zero without touching the existing file.

#### Tunables

Backup behavior is controlled by six environment variables — `BACKUP_ENABLED`, `BACKUP_INTERVAL`, `BACKUP_DIR`, `BACKUP_KEEP_DAILY`, `BACKUP_KEEP_WEEKLY`, `BACKUP_KEEP_MONTHLY`. All six are documented in the [Uploads, database, and backups](#uploads-database-and-backups) section of the environment variables table. Most deployments never need to change any of them; the common adjustments are `BACKUP_INTERVAL=12h` for twice-daily backups and `BACKUP_ENABLED=false` for throwaway test environments.

#### Restore drill — do this at least once before you trust the system

A backup you have never restored from is a wish, not a backup. Run this drill once on your actual deployment so you know the commands work, the permissions are right, and the data comes back intact. It takes about two minutes.

```bash
# 1. Pick a backup to restore (newest is usually what you want)
docker exec spendrop ls -lt /app/data/backups/

# 2. Verify the checksum still matches the file.
#    Replace the filename with the one you picked in step 1.
docker exec spendrop sh -c 'cd /app/data/backups && sha256sum -c spendrop-2026-04-13T0300Z.db.sha256'

# 3. Stop the container so the live DB is closed cleanly
docker compose stop spendrop

# 4. Replace the live DB. Keep the old one with a .bak suffix in case the
#    restore is wrong. The -wal and -shm files belong to the OLD database —
#    leaving them in place would corrupt the restored one.
docker run --rm -v spendrop-data:/data alpine sh -c '
  mv /data/spendrop.db /data/spendrop.db.bak &&
  rm -f /data/spendrop.db-wal /data/spendrop.db-shm &&
  cp /data/backups/spendrop-2026-04-13T0300Z.db /data/spendrop.db
'

# 5. Start the container back up
docker compose start spendrop

# 6. Verify with a real query
curl -s http://localhost:3535/api/health
#    Then log in and spot-check that the most recent transactions are present.
```

If something looks wrong, you can roll back to the original by repeating step 4 with `spendrop.db.bak` as the source. Once the restore is verified, delete the `.bak` file to reclaim space.

#### Integrity checks

SpenDrop runs a full `PRAGMA integrity_check` **synchronously at startup** — after migrations, before the HTTP server binds a port — and refuses to start on any non-`ok` result. A corrupt database under a live server compounds damage with every write, so the right operator response is "crash loudly, restore from backup, investigate" rather than "limp along and hope." The full result (not just pass/fail) is logged verbatim so the exact SQLite error list is in `docker logs spendrop`.

Once the server is up, a background ticker reruns the full check **every 24 hours** and caches the result. `GET /healthz/data` surfaces both the cached full-check result (`last_integrity_check_at`, `last_integrity_check_result`) and a per-request `PRAGMA quick_check` that catches torn page writes between scheduled runs. Monitoring scrapers should alert on the HTTP status code (503 on degraded) and include the response body in the alert payload so the exact wording reaches the operator without needing another shell.

#### Pre-migration snapshots

Schema migrations are the riskiest thing SpenDrop does to your database — a bug in a new migration can corrupt data in ways the scheduled backup may not have captured yet if the last one was hours ago. Before applying **any** pending migration on startup, SpenDrop writes a dedicated `VACUUM INTO` snapshot to `/app/data/migration-snapshots/` so you always have a pre-upgrade anchor, no matter when the last scheduled backup ran.

Snapshot filenames carry the migration version they are capturing *state before*, e.g. `pre-migration-004_drop_categories_color-2026-04-13T175643Z.db`. Each snapshot has a `.sha256` sidecar with the same trust semantics as the Tier 1 backups above — if the sidecar is present, the file passed the `PRAGMA integrity_check` and row-count checks before being committed.

- **Refuses to start if the snapshot fails.** If the snapshot directory is read-only, full, or missing, the server process exits non-zero with a "refusing to migrate" error and the database is left at its pre-migration version. Fix the disk condition and restart — the migration will retry from the same state.
- **Hardcoded retention.** The three most recent snapshots are kept; older ones are pruned automatically on the next successful migration. The count is not tunable by design: snapshots are short-term recovery anchors, not history, and the sibling Tier 1 scheduled backups cover the longer tail.
- **No-op on clean boots.** If there are no pending migrations, no snapshot is written. A normal restart does not churn the directory.

To restore from a migration snapshot, follow the same restore drill above but with a `pre-migration-*.db` file as the source in step 4. The snapshot is a full SQLite database — the restore steps are identical.

#### Mutation audit log

Every change to a transaction — create, update, delete, batch create, batch delete, bulk rename, delete-by-filter — writes an append-only row to the `transaction_audit` table **in the same SQL transaction as the mutation**. The audit row exists if and only if the mutation committed: a rollback of either rolls back both. This means you always have a record of *who changed what, and when*, even if the row itself is later hard-deleted.

The table stores the action (`insert` / `update` / `delete`), the acting user (`actor_user_id`, which is `ON DELETE SET NULL` so history outlives the account), the timestamp, and JSON blobs of the row state before and after the mutation. Single-row operations get one audit row per transaction touched. **Bulk operations get a single summary row** with `transaction_id = 0` and a payload like `{"bulk":true,"count":142,"filter":"..."}` — per-row diffs for an endpoint that can rename tens of thousands of rows in a single call would balloon the audit table and slow the operation the endpoint exists to serve.

> The audit log is a **CLI-only operator tool**, not a user-facing feature. There is no REST endpoint that returns audit rows, no UI to browse them, and no per-user authorization check. Read access is via `docker exec` into the container — treat it like a database log, not like a timeline.

The `spendrop audit` subcommand dumps matching rows as JSON-lines on stdout so the output composes cleanly with `jq`, `grep`, and `less`:

```bash
# Every audit row for one transaction, oldest first — the full lifecycle
# of a single row including any updates before it was deleted.
docker exec spendrop ./spendrop audit --transaction-id 1234

# Recent mutations across the whole database (default: last 24h, cap 100 rows,
# newest first). Bulk summary rows show up here with transaction_id=0.
docker exec spendrop ./spendrop audit

# Everything since a specific wall-clock date, up to 500 rows. The --since
# flag accepts either YYYY-MM-DD (interpreted as UTC midnight) or RFC3339.
docker exec spendrop ./spendrop audit --since 2026-04-01 --limit 500

# Pipe into jq for readable diffs — useful when investigating "who renamed
# these 200 transactions last Tuesday?"
docker exec spendrop ./spendrop audit --since 2026-04-07 \
  | jq -r 'select(.transaction_id == 0) | "\(.occurred_at) actor=\(.actor_user_id // "?") \(.before_json)"'
```

The audit table is append-only by convention — application code never `UPDATE`s or `DELETE`s rows from it — so the table grows over the lifetime of the deployment. For a typical household the growth is negligible (a few KB per mutation, most days produce fewer than ten mutations). If you ever need to prune it for forensic reasons, do so from `sqlite3` directly, not from the app.

#### Off-host transport (pick one)

Backups in the same Docker volume protect you from application bugs and accidental deletes inside the app, but **not** from the host disk dying or the SD card wearing out. Copy backups off the box on whatever schedule fits your paranoia level. Three reasonable choices:

- **[rclone](https://rclone.org)** — many cloud backends (Backblaze B2, S3, Google Drive, Dropbox, WebDAV…), single binary, simplest "copy this folder somewhere else" tool. Good default if you already have any cloud storage.
- **[restic](https://restic.net)** — deduplicating, encrypted, append-only repositories. Slightly more setup; pays off when you want years of history without paying for years of storage.
- **rsync to a NAS** — LAN-only, no encryption, no cloud, no monthly cost. The right answer if your threat model is "the Pi's SD card dies" and not "the house burns down".

Run any of these from the host (or another container) against the bind path of the named volume:

```bash
# Find the host path
docker volume inspect spendrop-data --format '{{ .Mountpoint }}'
# /var/lib/docker/volumes/spendrop-data/_data
```

Then point your tool of choice at `<mountpoint>/backups/`. **Don't forget to also copy the `.sha256` sidecars** — they're how the restore drill decides which backup is trustworthy when you pull one back from off-host storage.

### Threat model and encryption at rest

SpenDrop stores everything in a single SQLite file at `/app/data/spendrop.db`. Encrypting that file at rest is a good idea — but only if you are clear about *what* it protects.

**What disk-level encryption protects:**
- A stolen laptop, NAS drive, or decommissioned hard disk that is powered off when stolen.
- Backups copied off the host (provided the backup is also encrypted, which is a separate problem).
- An attacker with physical access to the storage medium but not to a running, unlocked system.

**What it does NOT protect:**
- A running container. Anyone with `root` on the host machine can `sqlite3 /app/data/spendrop.db .dump` because the kernel has already decrypted it for the running process.
- Live memory dumps. The encryption key and SQLite's cached database pages (page cache plus any uncheckpointed WAL frames) sit in RAM in plaintext.
- Application-level attacks: a compromised SpenDrop binary, a stolen session cookie, an SQL-injection bug — encryption at rest is invisible to all of these.
- Network traffic. That is what TLS via the Caddy reverse proxy (above) is for. Encryption at rest and encryption in transit are *different problems*.

If your threat model is "my landlord might unplug the NAS and walk off with it," disk encryption is exactly right. If it is "my roommate has shell access to the box," disk encryption does nothing — you need OS user separation and TLS instead.

**Why SpenDrop does not integrate SQLCipher:** SQLCipher requires CGO with a custom build of SQLite, has no upstream support in `mattn/go-sqlite3`, complicates key management (where do you store the key? how do you rotate it? what happens during a migration?), and adds per-query overhead — all to solve a problem that the operating system already solves better, with hardware acceleration, with a key hierarchy your distro already understands. The honest recommendation is: encrypt the volume that holds `/app/data`, not the SQLite file itself.

**Recommended approaches by platform:**

| OS / filesystem | Tool | When to pick it |
| --- | --- | --- |
| Linux, single disk | LUKS (dm-crypt) | The default. Use whatever your distro installer offered. |
| Linux, ZFS pool | ZFS native encryption | If you already run ZFS, do not bolt LUKS on top — use the built-in. |
| Linux, container volume only | LUKS on a loopback file (via `losetup`) | When you only want to encrypt SpenDrop's data, not the whole host. |
| Cross-platform / portable | VeraCrypt | Removable disks, NAS drives shared between OSes. |
| Windows | BitLocker | The Windows-native answer. Hardware-accelerated, manageable. |
| macOS | FileVault | The macOS-native answer. Opt-in — enable it in System Settings → Privacy & Security. |

**LUKS — encrypt a dedicated volume for SpenDrop:**

```bash
# WARNING: cryptsetup luksFormat is destructive. /dev/sdX is the device
# holding ONLY SpenDrop data — never your boot disk.
sudo cryptsetup luksFormat /dev/sdX
sudo cryptsetup open /dev/sdX spendrop-data
sudo mkfs.ext4 /dev/mapper/spendrop-data
sudo mkdir -p /srv/spendrop
sudo mount /dev/mapper/spendrop-data /srv/spendrop

# Then in docker-compose.yml, point the bind mount at /srv/spendrop:
#   volumes:
#     - /srv/spendrop:/app/data
```

To unlock at boot without typing a passphrase, add an entry to `/etc/crypttab` with a keyfile stored on the (also-encrypted) root filesystem. Document the recovery passphrase somewhere offline — *if you lose it, your data is gone, full stop*. That is the deal disk encryption makes with you.

**ZFS native encryption — encrypt a dataset for SpenDrop:**

```bash
# Create an encrypted dataset using a passphrase (interactive prompt).
sudo zfs create -o encryption=on -o keyformat=passphrase \
    -o mountpoint=/srv/spendrop tank/spendrop

# On reboot you must unlock and mount it before starting SpenDrop:
sudo zfs load-key tank/spendrop
sudo zfs mount tank/spendrop
```

Then bind-mount `/srv/spendrop` into the container exactly as in the LUKS example. The advantage of ZFS encryption is that `zfs send -w` (raw send) ships the dataset *still encrypted* to a backup target — perfect for offsite backups to a host you do not fully trust. Plain `zfs send` without `-w` decrypts before streaming, which defeats the point.

**A reminder.** None of the above protects you against the running SpenDrop process being compromised. Encryption at rest is the lock on the front door of the building. TLS is the lock on the apartment door. Application-level auth is the lock on the diary inside. You need all three, and they are sold separately.

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
| DELETE | `/api/transactions/{id}` | Soft-delete a transaction (flips `deleted_at`; the row is hidden from every user-facing read but recoverable via the trash endpoints below) |

#### Trash view (admin only)
Deleted transactions are retained as tombstones and surfaced through admin-only endpoints so the "undo the last nuke" recovery story is in-band rather than a DB shell visit.

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/api/transactions/deleted` | Paginated list of tombstoned transactions, newest first |
| POST | `/api/transactions/{id}/restore` | Restore a single tombstoned row (clears `deleted_at`, emits a `restore` audit row) |
| POST | `/api/transactions/restore-batch` | Restore up to 500 rows in one request; already-live or missing IDs are silently skipped |
| POST | `/api/transactions/restore-all` | Restore every tombstoned row in one shot. Snapshots IDs first, then iterates inside a single SQL transaction so each restore still emits its own `restore` audit row. Returns `{"restored": N}`. |
| DELETE | `/api/transactions/{id}/purge` | Hard-delete a tombstoned row (the only code path that physically removes a transaction) |
| DELETE | `/api/transactions/trash` | Hard-delete every tombstoned row in one SQL statement. Returns `{"purged": N}`. Writes no audit rows — purge is intentionally unaudited because the target rows are already tombstones and the audit schema's `FOREIGN KEY(transaction_id)` has no way to reference a row that no longer exists. |

### Health and monitoring
| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/api/health` | Minimal liveness probe (always `{"status":"ok"}` while the HTTP server is accepting) |
| GET | `/healthz/data` | DB-aware health: schema version, live/deleted transaction counts, last write timestamp, per-request `PRAGMA quick_check`, and the cached result of the daily full `PRAGMA integrity_check`. Returns 200 on "ok" and 503 on any degraded sub-check — monitoring scrapers should alert on the HTTP code. Public (unauthenticated); every field is a count, timestamp, or version string that is safe to expose on a self-hosted LAN. Keep scrape intervals ≥10s to avoid reintroducing WAL busy-writes on larger databases. |

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
| POST | `/api/import/upload` | Upload Excel file and open a preview session (returns `import_id`, parsed rows, and any content-hash collision groups) |
| GET | `/api/import/{importID}` | Resume an existing preview session (used by the frontend after a reload to restore the in-progress import) |
| PATCH | `/api/import/{importID}/rows/{rowID}` | Edit a single field (`date` / `description` / `amount` / `skip`) on a preview row; backend recomputes collisions and returns the full session snapshot |
| DELETE | `/api/import/{importID}` | Cancel the preview session and free the server-side slot |
| POST | `/api/import/confirm` | Confirm and import the previewed rows (rejected with 409 `UNRESOLVED_COLLISIONS` if any content-hash conflict is still active) |

## Project Structure

```
cmd/spendrop/              Go entrypoint (main.go)
  backup.go                CLI subcommand dispatch + `spendrop backup`
  audit.go                 CLI subcommand `spendrop audit` (operator forensics)
internal/
  api/                     HTTP handlers, router, middleware
  auth/                    Password hashing, session management, middleware
  backup/                  VACUUM INTO snapshot primitive + scheduler loop
  database/
    migrations/            SQL migration files (auto-applied on startup)
    queries/               sqlc SQL query definitions
    store.go               TransactionStore chokepoint — every mutation
                           writes an audit row in the same SQL transaction
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
