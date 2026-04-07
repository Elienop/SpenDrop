package api

import (
	"database/sql"

	"github.com/elienop/spendrop/internal/database"
)

// Handler holds dependencies for all API handlers.
type Handler struct {
	queries *database.Queries
	db      *sql.DB
}

// NewHandler creates a new Handler with the given dependencies.
func NewHandler(queries *database.Queries, db *sql.DB) *Handler {
	return &Handler{queries: queries, db: db}
}
