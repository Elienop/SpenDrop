-- Add ON DELETE CASCADE to transactions and saved_filters foreign keys.
-- SQLite does not support ALTER TABLE ... ADD CONSTRAINT, so we recreate tables.

-- Transactions: add ON DELETE CASCADE for user_id and category_id
CREATE TABLE transactions_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    date DATE NOT NULL,
    amount REAL NOT NULL,
    original_amount REAL,
    original_currency TEXT,
    description TEXT NOT NULL DEFAULT '',
    category_id INTEGER NOT NULL REFERENCES categories(id),
    tags TEXT,
    notes TEXT,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO transactions_new SELECT * FROM transactions;
DROP TABLE transactions;
ALTER TABLE transactions_new RENAME TO transactions;

-- Recreate indexes lost by table recreation
CREATE INDEX idx_transactions_date ON transactions(date);
CREATE INDEX idx_transactions_category_id ON transactions(category_id);
CREATE INDEX idx_transactions_user_id ON transactions(user_id);

-- Saved filters: add ON DELETE CASCADE for user_id
CREATE TABLE saved_filters_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    filter_json TEXT NOT NULL DEFAULT '{}',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO saved_filters_new SELECT * FROM saved_filters;
DROP TABLE saved_filters;
ALTER TABLE saved_filters_new RENAME TO saved_filters;

-- Recreate index lost by table recreation
CREATE INDEX idx_saved_filters_user_id ON saved_filters(user_id);
