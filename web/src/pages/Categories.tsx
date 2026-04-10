import { useState, useEffect, useCallback, useMemo, useRef } from 'react';
import { AlertCircle, MoreHorizontal, Plus } from 'lucide-react';
import { api } from '../api/client';
import { useAuth } from '../hooks/useAuth';
import type { Category } from '../api/types';
import { Button } from '@/components/ui/button';
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
} from '@/components/ui/card';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { Skeleton } from '@/components/ui/skeleton';

type CategoryType = 'expense' | 'income';

interface CategoryFormData {
  name: string;
  type: CategoryType;
  icon: string;
}

interface CategoryEditorState {
  mode: 'create' | 'edit';
  category?: Category;
}

export function Categories() {
  const { user } = useAuth();
  const isAdmin = user?.role === 'admin';

  const [categories, setCategories] = useState<Category[]>([]);
  const [loading, setLoading] = useState(true);
  const [fetching, setFetching] = useState(false);
  const [error, setError] = useState('');
  const [editor, setEditor] = useState<CategoryEditorState | null>(null);
  const fetchSeqRef = useRef(0);

  const fetchCategories = useCallback(() => {
    const seq = ++fetchSeqRef.current;
    setFetching(true);
    api
      .get<Category[]>('categories?include_inactive=true')
      .then((cats) => {
        if (seq !== fetchSeqRef.current) return;
        setCategories(cats);
        setError('');
      })
      .catch((err) => {
        if (seq !== fetchSeqRef.current) return;
        setCategories([]);
        setError(
          err instanceof Error ? err.message : 'Failed to load categories',
        );
      })
      .finally(() => {
        if (seq !== fetchSeqRef.current) return;
        setLoading(false);
        setFetching(false);
      });
  }, []);

  useEffect(() => {
    fetchCategories();
  }, [fetchCategories]);

  async function handleSave(data: CategoryFormData) {
    try {
      if (editor?.mode === 'edit' && editor.category) {
        // PUT only sends `name` and `icon` — the backend's
        // `handleUpdateCategory` accepts only {name, color, icon}, and a
        // category's `type` is immutable after creation. `color` is
        // intentionally omitted; the backend keeps the stored value until
        // commit 12 drops the column.
        await api.put(`categories/${editor.category.id}`, {
          name: data.name,
          icon: data.icon,
        });
      } else {
        await api.post('categories', {
          name: data.name,
          type: data.type,
          icon: data.icon,
        });
      }
      setEditor(null);
      fetchCategories();
    } catch (err) {
      setError(
        err instanceof Error ? err.message : 'Failed to save category',
      );
    }
  }

  async function handleToggleActive(cat: Category) {
    try {
      await api.patch(`categories/${cat.id}`, { is_active: !cat.is_active });
      fetchCategories();
    } catch (err) {
      setError(
        err instanceof Error ? err.message : 'Failed to update category',
      );
    }
  }

  async function handleDelete(cat: Category) {
    try {
      await api.del(`categories/${cat.id}`);
      fetchCategories();
    } catch (err) {
      setError(
        err instanceof Error ? err.message : 'Failed to delete category',
      );
    }
  }

  // Sort: expense first, then income; within each group, by sort_order
  const sortedCategories = useMemo(
    () =>
      [...categories].sort((a, b) => {
        if (a.type !== b.type) return a.type === 'expense' ? -1 : 1;
        return a.sort_order - b.sort_order;
      }),
    [categories],
  );

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-semibold tracking-tight">Categories</h1>
        {isAdmin && (
          <Button
            onClick={() => setEditor({ mode: 'create' })}
            aria-label="Add category"
          >
            <Plus data-icon="inline-start" />
            Add category
          </Button>
        )}
      </div>

      {error && (
        <Alert variant="destructive">
          <AlertCircle />
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}

      <Card>
        <CardHeader>
          <CardDescription>
            Manage expense and income categories. Deactivated categories stay
            attached to past transactions but no longer appear in the entry row.
          </CardDescription>
        </CardHeader>
        <CardContent>
          {loading ? (
            <div className="flex flex-col gap-2">
              {Array.from({ length: 4 }).map((_, i) => (
                <Skeleton key={i} className="h-12 w-full" />
              ))}
            </div>
          ) : sortedCategories.length === 0 ? (
            <p className="text-sm text-muted-foreground">
              No categories yet.
            </p>
          ) : (
            <Table className={fetching ? 'opacity-60 transition-opacity' : 'transition-opacity'}>
              <TableHeader>
                <TableRow>
                  <TableHead>Name</TableHead>
                  <TableHead>Type</TableHead>
                  <TableHead className="text-right">Actions</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {sortedCategories.map((cat) => (
                  <TableRow
                    key={cat.id}
                    className={!cat.is_active ? 'opacity-60' : undefined}
                  >
                    <TableCell className="font-medium">
                      {cat.name}
                      {!cat.is_active && (
                        <span className="ml-2 text-xs text-muted-foreground">
                          (inactive)
                        </span>
                      )}
                    </TableCell>
                    <TableCell>
                      <Badge
                        variant={cat.type === 'expense' ? 'outline' : 'secondary'}
                      >
                        {cat.type === 'expense' ? 'Expense' : 'Income'}
                      </Badge>
                    </TableCell>
                    <TableCell className="text-right">
                      {isAdmin && (
                        <DropdownMenu>
                          <DropdownMenuTrigger asChild>
                            <Button
                              variant="ghost"
                              size="icon"
                              className="size-8 data-[state=open]:bg-accent"
                              aria-label={`Actions for ${cat.name}`}
                            >
                              <MoreHorizontal />
                              <span className="sr-only">Open menu</span>
                            </Button>
                          </DropdownMenuTrigger>
                          <DropdownMenuContent align="end">
                            <DropdownMenuItem
                              onClick={() =>
                                setEditor({ mode: 'edit', category: cat })
                              }
                            >
                              Edit
                            </DropdownMenuItem>
                            <DropdownMenuItem
                              onClick={() => void handleToggleActive(cat)}
                            >
                              {cat.is_active ? 'Deactivate' : 'Activate'}
                            </DropdownMenuItem>
                            <DropdownMenuSeparator />
                            <DropdownMenuItem
                              variant="destructive"
                              onClick={() => void handleDelete(cat)}
                            >
                              Delete
                            </DropdownMenuItem>
                          </DropdownMenuContent>
                        </DropdownMenu>
                      )}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>

      <CategoryEditorSheet
        state={editor}
        onClose={() => setEditor(null)}
        onSave={(data) => void handleSave(data)}
      />
    </div>
  );
}

function CategoryEditorSheet({
  state,
  onClose,
  onSave,
}: {
  state: CategoryEditorState | null;
  onClose: () => void;
  onSave: (data: CategoryFormData) => void;
}) {
  const [name, setName] = useState('');
  const [type, setType] = useState<CategoryType>('expense');
  const [icon, setIcon] = useState('');

  const mode = state?.mode;
  const editingId = state?.mode === 'edit' ? state.category?.id : undefined;

  // Seed the form whenever the editor is (re)opened.
  // We intentionally depend on the identity fields (`mode` + editing category id),
  // not the whole `state` object, so unrelated parent re-renders don't re-seed
  // the form mid-edit.
  useEffect(() => {
    if (!state) return;
    if (state.mode === 'edit' && state.category) {
      setName(state.category.name);
      setType(state.category.type);
      setIcon(state.category.icon ?? '');
    } else {
      setName('');
      setType('expense');
      setIcon('');
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [mode, editingId]);

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    if (!name.trim()) return;
    onSave({ name: name.trim(), type, icon: icon.trim() });
  }

  return (
    <Sheet
      open={state !== null}
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
    >
      <SheetContent side="right" className="sm:max-w-md">
        <SheetHeader>
          <SheetTitle>
            {state?.mode === 'edit' ? 'Edit category' : 'Add category'}
          </SheetTitle>
          <SheetDescription>
            {state?.mode === 'edit'
              ? "Update this category's name or icon. Type can't be changed after creation."
              : 'Create a new expense or income category.'}
          </SheetDescription>
        </SheetHeader>

        <form onSubmit={handleSubmit} className="mt-6 flex flex-col gap-4">
          <div className="flex flex-col gap-2">
            <Label htmlFor="category-name">Name</Label>
            <Input
              id="category-name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="e.g. Groceries"
              required
              pattern=".*\S.*"
              title="Name must contain at least one non-whitespace character"
            />
          </div>

          {state?.mode === 'create' && (
            <div className="flex flex-col gap-2">
              <Label htmlFor="category-type">Type</Label>
              <Select
                value={type}
                onValueChange={(v) => setType(v as CategoryType)}
              >
                <SelectTrigger id="category-type">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectGroup>
                    <SelectItem value="expense">Expense</SelectItem>
                    <SelectItem value="income">Income</SelectItem>
                  </SelectGroup>
                </SelectContent>
              </Select>
            </div>
          )}

          <div className="flex flex-col gap-2">
            <Label htmlFor="category-icon">Icon (optional)</Label>
            <Input
              id="category-icon"
              value={icon}
              onChange={(e) => setIcon(e.target.value)}
              placeholder="e.g. 🛒"
            />
          </div>

          <SheetFooter className="mt-6 gap-2">
            <Button type="button" variant="outline" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit">Save</Button>
          </SheetFooter>
        </form>
      </SheetContent>
    </Sheet>
  );
}
