-- Drop the user-editable categories.color column. Category colors are now
-- derived at render time in the frontend from chart-colors.ts via
-- getCategoryColorVar(), which hashes category.id into one of 11 Radix
-- palette slots. The stored column is no longer read by any code path.
--
-- SQLite 3.35+ supports ALTER TABLE DROP COLUMN. Runs unconditionally on
-- both freshly-migrated and pre-existing databases — migration 001 still
-- creates the column, and this migration removes it in one atomic step.
ALTER TABLE categories DROP COLUMN color;
