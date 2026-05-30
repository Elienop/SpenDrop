import { useState, useEffect, useCallback, useMemo, useRef } from 'react';
import type { ChangeEvent, FormEvent } from 'react';
import { useSearchParams, useNavigate } from 'react-router-dom';
import { zodResolver } from '@hookform/resolvers/zod';
import { useForm } from 'react-hook-form';
import * as z from 'zod';
import {
  AlertCircle,
  AlertTriangle,
  CheckCircle2,
  CircleAlert,
  Copy,
  KeyRound,
  Upload,
} from 'lucide-react';
import { toast } from 'sonner';
import { formatDistanceToNowStrict } from 'date-fns';
import { api, ApiError } from '../api/client';
import { useAuth } from '../hooks/useAuth';
import type {
  ApiToken,
  Category,
  ChangePasswordResponse,
  CreateTokenResponse,
  Currency,
  ImportPreview,
  ListTokensResponse,
  NotificationSettings,
  PatchRowRequest,
  ResetPasswordResponse,
  RevokeAllResponse,
  RevokeOneResponse,
  User,
} from '../api/types';
import { ImportPreviewTable } from '@/components/ImportPreviewTable';
import { useImportSession, type CellError } from '@/hooks/useImportSession';
import { useWebPush } from '@/hooks/useWebPush';
import { useNotificationPrefs } from '@/hooks/useNotificationPrefs';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Switch } from '@/components/ui/switch';
import { cn, selectAllOnFocus } from '@/lib/utils';
import {
  Card,
  CardContent,
  CardDescription,
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
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog';
import { Badge } from '@/components/ui/badge';
import { Separator } from '@/components/ui/separator';
import { MIN_YEAR, MAX_YEAR, MONTH_NAMES_FULL } from '@/lib/dates';
import { ROLE_ADMIN, ROLE_MEMBER, isAdmin, type Role } from '@/lib/roles';
import { destructiveActionClass } from '@/lib/styles';

/* ---------- Module-scope constants ---------- */

const VALID_TABS = [
  'account',
  'currencies',
  'users',
  'api-tokens',
  'notifications',
  'data',
] as const;
type SettingsTab = (typeof VALID_TABS)[number];

const EXPIRY_OPTIONS = {
  never: null,
  '7d': 7,
  '30d': 30,
  '90d': 90,
  '1y': 365,
} as const;

type ExpiryChoice = keyof typeof EXPIRY_OPTIONS;

function computeExpiresAt(choice: ExpiryChoice): string | null {
  const days = EXPIRY_OPTIONS[choice];
  if (days === null) return null;
  // Compute in UTC — the server validates `expires_at > now`, and a
  // tz-naive local-midnight string could land in the past for any user
  // east of UTC on the day the token is minted.
  const d = new Date();
  d.setUTCDate(d.getUTCDate() + days);
  return d.toISOString();
}

const createTokenSchema = z.object({
  name: z
    .string()
    .trim()
    .min(1, 'Name is required')
    .max(100, 'Name must be 100 characters or fewer'),
  expires: z.enum(['never', '7d', '30d', '90d', '1y']),
});
type CreateTokenValues = z.infer<typeof createTokenSchema>;

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

/* ---------- Account Tab ---------- */

// Client-side floor for a new password. The backend enforces its own
// runtime-configured bounds (and a 400 surfaces the server's exact message
// on the new-password field), but a sane min here gives instant feedback
// before a round-trip. Kept conservative so it never *exceeds* the server
// minimum and pre-rejects a password the server would actually accept.
const MIN_PASSWORD_LENGTH = 8;

const changePasswordSchema = z
  .object({
    current_password: z.string().min(1, 'Current password is required'),
    new_password: z
      .string()
      .min(
        MIN_PASSWORD_LENGTH,
        `New password must be at least ${MIN_PASSWORD_LENGTH} characters`,
      ),
    confirm_password: z.string().min(1, 'Please confirm your new password'),
  })
  .refine((data) => data.new_password === data.confirm_password, {
    message: 'Passwords do not match',
    path: ['confirm_password'],
  });
type ChangePasswordValues = z.infer<typeof changePasswordSchema>;

function AccountSection() {
  const { logout } = useAuth();
  const navigate = useNavigate();

  const form = useForm<ChangePasswordValues>({
    resolver: zodResolver(changePasswordSchema),
    defaultValues: {
      current_password: '',
      new_password: '',
      confirm_password: '',
    },
  });

  async function onSubmit(values: ChangePasswordValues) {
    try {
      const res = await api.post<ChangePasswordResponse>('auth/password', {
        current_password: values.current_password,
        new_password: values.new_password,
      });
      const n = res.tokens_revoked;
      toast.success(
        n > 0
          ? `Password changed — ${n} API token${n === 1 ? '' : 's'} revoked. Sign in again.`
          : 'Password changed. Sign in again.',
      );
      // The server killed every session for this user, including the one
      // that made this request. Clear local auth state and bounce to
      // /login. Prefer useAuth().logout (which POSTs auth/logout, resets
      // the user, and navigates) — if that POST 401s (session already
      // dead) it rejects, so fall back to a hard navigate.
      try {
        await logout();
      } catch {
        navigate('/login');
      }
    } catch (err) {
      // Branch on the HTTP status, not the message text. The api client
      // returns 401 only when the current password is wrong, so map that to
      // a field-level error on the current-password input. Every other
      // status (e.g. the 400 with a bound-message body) lands on the
      // new-password field using the server-provided message.
      if (err instanceof ApiError && err.status === 401) {
        form.setError('current_password', {
          message: 'Current password is incorrect',
        });
      } else {
        const msg =
          err instanceof Error ? err.message : 'Failed to change password';
        form.setError('new_password', { message: msg });
      }
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">Change password</CardTitle>
        <CardDescription>
          Update the password you use to sign in to SpenDrop.
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        <Alert variant="warning">
          <AlertTriangle className="h-4 w-4" />
          <AlertTitle>This signs you out everywhere</AlertTitle>
          <AlertDescription>
            Changing your password ends every active session and revokes all
            your API tokens. You'll need to sign in again here and recreate
            any tokens your scripts or dashboards depend on.
          </AlertDescription>
        </Alert>
        <Form {...form}>
          <form
            onSubmit={(e) => void form.handleSubmit(onSubmit)(e)}
            className="flex max-w-md flex-col gap-4"
            noValidate
          >
            <FormField
              control={form.control}
              name="current_password"
              render={({ field }) => (
                <FormItem>
                  <FormLabel>Current password</FormLabel>
                  <FormControl>
                    <Input
                      type="password"
                      autoComplete="current-password"
                      {...field}
                    />
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
            <FormField
              control={form.control}
              name="new_password"
              render={({ field }) => (
                <FormItem>
                  <FormLabel>New password</FormLabel>
                  <FormControl>
                    <Input
                      type="password"
                      autoComplete="new-password"
                      {...field}
                    />
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
            <FormField
              control={form.control}
              name="confirm_password"
              render={({ field }) => (
                <FormItem>
                  <FormLabel>Confirm new password</FormLabel>
                  <FormControl>
                    <Input
                      type="password"
                      autoComplete="new-password"
                      {...field}
                    />
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
            <Button
              type="submit"
              className="w-fit"
              disabled={form.formState.isSubmitting}
            >
              {form.formState.isSubmitting
                ? 'Changing…'
                : 'Change password'}
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

/* ---------- Users Tab (Admin only) ---------- */

const newUserSchema = z.object({
  username: z.string().min(1, 'Username required'),
  password: z.string().min(1, 'Password required'),
  display_name: z.string().min(1, 'Display name required'),
  role: z.enum([ROLE_ADMIN, ROLE_MEMBER] as const),
});
type NewUserValues = z.infer<typeof newUserSchema>;

const resetPasswordSchema = z
  .object({
    new_password: z
      .string()
      .min(
        MIN_PASSWORD_LENGTH,
        `New password must be at least ${MIN_PASSWORD_LENGTH} characters`,
      ),
    confirm_password: z.string().min(1, 'Please confirm the new password'),
  })
  .refine((data) => data.new_password === data.confirm_password, {
    message: 'Passwords do not match',
    path: ['confirm_password'],
  });
type ResetPasswordValues = z.infer<typeof resetPasswordSchema>;

function UsersSection() {
  const { user: currentUser } = useAuth();
  const [users, setUsers] = useState<User[]>([]);
  const [addOpen, setAddOpen] = useState(false);
  // The user currently targeted by the reset-password dialog. Null = closed.
  const [resettingUser, setResettingUser] = useState<User | null>(null);
  // Sticky display-name so the dialog title can keep showing the target's
  // name through Radix's close-exit animation after resettingUser flips to
  // null. Mirrors the revoke-one pattern in ApiTokensSection.
  const [resettingUserName, setResettingUserName] = useState('');
  const [resetting, setResetting] = useState(false);

  const resetForm = useForm<ResetPasswordValues>({
    resolver: zodResolver(resetPasswordSchema),
    defaultValues: { new_password: '', confirm_password: '' },
  });

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

  function openReset(u: User) {
    resetForm.reset();
    setResettingUserName(u.display_name);
    setResettingUser(u);
  }

  async function onConfirmReset(values: ResetPasswordValues) {
    if (!resettingUser) return;
    setResetting(true);
    try {
      const res = await api.post<ResetPasswordResponse>(
        `users/${resettingUser.id}/reset-password`,
        { new_password: values.new_password },
      );
      const n = res.tokens_revoked;
      toast.success(
        n > 0
          ? `Password reset for ${resettingUser.display_name} — signed out, ${n} API token${n === 1 ? '' : 's'} revoked.`
          : `Password reset for ${resettingUser.display_name} — signed out everywhere.`,
      );
      setResettingUser(null);
    } catch (err) {
      const msg = err instanceof Error ? err.message : 'Failed to reset password';
      resetForm.setError('new_password', { message: msg });
    } finally {
      setResetting(false);
    }
  }

  return (
    <>
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
                  <div className="flex items-center justify-end gap-2">
                    {/* Reset is hidden on the admin's own row — they
                        rotate their own password from the Account tab,
                        which runs the same cascade with a current-password
                        check (the admin reset deliberately has none). */}
                    {currentUser && u.id !== currentUser.id && (
                      <Button
                        type="button"
                        variant="outline"
                        size="sm"
                        onClick={() => openReset(u)}
                        aria-label={`Reset password for ${u.username}`}
                      >
                        Reset password
                      </Button>
                    )}
                    <Button
                      type="button"
                      variant="destructive"
                      size="sm"
                      onClick={() => void handleDeleteUser(u.id)}
                      aria-label={`Delete ${u.username}`}
                    >
                      Delete
                    </Button>
                  </div>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </CardContent>
    </Card>
      <AlertDialog
        open={resettingUser !== null}
        onOpenChange={(open) => {
          if (resetting) return;
          if (!open) setResettingUser(null);
        }}
      >
        <AlertDialogContent>
          <Form {...resetForm}>
            <form
              onSubmit={(e) => void resetForm.handleSubmit(onConfirmReset)(e)}
              className="grid gap-4"
              noValidate
            >
              <AlertDialogHeader>
                <AlertDialogTitle>
                  Reset password for{' '}
                  <span className="font-mono">{resettingUserName}</span>?
                </AlertDialogTitle>
                <AlertDialogDescription>
                  This signs {resettingUserName} out of every device and
                  revokes all of their API tokens. They'll need the new
                  password to sign back in. This cannot be undone.
                </AlertDialogDescription>
              </AlertDialogHeader>
              <FormField
                control={resetForm.control}
                name="new_password"
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>New password</FormLabel>
                    <FormControl>
                      <Input
                        type="password"
                        autoComplete="new-password"
                        {...field}
                      />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <FormField
                control={resetForm.control}
                name="confirm_password"
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>Confirm new password</FormLabel>
                    <FormControl>
                      <Input
                        type="password"
                        autoComplete="new-password"
                        {...field}
                      />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <AlertDialogFooter>
                <AlertDialogCancel type="button" disabled={resetting}>
                  Cancel
                </AlertDialogCancel>
                {/* Plain submit Button, not AlertDialogAction: the action
                    component auto-closes the dialog on click, which would
                    tear down the form before validation/submit could run.
                    Keeping it a submit button lets react-hook-form gate the
                    POST behind the confirm-match + min-length checks. */}
                <Button
                  type="submit"
                  disabled={resetting}
                  className={destructiveActionClass}
                >
                  {resetting ? 'Resetting…' : 'Reset password'}
                </Button>
              </AlertDialogFooter>
            </form>
          </Form>
        </AlertDialogContent>
      </AlertDialog>
    </>
  );
}

/* ---------- API tokens tab ---------- */

function ShowOnceReveal({
  token,
  onClose,
}: {
  token: CreateTokenResponse;
  onClose: () => void;
}) {
  // The amber Alert below already carries the "one-time" warning with a
  // visible title. DialogDescription would restate the same message
  // verbatim to screen readers, so we only render DialogTitle here and
  // let the Alert be the description surface. Radix's a11y warning for a
  // missing DialogDescription is silenced via `aria-describedby` on the
  // AlertTitle element below.
  return (
    <>
      <DialogHeader>
        <DialogTitle>Save your new token</DialogTitle>
      </DialogHeader>
      <div className="grid gap-4">
        <Alert variant="warning">
          <AlertTriangle className="h-4 w-4" />
          <AlertTitle>Copy it now</AlertTitle>
          <AlertDescription>
            This is the only time your token will be shown. We hash
            tokens at rest — if you lose it, revoke and create a new
            one.
          </AlertDescription>
        </Alert>
        <div className="grid gap-2">
          <Label htmlFor="api-token-reveal">Your new API token</Label>
          <div className="flex items-center gap-2">
            <Input
              id="api-token-reveal"
              value={token.token}
              readOnly
              className="font-mono"
              onFocus={selectAllOnFocus}
            />
            <Button
              type="button"
              variant="outline"
              onClick={async () => {
                try {
                  await navigator.clipboard.writeText(token.token);
                  toast.success('Copied to clipboard');
                } catch {
                  // navigator.clipboard.writeText rejects on insecure
                  // contexts (common for self-hosted SpenDrop served
                  // over HTTP on a LAN IP — localhost is treated as
                  // secure, but `http://192.168.x.x` is not) and on
                  // explicit permission denial. Either way the user
                  // cannot rely on the button alone. Focus + select
                  // the reveal <Input> so one Ctrl/Cmd+C still copies,
                  // and tell them so via a toast. The dialog stays
                  // open and the plaintext stays on screen — the user
                  // does not have to re-trigger the Create flow.
                  const revealInput = document.getElementById(
                    'api-token-reveal',
                  ) as HTMLInputElement | null;
                  if (revealInput) {
                    revealInput.focus();
                    revealInput.select();
                  }
                  toast.info(
                    'Press Ctrl/Cmd+C to copy \u2014 clipboard blocked in this context.',
                  );
                }
              }}
            >
              <Copy className="mr-2 h-4 w-4" aria-hidden="true" />
              Copy
            </Button>
          </div>
        </div>
      </div>
      <DialogFooter>
        <Button type="button" onClick={onClose}>
          I've saved my token
        </Button>
      </DialogFooter>
    </>
  );
}

function ApiTokensSection() {
  const [tokens, setTokens] = useState<ApiToken[]>([]);
  const [createOpen, setCreateOpen] = useState(false);
  const [createdToken, setCreatedToken] = useState<CreateTokenResponse | null>(null);
  const [creating, setCreating] = useState(false);
  const [revokingToken, setRevokingToken] = useState<ApiToken | null>(null);
  // Sticky display-name for the revoke-one dialog title, captured when the
  // user clicks a Revoke button. Kept as state (not derived from
  // revokingToken) so Radix's close-exit animation can still show the name
  // while revokingToken has already flipped to null.
  const [revokingTokenName, setRevokingTokenName] = useState('');
  const [revokeAllOpen, setRevokeAllOpen] = useState(false);
  const [revoking, setRevoking] = useState(false);

  const createForm = useForm<CreateTokenValues>({
    resolver: zodResolver(createTokenSchema),
    defaultValues: { name: '', expires: 'never' },
  });

  const fetchTokens = useCallback(async () => {
    const data = await api.get<ListTokensResponse>('api-tokens');
    setTokens(data.tokens);
  }, []);

  useEffect(() => {
    fetchTokens().catch(() => {
      /* Initial load failure is non-critical; empty table renders and
         the user can retry by creating a token. */
    });
  }, [fetchTokens]);

  function formatLastUsed(t: ApiToken): string {
    if (!t.last_used_at) return 'Never used';
    const relative = formatDistanceToNowStrict(new Date(t.last_used_at), {
      addSuffix: true,
    });
    return t.last_used_ip ? `${relative} \u00b7 ${t.last_used_ip}` : relative;
  }

  function formatExpires(t: ApiToken): string {
    if (!t.expires_at) return 'Never';
    const d = new Date(t.expires_at);
    if (d.getTime() <= Date.now()) return 'Expired';
    return d.toLocaleDateString();
  }

  async function onCreate(values: CreateTokenValues) {
    setCreating(true);
    try {
      const body: CreateTokenResponse = await api.post<CreateTokenResponse>(
        'api-tokens',
        {
          name: values.name.trim(),
          expires_at: computeExpiresAt(values.expires),
        },
      );
      setCreatedToken(body);
      createForm.reset();
      // Optimistic update: the new row (sans full plaintext) slides
      // into the list behind the reveal dialog, so when the user closes
      // the reveal the list is already current. We assemble the list
      // row by picking the `ApiToken` fields explicitly rather than
      // destructuring `token` out, so we don't trip `noUnusedLocals`
      // on the discarded `token` variable.
      const listRow: ApiToken = {
        id: body.id,
        name: body.name,
        token_prefix: body.token_prefix,
        created_at: body.created_at,
        last_used_at: body.last_used_at,
        last_used_ip: body.last_used_ip,
        expires_at: body.expires_at,
      };
      setTokens((prev) => [listRow, ...prev]);
    } catch (err) {
      toast.error(
        err instanceof Error ? err.message : 'Failed to create token',
      );
    } finally {
      setCreating(false);
    }
  }

  async function onConfirmRevoke() {
    if (!revokingToken) return;
    setRevoking(true);
    try {
      // Generic pins the 200 OK JSON body shape from Chunk 4
      // (`{"ok":true}`). `api.del` is untyped by default — passing the
      // type parameter surfaces wire-contract drift at compile time
      // instead of letting the response degrade to `unknown`.
      await api.del<RevokeOneResponse>(`api-tokens/${revokingToken.id}`);
      setTokens((prev) => prev.filter((t) => t.id !== revokingToken.id));
      toast.success('Token revoked');
      setRevokingToken(null);
    } catch (err) {
      toast.error(
        err instanceof Error ? err.message : 'Failed to revoke token',
      );
    } finally {
      setRevoking(false);
    }
  }

  async function onConfirmRevokeAll() {
    setRevoking(true);
    try {
      // Chunk 4 returns `{"revoked":n}` with 200 OK on mass-revoke. We
      // surface the count in the success toast rather than hard-coding
      // "All tokens revoked" so the user gets feedback even when the
      // list they saw pre-request is out of sync with what was actually
      // live server-side (e.g. another tab opened, background expiry).
      const result = await api.del<RevokeAllResponse>('api-tokens');
      setTokens([]);
      const n = result.revoked;
      toast.success(`Revoked ${n} token${n === 1 ? '' : 's'}`);
      setRevokeAllOpen(false);
    } catch (err) {
      toast.error(
        err instanceof Error ? err.message : 'Failed to revoke all tokens',
      );
    } finally {
      setRevoking(false);
    }
  }

  return (
    <>
      <Card>
      <CardHeader className="flex flex-row items-start justify-between gap-4">
        <div className="grid gap-1.5">
          <CardTitle className="text-base">API tokens</CardTitle>
          <CardDescription>
            Personal access tokens for scripts, dashboards, and
            third-party integrations. Each token has full access to
            your account.
          </CardDescription>
        </div>
        <div className="flex gap-2">
          <Dialog
            open={createOpen}
            onOpenChange={(open) => {
              // Guard close-while-submitting: if the POST is in flight and we
              // let the user close the dialog here, setCreatedToken(body) in
              // onCreate resolves into a closed dialog and the next "Create
              // token" click jumps straight to the reveal of a token the user
              // thought they'd cancelled. Mirror the revoke-dialog busy guard.
              if (creating) return;
              // Guard close-while-revealed: once the plaintext is on
              // screen, Escape/backdrop must not silently destroy it.
              // The only way out of the reveal is the "I've saved my
              // token" button, which calls `onClose` and clears both
              // states. Mirrors GitHub's one-time-secret UX.
              if (createdToken !== null && !open) return;
              if (open) {
                setCreateOpen(true);
                return;
              }
              setCreateOpen(false);
              setCreatedToken(null);
              createForm.reset();
            }}
          >
            <DialogTrigger asChild>
              <Button size="sm">Create token</Button>
            </DialogTrigger>
            <DialogContent>
              {createdToken === null ? (
                <>
                  <DialogHeader>
                    <DialogTitle>Create API token</DialogTitle>
                    <DialogDescription>
                      Name it after the script or service you'll use it
                      from so you can revoke it individually later.
                    </DialogDescription>
                  </DialogHeader>
                  <Form {...createForm}>
                    <form
                      onSubmit={(e) => void createForm.handleSubmit(onCreate)(e)}
                      className="grid gap-4"
                      noValidate
                    >
                      <FormField
                        control={createForm.control}
                        name="name"
                        render={({ field }) => (
                          <FormItem>
                            <FormLabel>Name</FormLabel>
                            <FormControl>
                              <Input
                                {...field}
                                placeholder="e.g. Homepage dashboard, backup script"
                              />
                            </FormControl>
                            <FormMessage />
                          </FormItem>
                        )}
                      />
                      <FormField
                        control={createForm.control}
                        name="expires"
                        render={({ field }) => (
                          <FormItem>
                            <FormLabel>Expires</FormLabel>
                            <Select
                              value={field.value}
                              onValueChange={(v) => {
                                if (!(v in EXPIRY_OPTIONS)) return;
                                field.onChange(v as ExpiryChoice);
                              }}
                            >
                              <FormControl>
                                <SelectTrigger aria-label="Expires">
                                  <SelectValue />
                                </SelectTrigger>
                              </FormControl>
                              <SelectContent>
                                <SelectGroup>
                                  <SelectItem value="never">Never</SelectItem>
                                  <SelectItem value="7d">7 days</SelectItem>
                                  <SelectItem value="30d">30 days</SelectItem>
                                  <SelectItem value="90d">90 days</SelectItem>
                                  <SelectItem value="1y">1 year</SelectItem>
                                </SelectGroup>
                              </SelectContent>
                            </Select>
                            <FormMessage />
                          </FormItem>
                        )}
                      />
                      <DialogFooter>
                        <Button type="submit" disabled={creating}>
                          {creating ? 'Creating\u2026' : 'Create token'}
                        </Button>
                      </DialogFooter>
                    </form>
                  </Form>
                </>
              ) : (
                <ShowOnceReveal
                  token={createdToken}
                  onClose={() => {
                    // Clicking "I've saved my token" closes the whole
                    // dialog. Radix's `onOpenChange` does NOT re-fire
                    // when `open` is controlled and we set it to false
                    // ourselves, so clear the plaintext here as well to
                    // ensure the reveal cannot be re-summoned.
                    setCreateOpen(false);
                    setCreatedToken(null);
                    createForm.reset();
                  }}
                />
              )}
            </DialogContent>
          </Dialog>
          {tokens.length > 0 && (
            <Button
              size="sm"
              variant="destructive"
              onClick={() => setRevokeAllOpen(true)}
            >
              Revoke all ({tokens.length})
            </Button>
          )}
        </div>
      </CardHeader>
      <CardContent>
        {tokens.length === 0 ? (
          <div className="flex flex-col items-center gap-3 py-8 text-center">
            <KeyRound
              className="h-8 w-8 text-muted-foreground/60"
              aria-hidden="true"
            />
            <div className="grid gap-1">
              <p className="text-sm font-medium">No API tokens yet</p>
              <p className="text-sm text-muted-foreground">
                Create one to connect a script, dashboard, or other
                tool to SpenDrop.
              </p>
            </div>
          </div>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>Prefix</TableHead>
                <TableHead>Last used</TableHead>
                <TableHead>Expires</TableHead>
                <TableHead>Created</TableHead>
                <TableHead className="text-right">Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {tokens.map((t) => (
                <TableRow key={t.id}>
                  <TableCell>{t.name}</TableCell>
                  <TableCell className="font-mono">{t.token_prefix}</TableCell>
                  <TableCell>{formatLastUsed(t)}</TableCell>
                  <TableCell>{formatExpires(t)}</TableCell>
                  <TableCell
                    title={new Date(t.created_at).toLocaleString()}
                  >
                    {formatDistanceToNowStrict(new Date(t.created_at), {
                      addSuffix: true,
                    })}
                  </TableCell>
                  <TableCell className="text-right">
                    <Button
                      type="button"
                      variant="destructive"
                      size="sm"
                      onClick={() => {
                        // Capture the name at click time — the AlertDialog
                        // title reads revokingTokenName, which stays set
                        // through the close-exit animation after
                        // revokingToken flips back to null.
                        setRevokingTokenName(t.name);
                        setRevokingToken(t);
                      }}
                      aria-label={`Revoke ${t.name}`}
                    >
                      Revoke
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </CardContent>
    </Card>
      <AlertDialog
        open={revokingToken !== null}
        onOpenChange={(open) => {
          if (revoking) return;
          if (!open) setRevokingToken(null);
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              Revoke{' '}
              <span className="font-mono">
                &quot;{revokingTokenName}&quot;
              </span>
              ?
            </AlertDialogTitle>
            <AlertDialogDescription>
              Anything using this token will stop working immediately.
              This cannot be undone.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={revoking}>
              Cancel
            </AlertDialogCancel>
            <AlertDialogAction
              onClick={(e) => {
                // Radix's AlertDialogAction auto-closes on click. We
                // want the dialog to stay open while the DELETE is in
                // flight so the "Revoking…" label is visible and the
                // user can't double-fire. Prevent the default close
                // and trigger the mutation manually — `onConfirmRevoke`
                // flips `revokingToken` to null on success which
                // unmounts the dialog cleanly.
                e.preventDefault();
                void onConfirmRevoke();
              }}
              disabled={revoking}
              className={destructiveActionClass}
            >
              {revoking ? 'Revoking\u2026' : 'Revoke token'}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
      <AlertDialog
        open={revokeAllOpen}
        onOpenChange={(open) => {
          if (revoking) return;
          if (!open) setRevokeAllOpen(false);
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Revoke all tokens?</AlertDialogTitle>
            <AlertDialogDescription>
              Every script or dashboard you've connected will stop
              working until you create new tokens. This cannot be
              undone.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={revoking}>
              Cancel
            </AlertDialogCancel>
            <AlertDialogAction
              onClick={(e) => {
                e.preventDefault();
                void onConfirmRevokeAll();
              }}
              disabled={revoking}
              className={destructiveActionClass}
            >
              {revoking ? 'Revoking\u2026' : `Revoke all (${tokens.length})`}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  );
}

/* ---------- Import Preview Step ---------- */

interface ImportPreviewStepProps {
  preview: ImportPreview;
  cellErrors: Record<string, CellError>;
  unresolvedCount: number;
  canImport: boolean;
  pendingPatchCount: number;
  patchRow: (
    rowID: number,
    field: PatchRowRequest['field'],
    value: string | boolean,
  ) => Promise<void>;
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
  cellErrors,
  unresolvedCount,
  canImport,
  pendingPatchCount,
  patchRow,
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

      {/* Data table — editable, with inline collision resolution.
          The component owns its entire footer (status + Cancel + Import)
          so this step renders no standalone action block below. Pairing
          Cancel and Import on the same decision row keeps the primary
          and abort actions together — Fitts's and user-expectation both. */}
      <ImportPreviewTable
        preview={preview}
        cellErrors={cellErrors}
        unresolvedCount={unresolvedCount}
        canImport={canImport}
        pendingPatchCount={pendingPatchCount}
        onPatchRow={patchRow}
        onConfirm={onConfirm}
        onCancel={onCancel}
      />

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
    </div>
  );
}

/* ---------- Import / Export Tab ---------- */

function DataSection() {
  const now = new Date();
  const [year, setYear] = useState(now.getFullYear());
  const [month, setMonth] = useState(now.getMonth() + 1);
  const [exportMode, setExportMode] = useState<'monthly' | 'yearly'>('monthly');

  // Import wizard state — preview / importStep / result are owned by the
  // hook now; destructure them so the rest of the function reads identically
  // to the old local-state version.
  const importSession = useImportSession();
  const { preview, importStep, result, error: importError } = importSession;

  const [defaultCategoryId, setDefaultCategoryId] = useState<number | null>(
    null,
  );
  const [categories, setCategories] = useState<Category[]>([]);
  const [categoryMap, setCategoryMap] = useState<Record<string, string>>({});
  const fileInputRef = useRef<HTMLInputElement | null>(null);
  // Tracks the import_id we last auto-mapped categories for. Resets to null
  // on cancel / startOver so the next upload (or a re-upload) re-runs the
  // auto-map exactly once. See autoMapCategories effect below.
  const lastAutoMappedImportIdRef = useRef<string | null>(null);

  useEffect(() => {
    api
      .get<Category[]>('categories')
      .then(setCategories)
      .catch(() => {
        /* non-critical */
      });
  }, []);

  // Auto-map categories whenever the hook's preview changes to a new
  // import_id. The guard avoids clobbering the user's manual re-mapping on
  // unrelated re-renders (e.g. after a PATCH that only updates one row).
  useEffect(() => {
    if (!preview) {
      // Upload cancelled / session reset — arm the ref for the next preview.
      lastAutoMappedImportIdRef.current = null;
      return;
    }
    if (lastAutoMappedImportIdRef.current === preview.import_id) return;
    setCategoryMap(autoMapCategories(preview, categories));
    lastAutoMappedImportIdRef.current = preview.import_id;
  }, [preview, categories]);

  function clearFileInput() {
    if (fileInputRef.current) fileInputRef.current.value = '';
  }

  async function handleFileChange(e: ChangeEvent<HTMLInputElement>) {
    const file = e.target.files?.[0];
    if (!file) return;
    // uploadFile catches internally and sets importSession.error, which
    // the Alert below renders. On success importStep flips to 'preview'
    // and the file input unmounts, so clearing it here only matters for
    // the rejected path (so the same file can be re-selected without
    // tripping the browser's value-equality no-op on change events).
    await importSession.uploadFile(file);
    clearFileInput();
  }

  async function handleConfirmImport() {
    if (!preview) return;
    // Convert string IDs to numbers for the backend (Go expects int64).
    const numericCategoryMap: Record<string, number> = {};
    for (const [name, id] of Object.entries(categoryMap)) {
      if (id) numericCategoryMap[name] = parseInt(id, 10);
    }
    // confirmImport never throws — it converts 409s into a local
    // collision_groups update + error message, and non-409s into
    // importSession.error. The Alert below renders that state.
    await importSession.confirmImport(numericCategoryMap, defaultCategoryId);
  }

  async function handleCancelImport() {
    await importSession.cancelImport();
    setCategoryMap({});
    setDefaultCategoryId(null);
    clearFileInput();
  }

  function handleImportAnother() {
    importSession.startOver();
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
            <ImportPreviewStep
              preview={preview}
              cellErrors={importSession.cellErrors}
              unresolvedCount={importSession.unresolvedCount}
              canImport={importSession.canImport}
              pendingPatchCount={importSession.pendingPatchCount}
              patchRow={importSession.patchRow}
              categories={categories}
              categoryMap={categoryMap}
              setCategoryMap={setCategoryMap}
              defaultCategoryId={defaultCategoryId}
              setDefaultCategoryId={setDefaultCategoryId}
              onConfirm={() => void handleConfirmImport()}
              onCancel={() => void handleCancelImport()}
            />
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

/* ---------- Notifications Tab ---------- */

export function NotificationsSection() {
  const { supported, permission, subscribed, busy, enable, disable, sendTest } =
    useWebPush();
  const {
    settings,
    loading: prefsLoading,
    error: prefsError,
    canEdit,
    update,
  } = useNotificationPrefs();

  // Per-type rows render in this fixed order. The `key` is the wire field
  // name shared with the backend fan-out type ids (see notifications.go).
  const TYPE_ROWS: { key: keyof NotificationSettings; label: string }[] = [
    { key: 'over_budget', label: 'Over budget' },
    { key: 'txn_added', label: 'Transaction added' },
    { key: 'txn_deleted', label: 'Transaction deleted' },
    { key: 'txn_edited', label: 'Transaction edited' },
    { key: 'large_txn', label: 'Large transaction' },
  ];

  async function handleTypeToggle(
    key: keyof NotificationSettings,
    next: boolean,
  ) {
    try {
      await update({ [key]: next });
    } catch (err) {
      toast.error(
        err instanceof Error ? err.message : 'Failed to update preferences',
      );
    }
  }

  async function handleThresholdChange(raw: string) {
    // A cleared field reads as '' → Number('') === 0, which would silently
    // set the household large-transaction threshold to $0 (every txn "large").
    // Treat blank as a no-op and revert the visible value to the server echo.
    if (raw.trim() === '') return;
    const dollars = Number(raw);
    if (!Number.isFinite(dollars) || dollars < 0) return;
    try {
      await update({ large_txn_threshold_dollars: dollars });
    } catch (err) {
      toast.error(
        err instanceof Error ? err.message : 'Failed to update preferences',
      );
    }
  }

  async function handleToggle(next: boolean) {
    try {
      if (next) {
        await enable();
      } else {
        await disable();
      }
    } catch (err) {
      toast.error(
        err instanceof Error ? err.message : 'Failed to update notifications',
      );
    }
  }

  async function handleSendTest() {
    try {
      await sendTest();
      toast.success('Test notification sent');
    } catch (err) {
      toast.error(
        err instanceof Error ? err.message : 'Failed to send test notification',
      );
    }
  }

  // Intentional scoping (Task 8): the household notification-type policy is
  // co-located inside this per-device push card, so a browser without push
  // support shows only the unsupported notice and not the household block.
  // Household policy is device-agnostic, but per task scope it lives here.
  if (!supported) {
    return (
      <Card>
        <CardHeader>
          <CardTitle className="text-base">Notifications</CardTitle>
          <CardDescription>
            This browser does not support web push notifications.
          </CardDescription>
        </CardHeader>
      </Card>
    );
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">Notifications</CardTitle>
        <CardDescription>
          Get a push notification on this device when a budget category goes
          over its limit.
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        {permission === 'denied' && (
          <Alert variant="warning">
            <AlertTriangle className="h-4 w-4" />
            <AlertTitle>Notifications are blocked</AlertTitle>
            <AlertDescription>
              You've blocked notifications for this site in your browser.
              Re-enable them in your browser's site settings, then toggle this
              on.
            </AlertDescription>
          </Alert>
        )}
        <div className="flex max-w-md items-center justify-between gap-4">
          <Label htmlFor="push-toggle" className="flex flex-col gap-1">
            <span>Push notifications on this device</span>
            <span className="text-xs font-normal text-muted-foreground">
              Each device subscribes separately.
            </span>
          </Label>
          <Switch
            id="push-toggle"
            checked={subscribed}
            disabled={busy || permission === 'denied'}
            onCheckedChange={(v) => void handleToggle(v)}
            aria-label="Push notifications on this device"
          />
        </div>
        <Button
          type="button"
          variant="outline"
          className="w-fit"
          disabled={busy || permission !== 'granted' || !subscribed}
          onClick={() => void handleSendTest()}
        >
          Send test
        </Button>

        <Separator />
        <div className="flex flex-col gap-1">
          <h3 className="text-sm font-medium">Household notification types</h3>
          <p className="text-xs text-muted-foreground">
            {canEdit
              ? 'Choose which events send a push to every subscribed device in the household.'
              : 'Managed by your admin. These settings apply to the whole household.'}
          </p>
        </div>

        {prefsError ? (
          <p className="text-sm text-destructive">{prefsError}</p>
        ) : prefsLoading || !settings ? (
          <p className="text-sm text-muted-foreground">Loading…</p>
        ) : (
          <div className="flex flex-col gap-4">
            {TYPE_ROWS.map(({ key, label }) => {
              const id = `notif-type-${key}`;
              return (
                <div
                  key={key}
                  className="flex max-w-md items-center justify-between gap-4"
                >
                  <Label htmlFor={id}>{label}</Label>
                  <Switch
                    id={id}
                    checked={Boolean(settings[key])}
                    disabled={!canEdit}
                    onCheckedChange={(v) => void handleTypeToggle(key, v)}
                    aria-label={label}
                  />
                </div>
              );
            })}

            <div className="flex max-w-md items-center justify-between gap-4">
              <Label
                htmlFor="large-txn-threshold"
                className="flex flex-col gap-1"
              >
                <span>Large transaction threshold</span>
                <span className="text-xs font-normal text-muted-foreground">
                  Amount in dollars that counts as a large transaction.
                </span>
              </Label>
              <div className="flex items-center gap-1">
                <span className="text-sm text-muted-foreground">$</span>
                <Input
                  // `key` remounts the uncontrolled input on each server echo
                  // so a >2-decimal entry (e.g. 500.999 → stored → echoed
                  // 501.00) re-syncs the visible value to the rounded truth.
                  key={settings.large_txn_threshold_dollars}
                  id="large-txn-threshold"
                  type="number"
                  min={0}
                  step="0.01"
                  inputMode="decimal"
                  className="w-28"
                  disabled={!canEdit}
                  aria-label="Large transaction threshold"
                  defaultValue={settings.large_txn_threshold_dollars}
                  onBlur={(e) =>
                    void handleThresholdChange(e.currentTarget.value)
                  }
                />
              </div>
            </div>
          </div>
        )}
      </CardContent>
    </Card>
  );
}

/* ---------- Main Settings Page ---------- */

// Old `?tab=…` values that no longer correspond to a Settings tab —
// Budgets and Savings became their own top-level pages, and the old
// General tab's contents split across /budgets (Monthly Budgets +
// Category Limits) and the Account/Currencies tabs. Bookmarks pointing
// here surface a one-shot toast with an Open action.
const MOVED_TABS: Record<string, { route: string; label: string }> = {
  savings: { route: '/savings', label: 'Savings' },
  budgets: { route: '/budgets', label: 'Budgets' },
  // General split across Budgets and the remaining Settings tabs —
  // point at Budgets since that's where the monthly-budget editor lives.
  general: { route: '/budgets', label: 'Budgets' },
};

export function Settings() {
  const { user } = useAuth();
  const admin = isAdmin(user);
  const [searchParams] = useSearchParams();
  const navigate = useNavigate();
  const tabParam = searchParams.get('tab');
  const initialTab = isValidTab(tabParam) ? tabParam : 'account';
  const [activeTab, setActiveTab] = useState<SettingsTab>(initialTab);

  // One-shot forwarding toast for `?tab=savings|budgets|general`
  // bookmarks left over from before the page split. Runs on mount
  // only — re-toasting on subsequent in-app tab clicks would be
  // noisy.
  useEffect(() => {
    if (tabParam && MOVED_TABS[tabParam]) {
      const moved = MOVED_TABS[tabParam];
      toast.info(`${moved.label} has its own page now`, {
        action: {
          label: 'Open',
          onClick: () => navigate(moved.route),
        },
      });
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    if (isValidTab(tabParam) && tabParam !== activeTab) {
      setActiveTab(tabParam);
    }
    // activeTab is intentionally excluded from deps: this effect is a
    // one-way URL → state sync. Including activeTab would re-run the
    // effect every time the user clicks a tab and could race with the
    // history listener. The guard above already prevents redundant
    // setState on equal values, so the only meaningful trigger is a
    // fresh tabParam coming in from the router.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tabParam]);

  function handleTabChange(v: string) {
    const next = v as SettingsTab;
    if (next === activeTab) return;
    setActiveTab(next);
  }

  return (
    <div className="flex flex-col gap-6">
      <h1 className="text-2xl font-semibold tracking-tight">Settings</h1>
      <Tabs
        value={activeTab}
        onValueChange={handleTabChange}
        activationMode="manual"
        className="w-full"
      >
        <TabsList>
          <TabsTrigger value="account">Account</TabsTrigger>
          <TabsTrigger value="currencies">Currencies</TabsTrigger>
          {admin && <TabsTrigger value="users">Users</TabsTrigger>}
          <TabsTrigger value="api-tokens">API tokens</TabsTrigger>
          <TabsTrigger value="notifications">Notifications</TabsTrigger>
          <TabsTrigger value="data">Import / Export</TabsTrigger>
        </TabsList>
        <TabsContent value="account" className="mt-6">
          <AccountSection />
        </TabsContent>
        <TabsContent value="currencies" className="mt-6">
          <CurrenciesSection />
        </TabsContent>
        {admin && (
          <TabsContent value="users" className="mt-6">
            <UsersSection />
          </TabsContent>
        )}
        <TabsContent value="api-tokens" className="mt-6">
          <ApiTokensSection />
        </TabsContent>
        <TabsContent value="notifications" className="mt-6">
          <NotificationsSection />
        </TabsContent>
        <TabsContent value="data" className="mt-6">
          <DataSection />
        </TabsContent>
      </Tabs>
    </div>
  );
}
