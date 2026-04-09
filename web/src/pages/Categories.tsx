import { useState, useEffect, useCallback } from 'react';
import type { FormEvent, DragEvent } from 'react';
import { api } from '../api/client';
import { useAuth } from '../hooks/useAuth';
import type { Category } from '../api/types';
import styles from '../styles/Categories.module.css';

interface CategoryFormData {
  name: string;
  color: string;
  icon: string;
}

function CategoryEditForm({
  initial,
  onSave,
  onCancel,
}: {
  initial?: CategoryFormData;
  onSave: (data: CategoryFormData) => void;
  onCancel: () => void;
}) {
  const [name, setName] = useState(initial?.name ?? '');
  const [color, setColor] = useState(initial?.color ?? '#5347CE');
  const [icon, setIcon] = useState(initial?.icon ?? '');

  function handleSubmit(e: FormEvent) {
    e.preventDefault();
    if (!name.trim()) return;
    onSave({ name: name.trim(), color, icon });
  }

  return (
    <form
      className={styles.editForm}
      onSubmit={handleSubmit}
    >
      <div className={styles.editFormRow}>
        <input
          type="text"
          className={styles.editInput}
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="Category name"
          aria-label="Category name"
          required
        />
        <input
          type="color"
          className={styles.colorInput}
          value={color}
          onChange={(e) => setColor(e.target.value)}
          aria-label="Category color"
        />
        <input
          type="text"
          className={styles.editInput}
          value={icon}
          onChange={(e) => setIcon(e.target.value)}
          placeholder="Icon (optional)"
          aria-label="Category icon"
          style={{ maxWidth: 120 }}
        />
      </div>
      <div className={styles.editFormActions}>
        <button type="button" className={styles.cancelButton} onClick={onCancel}>
          Cancel
        </button>
        <button type="submit" className={styles.saveButton}>
          Save
        </button>
      </div>
    </form>
  );
}

function CategoryCard({
  category,
  isAdmin,
  onEdit,
  onToggle,
  onDragStart,
  onDragOver,
  onDrop,
}: {
  category: Category;
  isAdmin: boolean;
  onEdit: (cat: Category) => void;
  onToggle: (cat: Category) => void;
  onDragStart: (e: DragEvent, cat: Category) => void;
  onDragOver: (e: DragEvent) => void;
  onDrop: (e: DragEvent, cat: Category) => void;
}) {
  return (
    <div
      className={`${styles.card} ${!category.is_active ? styles.inactive : ''}`}
      draggable={isAdmin}
      onDragStart={(e) => onDragStart(e, category)}
      onDragOver={onDragOver}
      onDrop={(e) => onDrop(e, category)}
    >
      {isAdmin && (
        <span className={styles.dragHandle} aria-hidden="true">
          &#8942;&#8942;
        </span>
      )}
      <div
        className={styles.colorSwatch}
        style={{ backgroundColor: category.color }}
      />
      <div className={styles.cardInfo}>
        <div className={styles.cardName}>{category.name}</div>
        <div className={styles.cardStatus}>
          {category.is_active ? 'Active' : 'Inactive'}
        </div>
      </div>
      {isAdmin && (
        <div className={styles.cardActions}>
          <button
            type="button"
            className={`${styles.toggleButton} ${
              category.is_active ? styles.toggleActive : styles.toggleInactive
            }`}
            onClick={() => onToggle(category)}
            aria-label={`${category.is_active ? 'Deactivate' : 'Activate'} ${category.name}`}
          >
            {category.is_active ? 'Deactivate' : 'Activate'}
          </button>
          <button
            type="button"
            className={styles.editButton}
            onClick={() => onEdit(category)}
            aria-label={`Edit ${category.name}`}
          >
            Edit
          </button>
        </div>
      )}
    </div>
  );
}

export function Categories() {
  const { user } = useAuth();
  const isAdmin = user?.role === 'admin';
  const [categories, setCategories] = useState<Category[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [editingId, setEditingId] = useState<number | null>(null);
  const [addingType, setAddingType] = useState<'expense' | 'income' | null>(
    null,
  );
  const [draggedId, setDraggedId] = useState<number | null>(null);

  const fetchCategories = useCallback(() => {
    setLoading(true);
    api
      .get<Category[]>('categories?include_inactive=true')
      .then(setCategories)
      .catch((err) => {
        setError(
          err instanceof Error ? err.message : 'Failed to load categories',
        );
      })
      .finally(() => setLoading(false));
  }, []);

  useEffect(() => {
    fetchCategories();
  }, [fetchCategories]);

  const expenseCategories = categories
    .filter((c) => c.type === 'expense')
    .sort((a, b) => a.sort_order - b.sort_order);

  const incomeCategories = categories
    .filter((c) => c.type === 'income')
    .sort((a, b) => a.sort_order - b.sort_order);

  async function handleCreate(
    type: 'expense' | 'income',
    data: CategoryFormData,
  ) {
    try {
      await api.post('categories', { ...data, type });
      setAddingType(null);
      fetchCategories();
    } catch (err) {
      setError(
        err instanceof Error ? err.message : 'Failed to create category',
      );
    }
  }

  async function handleUpdate(id: number, data: CategoryFormData) {
    try {
      await api.put(`categories/${id}`, data);
      setEditingId(null);
      fetchCategories();
    } catch (err) {
      setError(
        err instanceof Error ? err.message : 'Failed to update category',
      );
    }
  }

  async function handleToggle(cat: Category) {
    try {
      await api.patch(`categories/${cat.id}`, {
        is_active: !cat.is_active,
      });
      fetchCategories();
    } catch (err) {
      setError(
        err instanceof Error ? err.message : 'Failed to update category',
      );
    }
  }

  function handleDragStart(e: DragEvent, cat: Category) {
    setDraggedId(cat.id);
    e.dataTransfer.effectAllowed = 'move';
  }

  function handleDragOver(e: DragEvent) {
    e.preventDefault();
    e.dataTransfer.dropEffect = 'move';
  }

  async function handleDrop(_e: DragEvent, target: Category) {
    if (draggedId === null || draggedId === target.id) return;

    // Find the dragged and target categories in the same type group
    const dragged = categories.find((c) => c.id === draggedId);
    if (!dragged || dragged.type !== target.type) {
      setDraggedId(null);
      return;
    }

    // Get the sorted list for this type
    const typeList = categories
      .filter((c) => c.type === dragged.type)
      .sort((a, b) => a.sort_order - b.sort_order);

    // Remove dragged from list and insert at target position
    const filtered = typeList.filter((c) => c.id !== draggedId);
    const targetIdx = filtered.findIndex((c) => c.id === target.id);
    filtered.splice(targetIdx, 0, dragged);

    // Build reorder payload
    const reorderPayload = filtered.map((c, i) => ({
      id: c.id,
      sort_order: i,
    }));

    try {
      await api.post('categories/reorder', reorderPayload);
      fetchCategories();
    } catch (err) {
      setError(
        err instanceof Error ? err.message : 'Failed to reorder',
      );
    } finally {
      setDraggedId(null);
    }
  }

  function renderSection(
    title: string,
    type: 'expense' | 'income',
    cats: Category[],
  ) {
    return (
      <section className={styles.section}>
        <h2 className={styles.sectionTitle}>{title}</h2>
        <div className={styles.grid}>
          {cats.map((cat) =>
            editingId === cat.id ? (
              <CategoryEditForm
                key={cat.id}
                initial={{
                  name: cat.name,
                  color: cat.color,
                  icon: cat.icon ?? '',
                }}
                onSave={(data) => void handleUpdate(cat.id, data)}
                onCancel={() => setEditingId(null)}
              />
            ) : (
              <CategoryCard
                key={cat.id}
                category={cat}
                isAdmin={isAdmin}
                onEdit={(c) => setEditingId(c.id)}
                onToggle={(c) => void handleToggle(c)}
                onDragStart={handleDragStart}
                onDragOver={handleDragOver}
                onDrop={(e, c) => void handleDrop(e, c)}
              />
            ),
          )}
          {isAdmin && addingType === type && (
            <CategoryEditForm
              onSave={(data) => void handleCreate(type, data)}
              onCancel={() => setAddingType(null)}
            />
          )}
          {isAdmin && addingType !== type && (
            <button
              type="button"
              className={styles.addCard}
              onClick={() => setAddingType(type)}
              aria-label={`Add ${type} category`}
            >
              + Add Category
            </button>
          )}
        </div>
        {cats.length === 0 && !addingType && (
          <p className={styles.emptyState}>No {type} categories yet</p>
        )}
      </section>
    );
  }

  return (
    <div className={styles.page}>
      <h1>Categories</h1>

      {error && (
        <div className={styles.error} role="alert">
          {error}
        </div>
      )}

      {loading ? (
        <div className={styles.loading}>Loading categories...</div>
      ) : (
        <>
          {renderSection('Expense Categories', 'expense', expenseCategories)}
          {renderSection('Income Categories', 'income', incomeCategories)}
        </>
      )}
    </div>
  );
}
