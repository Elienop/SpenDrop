import { useState, useEffect, useCallback } from 'react';
import type { ChangeEvent, FormEvent } from 'react';
import { api } from '../api/client';
import { useAuth } from '../hooks/useAuth';
import type {
  Budget,
  Category,
  Currency,
  ImportPreview,
  ImportResult,
  SavingsGoal,
  User,
} from '../api/types';
import styles from '../styles/Settings.module.css';

type SettingsTab = 'general' | 'currencies' | 'savings' | 'users' | 'data';

/* ---------- General Tab ---------- */

function GeneralSection() {
  const now = new Date();
  const [budget, setBudget] = useState('');
  const [year] = useState(now.getFullYear());
  const [month] = useState(now.getMonth() + 1);
  const [saving, setSaving] = useState(false);
  const [message, setMessage] = useState('');

  useEffect(() => {
    api
      .get<Budget[]>(`budgets?year=${year}`)
      .then((data) => {
        const match = data.find((b) => b.month === month);
        if (match) {
          setBudget(String(match.amount));
        }
      })
      .catch(() => {
        /* non-critical */
      });
  }, [year, month]);

  async function handleSave(e: FormEvent) {
    e.preventDefault();
    setSaving(true);
    setMessage('');
    try {
      await api.put(`budgets/${year}/${month}`, {
        amount: parseFloat(budget),
      });
      setMessage('Budget saved successfully');
    } catch (err) {
      setMessage(err instanceof Error ? err.message : 'Failed to save');
    } finally {
      setSaving(false);
    }
  }

  return (
    <div className={styles.card}>
      <h2 className={styles.sectionTitle}>General Settings</h2>
      <form onSubmit={(e) => void handleSave(e)}>
        <div className={styles.formGroup}>
          <label htmlFor="monthly-budget" className={styles.formLabel}>
            Monthly Budget
          </label>
          <input
            id="monthly-budget"
            type="number"
            className={styles.formInput}
            value={budget}
            onChange={(e) => setBudget(e.target.value)}
            step="0.01"
            min="0"
            placeholder="0.00"
          />
          <span className={styles.infoText}>
            Budget for {year}-{String(month).padStart(2, '0')}
          </span>
        </div>
        {message && (
          <div className={styles.success}>{message}</div>
        )}
        <button
          type="submit"
          className={styles.saveButton}
          disabled={saving}
        >
          {saving ? 'Saving...' : 'Save Budget'}
        </button>
      </form>
    </div>
  );
}

/* ---------- Currencies Tab ---------- */

function CurrenciesSection() {
  const [currencies, setCurrencies] = useState<Currency[]>([]);
  const [editRates, setEditRates] = useState<Record<string, string>>({});
  const [saving, setSaving] = useState(false);
  const [message, setMessage] = useState('');

  // New currency form
  const [newCode, setNewCode] = useState('');
  const [newName, setNewName] = useState('');
  const [newSymbol, setNewSymbol] = useState('');
  const [newRate, setNewRate] = useState('');

  const fetchCurrencies = useCallback(() => {
    api
      .get<Currency[]>('currencies')
      .then((data) => {
        setCurrencies(data);
        const rates: Record<string, string> = {};
        data.forEach((c) => {
          rates[c.code] = String(c.rate_to_base);
        });
        setEditRates(rates);
      })
      .catch(() => {
        /* non-critical */
      });
  }, []);

  useEffect(() => {
    fetchCurrencies();
  }, [fetchCurrencies]);

  async function handleSaveRates(e: FormEvent) {
    e.preventDefault();
    setSaving(true);
    setMessage('');
    try {
      for (const currency of currencies) {
        if (currency.is_base) continue;
        const newRateVal = editRates[currency.code];
        if (newRateVal && parseFloat(newRateVal) !== currency.rate_to_base) {
          await api.put(`currencies/${currency.code}`, {
            name: currency.name,
            symbol: currency.symbol,
            rate_to_base: parseFloat(newRateVal),
            is_base: currency.is_base,
          });
        }
      }
      setMessage('Rates updated successfully');
      fetchCurrencies();
    } catch (err) {
      setMessage(err instanceof Error ? err.message : 'Failed to update');
    } finally {
      setSaving(false);
    }
  }

  async function handleAddCurrency(e: FormEvent) {
    e.preventDefault();
    try {
      await api.post('currencies', {
        code: newCode.toUpperCase(),
        name: newName,
        symbol: newSymbol,
        rate_to_base: parseFloat(newRate),
      });
      setNewCode('');
      setNewName('');
      setNewSymbol('');
      setNewRate('');
      fetchCurrencies();
    } catch (err) {
      setMessage(err instanceof Error ? err.message : 'Failed to add currency');
    }
  }

  return (
    <div className={styles.card}>
      <h2 className={styles.sectionTitle}>Currencies</h2>
      <form onSubmit={(e) => void handleSaveRates(e)}>
        <table className={styles.table}>
          <thead>
            <tr>
              <th>Code</th>
              <th>Name</th>
              <th>Symbol</th>
              <th>Rate to Base</th>
              <th>Status</th>
            </tr>
          </thead>
          <tbody>
            {currencies.map((c) => (
              <tr key={c.code}>
                <td>{c.code}</td>
                <td>{c.name}</td>
                <td>{c.symbol}</td>
                <td>
                  {c.is_base ? (
                    '1.0000'
                  ) : (
                    <input
                      type="number"
                      className={styles.tableInput}
                      value={editRates[c.code] ?? ''}
                      onChange={(e) =>
                        setEditRates((prev) => ({
                          ...prev,
                          [c.code]: e.target.value,
                        }))
                      }
                      step="0.0001"
                      min="0"
                      aria-label={`Rate for ${c.code}`}
                    />
                  )}
                </td>
                <td>
                  {c.is_base && (
                    <span className={styles.baseBadge}>Base</span>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        {message && <div className={styles.success}>{message}</div>}
        <button
          type="submit"
          className={styles.saveButton}
          disabled={saving}
        >
          {saving ? 'Saving...' : 'Save Rates'}
        </button>
      </form>

      <form
        className={styles.addForm}
        onSubmit={(e) => void handleAddCurrency(e)}
      >
        <div className={styles.addFormField}>
          <label htmlFor="new-curr-code" className={styles.addFormLabel}>
            Code
          </label>
          <input
            id="new-curr-code"
            type="text"
            className={styles.addFormInput}
            value={newCode}
            onChange={(e) => setNewCode(e.target.value)}
            placeholder="GBP"
            maxLength={3}
            required
          />
        </div>
        <div className={styles.addFormField}>
          <label htmlFor="new-curr-name" className={styles.addFormLabel}>
            Name
          </label>
          <input
            id="new-curr-name"
            type="text"
            className={styles.addFormInput}
            value={newName}
            onChange={(e) => setNewName(e.target.value)}
            placeholder="British Pound"
            required
          />
        </div>
        <div className={styles.addFormField}>
          <label htmlFor="new-curr-symbol" className={styles.addFormLabel}>
            Symbol
          </label>
          <input
            id="new-curr-symbol"
            type="text"
            className={styles.addFormInput}
            value={newSymbol}
            onChange={(e) => setNewSymbol(e.target.value)}
            placeholder="\u00A3"
            maxLength={3}
            required
          />
        </div>
        <div className={styles.addFormField}>
          <label htmlFor="new-curr-rate" className={styles.addFormLabel}>
            Rate
          </label>
          <input
            id="new-curr-rate"
            type="number"
            className={styles.addFormInput}
            value={newRate}
            onChange={(e) => setNewRate(e.target.value)}
            placeholder="0.79"
            step="0.0001"
            required
          />
        </div>
        <button type="submit" className={styles.addButton}>
          Add Currency
        </button>
      </form>
    </div>
  );
}

/* ---------- Savings Tab ---------- */

function SavingsSection() {
  const [goals, setGoals] = useState<SavingsGoal[]>([]);
  const [newYear, setNewYear] = useState(String(new Date().getFullYear()));
  const [newAmount, setNewAmount] = useState('');
  const [message, setMessage] = useState('');

  const fetchGoals = useCallback(() => {
    api
      .get<SavingsGoal[]>('savings-goals')
      .then(setGoals)
      .catch(() => {
        /* non-critical */
      });
  }, []);

  useEffect(() => {
    fetchGoals();
  }, [fetchGoals]);

  async function handleAdd(e: FormEvent) {
    e.preventDefault();
    try {
      await api.put(`savings-goals/${parseInt(newYear, 10)}`, {
        target_amount: parseFloat(newAmount),
      });
      setNewAmount('');
      fetchGoals();
    } catch (err) {
      setMessage(err instanceof Error ? err.message : 'Failed to add goal');
    }
  }

  async function handleDelete(goal: SavingsGoal) {
    try {
      await api.put(`savings-goals/${goal.year}`, {
        target_amount: 0,
      });
      fetchGoals();
    } catch (err) {
      setMessage(err instanceof Error ? err.message : 'Failed to delete');
    }
  }

  return (
    <div className={styles.card}>
      <h2 className={styles.sectionTitle}>Savings Goals</h2>
      <table className={styles.table}>
        <thead>
          <tr>
            <th>Year</th>
            <th>Target Amount</th>
            <th>Actions</th>
          </tr>
        </thead>
        <tbody>
          {goals.map((g) => (
            <tr key={g.id}>
              <td>{g.year}</td>
              <td>
                {g.target_amount.toLocaleString('en-US', {
                  style: 'currency',
                  currency: 'USD',
                })}
              </td>
              <td>
                <button
                  type="button"
                  className={`${styles.actionButton} ${styles.deleteButton}`}
                  onClick={() => void handleDelete(g)}
                  aria-label={`Delete ${g.year} goal`}
                >
                  Delete
                </button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      {message && <div className={styles.error}>{message}</div>}
      <form
        className={styles.addForm}
        onSubmit={(e) => void handleAdd(e)}
      >
        <div className={styles.addFormField}>
          <label htmlFor="goal-year" className={styles.addFormLabel}>
            Year
          </label>
          <input
            id="goal-year"
            type="number"
            className={styles.addFormInput}
            value={newYear}
            onChange={(e) => setNewYear(e.target.value)}
            required
          />
        </div>
        <div className={styles.addFormField}>
          <label htmlFor="goal-amount" className={styles.addFormLabel}>
            Target Amount
          </label>
          <input
            id="goal-amount"
            type="number"
            className={styles.addFormInput}
            value={newAmount}
            onChange={(e) => setNewAmount(e.target.value)}
            step="0.01"
            placeholder="0.00"
            required
          />
        </div>
        <button type="submit" className={styles.addButton}>
          Add Goal
        </button>
      </form>
    </div>
  );
}

/* ---------- Users Tab (Admin only) ---------- */

function UsersSection() {
  const [users, setUsers] = useState<User[]>([]);
  const [message, setMessage] = useState('');

  // New user form
  const [newUsername, setNewUsername] = useState('');
  const [newPassword, setNewPassword] = useState('');
  const [newDisplayName, setNewDisplayName] = useState('');
  const [newRole, setNewRole] = useState<'admin' | 'member'>('member');

  const fetchUsers = useCallback(() => {
    api
      .get<User[]>('users')
      .then(setUsers)
      .catch(() => {
        /* non-critical */
      });
  }, []);

  useEffect(() => {
    fetchUsers();
  }, [fetchUsers]);

  async function handleAddUser(e: FormEvent) {
    e.preventDefault();
    try {
      await api.post('users', {
        username: newUsername,
        password: newPassword,
        display_name: newDisplayName,
        role: newRole,
      });
      setNewUsername('');
      setNewPassword('');
      setNewDisplayName('');
      setNewRole('member');
      fetchUsers();
    } catch (err) {
      setMessage(err instanceof Error ? err.message : 'Failed to add user');
    }
  }

  async function handleRoleChange(userId: number, role: 'admin' | 'member') {
    try {
      await api.put(`users/${userId}`, { role });
      fetchUsers();
    } catch (err) {
      setMessage(err instanceof Error ? err.message : 'Failed to update role');
    }
  }

  async function handleDeleteUser(userId: number) {
    try {
      await api.del(`users/${userId}`);
      fetchUsers();
    } catch (err) {
      setMessage(err instanceof Error ? err.message : 'Failed to delete user');
    }
  }

  return (
    <div className={styles.card}>
      <h2 className={styles.sectionTitle}>Users</h2>
      <table className={styles.table}>
        <thead>
          <tr>
            <th>Username</th>
            <th>Display Name</th>
            <th>Role</th>
            <th>Actions</th>
          </tr>
        </thead>
        <tbody>
          {users.map((u) => (
            <tr key={u.id}>
              <td>{u.username}</td>
              <td>{u.display_name}</td>
              <td>
                <select
                  className={styles.roleSelect}
                  value={u.role}
                  onChange={(e) =>
                    void handleRoleChange(
                      u.id,
                      e.target.value as 'admin' | 'member',
                    )
                  }
                  aria-label={`Role for ${u.username}`}
                >
                  <option value="admin">Admin</option>
                  <option value="member">Member</option>
                </select>
              </td>
              <td>
                <button
                  type="button"
                  className={`${styles.actionButton} ${styles.deleteButton}`}
                  onClick={() => void handleDeleteUser(u.id)}
                  aria-label={`Delete ${u.username}`}
                >
                  Delete
                </button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      {message && <div className={styles.error}>{message}</div>}
      <form
        className={styles.addForm}
        onSubmit={(e) => void handleAddUser(e)}
      >
        <div className={styles.addFormField}>
          <label htmlFor="new-user-name" className={styles.addFormLabel}>
            Username
          </label>
          <input
            id="new-user-name"
            type="text"
            className={styles.addFormInput}
            value={newUsername}
            onChange={(e) => setNewUsername(e.target.value)}
            required
          />
        </div>
        <div className={styles.addFormField}>
          <label htmlFor="new-user-pass" className={styles.addFormLabel}>
            Password
          </label>
          <input
            id="new-user-pass"
            type="password"
            className={styles.addFormInput}
            value={newPassword}
            onChange={(e) => setNewPassword(e.target.value)}
            required
          />
        </div>
        <div className={styles.addFormField}>
          <label htmlFor="new-user-display" className={styles.addFormLabel}>
            Display Name
          </label>
          <input
            id="new-user-display"
            type="text"
            className={styles.addFormInput}
            value={newDisplayName}
            onChange={(e) => setNewDisplayName(e.target.value)}
            required
          />
        </div>
        <div className={styles.addFormField}>
          <label htmlFor="new-user-role" className={styles.addFormLabel}>
            Role
          </label>
          <select
            id="new-user-role"
            className={styles.roleSelect}
            value={newRole}
            onChange={(e) =>
              setNewRole(e.target.value as 'admin' | 'member')
            }
          >
            <option value="admin">Admin</option>
            <option value="member">Member</option>
          </select>
        </div>
        <button type="submit" className={styles.addButton}>
          Add User
        </button>
      </form>
    </div>
  );
}

/* ---------- Import / Export Tab ---------- */

type ImportStep = 'upload' | 'preview' | 'done';

function DataSection() {
  const now = new Date();
  const [exportYear, setExportYear] = useState(String(now.getFullYear()));
  const [exportMonth, setExportMonth] = useState(String(now.getMonth() + 1));

  // Import wizard state
  const [importStep, setImportStep] = useState<ImportStep>('upload');
  const [importing, setImporting] = useState(false);
  const [preview, setPreview] = useState<ImportPreview | null>(null);
  const [importResult, setImportResult] = useState<ImportResult | null>(null);
  const [importError, setImportError] = useState('');
  const [defaultCategoryId, setDefaultCategoryId] = useState('');
  const [categories, setCategories] = useState<Category[]>([]);
  const [categoryMap, setCategoryMap] = useState<Record<string, string>>({});

  // Fetch categories on mount for mapping dropdowns
  useEffect(() => {
    api
      .get<Category[]>('categories')
      .then(setCategories)
      .catch(() => {
        /* non-critical */
      });
  }, []);

  // Auto-detect category mappings when preview arrives
  function autoMapCategories(
    previewData: ImportPreview,
    cats: Category[],
  ): Record<string, string> {
    const map: Record<string, string> = {};
    const uniqueCategories = previewData.unique_categories ?? [];
    for (const catName of uniqueCategories) {
      const match = cats.find(
        (c) => c.name.toLowerCase() === catName.toLowerCase(),
      );
      if (match) {
        map[catName] = String(match.id);
      }
    }
    return map;
  }

  async function handleFileChange(e: ChangeEvent<HTMLInputElement>) {
    const file = e.target.files?.[0];
    if (!file) return;

    setImportError('');
    setImporting(true);

    try {
      const data = await api.upload<ImportPreview>('import/upload', file);
      setPreview(data);
      setCategoryMap(autoMapCategories(data, categories));
      setImportStep('preview');
    } catch (err) {
      setImportError(
        err instanceof Error ? err.message : 'Upload failed',
      );
    } finally {
      setImporting(false);
    }
  }

  async function handleConfirmImport() {
    if (!preview) return;

    setImportError('');
    setImporting(true);

    try {
      // Convert string IDs to numbers for the backend (Go expects int64)
      const numericCategoryMap: Record<string, number> = {};
      for (const [name, id] of Object.entries(categoryMap)) {
        if (id) numericCategoryMap[name] = parseInt(id, 10);
      }

      const result = await api.post<ImportResult>('import/confirm', {
        import_id: preview.import_id,
        default_category_id: defaultCategoryId ? parseInt(defaultCategoryId, 10) : 0,
        category_map: numericCategoryMap,
      });
      setImportResult(result);
      setImportStep('done');
    } catch (err) {
      setImportError(
        err instanceof Error ? err.message : 'Import failed',
      );
    } finally {
      setImporting(false);
    }
  }

  function handleCancelImport() {
    setImportStep('upload');
    setPreview(null);
    setImportError('');
    setCategoryMap({});
    setDefaultCategoryId('');
  }

  function handleImportAnother() {
    setImportStep('upload');
    setPreview(null);
    setImportResult(null);
    setImportError('');
    setCategoryMap({});
    setDefaultCategoryId('');
  }

  // Return unique category names from the full import (not just preview rows)
  function getUniqueImportCategories(): string[] {
    if (!preview) return [];
    return preview.unique_categories ?? [];
  }

  function handleExportMonthly() {
    window.open(`/api/export/monthly/${exportYear}/${exportMonth}`, '_blank');
  }

  function handleExportYearly() {
    window.open(`/api/export/yearly/${exportYear}`, '_blank');
  }

  return (
    <div className={styles.card}>
      {/* ---------- Import Section ---------- */}
      <h2 className={styles.sectionTitle}>Import</h2>

      {importError && (
        <div className={styles.error}>{importError}</div>
      )}

      {importStep === 'upload' && (
        <div>
          <p className={styles.infoText}>
            Upload an Excel file with columns: date, description, amount.
            Optional columns: category, tags, notes, original_amount,
            original_currency.
          </p>
          <div className={styles.formGroup}>
            <label htmlFor="import-file" className={styles.formLabel}>
              Excel File
            </label>
            <input
              id="import-file"
              type="file"
              accept=".xlsx,.xls"
              onChange={(e) => void handleFileChange(e)}
              disabled={importing}
            />
          </div>
          {importing && <p className={styles.infoText}>Uploading...</p>}
        </div>
      )}

      {importStep === 'preview' && preview && (
        <div>
          <p className={styles.infoText}>
            Found {preview.row_count} rows to import. Preview of first{' '}
            {preview.preview.length} rows:
          </p>

          <table className={styles.table}>
            <thead>
              <tr>
                <th>Date</th>
                <th>Description</th>
                <th>Amount</th>
                <th>Category</th>
              </tr>
            </thead>
            <tbody>
              {preview.preview.map((row, i) => (
                <tr key={i}>
                  <td>{row.date}</td>
                  <td>{row.description}</td>
                  <td>{row.amount.toFixed(2)}</td>
                  <td>{row.category}</td>
                </tr>
              ))}
            </tbody>
          </table>

          <div className={styles.formGroup}>
            <label htmlFor="default-category" className={styles.formLabel}>
              Default Category
            </label>
            <select
              id="default-category"
              className={styles.addFormInput}
              value={defaultCategoryId}
              onChange={(e) => setDefaultCategoryId(e.target.value)}
            >
              <option value="">-- Select --</option>
              {categories.map((c) => (
                <option key={c.id} value={String(c.id)}>
                  {c.name}
                </option>
              ))}
            </select>
          </div>

          {getUniqueImportCategories().length > 0 && (
            <div>
              <h3>Category Mapping</h3>
              {getUniqueImportCategories().map((catName) => (
                <div key={catName} className={styles.formGroup}>
                  <label
                    htmlFor={`cat-map-${catName}`}
                    className={styles.formLabel}
                  >
                    {catName}
                  </label>
                  <select
                    id={`cat-map-${catName}`}
                    className={styles.addFormInput}
                    value={categoryMap[catName] ?? ''}
                    onChange={(e) =>
                      setCategoryMap((prev) => ({
                        ...prev,
                        [catName]: e.target.value,
                      }))
                    }
                  >
                    <option value="">-- Use Default --</option>
                    {categories.map((c) => (
                      <option key={c.id} value={String(c.id)}>
                        {c.name}
                      </option>
                    ))}
                  </select>
                </div>
              ))}
            </div>
          )}

          <div className={styles.formRow}>
            <button
              type="button"
              className={styles.saveButton}
              onClick={() => void handleConfirmImport()}
              disabled={importing}
            >
              {importing ? 'Importing...' : 'Import'}
            </button>
            <button
              type="button"
              className={styles.actionButton}
              onClick={handleCancelImport}
              disabled={importing}
            >
              Cancel
            </button>
          </div>
        </div>
      )}

      {importStep === 'done' && importResult && (
        <div>
          <div className={styles.success}>
            <p>
              {importResult.imported} imported, {importResult.skipped} skipped
              out of {importResult.total} total rows.
            </p>
          </div>
          <button
            type="button"
            className={styles.saveButton}
            onClick={handleImportAnother}
          >
            Import Another
          </button>
        </div>
      )}

      <hr className={styles.divider} />

      {/* ---------- Export Section ---------- */}
      <h2 className={styles.sectionTitle}>Export</h2>
      <div className={styles.formRow}>
        <div className={styles.addFormField}>
          <label htmlFor="export-year" className={styles.addFormLabel}>
            Year
          </label>
          <input
            id="export-year"
            type="number"
            className={styles.addFormInput}
            value={exportYear}
            onChange={(e) => setExportYear(e.target.value)}
            min="2000"
            max="2099"
          />
        </div>
        <div className={styles.addFormField}>
          <label htmlFor="export-month" className={styles.addFormLabel}>
            Month
          </label>
          <select
            id="export-month"
            className={styles.addFormInput}
            value={exportMonth}
            onChange={(e) => setExportMonth(e.target.value)}
          >
            <option value="1">January</option>
            <option value="2">February</option>
            <option value="3">March</option>
            <option value="4">April</option>
            <option value="5">May</option>
            <option value="6">June</option>
            <option value="7">July</option>
            <option value="8">August</option>
            <option value="9">September</option>
            <option value="10">October</option>
            <option value="11">November</option>
            <option value="12">December</option>
          </select>
        </div>
      </div>
      <div className={styles.formRow}>
        <button
          type="button"
          className={styles.saveButton}
          onClick={handleExportMonthly}
        >
          Export Monthly
        </button>
        <button
          type="button"
          className={styles.saveButton}
          onClick={handleExportYearly}
        >
          Export Yearly
        </button>
      </div>
    </div>
  );
}

/* ---------- Main Settings Page ---------- */

export function Settings() {
  const { user } = useAuth();
  const isAdmin = user?.role === 'admin';
  const [activeTab, setActiveTab] = useState<SettingsTab>('general');

  const tabs: { key: SettingsTab; label: string; adminOnly?: boolean }[] = [
    { key: 'general', label: 'General' },
    { key: 'currencies', label: 'Currencies' },
    { key: 'savings', label: 'Savings' },
    { key: 'users', label: 'Users', adminOnly: true },
    { key: 'data', label: 'Import / Export' },
  ];

  return (
    <div className={styles.page}>
      <h1>Settings</h1>

      <div className={styles.tabs} role="tablist">
        {tabs
          .filter((t) => !t.adminOnly || isAdmin)
          .map((t) => (
            <button
              key={t.key}
              type="button"
              role="tab"
              aria-selected={activeTab === t.key}
              className={`${styles.tab} ${
                activeTab === t.key ? styles.tabActive : ''
              }`}
              onClick={() => setActiveTab(t.key)}
            >
              {t.label}
            </button>
          ))}
      </div>

      {activeTab === 'general' && <GeneralSection />}
      {activeTab === 'currencies' && <CurrenciesSection />}
      {activeTab === 'savings' && <SavingsSection />}
      {activeTab === 'users' && isAdmin && <UsersSection />}
      {activeTab === 'data' && <DataSection />}
    </div>
  );
}
