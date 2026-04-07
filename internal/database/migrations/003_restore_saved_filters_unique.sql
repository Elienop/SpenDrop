-- Restore UNIQUE(user_id, name) constraint that was lost in migration 002.
CREATE UNIQUE INDEX IF NOT EXISTS idx_saved_filters_user_name ON saved_filters(user_id, name);
