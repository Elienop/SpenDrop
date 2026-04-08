-- 001_initial_schema.sql
-- SpenDrop Phase 1 schema

CREATE TABLE IF NOT EXISTS users (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    username TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    display_name TEXT NOT NULL,
    role TEXT NOT NULL DEFAULT 'member',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS sessions (
    token TEXT PRIMARY KEY,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at DATETIME NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_sessions_user_id ON sessions(user_id);
CREATE INDEX idx_sessions_expires_at ON sessions(expires_at);

CREATE TABLE IF NOT EXISTS categories (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE,
    type TEXT NOT NULL CHECK(type IN ('expense', 'income')),
    color TEXT NOT NULL DEFAULT '#888888',
    icon TEXT,
    sort_order INTEGER NOT NULL DEFAULT 0,
    is_active BOOLEAN NOT NULL DEFAULT 1,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS currencies (
    code TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    symbol TEXT NOT NULL,
    rate_to_base REAL NOT NULL,
    is_base BOOLEAN NOT NULL DEFAULT 0,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS transactions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER NOT NULL REFERENCES users(id),
    date DATE NOT NULL,
    amount REAL NOT NULL,
    original_amount REAL,
    original_currency TEXT,
    description TEXT NOT NULL,
    category_id INTEGER NOT NULL REFERENCES categories(id),
    tags TEXT,
    notes TEXT,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_transactions_date ON transactions(date);
CREATE INDEX idx_transactions_category_id ON transactions(category_id);
CREATE INDEX idx_transactions_user_id ON transactions(user_id);

CREATE TABLE IF NOT EXISTS budgets (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    year INTEGER NOT NULL,
    month INTEGER NOT NULL,
    amount REAL NOT NULL,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(year, month)
);

CREATE TABLE IF NOT EXISTS savings_goals (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    year INTEGER NOT NULL UNIQUE,
    target_amount REAL NOT NULL,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS saved_filters (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER NOT NULL REFERENCES users(id),
    name TEXT NOT NULL,
    filter_json TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(user_id, name)
);

CREATE INDEX idx_saved_filters_user_id ON saved_filters(user_id);

CREATE TABLE IF NOT EXISTS app_settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Seed default categories
INSERT INTO categories (name, type, color, sort_order) VALUES
    ('Food', 'expense', '#5347CE', 1),
    ('Gifts', 'expense', '#B794D8', 2),
    ('Health/medical', 'expense', '#7B8AFE', 3),
    ('Home', 'expense', '#4896FE', 4),
    ('Transportation', 'expense', '#2DB3D9', 5),
    ('Personal', 'expense', '#16C8C7', 6),
    ('Repair', 'expense', '#3EBD80', 7),
    ('Utilities', 'expense', '#7EB854', 8),
    ('Outing', 'expense', '#C4B83A', 9),
    ('Debt', 'expense', '#F0C84D', 10),
    ('School', 'expense', '#4030A6', 11),
    ('Takeout', 'expense', '#6B8AFE', 12),
    ('Shopping', 'expense', '#2DB3D9', 13),
    ('Sport', 'expense', '#3EBD80', 14),
    ('Paycheck', 'income', '#5347CE', 1),
    ('Bonus', 'income', '#7B8AFE', 2),
    ('Interest', 'income', '#16C8C7', 3),
    ('Savings', 'income', '#4896FE', 4),
    ('Other', 'income', '#B8BCC8', 5);

-- Seed default currencies
INSERT INTO currencies (code, name, symbol, rate_to_base, is_base) VALUES
    ('USD', 'US Dollar', '$', 1, 1),
    ('LBP', 'Lebanese Pound', 'ل.ل', 89000, 0),
    ('EUR', 'Euro', '€', 0.92, 0);

-- Seed default settings
INSERT INTO app_settings (key, value) VALUES
    ('default_budget', '2000'),
    ('base_currency', 'USD');
