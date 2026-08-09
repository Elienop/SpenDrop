import { useState, useEffect, useCallback, useMemo, useRef } from 'react';
import { AlertCircle, MoreHorizontal, Plus } from 'lucide-react';
import { api } from '../api/client';
import { useAuth } from '../hooks/useAuth';
import { useIsMobileViewport } from '@/hooks/useIsMobileViewport';
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
import { isAdmin } from '@/lib/roles';
import { cn } from '@/lib/utils';
import {
  TYPE_EXPENSE,
  TYPE_INCOME,
  type TransactionType,
} from '@/lib/transaction-types';

interface CategoryFormData {
  name: string;
  type: TransactionType;
  icon: string;
}

interface CategoryEditorState {
  mode: 'create' | 'edit';
  category?: Category;
}

interface CategoryActions {
  onEdit: (cat: Category) => void;
  onToggleActive: (cat: Category) => void;
  onDelete: (cat: Category) => void;
}

/**
 * The width bound for a category name, in both directions.
 *
 * A name is user-supplied and capped only by the server's
 * `MaxCategoryNameLength` = 100 (`internal/api/limits.go`). The two classes do
 * two different jobs and must travel together:
 *
 *   - `overflow-wrap:anywhere` bounds the WIDTH. Tailwind's `break-words`
 *     (`overflow-wrap: break-word`) breaks a run for painting but leaves the
 *     element's min-content contribution at the full token width, so the box
 *     keeps sizing itself to it — which is how an unbounded name pans a phone
 *     surface sideways. Measured on the Dashboard's gauge rows, same field: a
 *     456px span at a 360px viewport, dragging the amount off with it.
 *   - `line-clamp-2` bounds the HEIGHT, which wrapping alone trades the width
 *     problem for. Two lines rather than the three a description gets: a name
 *     is an identifier, ~60 characters of one is already unambiguous, and a
 *     browse list of twenty-odd rows is scanned rather than read.
 *
 * Neither does anything without a `min-w-0` ancestor — a flex item's automatic
 * minimum is `min-content`, so the block refuses to shrink however it is
 * allowed to wrap. The card below puts that on the identity column.
 */
const CLAMPED_CATEGORY_NAME = 'line-clamp-2 [overflow-wrap:anywhere]';

/**
 * The type badge, shared by both presentations so the label and the variant
 * cannot drift apart between the table and the phone card.
 */
function CategoryTypeBadge({ type }: { type: TransactionType }) {
  return (
    <Badge variant={type === TYPE_EXPENSE ? 'outline' : 'secondary'}>
      {type === TYPE_EXPENSE ? 'Expense' : 'Income'}
    </Badge>
  );
}

/**
 * The per-row actions menu, rendered by the desktop table AND the phone card.
 *
 * Shared rather than copied because the two presentations must offer the same
 * three actions: a card that quietly dropped Delete would leave the phone with
 * no way to do something the desktop can, and nothing would fail.
 *
 * `coarse:min-h-11` on each item is the 44px floor, and the items are where it
 * matters most — measured on the built container at 360px with a confirmed
 * coarse pointer, the trigger was already 44px while all three menu items were
 * 32px. The thing that actually performs the action was the one under the
 * floor. Gated on the pointer, per `lib/touch-target.ts`: an unconditional
 * floor would inflate the desktop menu too. Written here rather than in
 * `ui/dropdown-menu.tsx` because that primitive has thirty-odd consumers this
 * task did not measure; if it later grows the floor itself, this agrees with it
 * on both the value and the gate, so the pair is inert rather than conflicting.
 */
function CategoryActionsMenu({
  category,
  triggerClassName,
  onEdit,
  onToggleActive,
  onDelete,
}: CategoryActions & { category: Category; triggerClassName?: string }) {
  return (
    /*
      `modal={false}`: this row-actions menu opens the Edit Sheet (a modal Radix
      Dialog). A *modal* dropdown sets `body { pointer-events: none }`; opening
      the Sheet from it can leave a stuck pointer-events lock / lingering
      dismissable layer so the first *mouse* click on Save is swallowed while
      keyboard Enter still submits (keyboard skips hit-testing). Making the
      small actions menu non-modal removes its body lock entirely — the Sheet's
      own modal lifecycle is then the only one in play. This is the
      Radix-recommended pattern for a menu that opens a dialog.
      See https://github.com/radix-ui/primitives/issues/1241
    */
    <DropdownMenu modal={false}>
      <DropdownMenuTrigger asChild>
        <Button
          variant="ghost"
          size="icon"
          className={cn('data-[state=open]:bg-accent', triggerClassName)}
          aria-label={`Actions for ${category.name}`}
        >
          <MoreHorizontal />
          <span className="sr-only">Open menu</span>
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        <DropdownMenuItem
          className="coarse:min-h-11"
          onClick={() => onEdit(category)}
        >
          Edit
        </DropdownMenuItem>
        <DropdownMenuItem
          className="coarse:min-h-11"
          onClick={() => onToggleActive(category)}
        >
          {category.is_active ? 'Deactivate' : 'Activate'}
        </DropdownMenuItem>
        <DropdownMenuSeparator />
        <DropdownMenuItem
          className="coarse:min-h-11"
          variant="destructive"
          onClick={() => onDelete(category)}
        >
          Delete
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

/**
 * One category as a stacked card — the below-`md` presentation of the row the
 * table renders.
 *
 * Three columns do not survive a 360px viewport. Measured on the built
 * container with a coarse pointer: the table wanted 672px inside a 263px
 * content box, so 409px of it sat behind a horizontal scroll — and what was out
 * there was the Actions column, every row's only control, at x=653 in a 345px
 * window. The page itself did not pan, which is what made this easy to miss:
 * the overflow is inside the table's own `overflow-auto` wrapper, so nothing
 * looks wrong until you go looking for the actions.
 *
 * ANATOMY: an identity column (name, then type and state) and the actions menu
 * beside it, rather than <TrashCard>'s full-width action row. Three reasons,
 * and the first is a measurement:
 *
 *   - Three nowrap buttons do not fit. `Edit` / `Deactivate` / `Delete` at
 *     `size="sm"` need ~300px against the 279px this card has inside its
 *     gutters, and `Button` is `whitespace-nowrap` — so they would not wrap,
 *     they would overflow, trading a table pan for a card pan.
 *   - Delete has no confirm dialog on this page (it fires straight at the API;
 *     the backend refuses a category that still has transactions, which is the
 *     only guard). Promoting it to a one-tap full-width button would make it
 *     easier to hit by accident here than on the desktop it mirrors. Trash's
 *     card can afford visible actions because its destructive one walks through
 *     a dialog and its primary one is the page's whole purpose; here the page's
 *     purpose is reading the list, and a member sees no actions at all.
 *   - Density. Twenty-odd rows are browsed on this surface; an action row per
 *     card doubles the scroll for a control used a few times a month.
 */
function CategoryCard({
  category,
  admin,
  onEdit,
  onToggleActive,
  onDelete,
}: CategoryActions & { category: Category; admin: boolean }) {
  return (
    <li className="flex items-start justify-between gap-3 p-4">
      {/*
        `opacity-60` marks a deactivated category, exactly as the table row
        does — but scoped to the identity block rather than the whole card, so
        the actions trigger beside it keeps full contrast. The desktop row can
        fade its own control because a mouse target does not have to be seen to
        be hit precisely; a 44px thumb target at 60% opacity on a phone is the
        one control on the card and should not be the hardest thing to read.
        `min-w-0` is what lets the name clamp — see CLAMPED_CATEGORY_NAME.
      */}
      <div
        className={cn(
          'flex min-w-0 flex-col gap-1.5',
          !category.is_active && 'opacity-60',
        )}
      >
        <span className={cn('font-medium', CLAMPED_CATEGORY_NAME)}>
          {category.name}
        </span>
        <div className="flex flex-wrap items-center gap-2">
          <CategoryTypeBadge type={category.type} />
          {!category.is_active && (
            <span className="text-xs text-muted-foreground">(inactive)</span>
          )}
        </div>
      </div>
      {admin && (
        <CategoryActionsMenu
          category={category}
          /*
            44px for a MOUSE too, not only for a coarse pointer, because this
            subtree only exists below `md` — the same call the phone cards in
            Trash make. `min-*` rather than `size-*`: it is a different
            tailwind-merge group from the icon variant's `h-10 w-10`, so it
            composes with the primitive instead of racing it, and it agrees with
            the primitive's own coarse floor on the value, which is what makes
            stylesheet emission order irrelevant here.
          */
          triggerClassName="min-h-11 min-w-11 shrink-0"
          onEdit={onEdit}
          onToggleActive={onToggleActive}
          onDelete={onDelete}
        />
      )}
    </li>
  );
}

export function Categories() {
  const { user } = useAuth();
  const admin = isAdmin(user);
  const isMobile = useIsMobileViewport();

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
        if (a.type !== b.type) return a.type === TYPE_EXPENSE ? -1 : 1;
        return a.sort_order - b.sort_order;
      }),
    [categories],
  );

  return (
    <div className="flex flex-col gap-6">
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-semibold tracking-tight">Categories</h1>
        {admin && (
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

      {/*
        `overflow-hidden` only on the phone: the card list below runs edge to
        edge, so without it the first and last rows paint over the card's
        rounded corners. Withheld from the desktop branch because the table
        there has its own `overflow-auto` wrapper and a clipping ancestor is a
        change to a surface this task did not measure.
      */}
      <Card className={cn(isMobile && 'overflow-hidden')}>
        <CardHeader>
          <CardDescription>
            Manage expense and income categories. Deactivated categories stay
            attached to past transactions but no longer appear in the entry row.
          </CardDescription>
        </CardHeader>
        {loading ? (
          <CardContent>
            <div className="flex flex-col gap-2">
              {Array.from({ length: 4 }).map((_, i) => (
                <Skeleton key={i} className="h-12 w-full" />
              ))}
            </div>
          </CardContent>
        ) : sortedCategories.length === 0 ? (
          <CardContent>
            <p className="text-sm text-muted-foreground">
              No categories yet.
            </p>
          </CardContent>
        ) : isMobile ? (
          /*
            --- The presentation fork ------------------------------------
            Below `md` the table is replaced wholesale by stacked cards, not
            hidden with `md:hidden` — see `useIsMobileViewport` for why only one
            tree may be mounted. Everything above the fork (the header, the
            error banner, the loading and empty states) is shared and already
            width-agnostic, and the editor Sheet is reached identically from
            either side.

            Outside `CardContent` on purpose: its `p-6` would spend 48px of a
            360px viewport on gutters the rows do not need, and the row dividers
            are meant to run edge to edge. Each card restores a 16px gutter of
            its own.

            `role="list"` is not redundant: Tailwind's preflight sets
            `list-style: none` and Safari/VoiceOver drop the list role with the
            marker, so without it the rows stop being announced as a list.
          */
          <ul
            role="list"
            aria-label="Categories"
            className={cn(
              'flex flex-col divide-y divide-border border-t transition-opacity',
              fetching && 'opacity-60',
            )}
          >
            {sortedCategories.map((cat) => (
              <CategoryCard
                key={cat.id}
                category={cat}
                admin={admin}
                onEdit={(c) => setEditor({ mode: 'edit', category: c })}
                onToggleActive={(c) => void handleToggleActive(c)}
                onDelete={(c) => void handleDelete(c)}
              />
            ))}
          </ul>
        ) : (
          <CardContent>
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
                      <CategoryTypeBadge type={cat.type} />
                    </TableCell>
                    <TableCell className="text-right">
                      {admin && (
                        <CategoryActionsMenu
                          category={cat}
                          triggerClassName="size-8"
                          onEdit={(c) => setEditor({ mode: 'edit', category: c })}
                          onToggleActive={(c) => void handleToggleActive(c)}
                          onDelete={(c) => void handleDelete(c)}
                        />
                      )}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </CardContent>
        )}
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
  const [type, setType] = useState<TransactionType>(TYPE_EXPENSE);
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
      setType(TYPE_EXPENSE);
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
                onValueChange={(v) => setType(v as TransactionType)}
              >
                <SelectTrigger id="category-type">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectGroup>
                    <SelectItem value={TYPE_EXPENSE}>Expense</SelectItem>
                    <SelectItem value={TYPE_INCOME}>Income</SelectItem>
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
