import { useState, useEffect, useCallback, useMemo, useRef } from 'react';
import type { ChangeEvent, FormEvent } from 'react';
import { useSearchParams } from 'react-router-dom';
import { zodResolver } from '@hookform/resolvers/zod';
import { useForm } from 'react-hook-form';
import * as z from 'zod';
import { AlertCircle, CheckCircle2, CircleAlert, Upload } from 'lucide-react';
import { toast } from 'sonner';
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
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { cn, selectAllOnFocus } from '@/lib/utils';
import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
} from '@/components/ui/card';
import {
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
} from '@/components/ui/tabs';
import { ButtonGroup } from '@/components/ui/button-group';
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from '@/components/ui/dialog';
import {
  Form,
  FormControl,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Separator } from '@/components/ui/separator';
import { MIN_YEAR, MAX_YEAR, MONTH_NAMES_FULL } from '@/lib/dates';
import { formatCurrency } from '@/lib/format';
import { useBaseCurrency } from '@/hooks/useBaseCurrency';
import { ROLE_ADMIN, ROLE_MEMBER, isAdmin, type Role } from '@/lib/roles';

/* ---------- Module-scope constants ---------- */

const VALID_TABS = [
  'general',
  'currencies',
  'savings',
  'users',
  'data',
] as const;
type SettingsTab = (typeof VALID_TABS)[number];

/* ---------- Pure helpers ---------- */

function isValidTab(value: string | null): value is SettingsTab {
  return value !== null && (VALID_TABS as readonly string[]).includes(value);
}

// Match preview category names (case-insensitive) to existing category ids.
// Pure — captures no closure, hoisted for test-ability and perf.
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

/* ---------- General Tab ---------- */

const budgetSchema = z.object({
  amount: z.number().min(0, 'Must be at least 0'),
});
type BudgetValues = z.infer<typeof budgetSchema>;

function GeneralSection() {
  // Lazy init so year/month are captured once at mount and remain stable as
  // effect deps — also matches the Reports page precedent.
  const [ym] = useState(() => {
    const d = new Date();
    return { year: d.getFullYear(), month: d.getMonth() + 1 };
  });
  const { year, month } = ym;

  const form = useForm<BudgetValues>({
    resolver: zodResolver(budgetSchema),
    defaultValues: { amount: 0 },
  });

  useEffect(() => {
    api
      .get<Budget[]>(`budgets?year=${year}`)
      .then((data) => {
        const match = data.find((b) => b.month === month);
        if (match) form.reset({ amount: match.amount });
      })
      .catch(() => {
        /* non-critical */
      });
    // form.reset is stable across renders and intentionally not a dep —
    // including it would re-fetch on every render.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [year, month]);

  async function onSubmit(values: BudgetValues) {
    try {
      await api.put(`budgets/${year}/${month}`, { amount: values.amount });
      toast.success('Budget saved successfully');
    } catch (err) {
      toast.error(err instanceof Error ? err.message : 'Failed to save');
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">General Settings</CardTitle>
      </CardHeader>
      <CardContent>
        <Form {...form}>
          <form
            onSubmit={(e) => void form.handleSubmit(onSubmit)(e)}
            className="flex max-w-sm flex-col gap-4"
            noValidate
          >
            <FormField
              control={form.control}
              name="amount"
              render={({ field }) => (
                <FormItem>
                  <FormLabel>Monthly Budget</FormLabel>
                  <FormControl>
                    <Input
                      type="number"
                      step="0.01"
                      min="0"
                      placeholder="0.00"
                      name={field.name}
                      onBlur={field.onBlur}
                      ref={field.ref}
                      value={field.value ?? ''}
                      onChange={(e) =>
                        field.onChange(
                          e.target.value === '' ? 0 : Number(e.target.value),
                        )
                      }
                      onFocus={selectAllOnFocus}
                    />
                  </FormControl>
                  <p className="text-xs text-muted-foreground">
                    Budget for {year}-{String(month).padStart(2, '0')}
                  </p>
                  <FormMessage />
                </FormItem>
              )}
            />
            <Button type="submit" className="w-fit" disabled={form.formState.isSubmitting}>
              {form.formState.isSubmitting ? 'Saving...' : 'Save Budget'}
            </Button>
          </form>
        </Form>
      </CardContent>
    </Card>
  );
}

/* ---------- Currencies Tab ---------- */

const newCurrencySchema = z.object({
  code: z.string().min(1, 'Code is required').max(3),
  name: z.string().min(1, 'Name is required'),
  symbol: z.string().min(1, 'Symbol is required').max(3),
  rate_to_base: z.number().positive('Rate must be positive'),
});
type NewCurrencyValues = z.infer<typeof newCurrencySchema>;

function CurrenciesSection() {
  const [currencies, setCurrencies] = useState<Currency[]>([]);
  const [editRates, setEditRates] = useState<Record<string, string>>({});
  const [saving, setSaving] = useState(false);

  const addForm = useForm<NewCurrencyValues>({
    resolver: zodResolver(newCurrencySchema),
    defaultValues: { code: '', name: '', symbol: '', rate_to_base: 0 },
  });

  const fetchCurrencies = useCallback(async () => {
    const data = await api.get<Currency[]>('currencies');
    setCurrencies(data);
    const rates: Record<string, string> = {};
    data.forEach((c) => {
      rates[c.code] = String(c.rate_to_base);
    });
    setEditRates(rates);
  }, []);

  useEffect(() => {
    fetchCurrencies().catch(() => {
      /* initial load failure is non-critical; table will show empty */
    });
  }, [fetchCurrencies]);

  async function handleSaveRates(e: FormEvent) {
    e.preventDefault();
    setSaving(true);
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
      toast.success('Rates updated successfully');
    } catch (err) {
      toast.error(err instanceof Error ? err.message : 'Failed to update');
    } finally {
      setSaving(false);
      // Re-sync with server truth whether or not the save loop threw. Saves
      // could have been partially applied before a later PUT failed; without
      // this refetch the table would keep showing stale local edits.
      fetchCurrencies().catch((err) => {
        toast.error(
          'Refresh failed: ' +
            (err instanceof Error ? err.message : 'unknown'),
        );
      });
    }
  }

  async function onAddCurrency(values: NewCurrencyValues) {
    try {
      await api.post('currencies', {
        code: values.code.toUpperCase(),
        name: values.name,
        symbol: values.symbol,
        rate_to_base: values.rate_to_base,
      });
      addForm.reset();
      toast.success('Currency added');
      fetchCurrencies().catch((err) => {
        toast.error(
          'Refresh failed: ' +
            (err instanceof Error ? err.message : 'unknown'),
        );
      });
    } catch (err) {
      toast.error(err instanceof Error ? err.message : 'Failed to add currency');
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">Currencies</CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-6">
        <form
          onSubmit={(e) => void handleSaveRates(e)}
          className="flex flex-col gap-4"
          noValidate
        >
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Code</TableHead>
                <TableHead>Name</TableHead>
                <TableHead>Symbol</TableHead>
                <TableHead>Rate to Base</TableHead>
                <TableHead>Status</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {currencies.map((c) => (
                <TableRow key={c.code}>
                  <TableCell className="font-mono">{c.code}</TableCell>
                  <TableCell>{c.name}</TableCell>
                  <TableCell>{c.symbol}</TableCell>
                  <TableCell>
                    {c.is_base ? (
                      <span className="text-muted-foreground font-mono tabular-nums">
                        1.0000
                      </span>
                    ) : (
                      <Input
                        type="number"
                        step="0.0001"
                        min="0"
                        value={editRates[c.code] ?? ''}
                        onChange={(e) =>
                          setEditRates((prev) => ({
                            ...prev,
                            [c.code]: e.target.value,
                          }))
                        }
                        onFocus={selectAllOnFocus}
                        aria-label={`Rate for ${c.code}`}
                        className="max-w-[160px]"
                      />
                    )}
                  </TableCell>
                  <TableCell>
                    {c.is_base && <Badge variant="secondary">Base</Badge>}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
          <Button type="submit" className="w-fit" disabled={saving}>
            {saving ? 'Saving...' : 'Save Rates'}
          </Button>
        </form>

        <Separator />
        <div className="flex flex-col gap-4">
          <h3 className="text-sm font-semibold">Add Currency</h3>
          <Form {...addForm}>
            <form
              onSubmit={(e) => void addForm.handleSubmit(onAddCurrency)(e)}
              className="grid gap-4 sm:grid-cols-2 md:grid-cols-4"
              noValidate
            >
              <FormField
                control={addForm.control}
                name="code"
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>Code</FormLabel>
                    <FormControl>
                      <Input
                        {...field}
                        placeholder="GBP"
                        maxLength={3}
                      />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <FormField
                control={addForm.control}
                name="name"
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>Name</FormLabel>
                    <FormControl>
                      <Input {...field} placeholder="British Pound" />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <FormField
                control={addForm.control}
                name="symbol"
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>Symbol</FormLabel>
                    <FormControl>
                      <Input {...field} placeholder="\u00A3" maxLength={3} />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <FormField
                control={addForm.control}
                name="rate_to_base"
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>Rate to Base</FormLabel>
                    <FormControl>
                      <Input
                        type="number"
                        step="0.0001"
                        placeholder="0.79"
                        name={field.name}
                        onBlur={field.onBlur}
                        ref={field.ref}
                        value={field.value ?? ''}
                        onChange={(e) =>
                          field.onChange(
                            e.target.value === ''
                              ? 0
                              : Number(e.target.value),
                          )
                        }
                        onFocus={selectAllOnFocus}
                      />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <div className="sm:col-span-2 md:col-span-4">
                <Button type="submit">Add Currency</Button>
              </div>
            </form>
          </Form>
        </div>
      </CardContent>
    </Card>
  );
}

/* ---------- Savings Tab ---------- */

const goalSchema = z.object({
  year: z.number().int().min(MIN_YEAR).max(MAX_YEAR),
  target_amount: z.number().min(0),
});
type GoalValues = z.infer<typeof goalSchema>;

function SavingsSection() {
  const baseCurrency = useBaseCurrency();
  const [goals, setGoals] = useState<SavingsGoal[]>([]);
  const [addOpen, setAddOpen] = useState(false);

  const form = useForm<GoalValues>({
    resolver: zodResolver(goalSchema),
    defaultValues: {
      year: new Date().getFullYear(),
      target_amount: 0,
    },
  });

  const fetchGoals = useCallback(async () => {
    const data = await api.get<SavingsGoal[]>('savings-goals');
    setGoals(data);
  }, []);

  useEffect(() => {
    fetchGoals().catch(() => {
      /* initial load failure is non-critical; list will show empty */
    });
  }, [fetchGoals]);

  function refreshGoals() {
    fetchGoals().catch((err) => {
      toast.error(
        'Refresh failed: ' + (err instanceof Error ? err.message : 'unknown'),
      );
    });
  }

  async function onAdd(values: GoalValues) {
    try {
      await api.put(`savings-goals/${values.year}`, {
        target_amount: values.target_amount,
      });
      form.reset();
      setAddOpen(false);
      toast.success('Savings goal added');
      refreshGoals();
    } catch (err) {
      toast.error(err instanceof Error ? err.message : 'Failed to add goal');
    }
  }

  async function handleDelete(goal: SavingsGoal) {
    try {
      await api.put(`savings-goals/${goal.year}`, {
        target_amount: 0,
      });
      toast.success('Savings goal removed');
      refreshGoals();
    } catch (err) {
      toast.error(err instanceof Error ? err.message : 'Failed to delete');
    }
  }

  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between">
        <CardTitle className="text-base">Savings Goals</CardTitle>
        <Dialog open={addOpen} onOpenChange={(open) => {
          setAddOpen(open);
          if (!open) form.reset();
        }}>
          <DialogTrigger asChild>
            <Button size="sm">Add Goal</Button>
          </DialogTrigger>
          <DialogContent>
            <DialogHeader>
              <DialogTitle>Add Savings Goal</DialogTitle>
              <DialogDescription>
                Set a yearly savings target.
              </DialogDescription>
            </DialogHeader>
            <Form {...form}>
              <form
                onSubmit={(e) => void form.handleSubmit(onAdd)(e)}
                className="grid gap-4"
                noValidate
              >
                <FormField
                  control={form.control}
                  name="year"
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>Year</FormLabel>
                      <FormControl>
                        <Input
                          type="number"
                          name={field.name}
                          onBlur={field.onBlur}
                          ref={field.ref}
                          value={field.value ?? ''}
                          onChange={(e) =>
                            field.onChange(
                              e.target.value === ''
                                ? 0
                                : Number(e.target.value),
                            )
                          }
                          onFocus={selectAllOnFocus}
                        />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />
                <FormField
                  control={form.control}
                  name="target_amount"
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>Target Amount</FormLabel>
                      <FormControl>
                        <Input
                          type="number"
                          step="0.01"
                          placeholder="0.00"
                          name={field.name}
                          onBlur={field.onBlur}
                          ref={field.ref}
                          value={field.value ?? ''}
                          onChange={(e) =>
                            field.onChange(
                              e.target.value === ''
                                ? 0
                                : Number(e.target.value),
                            )
                          }
                          onFocus={selectAllOnFocus}
                        />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />
                <DialogFooter>
                  <Button type="submit">Add Goal</Button>
                </DialogFooter>
              </form>
            </Form>
          </DialogContent>
        </Dialog>
      </CardHeader>
      <CardContent>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Year</TableHead>
              <TableHead>Target Amount</TableHead>
              <TableHead className="text-right">Actions</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {goals.map((g) => (
              <TableRow key={g.id}>
                <TableCell>{g.year}</TableCell>
                <TableCell className="font-mono tabular-nums">
                  {formatCurrency(g.target_amount, baseCurrency)}
                </TableCell>
                <TableCell className="text-right">
                  <Button
                    type="button"
                    variant="destructive"
                    size="sm"
                    onClick={() => void handleDelete(g)}
                    aria-label={`Delete ${g.year} goal`}
                  >
                    Delete
                  </Button>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </CardContent>
    </Card>
  );
}

/* ---------- Users Tab (Admin only) ---------- */

const newUserSchema = z.object({
  username: z.string().min(1, 'Username required'),
  password: z.string().min(1, 'Password required'),
  display_name: z.string().min(1, 'Display name required'),
  role: z.enum([ROLE_ADMIN, ROLE_MEMBER] as const),
});
type NewUserValues = z.infer<typeof newUserSchema>;

function UsersSection() {
  const [users, setUsers] = useState<User[]>([]);
  const [addOpen, setAddOpen] = useState(false);

  const form = useForm<NewUserValues>({
    resolver: zodResolver(newUserSchema),
    defaultValues: {
      username: '',
      password: '',
      display_name: '',
      role: ROLE_MEMBER,
    },
  });

  const fetchUsers = useCallback(async () => {
    const data = await api.get<User[]>('users');
    setUsers(data);
  }, []);

  useEffect(() => {
    fetchUsers().catch(() => {
      /* initial load failure is non-critical; list will show empty */
    });
  }, [fetchUsers]);

  function refreshUsers() {
    fetchUsers().catch((err) => {
      toast.error(
        'Refresh failed: ' + (err instanceof Error ? err.message : 'unknown'),
      );
    });
  }

  async function onAddUser(values: NewUserValues) {
    try {
      await api.post('users', {
        username: values.username,
        password: values.password,
        display_name: values.display_name,
        role: values.role,
      });
      form.reset();
      setAddOpen(false);
      toast.success('User added');
      refreshUsers();
    } catch (err) {
      toast.error(err instanceof Error ? err.message : 'Failed to add user');
    }
  }

  async function handleRoleChange(userId: number, role: Role) {
    try {
      await api.put(`users/${userId}`, { role });
      toast.success('Role updated');
      refreshUsers();
    } catch (err) {
      toast.error(err instanceof Error ? err.message : 'Failed to update role');
    }
  }

  async function handleDeleteUser(userId: number) {
    try {
      await api.del(`users/${userId}`);
      toast.success('User deleted');
      refreshUsers();
    } catch (err) {
      toast.error(err instanceof Error ? err.message : 'Failed to delete user');
    }
  }

  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between">
        <CardTitle className="text-base">Users</CardTitle>
        <Dialog open={addOpen} onOpenChange={(open) => {
          setAddOpen(open);
          if (!open) form.reset();
        }}>
          <DialogTrigger asChild>
            <Button size="sm">Add User</Button>
          </DialogTrigger>
          <DialogContent>
            <DialogHeader>
              <DialogTitle>Add User</DialogTitle>
              <DialogDescription>
                Create a new household member account.
              </DialogDescription>
            </DialogHeader>
            <Form {...form}>
              <form
                onSubmit={(e) => void form.handleSubmit(onAddUser)(e)}
                className="grid gap-4"
                noValidate
              >
                <FormField
                  control={form.control}
                  name="username"
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>Username</FormLabel>
                      <FormControl>
                        <Input {...field} />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />
                <FormField
                  control={form.control}
                  name="password"
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>Password</FormLabel>
                      <FormControl>
                        <Input type="password" {...field} />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />
                <FormField
                  control={form.control}
                  name="display_name"
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>Display Name</FormLabel>
                      <FormControl>
                        <Input {...field} />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />
                <FormField
                  control={form.control}
                  name="role"
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>Role</FormLabel>
                      <Select
                        value={field.value}
                        onValueChange={(v) => {
                          if (v !== ROLE_ADMIN && v !== ROLE_MEMBER) return;
                          field.onChange(v);
                        }}
                      >
                        <FormControl>
                          <SelectTrigger aria-label="New user role">
                            <SelectValue />
                          </SelectTrigger>
                        </FormControl>
                        <SelectContent>
                          <SelectGroup>
                            <SelectItem value={ROLE_ADMIN}>Admin</SelectItem>
                            <SelectItem value={ROLE_MEMBER}>Member</SelectItem>
                          </SelectGroup>
                        </SelectContent>
                      </Select>
                      <FormMessage />
                    </FormItem>
                  )}
                />
                <DialogFooter>
                  <Button type="submit">Add User</Button>
                </DialogFooter>
              </form>
            </Form>
          </DialogContent>
        </Dialog>
      </CardHeader>
      <CardContent>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Username</TableHead>
              <TableHead>Display Name</TableHead>
              <TableHead>Role</TableHead>
              <TableHead className="text-right">Actions</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {users.map((u) => (
              <TableRow key={u.id}>
                <TableCell className="font-mono">{u.username}</TableCell>
                <TableCell>{u.display_name}</TableCell>
                <TableCell>
                  <Select
                    value={u.role}
                    onValueChange={(v) => {
                      if (v !== ROLE_ADMIN && v !== ROLE_MEMBER) return;
                      void handleRoleChange(u.id, v);
                    }}
                  >
                    <SelectTrigger
                      aria-label={`Role for ${u.username}`}
                      className="w-[140px]"
                    >
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectGroup>
                        <SelectItem value={ROLE_ADMIN}>Admin</SelectItem>
                        <SelectItem value={ROLE_MEMBER}>Member</SelectItem>
                      </SelectGroup>
                    </SelectContent>
                  </Select>
                </TableCell>
                <TableCell className="text-right">
                  <Button
                    type="button"
                    variant="destructive"
                    size="sm"
                    onClick={() => void handleDeleteUser(u.id)}
                    aria-label={`Delete ${u.username}`}
                  >
                    Delete
                  </Button>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </CardContent>
    </Card>
  );
}

/* ---------- Import Preview Step ---------- */

interface ImportPreviewStepProps {
  preview: ImportPreview;
  categories: Category[];
  categoryMap: Record<string, string>;
  setCategoryMap: React.Dispatch<React.SetStateAction<Record<string, string>>>;
  defaultCategoryId: number | null;
  setDefaultCategoryId: (id: number | null) => void;
  onConfirm: () => void;
  onCancel: () => void;
}

function ImportPreviewStep({
  preview,
  categories,
  categoryMap,
  setCategoryMap,
  defaultCategoryId,
  setDefaultCategoryId,
  onConfirm,
  onCancel,
}: ImportPreviewStepProps) {
  const uniqueImportCategories = useMemo(
    () => preview.unique_categories ?? [],
    [preview.unique_categories],
  );

  const { matched, unmatched } = useMemo(() => {
    const m: { name: string; target: string }[] = [];
    const u: string[] = [];
    for (const catName of uniqueImportCategories) {
      const mappedId = categoryMap[catName];
      if (mappedId) {
        const target = categories.find((c) => String(c.id) === mappedId);
        m.push({ name: catName, target: target?.name ?? mappedId });
      } else {
        u.push(catName);
      }
    }
    return { matched: m, unmatched: u };
  }, [uniqueImportCategories, categoryMap, categories]);

  const rowsWithoutCategory = useMemo(
    () => preview.rows.filter((r) => !r.category).length,
    [preview.rows],
  );

  const needsDefaultCategory = unmatched.length > 0 || rowsWithoutCategory > 0;

  return (
    <div className="flex flex-col gap-4">
      <p className="text-sm font-medium">
        {`Found ${preview.row_count} rows to import.`}
      </p>

      {/* Data table */}
      <div className="max-h-[400px] overflow-auto rounded-md border">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="sticky top-0 bg-card">Date</TableHead>
              <TableHead className="sticky top-0 bg-card">Description</TableHead>
              <TableHead className="sticky top-0 bg-card text-right">Amount</TableHead>
              <TableHead className="sticky top-0 bg-card">Category</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {preview.rows.map((row, i) => (
              <TableRow key={i}>
                <TableCell className="whitespace-nowrap">{row.date}</TableCell>
                <TableCell>{row.description}</TableCell>
                <TableCell className="text-right font-mono tabular-nums">
                  {row.amount.toFixed(2)}
                </TableCell>
                <TableCell>{row.category}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>

      {/* Category mapping summary */}
      {uniqueImportCategories.length > 0 && (
        <div className="flex flex-col gap-3">
          <h3 className="text-sm font-semibold">Category Mapping</h3>

          {/* Matched categories - compact summary */}
          {matched.length > 0 && (
            <div className="flex flex-col gap-2 rounded-md border p-3">
              <div className="flex items-center gap-2 text-sm text-muted-foreground">
                <CheckCircle2 className="h-4 w-4 text-emerald-500" />
                <span>
                  {matched.length} of {uniqueImportCategories.length} categories
                  matched automatically
                </span>
              </div>
              <div className="flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground">
                {matched.map((m) => (
                  <span key={m.name}>
                    {m.name === m.target
                      ? m.name
                      : `${m.name} \u2192 ${m.target}`}
                  </span>
                ))}
              </div>
            </div>
          )}

          {/* Unmatched categories - show dropdowns */}
          {unmatched.length > 0 && (
            <div className="flex flex-col gap-3 rounded-md border border-yellow-500/30 bg-yellow-500/5 p-3">
              <div className="flex items-center gap-2 text-sm">
                <CircleAlert className="h-4 w-4 text-yellow-500" />
                <span>
                  {unmatched.length} {unmatched.length === 1 ? 'category needs' : 'categories need'} mapping
                </span>
              </div>
              {unmatched.map((catName) => (
                <div key={catName} className="flex max-w-sm items-center gap-3">
                  <Label className="w-32 shrink-0 text-sm">{catName}</Label>
                  <Select
                    value={categoryMap[catName] ?? undefined}
                    onValueChange={(v) =>
                      setCategoryMap((prev) => ({
                        ...prev,
                        [catName]: v,
                      }))
                    }
                  >
                    <SelectTrigger aria-label={`Map category ${catName}`}>
                      <SelectValue placeholder="Select category..." />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectGroup>
                        {categories.map((c) => (
                          <SelectItem key={c.id} value={String(c.id)}>
                            {c.name}
                          </SelectItem>
                        ))}
                      </SelectGroup>
                    </SelectContent>
                  </Select>
                </div>
              ))}
            </div>
          )}
        </div>
      )}

      {/* Default category - only when needed */}
      {needsDefaultCategory && (
        <div className="flex max-w-sm flex-col gap-2">
          <Label htmlFor="default-category">
            Default Category
            <span className="ml-1 text-xs font-normal text-muted-foreground">
              (for unmapped or missing categories)
            </span>
          </Label>
          <Select
            value={defaultCategoryId ? String(defaultCategoryId) : undefined}
            onValueChange={(v) => setDefaultCategoryId(Number(v))}
          >
            <SelectTrigger id="default-category" aria-label="Default Category">
              <SelectValue placeholder="Select default..." />
            </SelectTrigger>
            <SelectContent>
              <SelectGroup>
                {categories.map((c) => (
                  <SelectItem key={c.id} value={String(c.id)}>
                    {c.name}
                  </SelectItem>
                ))}
              </SelectGroup>
            </SelectContent>
          </Select>
        </div>
      )}

      {/* Actions */}
      <div className="flex gap-2">
        <Button type="button" variant="outline" onClick={onConfirm}>
          Import {preview.row_count} Rows
        </Button>
        <Button type="button" variant="outline" onClick={onCancel}>
          Cancel
        </Button>
      </div>
    </div>
  );
}

/* ---------- Import / Export Tab ---------- */

type ImportStep = 'upload' | 'preview' | 'done';

function DataSection() {
  const now = new Date();
  const [year, setYear] = useState(now.getFullYear());
  const [month, setMonth] = useState(now.getMonth() + 1);
  const [exportMode, setExportMode] = useState<'monthly' | 'yearly'>('monthly');

  // Import wizard state
  const [importStep, setImportStep] = useState<ImportStep>('upload');
  const [preview, setPreview] = useState<ImportPreview | null>(null);
  const [result, setResult] = useState<ImportResult | null>(null);
  const [importError, setImportError] = useState<string | null>(null);
  const [defaultCategoryId, setDefaultCategoryId] = useState<number | null>(
    null,
  );
  const [categories, setCategories] = useState<Category[]>([]);
  const [categoryMap, setCategoryMap] = useState<Record<string, string>>({});
  const [confirmOpen, setConfirmOpen] = useState(false);
  const fileInputRef = useRef<HTMLInputElement | null>(null);

  useEffect(() => {
    api
      .get<Category[]>('categories')
      .then(setCategories)
      .catch(() => {
        /* non-critical */
      });
  }, []);

  function clearFileInput() {
    if (fileInputRef.current) fileInputRef.current.value = '';
  }

  async function handleFileChange(e: ChangeEvent<HTMLInputElement>) {
    const file = e.target.files?.[0];
    if (!file) return;
    setImportError(null);
    try {
      const data = await api.upload<ImportPreview>('import/upload', file);
      setPreview(data);
      setCategoryMap(autoMapCategories(data, categories));
      setImportStep('preview');
    } catch (err) {
      setImportError(err instanceof Error ? err.message : 'Upload failed');
      clearFileInput();
    }
  }

  async function handleConfirmImport() {
    if (!preview) return;
    try {
      // Convert string IDs to numbers for the backend (Go expects int64)
      const numericCategoryMap: Record<string, number> = {};
      for (const [name, id] of Object.entries(categoryMap)) {
        if (id) numericCategoryMap[name] = parseInt(id, 10);
      }

      const payload: {
        import_id: string;
        default_category_id?: number;
        category_map: Record<string, number>;
      } = {
        import_id: preview.import_id,
        category_map: numericCategoryMap,
      };
      if (defaultCategoryId !== null) {
        payload.default_category_id = defaultCategoryId;
      }

      const res = await api.post<ImportResult>('import/confirm', payload);
      setResult(res);
      setImportStep('done');
      setConfirmOpen(false);
      setImportError(null);
    } catch (err) {
      toast.error(err instanceof Error ? err.message : 'Import failed');
      setConfirmOpen(false);
    }
  }

  function handleCancelImport() {
    // Tell backend to free the import slot
    if (preview?.import_id) {
      void api.del(`import/${preview.import_id}`).catch(() => {});
    }
    setImportStep('upload');
    setPreview(null);
    setImportError(null);
    setCategoryMap({});
    setDefaultCategoryId(null);
    clearFileInput();
  }

  function handleImportAnother() {
    setImportStep('upload');
    setPreview(null);
    setResult(null);
    setImportError(null);
    setCategoryMap({});
    setDefaultCategoryId(null);
    clearFileInput();
  }

  function handleExportMonthly() {
    window.open(`/api/export/monthly/${year}/${month}`, '_blank');
  }

  function handleExportYearly() {
    window.open(`/api/export/yearly/${year}`, '_blank');
  }

  return (
    <div className="flex flex-col gap-6">
      {/* ---------- Import card ---------- */}
      <Card>
        <CardHeader>
          <CardTitle className="text-base">Import</CardTitle>
        </CardHeader>
        <CardContent className="flex flex-col gap-4">
          {importError && (
            <Alert variant="destructive">
              <AlertCircle className="h-4 w-4" />
              <AlertDescription>{importError}</AlertDescription>
            </Alert>
          )}

          {importStep === 'upload' && (
            <div className="flex flex-col gap-4">
              <p className="text-sm text-muted-foreground">
                Upload an Excel file with columns: date, description, amount.
                Optional columns: category, tags, notes, original_amount,
                original_currency.
              </p>
              <Input
                ref={fileInputRef}
                id="excel-file"
                type="file"
                accept=".xlsx,.xls"
                aria-label="Excel File"
                onChange={(e) => void handleFileChange(e)}
                className="hidden"
              />
              <button
                type="button"
                onClick={() => fileInputRef.current?.click()}
                onDragOver={(e) => {
                  e.preventDefault();
                  e.currentTarget.setAttribute('data-drag-over', 'true');
                }}
                onDragLeave={(e) => {
                  e.preventDefault();
                  e.currentTarget.removeAttribute('data-drag-over');
                }}
                onDrop={(e) => {
                  e.preventDefault();
                  e.currentTarget.removeAttribute('data-drag-over');
                  const file = e.dataTransfer.files[0];
                  if (file) {
                    const dt = new DataTransfer();
                    dt.items.add(file);
                    if (fileInputRef.current) {
                      fileInputRef.current.files = dt.files;
                      fileInputRef.current.dispatchEvent(
                        new Event('change', { bubbles: true }),
                      );
                    }
                  }
                }}
                className={cn(
                  'flex max-w-sm flex-col items-center gap-2 rounded-lg border-2 border-dashed border-muted-foreground/25 px-6 py-8 text-center transition-colors',
                  'hover:border-muted-foreground/50 hover:bg-muted/50',
                  'data-[drag-over=true]:border-primary data-[drag-over=true]:bg-primary/5',
                  'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2',
                )}
              >
                <Upload className="size-8 text-muted-foreground" />
                <div className="flex flex-col gap-1">
                  <span className="text-sm font-medium">
                    Drag & drop your Excel file here, or click to browse
                  </span>
                  <span className="text-xs text-muted-foreground">
                    .xlsx, .xls
                  </span>
                </div>
              </button>
            </div>
          )}

          {importStep === 'preview' && preview && (
            <>
              <ImportPreviewStep
                preview={preview}
                categories={categories}
                categoryMap={categoryMap}
                setCategoryMap={setCategoryMap}
                defaultCategoryId={defaultCategoryId}
                setDefaultCategoryId={setDefaultCategoryId}
                onConfirm={() => setConfirmOpen(true)}
                onCancel={handleCancelImport}
              />

              <Dialog open={confirmOpen} onOpenChange={setConfirmOpen}>
                <DialogContent aria-label="Confirm Import">
                  <DialogHeader>
                    <DialogTitle>Confirm Import</DialogTitle>
                    <DialogDescription>
                      Import {preview.row_count} transactions
                      {defaultCategoryId
                        ? ` with default category "${
                            categories.find(
                              (c) => c.id === defaultCategoryId,
                            )?.name ?? ''
                          }"`
                        : ''}
                      ? This cannot be undone.
                    </DialogDescription>
                  </DialogHeader>
                  <DialogFooter>
                    <Button
                      type="button"
                      variant="outline"
                      onClick={() => setConfirmOpen(false)}
                    >
                      Cancel
                    </Button>
                    <Button
                      type="button"
                      onClick={() => void handleConfirmImport()}
                    >
                      Confirm and Import
                    </Button>
                  </DialogFooter>
                </DialogContent>
              </Dialog>
            </>
          )}

          {importStep === 'done' && result && (
            <div className="flex flex-col gap-4">
              <p className="text-sm font-medium">
                {`${result.imported} imported, ${result.skipped} skipped out of ${result.total} total rows.`}
              </p>
              <Button type="button" variant="outline" className="w-fit" onClick={handleImportAnother}>
                Import Another
              </Button>
            </div>
          )}
        </CardContent>
      </Card>

      {/* ---------- Export card ---------- */}
      <Card>
        <CardHeader>
          <CardTitle className="text-base">Export</CardTitle>
        </CardHeader>
        <CardContent className="flex flex-col gap-4">
          <ButtonGroup>
            {([
              { value: 'monthly', label: 'Monthly' },
              { value: 'yearly', label: 'Yearly' },
            ] as const).map((opt) => (
              <Button
                key={opt.value}
                type="button"
                variant={exportMode === opt.value ? 'secondary' : 'outline'}
                size="sm"
                onClick={() => setExportMode(opt.value)}
              >
                {opt.label}
              </Button>
            ))}
          </ButtonGroup>
          <div className="flex max-w-md items-end gap-3">
            <div className="flex flex-col gap-2">
              <Label htmlFor="export-year">Year</Label>
              <Input
                id="export-year"
                type="number"
                value={year}
                onChange={(e) => setYear(Number(e.target.value))}
                onFocus={selectAllOnFocus}
                min={MIN_YEAR}
                max={MAX_YEAR}
                className="w-28"
              />
            </div>
            {exportMode === 'monthly' && (
              <div className="flex flex-col gap-2">
                <Label>Month</Label>
                <Select
                  value={String(month)}
                  onValueChange={(v) => setMonth(Number(v))}
                >
                  <SelectTrigger aria-label="Export Month" className="w-36">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectGroup>
                      {MONTH_NAMES_FULL.map((m, i) => (
                        <SelectItem key={m} value={String(i + 1)}>
                          {m}
                        </SelectItem>
                      ))}
                    </SelectGroup>
                  </SelectContent>
                </Select>
              </div>
            )}
            <Button
              type="button"
              onClick={exportMode === 'monthly' ? handleExportMonthly : handleExportYearly}
            >
              Export
            </Button>
          </div>
        </CardContent>
      </Card>
    </div>
  );
}

/* ---------- Main Settings Page ---------- */

export function Settings() {
  const { user } = useAuth();
  const admin = isAdmin(user);
  const [searchParams] = useSearchParams();
  const tabParam = searchParams.get('tab');
  const initialTab = isValidTab(tabParam) ? tabParam : 'general';
  const [activeTab, setActiveTab] = useState<SettingsTab>(initialTab);

  useEffect(() => {
    if (isValidTab(tabParam) && tabParam !== activeTab) {
      setActiveTab(tabParam);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tabParam]);

  return (
    <div className="flex flex-col gap-6">
      <h1 className="text-2xl font-semibold tracking-tight">Settings</h1>
      <Tabs
        value={activeTab}
        onValueChange={(v) => setActiveTab(v as SettingsTab)}
        className="w-full"
      >
        <TabsList>
          <TabsTrigger value="general">General</TabsTrigger>
          <TabsTrigger value="currencies">Currencies</TabsTrigger>
          <TabsTrigger value="savings">Savings</TabsTrigger>
          {admin && <TabsTrigger value="users">Users</TabsTrigger>}
          <TabsTrigger value="data">Import / Export</TabsTrigger>
        </TabsList>
        <TabsContent value="general" className="mt-6">
          <GeneralSection />
        </TabsContent>
        <TabsContent value="currencies" className="mt-6">
          <CurrenciesSection />
        </TabsContent>
        <TabsContent value="savings" className="mt-6">
          <SavingsSection />
        </TabsContent>
        {admin && (
          <TabsContent value="users" className="mt-6">
            <UsersSection />
          </TabsContent>
        )}
        <TabsContent value="data" className="mt-6">
          <DataSection />
        </TabsContent>
      </Tabs>
    </div>
  );
}
