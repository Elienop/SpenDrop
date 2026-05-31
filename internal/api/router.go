package api

import (
	"database/sql"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"

	"github.com/elienop/spendrop/internal/auth"
	"github.com/elienop/spendrop/internal/config"
	"github.com/elienop/spendrop/internal/database"
	"github.com/elienop/spendrop/internal/ratelimit"
)

// NewRouter creates the main chi router with all API routes registered. cfg
// controls all runtime-tunable limits (rate limits, body size caps, session
// TTL) — pass config.Defaults() if you don't care. A nil cfg also falls back
// to defaults so tests and callers that don't need custom limits can stay
// terse.
//
// NewRouter discards the constructed *Handler. Production main.go uses
// NewRouterWithHandler instead so it can seed Handler.SetIntegrityResult
// with the startup integrity check outcome before any /healthz/data
// scrape can observe the zero value; tests that only need the router
// keep the shorter signature.
func NewRouter(queries *database.Queries, db *sql.DB, cfg *config.Config) chi.Router {
	r, _ := NewRouterWithHandler(queries, db, cfg)
	return r
}

// NewRouterWithHandler is the full constructor used by production main.go.
// It returns both the chi router and the Handler instance so that the
// startup path can push the initial PRAGMA integrity_check result into
// Handler.SetIntegrityResult before the HTTP server accepts its first
// request. Without this seam the /healthz/data endpoint would briefly
// report "never checked" even though main.go ran the check synchronously
// a few statements earlier.
func NewRouterWithHandler(queries *database.Queries, db *sql.DB, cfg *config.Config) (chi.Router, *Handler) {
	if cfg == nil {
		d := config.Defaults()
		cfg = &d
	}
	ApplyConfig(cfg)
	startRateLimitReset()

	h := NewHandler(queries, db)
	r := chi.NewRouter()

	// Global middleware
	r.Use(securityHeaders)
	r.Use(chimw.Logger)
	r.Use(chimw.Recoverer)
	r.Use(corsMiddleware)

	// Shared auth-failure limiter for every Bearer code path (the combined
	// /api middleware, /api/auth/me, and the Bearer-only /api/homepage
	// subroute). Consolidating the bucket here keeps the 30-per-10-min
	// quota coherent across every endpoint a bearer can reach — moving
	// abuse to a different endpoint doesn't reset the counter.
	authFailLimiter := ratelimit.NewBucket(30, 10*time.Minute, h.clock)

	// Health check (public)
	r.Get("/api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	// Data-aware health endpoint (public). Exposes transaction counts,
	// schema version, last-write timestamp, and the cached integrity
	// result so monitoring scrapers on the Docker network can alert on
	// DB-level degradation without needing an auth session. See the long
	// comment on handleHealthzData for the sub-check ordering and why
	// every field is safe to expose unauthenticated.
	//
	// OPERATIONAL NOTE — scrape interval: every request runs the
	// synchronous PRAGMA quick_check plus three small SELECTs against
	// the transactions table. At household scale (≤10k rows) the whole
	// handler returns in single-digit milliseconds, and Prometheus/Kuma
	// scraping once every 15-30s is cost-free. On larger databases the
	// quick_check walks every page of the main DB file, which is still
	// fast but no longer free: a scraper that hits this endpoint every
	// second is both wasting I/O and reintroducing the "busy DB starves
	// writes" failure mode that WAL mode was supposed to eliminate.
	// Recommendation: keep scrape intervals at ≥10s. We do NOT rate
	// limit the endpoint in code — household deployments are trusted
	// by construction and an in-process cache would add complexity for
	// no real-world payoff. Revisit if we ever ship a multi-tenant build.
	r.Get("/healthz/data", h.handleHealthzData)

	// Web Push VAPID public key (public — the key is not a secret and the
	// browser fetches it before any session exists). 404s when push is
	// disabled. Mirrors /api/health's top-level public registration.
	r.Get("/api/push/vapid-public-key", h.handleGetVAPIDPublicKey)

	// Auth routes (public, except /me)
	r.Route("/api/auth", func(r chi.Router) {
		r.Post("/register", h.handleRegister)
		r.Post("/login", h.handleLogin)
		r.Post("/logout", h.handleLogout)

		// /me requires authentication. Accepts either session cookie OR
		// Bearer token so headless scripts can introspect their own
		// identity.
		r.With(auth.RequireAuthOrAPIToken(queries, authFailLimiter)).Get("/me", h.handleMe)

		// Self-service password change. Auth-required (any user) and
		// JSON-only (requireJSONContentType blocks cross-site form POSTs).
		// Rotates the caller's password and cascades a token revoke +
		// session wipe, so the caller is signed out everywhere on success.
		r.With(auth.RequireAuthOrAPIToken(queries, authFailLimiter)).
			With(requireJSONContentType).
			Post("/password", h.handleChangePassword)
	})

	// Authenticated API routes. Accepts either session cookie OR Bearer
	// token on every endpoint below — the two are interchangeable except
	// on the dedicated Bearer-only /api/homepage subroute further down.
	r.Route("/api", func(r chi.Router) {
		r.Use(auth.RequireAuthOrAPIToken(queries, authFailLimiter))
		r.Use(requireJSONContentType)

		// Transactions
		r.Get("/transactions", h.handleListTransactions)
		r.Post("/transactions", h.handleCreateTransaction)
		r.Post("/transactions/batch", h.handleBatchCreateTransactions)
		r.Put("/transactions/bulk-rename", h.handleBulkRename)
		r.Post("/transactions/batch-delete", h.handleBatchDeleteTransactions)
		r.Post("/transactions/batch-update", h.handleBatchUpdateTransactions)
		r.Post("/transactions/update-by-filter", h.handleUpdateTransactionsByFilter)
		r.Post("/transactions/delete-by-filter", h.handleDeleteTransactionsByFilter)
		r.Get("/transactions/suggestions", h.handleTransactionSuggestions)
		r.Put("/transactions/{id}", h.handleUpdateTransaction)
		r.Delete("/transactions/{id}", h.handleDeleteTransaction)

		// Categories
		r.Get("/categories", h.handleListCategories)
		r.Post("/categories", h.handleCreateCategory)
		r.Put("/categories/{id}", h.handleUpdateCategory)
		r.Patch("/categories/{id}", h.handlePatchCategory)
		r.Delete("/categories/{id}", h.handleDeleteCategory)
		r.Post("/categories/reorder", h.handleReorderCategories)

		// Currencies
		r.Get("/currencies", h.handleListCurrencies)
		r.Post("/currencies", h.handleCreateCurrency)
		r.Put("/currencies/{code}", h.handleUpdateCurrency)

		// Budgets
		r.Get("/budgets", h.handleGetBudgets)
		r.Put("/budgets/{year}/{month}", h.handleSetBudget)

		// Category Budgets (Gap 2) — per-category monthly limits.
		r.Get("/category-budgets", h.handleGetCategoryBudgets)
		r.Put("/category-budgets/{year}/{month}/{categoryId}", h.handleSetCategoryBudget)
		r.Delete("/category-budgets/{year}/{month}/{categoryId}", h.handleDeleteCategoryBudget)

		// Savings Goals
		r.Get("/savings-goals", h.handleGetSavingsGoals)
		r.Put("/savings-goals/{year}", h.handleSetSavingsGoal)

		// Dashboard
		r.Get("/dashboard/summary", h.handleDashboardSummary)
		r.Get("/dashboard/trend", h.handleDashboardTrend)
		r.Get("/dashboard/categories", h.handleDashboardCategories)

		// Reports
		r.Get("/reports/year-over-year", h.handleReportYoY)
		r.Get("/reports/category-trends", h.handleReportCategoryTrends)
		r.Get("/reports/income-expenses", h.handleReportIncomeExpenses)
		r.Get("/reports/top-merchants", h.handleReportTopMerchants)
		r.Get("/reports/budget-vs-actual", h.handleBudgetVsActual)
		r.Get("/reports/expense-velocity", h.handleExpenseVelocity)
		r.Get("/reports/spending-heatmap", h.handleSpendingHeatmap)
		r.Get("/reports/recurring", h.handleRecurring)
		r.Post("/reports/recurring/dismiss", h.handleDismissRecurring)
		r.Get("/reports/tag-breakdown", h.handleTagBreakdown)

		// Balance checkpoints (Phase 3.3). User-asserted sums that the
		// server re-runs on demand and after every transaction mutation.
		// Listed here rather than inside a subroute because the endpoint
		// set is flat (list/create/delete/verify) and sits at the same
		// level as /reports — checkpoints are a read-mostly surface
		// visible to every household member.
		r.Get("/checkpoints", h.handleListCheckpoints)
		r.Post("/checkpoints", h.handleCreateCheckpoint)
		r.Delete("/checkpoints/{id}", h.handleDeleteCheckpoint)
		r.Post("/checkpoints/{id}/verify", h.handleVerifyCheckpoint)

		// Users (admin only)
		r.Route("/users", func(r chi.Router) {
			r.Use(auth.RequireAdmin)
			r.Get("/", h.handleListUsers)
			r.Post("/", h.handleCreateUser)
			r.Put("/{id}", h.handleUpdateUser)
			r.Delete("/{id}", h.handleDeleteUser)
			// Admin resets another user's password. No current-password
			// check — the admin's authority is the authorization. Cascades
			// a token revoke + session wipe for the TARGET user.
			r.Post("/{id}/reset-password", h.handleAdminResetPassword)
		})

		// Trash view (admin only). Mounted as a chi.Group rather than a
		// Route sub-tree because the trash endpoints live alongside the
		// existing /transactions routes — using Route("/transactions", …)
		// here would shadow the member-facing list/create handlers
		// registered above. Group applies auth.RequireAdmin to all four
		// endpoints without adding a path prefix, and chi's radix router
		// distinguishes the literal segments (deleted, restore-batch)
		// from the {id} sub-routes cleanly.
		r.Group(func(r chi.Router) {
			r.Use(auth.RequireAdmin)
			r.Get("/transactions/deleted", h.handleListDeletedTransactions)
			r.Post("/transactions/restore-batch", h.handleBatchRestoreTransactions)
			r.Post("/transactions/restore-all", h.handleRestoreAllTransactions)
			r.Delete("/transactions/trash", h.handlePurgeAllTransactions)
			r.Post("/transactions/{id}/restore", h.handleRestoreTransaction)
			r.Delete("/transactions/{id}/purge", h.handlePurgeTransaction)
		})

		// Export
		r.Get("/export/transactions", h.handleExportTransactions)
		r.Get("/export/monthly/{year}/{month}", h.handleExportMonthly)
		r.Get("/export/yearly/{year}", h.handleExportYearly)

		// Saved Filters
		r.Get("/filters", h.handleListSavedFilters)
		r.Post("/filters", h.handleCreateSavedFilter)
		r.Put("/filters/{id}", h.handleUpdateSavedFilter)
		r.Delete("/filters/{id}", h.handleDeleteSavedFilter)

		// Import
		r.Post("/import/upload", h.handleImportUpload)
		r.Post("/import/confirm", h.handleImportConfirm)
		r.Delete("/import/{importID}", h.handleImportCancel)
		r.Patch("/import/{importID}/rows/{rowID}", h.handleImportPatchRow)
		r.Get("/import/{importID}", h.handleImportGetSession)

		// Settings
		r.Get("/settings/default-budget", h.handleDefaultBudget)
		r.Put("/settings/default-budget", h.handleDefaultBudget)

		// API tokens (user-scoped; every user manages their own)
		r.Route("/api-tokens", func(r chi.Router) {
			r.Post("/", h.handleCreateAPIToken)
			r.Get("/", h.handleListAPITokens)
			r.Delete("/", h.handleRevokeAllAPITokens)
			r.Delete("/{id}", h.handleRevokeAPIToken)
		})

		// Web Push subscription management (user-scoped).
		r.Route("/push", func(r chi.Router) {
			r.Post("/subscriptions", h.handleCreatePushSubscription)
			r.Delete("/subscriptions", h.handleDeletePushSubscription)
			r.Post("/test", h.handlePushTest)

			// Household-wide notification preferences. GET is readable by any
			// authed user; PUT is admin-only (enforced in-handler) because the
			// settings are household-wide, so the two share one path and cannot
			// use a RequireAdmin middleware on the route.
			r.Get("/preferences", h.handleGetNotificationSettings)
			r.Put("/preferences", h.handleUpdateNotificationSettings)
		})
	})

	// Bearer-ONLY homepage widget subroute. Deliberately does NOT accept
	// a session cookie: the widget is consumed by a headless dashboard
	// (e.g. Homepage/Homer) that holds a bearer token and never has a
	// browser session. Using RequireAPIToken (not RequireAuthOrAPIToken)
	// is the guardrail against accidental cookie-auth reuse from
	// same-origin XHR. authFailLimiter is the SAME bucket used by the
	// combined /api middleware above so Bearer abuse on any endpoint
	// counts toward the same 30-per-10-minute quota.
	r.Route("/api/homepage", func(r chi.Router) {
		r.Use(auth.RequireAPIToken(queries, authFailLimiter))
		r.Get("/summary", h.handleHomepageSummary)
	})

	// Live-updates SSE stream. Mounted as its own authed sub-route — NOT
	// inside the main /api group — so it does not inherit that group's
	// requireJSONContentType middleware (a GET would pass it anyway, since
	// that gate early-returns for GET/OPTIONS/HEAD; keeping the route out of
	// the group is the cleaner expression of that, and puts this long-lived
	// streaming GET on its own dedicated middleware chain).
	// Accepts either a session cookie (the browser EventSource path, which
	// cannot set headers) or a Bearer token, via RequireAuthOrAPIToken, and
	// shares the same authFailLimiter bucket so SSE connection attempts count
	// toward the household's 30-per-10-min Bearer quota. The stream is held
	// open by h.handleEvents; the global 60s WriteTimeout is cleared per
	// connection inside the handler. No CSP change is needed — the existing
	// `connect-src 'self'` in securityHeaders already permits a same-origin
	// EventSource to /api/events.
	r.Route("/api/events", func(r chi.Router) {
		r.Use(auth.RequireAuthOrAPIToken(queries, authFailLimiter))
		r.Get("/", h.handleEvents)
	})

	// SPA fallback: serve React build if web/dist exists
	distPath := "web/dist"
	if _, err := os.Stat(distPath); err == nil {
		r.NotFound(SPAHandler(distPath))
	}

	return r, h
}

// securityHeaders adds standard security headers to every response. HSTS is
// suppressed when the deployment has explicitly opted out of HTTPS (via
// COOKIE_SECURE=false or the deprecated SPENDROP_INSECURE=true) so that
// browsers don't pin a broken TLS upgrade.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		// style-src keeps 'unsafe-inline' as a conscious accepted risk: shadcn/
		// Radix components rely on inline style="" attributes (popover/dialog
		// positioning, CSS-variable animations) that no CSP hash or nonce can
		// practically cover. The risk is style injection, not script execution
		// — script-src is locked to 'self'. Do not re-flag without a Radix-wide
		// inline-style audit.
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'")
		if !insecureModeEnabled() {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

// corsMiddleware adds CORS headers for cross-origin deployments. Same-origin
// deployments (the default: Go binary serves the React bundle) do not need
// any CORS headers, so the middleware is fail-closed — headers are only
// emitted when CORS_ORIGIN is explicitly set. This prevents the previous
// behavior of silently whitelisting the Vite dev server in production.
//
// The Vary: Origin header is always emitted so that shared caches don't
// serve cross-origin responses from a different origin's cache entry.
//
// CORS_ORIGIN is captured at router construction time — changing the env
// var after startup has no effect.
func corsMiddleware(next http.Handler) http.Handler {
	origin := os.Getenv("CORS_ORIGIN")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Use Add rather than Set so a future handler that wants to vary on
		// additional headers (Cookie, Accept-Encoding) is not clobbered.
		w.Header().Add("Vary", "Origin")

		if origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			w.Header().Set("Access-Control-Allow-Credentials", "true")
		}

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// requireJSONContentType forces JSON Content-Type on mutations so the
// browser's CORS preflight blocks cross-site POSTs. Bearer-authorized
// requests skip this check — they're not cookies, not auto-attached
// cross-site, and carry their own auth proof (spec §3.8 #3, §6.3).
// GET/OPTIONS/HEAD always pass.
func requireJSONContentType(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodOptions || r.Method == http.MethodHead {
			next.ServeHTTP(w, r)
			return
		}
		// Bearer-auth bypass. Must come BEFORE the Content-Type check so a
		// bearer-authorized POST without application/json (hypothetical
		// future endpoint) is not 415'd. Case-sensitive "Bearer " prefix
		// mirrors RequireAPIToken — both must change together.
		if strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			next.ServeHTTP(w, r)
			return
		}
		ct := r.Header.Get("Content-Type")
		if strings.HasPrefix(ct, "multipart/form-data") {
			next.ServeHTTP(w, r)
			return
		}
		if !strings.HasPrefix(ct, "application/json") {
			writeError(w, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
			return
		}
		next.ServeHTTP(w, r)
	})
}
