import { useState, useEffect, useCallback, useMemo, useRef } from 'react';
import type { ChangeEvent, FormEvent } from 'react';
import { useSearchParams, useNavigate } from 'react-router-dom';
import { useQueryClient } from '@tanstack/react-query';
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
  ImportResult,
  ListTokensResponse,
  NotificationSettings,
  PatchRowRequest,
  ResetPasswordResponse,
  RevokeAllResponse,
  RevokeOneResponse,
  User,
} from '../api/types';
import { AppVersion } from '@/components/AppVersion';
import { ImportPreviewTable } from '@/components/ImportPreviewTable';
import { useImportSession, type CellError } from '@/hooks/useImportSession';
import { useWebPush } from '@/hooks/useWebPush';
import { useNotificationPrefs } from '@/hooks/useNotificationPrefs';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { PasswordInput } from '@/components/ui/password-input';
import { Label } from '@/components/ui/label';
import { Switch } from '@/components/ui/switch';
import { cn, selectAllOnFocus } from '@/lib/utils';
import {
  MAX_API_TOKEN_NAME_LENGTH,
  MAX_CURRENCY_SYMBOL_LENGTH,
  MAX_DISPLAY_NAME_LENGTH,
} from '@/lib/constants';
import { charCount } from '@/lib/text';
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
  scrollableTabsList,
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
import { PLANNING_MIN_YEAR, PLANNING_MAX_YEAR, MONTH_NAMES_FULL } from '@/lib/dates';
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
  // charCount, not Zod's .max(): .max() counts UTF-16 code units, so an emoji
  // in a token name would count 2 here and 1 in both the Go handler
  // (apiTokenNameMax, via charLen) and the column's own
  // CHECK(length(name) BETWEEN 1 AND 100) — SQLite counts characters too.
  name: z
    .string()
    .trim()
    .min(1, 'Name is required')
    .refine((value) => charCount(value) <= MAX_API_TOKEN_NAME_LENGTH, {
      message: `Name must be ${MAX_API_TOKEN_NAME_LENGTH} characters or fewer`,
    }),
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
                    <PasswordInput
                      toggleLabel="current password"
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
                    <PasswordInput
                      toggleLabel="new password"
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
                    <PasswordInput
                      toggleLabel="confirm new password"
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
  // `code` stays on Zod's .max(3), and stays measured in UTF-16 code units,
  // because an ISO 4217 code is three ASCII letters by definition and the
  // server gates it on /^[A-Z]{3}$/ — bytes, characters and code units are the
  // same number for every value that can be stored. The `maxLength={3}` on the
  // input is exact for the same reason.
  code: z.string().min(1, 'Code is required').max(3),
  name: z.string().min(1, 'Name is required'),
  // 10 CHARACTERS, matching the server's MaxCurrencySymbolLength. This was 3,
  // which is not a limit the product states anywhere and is too narrow for real
  // symbols — the Lebanese pound writes "ل.ل.", four characters, and this
  // household keeps its ledger in LBP and USD. Counted with charCount so an
  // astral-plane symbol costs the same 1 here as it does on the server.
  symbol: z
    .string()
    .min(1, 'Symbol is required')
    .refine((value) => charCount(value) <= MAX_CURRENCY_SYMBOL_LENGTH, {
      message: `Symbol must be ${MAX_CURRENCY_SYMBOL_LENGTH} characters or fewer`,
    }),
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
                      {/* No maxLength: the HTML attribute counts UTF-16 code
                          units, so it would stop typing at 5 astral-plane
                          characters against a 10-character limit. The schema's
                          charCount check is the gate.

                          The placeholder is a JS expression, not a JSX string
                          attribute. A JSX attribute value is HTML-like and does
                          NOT process backslash escapes, so writing the escape
                          directly in the quotes put those six literal
                          characters on screen instead of a pound sign. Braces
                          make it a real string literal, where the escape is
                          processed. Verified in dist/assets/index-*.js. */}
                      <Input {...field} placeholder={'\u00A3'} />
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

// Counted with charCount, not `.length`: MAX_DISPLAY_NAME_LENGTH mirrors the
// server's `MaxDisplayNameLength`, which is applied through `charLen` (a rune
// count), so `.length` would count an emoji as 2 and refuse a name the server
// accepts.
//
// The PUT merges: a display_name of "" leaves the stored name untouched (and,
// sent alone, 400s as "display_name or role is required"). So an empty box is a
// validation error rather than a way to clear the name — there is no clearing
// it, and letting the submit through would look like a save that did nothing.
// Trim first so " " is empty here exactly as it is on the server.
const displayNameSchema = z.object({
  display_name: z
    .string()
    .trim()
    .min(1, 'Display name is required')
    .refine((value) => charCount(value) <= MAX_DISPLAY_NAME_LENGTH, {
      message: `Display name must be ${MAX_DISPLAY_NAME_LENGTH} characters or fewer`,
    }),
});
type DisplayNameValues = z.infer<typeof displayNameSchema>;

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
  const { user: currentUser, refreshUser } = useAuth();
  const queryClient = useQueryClient();
  const [users, setUsers] = useState<User[]>([]);
  const [addOpen, setAddOpen] = useState(false);
  // The user currently targeted by the reset-password dialog. Null = closed.
  const [resettingUser, setResettingUser] = useState<User | null>(null);
  // Sticky display-name so the dialog title can keep showing the target's
  // name through Radix's close-exit animation after resettingUser flips to
  // null. Mirrors the revoke-one pattern in ApiTokensSection.
  const [resettingUserName, setResettingUserName] = useState('');
  const [resetting, setResetting] = useState(false);
  // Same two-piece shape for the display-name editor: target row, plus the
  // sticky name the dialog keeps reading while it animates out.
  const [renamingUser, setRenamingUser] = useState<User | null>(null);
  const [renamingUserName, setRenamingUserName] = useState('');
  // Whether the open editor is pointed at the signed-in admin's own row. Sticky
  // for the same reason the name is: deriving it from `renamingUser` would flip
  // the dialog's copy to the third person mid close-animation.
  const [renamingSelf, setRenamingSelf] = useState(false);
  const [renaming, setRenaming] = useState(false);
  // Delete confirmation, same two-piece shape again.
  const [deletingUser, setDeletingUser] = useState<User | null>(null);
  const [deletingUserName, setDeletingUserName] = useState('');
  const [deleting, setDeleting] = useState(false);

  const resetForm = useForm<ResetPasswordValues>({
    resolver: zodResolver(resetPasswordSchema),
    defaultValues: { new_password: '', confirm_password: '' },
  });

  const renameForm = useForm<DisplayNameValues>({
    resolver: zodResolver(displayNameSchema),
    defaultValues: { display_name: '' },
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

  function openDelete(u: User) {
    setDeletingUserName(u.display_name);
    setDeletingUser(u);
  }

  async function onConfirmDelete() {
    if (!deletingUser) return;
    setDeleting(true);
    try {
      await api.del(`users/${deletingUser.id}`);
      toast.success('User deleted');
      setDeletingUser(null);
      refreshUsers();
    } catch (err) {
      // Stays open on failure, and the toast carries the server's own words.
      // The likeliest failure here is the 409 refusing to delete somebody who
      // has entered transactions, which is a sentence the admin needs to read
      // rather than a dialog that simply vanished.
      toast.error(err instanceof Error ? err.message : 'Failed to delete user');
    } finally {
      setDeleting(false);
    }
  }

  function openReset(u: User) {
    resetForm.reset();
    setResettingUserName(u.display_name);
    setResettingUser(u);
  }

  // Prefilled with the current name: the motivating case is shortening a name
  // that is already there, not inventing one.
  function openRename(u: User) {
    renameForm.reset({ display_name: u.display_name });
    setRenamingUserName(u.display_name);
    setRenamingSelf(currentUser !== null && u.id === currentUser.id);
    setRenamingUser(u);
  }

  async function onRenameUser(values: DisplayNameValues) {
    if (!renamingUser) return;
    setRenaming(true);
    try {
      // display_name ALONE. The handler merges, so omitting `role` preserves
      // it; sending the row's current role back would turn every rename into a
      // role write, and a role write that differs from the stored one drops
      // that user's sessions.
      await api.put(`users/${renamingUser.id}`, {
        display_name: values.display_name,
      });
      toast.success(`Display name updated to ${values.display_name}`);
      setRenamingUser(null);
      // The response is {"status":"updated"} — no user object — so the table
      // can only be corrected by asking again.
      refreshUsers();
      // Display names are resolved server-side into every transaction's
      // `created_by`, and a user PUT emits no SSE event. Without this the
      // Transactions table and the /quick recently-added panel keep serving
      // the old name from cache until something else invalidates them. The
      // key is the prefix, so it also catches recent/suggestions/history.
      void queryClient.invalidateQueries({ queryKey: ['transactions'] });
      // Renaming YOURSELF also moves the name the shell renders out of the
      // auth context (Sidebar and MobileNav both read user.display_name), and
      // nothing above touches that. Only for your own row: every other row is
      // somebody else's identity, and a /auth/me round-trip would answer with
      // your own unchanged profile.
      if (currentUser && renamingUser.id === currentUser.id) {
        void refreshUser();
      }
    } catch (err) {
      // Two different failures, two different places to say so.
      //
      // A 400 is the server judging THIS VALUE — too long, or empty after its
      // own trim — so it belongs on the field, next to the box the user has to
      // fix. Anything else (a dropped request, a 500) is not about the name at
      // all, and rendering "Failed to fetch" as field validation tells the
      // admin their name is wrong when it is fine. That goes to a toast, where
      // the page's other transient failures already live.
      if (err instanceof ApiError && err.status === 400) {
        renameForm.setError('display_name', { message: err.message });
      } else {
        toast.error(
          err instanceof Error ? err.message : 'Failed to update display name',
        );
      }
    } finally {
      setRenaming(false);
    }
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
                        <PasswordInput
                          toggleLabel="password"
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
                    {/* Offered on EVERY row, own included: the case this
                        exists for is an admin shortening their own name now
                        that it labels every transaction they enter. */}
                    <Button
                      type="button"
                      variant="outline"
                      size="sm"
                      onClick={() => openRename(u)}
                      aria-label={`Edit display name for ${u.username}`}
                    >
                      Edit display name
                    </Button>
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
                    {/* Confirmed, not immediate. This is the third button in
                        a row on a surface whose primary input is a thumb, and
                        it is the only one of the three that destroys an
                        account. */}
                    <Button
                      type="button"
                      variant="destructive"
                      size="sm"
                      onClick={() => openDelete(u)}
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
                      <PasswordInput
                        toggleLabel="new password"
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
                      <PasswordInput
                        toggleLabel="confirm new password"
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
      {/* AlertDialog, like the reset flow and unlike the rename one: this
          destroys an account and cannot be undone, which is the register
          role="alertdialog" exists for.

          The copy says what the server ACTUALLY does, which is narrower than
          the schema suggests. transactions.user_id is ON DELETE CASCADE
          (migrations/002, re-declared in 010), but handleDeleteUser counts the
          target's transactions FIRST and answers 409 when there are any —
          tombstoned rows included — precisely so the cascade can never run.
          Same for balance_checkpoints. So a mis-tap here cannot take a ledger
          with it, and promising that it would would be a lie that reads as
          diligence. What a successful delete really removes is everything else
          hanging off users.id: sessions, api_tokens, push_subscriptions,
          saved_filters. (transaction_audit.actor_user_id is ON DELETE SET
          NULL, so their edit history survives, anonymised.) */}
      <AlertDialog
        open={deletingUser !== null}
        onOpenChange={(open) => {
          if (deleting) return;
          if (!open) setDeletingUser(null);
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              Delete <span className="font-mono">{deletingUserName}</span>?
            </AlertDialogTitle>
            <AlertDialogDescription>
              This permanently removes their account, and with it their API
              tokens, saved filters and any devices they had registered for
              notifications. It cannot be undone. Anyone who has entered a
              transaction — including rows still in the Trash — cannot be
              deleted at all, so no ledger history is at stake here.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel type="button" disabled={deleting}>
              Cancel
            </AlertDialogCancel>
            {/* Plain Button, not AlertDialogAction, for the same reason the
                reset flow gives: the action component closes the dialog on
                click, which would hide the pending state and leave a failed
                delete with nowhere to report back to. */}
            <Button
              type="button"
              disabled={deleting}
              onClick={() => void onConfirmDelete()}
              className={destructiveActionClass}
            >
              {deleting ? 'Deleting…' : 'Delete user'}
            </Button>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
      {/* A plain Dialog, not the AlertDialog the reset flow uses: this edits a
          label and is undone by editing it back, so it has nothing to confirm
          and no reason to interrupt the way role="alertdialog" does. The form
          wiring is the same one — react-hook-form + zodResolver, noValidate so
          the browser's own bubbles never pre-empt the field message, and a
          submit Button rather than any auto-closing action component. */}
      <Dialog
        open={renamingUser !== null}
        onOpenChange={(open) => {
          if (renaming) return;
          if (!open) setRenamingUser(null);
        }}
      >
        <DialogContent>
          <Form {...renameForm}>
            <form
              onSubmit={(e) => void renameForm.handleSubmit(onRenameUser)(e)}
              className="grid gap-4"
              noValidate
            >
              <DialogHeader>
                <DialogTitle>Edit display name</DialogTitle>
                {/* Second person for your own row — which is the case this
                    editor exists for, so the third-person wording was the
                    one an admin would actually read. */}
                <DialogDescription>
                  {renamingSelf ? (
                    <>
                      This is the name SpenDrop shows for you, including on
                      every transaction you enter. Your username and password
                      are unchanged.
                    </>
                  ) : (
                    <>
                      This is the name SpenDrop shows for {renamingUserName},
                      including on every transaction they enter. Their username
                      and password are unchanged.
                    </>
                  )}
                </DialogDescription>
              </DialogHeader>
              <FormField
                control={renameForm.control}
                name="display_name"
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>Display name</FormLabel>
                    <FormControl>
                      <Input autoComplete="off" {...field} />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <DialogFooter>
                <Button
                  type="button"
                  variant="outline"
                  onClick={() => setRenamingUser(null)}
                  disabled={renaming}
                >
                  Cancel
                </Button>
                <Button type="submit" disabled={renaming}>
                  {renaming ? 'Saving…' : 'Save'}
                </Button>
              </DialogFooter>
            </form>
          </Form>
        </DialogContent>
      </Dialog>
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

  // Wall-clock instant the token list was last read, used to decide which
  // rows read "Expired". Captured here — in the async callback that owns
  // the data — instead of during render: `Date.now()` is impure, so
  // reading it while rendering a row makes the row's output depend on
  // *when React happens to re-render*, and two renders of the same token
  // can legitimately disagree. The 0 sentinel is never observable:
  // `tokens` starts empty, so no row exists until the setState below
  // lands the list and the instant together in one batch.
  const [tokensReadAtMs, setTokensReadAtMs] = useState(0);

  const fetchTokens = useCallback(async () => {
    const data = await api.get<ListTokensResponse>('api-tokens');
    setTokensReadAtMs(Date.now());
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
    if (d.getTime() <= tokensReadAtMs) return 'Expired';
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
  unresolvedCategoryCount: number;
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
  unresolvedCategoryCount,
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

  // Which names need a decision is the SERVER's answer, not a client-side
  // re-derivation of it. The backend knows which rows are actually eligible
  // — it excludes skipped rows and rows it would drop for an unparseable
  // date before their category ever mattered — and reproducing that here
  // would mean parsing dates in the browser to decide what to ask about.
  const unresolvedCategories = useMemo(
    () => preview.unresolved_categories ?? [],
    [preview.unresolved_categories],
  );
  const unmatched = useMemo(
    () => unresolvedCategories.filter((u) => u.reason === 'unmapped'),
    [unresolvedCategories],
  );
  const missingCategoryRows = useMemo(
    () =>
      unresolvedCategories.find((u) => u.reason === 'missing')?.row_ids.length ??
      0,
    [unresolvedCategories],
  );

  // "Matched automatically" means the household already has a category by
  // that name — which is exactly the set the server did NOT list as needing
  // a decision. Deriving it from the local map instead would let a name the
  // user mapped by hand count as automatic.
  const matched = useMemo(() => {
    const needsDecision = new Set(unmatched.map((u) => u.name));
    return uniqueImportCategories.filter((name) => !needsDecision.has(name));
  }, [uniqueImportCategories, unmatched]);

  const unmappedNames = useMemo(
    () => unmatched.filter((u) => !categoryMap[u.name]).map((u) => u.name),
    [unmatched, categoryMap],
  );

  const defaultCategoryName = useMemo(
    () => categories.find((c) => c.id === defaultCategoryId)?.name ?? '',
    [categories, defaultCategoryId],
  );

  // The default is offered whenever it has a job to do: filling empty
  // Category cells, or serving as the source for the bulk-apply below.
  const needsDefaultCategory = unmatched.length > 0 || missingCategoryRows > 0;

  // One click that files every still-unmapped name under the chosen default.
  // This is what keeps "I know, put them all in Miscellaneous" cheap without
  // making it something that happens to the user: it writes real map
  // entries, so the decision is visible in every control afterwards and
  // travels to the server as an explicit mapping rather than as a fallback.
  function applyDefaultToUnmapped() {
    if (defaultCategoryId === null) return;
    setCategoryMap((prev) => {
      const next = { ...prev };
      for (const name of unmappedNames) {
        next[name] = String(defaultCategoryId);
      }
      return next;
    });
  }

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
        unresolvedCategoryCount={unresolvedCategoryCount}
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
                {matched.map((name) => (
                  <span key={name}>{name}</span>
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
                  {unmatched.length}{' '}
                  {unmatched.length === 1
                    ? 'category in this file matches nothing you have'
                    : 'categories in this file match nothing you have'}
                  . Nothing is imported until each has a destination.
                </span>
              </div>
              {unmatched.map((entry) => (
                <div
                  key={entry.name}
                  className="flex max-w-sm items-center gap-3"
                >
                  {/* The row count is the difference between "one typo" and
                      "half my ledger" \u2014 without it the user cannot tell
                      whether this decision matters. */}
                  <Label className="w-32 shrink-0 text-sm">
                    <span className="block truncate" title={entry.name}>
                      {entry.name}
                    </span>
                    <span className="text-xs font-normal text-muted-foreground">
                      {entry.row_ids.length === 1
                        ? '1 row'
                        : `${entry.row_ids.length} rows`}
                    </span>
                  </Label>
                  <Select
                    value={categoryMap[entry.name] ?? undefined}
                    onValueChange={(v) =>
                      setCategoryMap((prev) => ({
                        ...prev,
                        [entry.name]: v,
                      }))
                    }
                  >
                    <SelectTrigger aria-label={`Map category ${entry.name}`}>
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
              {unmappedNames.length > 0 && (
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  className="w-fit"
                  disabled={defaultCategoryId === null}
                  onClick={applyDefaultToUnmapped}
                >
                  {defaultCategoryId === null
                    ? 'Pick a default below to fill these at once'
                    : `Use ${defaultCategoryName} for the remaining ${unmappedNames.length}`}
                </Button>
              )}
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
              {missingCategoryRows > 0
                ? `(for the ${missingCategoryRows === 1 ? '1 row' : `${missingCategoryRows} rows`} with an empty Category cell)`
                : '(for rows with an empty Category cell)'}
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

/* ---------- Import result ---------- */

/**
 * Human phrasing for the backend's skip reasons. Anything not listed falls
 * back to the raw key rather than being dropped: a reason the backend adds
 * later must still show up in the total the user is reading, and a silently
 * omitted line would make the counts stop adding up.
 */
const IMPORT_SKIP_REASON_LABELS: Record<string, string> = {
  user_skipped: 'you skipped',
  duplicate: 'already in your ledger',
  unparseable_date: 'no readable date',
  empty_description: 'no description',
  zero_amount: 'no amount',
  missing_category: 'no category',
  field_too_long: 'too long to store',
  error: 'could not be saved',
};

/**
 * Outcome of a finished import.
 *
 * A run that lands 12 of 500 rows is not a success, and reporting it in the
 * same flat sentence as a clean run is how "my import worked" and "my import
 * dropped 488 rows" came to look identical. When anything did not land, the
 * count leads and the reasons are itemised — a bare number is something the
 * user can neither trust nor act on.
 */
function ImportResultSummary({ result }: { result: ImportResult }) {
  const reasons = Object.entries(result.skipped_reasons ?? {})
    .filter(([, count]) => count > 0)
    .sort(([, a], [, b]) => b - a);

  if (result.skipped === 0) {
    return (
      <p className="text-sm font-medium">
        {result.imported === 1
          ? 'Imported 1 row.'
          : `Imported all ${result.imported} rows.`}
      </p>
    );
  }

  return (
    <Alert>
      <AlertTriangle className="h-4 w-4 text-amber-500" />
      <AlertTitle>
        {`${result.skipped} of ${result.total} rows were not imported`}
      </AlertTitle>
      <AlertDescription>
        <div className="flex flex-col gap-1">
          <span>
            {result.imported === 1
              ? '1 row was added to your ledger.'
              : `${result.imported} rows were added to your ledger.`}
          </span>
          {reasons.length > 0 && (
            <ul className="list-disc pl-4">
              {reasons.map(([reason, count]) => (
                <li key={reason}>
                  {`${count} ${IMPORT_SKIP_REASON_LABELS[reason] ?? reason}`}
                </li>
              ))}
            </ul>
          )}
        </div>
      </AlertDescription>
    </Alert>
  );
}

/* ---------- Import / Export Tab ---------- */

/**
 * Excel import wizard. Admin-only: the whole `/api/import/*` route group
 * sits behind `auth.RequireAdmin`, so every control in this card would
 * earn a member a 403.
 *
 * A separate component rather than an `{admin && …}` block inside
 * <DataSection> so that none of the import hooks mount for a member. That
 * is load-bearing, not tidiness: `useImportSession` runs a mount effect
 * that resumes a stored import id with GET /api/import/{id}, and a member
 * who had the wizard open before the route was gated still has that id in
 * localStorage. Sharing <DataSection>'s hooks would let that resume fire
 * — once, not on every visit, since the effect's catch drops the stored
 * id before it inspects the error — and a 403 is not the NotFoundError
 * the catch stays silent for, so it would land as a raw `forbidden`
 * banner on a tab whose owner can do nothing about it. The `categories`
 * fetch below is import-only too.
 */
function ImportCard() {
  // Import wizard state — preview / importStep / result are owned by the
  // hook now; destructure them so the rest of the function reads identically
  // to the old local-state version.
  const [defaultCategoryId, setDefaultCategoryId] = useState<number | null>(
    null,
  );
  const [categories, setCategories] = useState<Category[]>([]);
  const [categoryMap, setCategoryMap] = useState<Record<string, string>>({});

  // The gate needs the category decisions, so they are passed in rather than
  // read back out. Memoised because the hook derives `unresolvedCategoryCount`
  // from this object — a fresh literal every render would recompute it every
  // render for no reason.
  const categoryDecisions = useMemo(
    () => ({ categoryMap, defaultCategoryId }),
    [categoryMap, defaultCategoryId],
  );
  const importSession = useImportSession(categoryDecisions);
  const { preview, importStep, result, error: importError } = importSession;
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

  return (
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
            unresolvedCategoryCount={importSession.unresolvedCategoryCount}
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
            <ImportResultSummary result={result} />
            <Button type="button" variant="outline" className="w-fit" onClick={handleImportAnother}>
              Import Another
            </Button>
          </div>
        )}
      </CardContent>
    </Card>
  );
}

interface DataSectionProps {
  // Import is admin-only (see <ImportCard>); export is not — /api/export/*
  // is registered in the plain authenticated group, so members keep it in
  // full. The tab's own label follows this flag as well: calling the tab
  // "Import / Export" for someone who only ever sees the Export card
  // advertises a capability they do not have.
  admin: boolean;
}

function DataSection({ admin }: DataSectionProps) {
  const now = new Date();
  const [year, setYear] = useState(now.getFullYear());
  const [month, setMonth] = useState(now.getMonth() + 1);
  const [exportMode, setExportMode] = useState<'monthly' | 'yearly'>('monthly');

  function handleExportMonthly() {
    window.open(`/api/export/monthly/${year}/${month}`, '_blank');
  }

  function handleExportYearly() {
    window.open(`/api/export/yearly/${year}`, '_blank');
  }

  return (
    <div className="flex flex-col gap-6">
      {admin && <ImportCard />}

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
                min={PLANNING_MIN_YEAR}
                max={PLANNING_MAX_YEAR}
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

  const quietStartRef = useRef<HTMLInputElement | null>(null);
  const quietEndRef = useRef<HTMLInputElement | null>(null);
  // True while exactly one quiet bound is filled. The backend rejects a
  // half-set window (400), and handleQuietWindow skips the PUT in that state,
  // so without a hint the user sees their entry silently fail to save. Driven
  // off the live inputs (uncontrolled) via recomputeQuietHalfSet.
  const [quietHalfSet, setQuietHalfSet] = useState(false);
  // Bumped to force-remount the uncontrolled digest-time input back to the
  // server value when a user clears it. handleDigestTime skips the PUT on a
  // blank value (the backend 400s on an empty HH:MM), so without a remount the
  // field lingers blank, showing a time that no longer matches what is saved.
  const [digestTimeNonce, setDigestTimeNonce] = useState(0);

  function recomputeQuietHalfSet() {
    const start = quietStartRef.current?.value ?? '';
    const end = quietEndRef.current?.value ?? '';
    setQuietHalfSet((start === '') !== (end === ''));
  }

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

  async function handleDigestMode(next: string) {
    try {
      await update({ digest_mode: next });
    } catch (err) {
      toast.error(
        err instanceof Error ? err.message : 'Failed to update preferences',
      );
    }
  }

  async function handleDigestTime(value: string) {
    // A native time input can be cleared to '' but digest_time has a NOT NULL
    // default and the backend requires a valid HH:MM (an empty/invalid value
    // makes the daily anchor unparseable → 400). Skip the PUT and bump the
    // remount nonce so the cleared field snaps back to the persisted server
    // value instead of lingering blank with a time that no longer matches.
    if (value.trim() === '') {
      setDigestTimeNonce((n) => n + 1);
      return;
    }
    try {
      await update({ digest_time: value });
    } catch (err) {
      toast.error(
        err instanceof Error ? err.message : 'Failed to update preferences',
      );
    }
  }

  async function handleQuietField(key: 'quiet_tz', value: string) {
    try {
      await update({ [key]: value });
    } catch (err) {
      toast.error(
        err instanceof Error ? err.message : 'Failed to update preferences',
      );
    }
  }

  // Quiet start/end must move as a pair: the backend rejects a half-set window
  // (exactly one bound empty) with a 400. Saving each bound independently makes
  // the transitional half-set state (e.g. start filled, end still empty) hit the
  // server. Read both live inputs on every blur and skip the PUT while half-set;
  // the save lands once the second bound is filled or both are cleared.
  async function handleQuietWindow() {
    const quiet_start = quietStartRef.current?.value ?? '';
    const quiet_end = quietEndRef.current?.value ?? '';
    if ((quiet_start === '') !== (quiet_end === '')) return;
    try {
      await update({ quiet_start, quiet_end });
    } catch (err) {
      toast.error(
        err instanceof Error ? err.message : 'Failed to update preferences',
      );
    }
  }

  async function handleQuietAllowOverBudget(next: boolean) {
    try {
      await update({ quiet_allow_over_budget: next });
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

  // The household notification-type policy below is device-agnostic and runs
  // server-side, so it must stay reachable even where the browser cannot
  // subscribe to web push. Only the per-device push enable toggle + Send-test
  // button are gated behind `supported`; the household block stays gated by
  // canEdit / the API.
  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">Notifications</CardTitle>
        <CardDescription>
          {supported
            ? 'Get a push notification on this device when a budget category goes over its limit.'
            : 'This browser does not support web push notifications on this device. The household-wide settings below still apply to every subscribed device.'}
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        {supported && (
          <>
            {permission === 'denied' && (
              <Alert variant="warning">
                <AlertTriangle className="h-4 w-4" />
                <AlertTitle>Notifications are blocked</AlertTitle>
                <AlertDescription>
                  You've blocked notifications for this site in your browser.
                  Re-enable them in your browser's site settings, then toggle
                  this on.
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
          </>
        )}
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

            <Separator />
            <div className="flex max-w-md items-center justify-between gap-4">
              <Label htmlFor="digest-mode" className="flex flex-col gap-1">
                <span>Daily digest</span>
                <span className="text-xs font-normal text-muted-foreground">
                  One summary push instead of per-event alerts.
                </span>
              </Label>
              <Select
                value={settings.digest_mode}
                disabled={!canEdit}
                onValueChange={(v) => void handleDigestMode(v)}
              >
                <SelectTrigger id="digest-mode" className="w-28" aria-label="Daily digest">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="off">Off</SelectItem>
                  <SelectItem value="daily">Daily</SelectItem>
                </SelectContent>
              </Select>
            </div>
            {settings.digest_mode === 'daily' && (
              <div className="flex max-w-md items-center justify-between gap-4">
                <Label htmlFor="digest-time" className="flex flex-col gap-1">
                  <span>Digest send time</span>
                  <span className="text-xs font-normal text-muted-foreground">
                    When the daily summary push goes out.
                  </span>
                </Label>
                <Input
                  id="digest-time"
                  type="time"
                  className="w-28"
                  disabled={!canEdit}
                  aria-label="Digest send time"
                  defaultValue={settings.digest_time}
                  key={`dt-${settings.digest_time}-${digestTimeNonce}`}
                  onBlur={(e) => void handleDigestTime(e.currentTarget.value)}
                />
              </div>
            )}
            <div className="flex max-w-md items-center justify-between gap-4">
              <Label htmlFor="quiet-start">Quiet hours start</Label>
              <Input
                id="quiet-start"
                type="time"
                className="w-28"
                disabled={!canEdit}
                aria-label="Quiet hours start"
                ref={quietStartRef}
                defaultValue={settings.quiet_start}
                key={`qs-${settings.quiet_start}`}
                onChange={recomputeQuietHalfSet}
                onBlur={() => void handleQuietWindow()}
              />
            </div>
            <div className="flex max-w-md items-center justify-between gap-4">
              <Label htmlFor="quiet-end">Quiet hours end</Label>
              <Input
                id="quiet-end"
                type="time"
                className="w-28"
                disabled={!canEdit}
                aria-label="Quiet hours end"
                ref={quietEndRef}
                defaultValue={settings.quiet_end}
                key={`qe-${settings.quiet_end}`}
                onChange={recomputeQuietHalfSet}
                onBlur={() => void handleQuietWindow()}
              />
            </div>
            <p className="max-w-md text-xs text-muted-foreground">
              Set both a start and end to silence non-urgent pushes during those
              hours. A single bound is ignored.
            </p>
            {quietHalfSet && (
              <p
                role="alert"
                className="max-w-md text-xs text-destructive"
              >
                Quiet hours need both a start and an end — fill in the other
                field to save.
              </p>
            )}
            <div className="flex max-w-md items-center justify-between gap-4">
              <Label htmlFor="quiet-tz">Time zone</Label>
              <Input
                id="quiet-tz"
                type="text"
                className="w-44"
                disabled={!canEdit}
                aria-label="Quiet hours time zone"
                defaultValue={settings.quiet_tz}
                key={`tz-${settings.quiet_tz}`}
                onBlur={(e) => void handleQuietField('quiet_tz', e.currentTarget.value)}
              />
            </div>
            <div className="flex max-w-md items-center justify-between gap-4">
              <Label htmlFor="quiet-allow-ob" className="flex flex-col gap-1">
                <span>Allow over-budget during quiet hours</span>
                <span className="text-xs font-normal text-muted-foreground">
                  Over-budget alerts still send while quiet hours are on.
                </span>
              </Label>
              <Switch
                id="quiet-allow-ob"
                checked={settings.quiet_allow_over_budget}
                disabled={!canEdit}
                onCheckedChange={(v) => void handleQuietAllowOverBudget(v)}
                aria-label="Allow over-budget during quiet hours"
              />
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
        {/* Six triggers for an admin come to roughly 550px of
            whitespace-nowrap content, against ~358px of content width on a
            390px phone. The strip scrolls sideways rather than widening the
            page — see the note on `scrollableTabsList`, which explains why
            `justify-start` is the half that must not be lost. */}
        <TabsList className={scrollableTabsList}>
          <TabsTrigger value="account">Account</TabsTrigger>
          <TabsTrigger value="currencies">Currencies</TabsTrigger>
          {admin && <TabsTrigger value="users">Users</TabsTrigger>}
          <TabsTrigger value="api-tokens">API tokens</TabsTrigger>
          <TabsTrigger value="notifications">Notifications</TabsTrigger>
          {/* The tab value stays "data" for both roles so existing
              ?tab=data bookmarks keep resolving; only the visible label
              narrows. Import is admin-only (see <ImportCard>), and a
              member whose panel holds nothing but the Export card should
              not be told the tab offers import. */}
          <TabsTrigger value="data">
            {admin ? 'Import / Export' : 'Export'}
          </TabsTrigger>
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
          <DataSection admin={admin} />
        </TabsContent>
      </Tabs>
      {/* Outside the Tabs on purpose: "which build am I on" is a property of
          the app, not of a tab, so it stays put wherever the user has
          navigated rather than hiding on five of the six panels. */}
      <AppVersion className="pt-2" />
    </div>
  );
}
