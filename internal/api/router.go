package api

import (
	"database/sql"
	"net/http"
	"os"
	"strings"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"

	"github.com/elienop/spendrop/internal/auth"
	"github.com/elienop/spendrop/internal/config"
	"github.com/elienop/spendrop/internal/database"
)

// NewRouter creates the main chi router with all API routes registered. cfg
// controls all runtime-tunable limits (rate limits, body size caps, session
// TTL) — pass config.Defaults() if you don't care. A nil cfg also falls back
// to defaults so tests and callers that don't need custom limits can stay
// terse.
func NewRouter(queries *database.Queries, db *sql.DB, cfg *config.Config) chi.Router {
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

	// Health check (public)
	r.Get("/api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	// Auth routes (public, except /me)
	r.Route("/api/auth", func(r chi.Router) {
		r.Post("/register", h.handleRegister)
		r.Post("/login", h.handleLogin)
		r.Post("/logout", h.handleLogout)

		// /me requires authentication
		r.With(auth.RequireAuth(queries)).Get("/me", h.handleMe)
	})

	// Authenticated API routes
	r.Route("/api", func(r chi.Router) {
		r.Use(auth.RequireAuth(queries))
		r.Use(requireJSONContentType)

		// Transactions
		r.Get("/transactions", h.handleListTransactions)
		r.Post("/transactions", h.handleCreateTransaction)
		r.Post("/transactions/batch", h.handleBatchCreateTransactions)
		r.Put("/transactions/bulk-rename", h.handleBulkRename)
		r.Post("/transactions/batch-delete", h.handleBatchDeleteTransactions)
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

		// Users (admin only)
		r.Route("/users", func(r chi.Router) {
			r.Use(auth.RequireAdmin)
			r.Get("/", h.handleListUsers)
			r.Post("/", h.handleCreateUser)
			r.Put("/{id}", h.handleUpdateUser)
			r.Delete("/{id}", h.handleDeleteUser)
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
		r.Delete("/import/{id}", h.handleImportCancel)

		// Settings
		r.Get("/settings/default-budget", h.handleDefaultBudget)
		r.Put("/settings/default-budget", h.handleDefaultBudget)
	})

	// SPA fallback: serve React build if web/dist exists
	distPath := "web/dist"
	if _, err := os.Stat(distPath); err == nil {
		r.NotFound(SPAHandler(distPath))
	}

	return r
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

// requireJSONContentType rejects mutation requests (POST/PUT/PATCH/DELETE) that
// do not carry an application/json Content-Type, unless the content type is
// multipart/form-data (used for file uploads). GET, OPTIONS, and HEAD requests
// are passed through unconditionally.
func requireJSONContentType(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodOptions || r.Method == http.MethodHead {
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
