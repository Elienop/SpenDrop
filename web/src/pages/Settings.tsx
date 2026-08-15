import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
} from 'react';
import type { ChangeEvent, FormEvent, ReactNode, RefObject } from 'react';
import { useSearchParams, useNavigate } from 'react-router-dom';
import { useQueryClient } from '@tanstack/react-query';
import { zodResolver } from '@hookform/resolvers/zod';
import { useForm } from 'react-hook-form';
import type { Control } from 'react-hook-form';
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
import { useIsMobileViewport } from '@/hooks/useIsMobileViewport';
import {
  resolveSettingsTab,
  settingsSectionLabel,
  visibleSettingsSections,
  type SettingsTab,
} from './settings-sections';
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

/* ---------- Shared: the display-name editor ---------- */

// Counted with charCount, not `.length`: MAX_DISPLAY_NAME_LENGTH mirrors the
// server's `MaxDisplayNameLength`, which is applied through `charLen` (a rune
// count), so `.length` would count an emoji as 2 and refuse a name the server
// accepts.
//
// Empty is a validation error rather than a way to clear the name. Neither
// write path lets you clear it: the admin PUT merges, so "" leaves the stored
// name untouched (and, sent alone, 400s as "display_name or role is
// required"), and PATCH /auth/me refuses "" outright because the whole request
// IS the new name. Letting the submit through would look like a save that
// quietly did nothing. Trim first so " " is empty here exactly as on the server.
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

/**
 * The one display-name input, shared by all three editors on this page: a
 * member (or admin) renaming themselves in their own account card, the admin's
 * table-row rename dialog, and the phone's manage-user dialog.
 *
 * Extracted rather than copied because the three would otherwise drift on the
 * things that are easy to get wrong once and never notice twice —
 * `autoComplete="off"` (a browser offering a saved username here is offering
 * the wrong field), and the FormMessage slot that both the client schema and
 * the server's 400 write into.
 */
function DisplayNameField({
  control,
}: {
  control: Control<DisplayNameValues>;
}) {
  return (
    <FormField
      control={control}
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
  );
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

/**
 * The `account` panel.
 *
 * THE ONLY PLACE THE ROLE FLAG ENTERS THIS SECTION. Everything a member may do
 * lives in `<SelfAccountCard>`, which takes no props at all — there is no role
 * flag in its scope to gate anything on, so gating something there would mean
 * adding a prop and threading it down, which a reviewer sees in the diff.
 * Everything a member may NOT do lives in `<HouseholdUsersCard>`, behind the
 * one `admin &&` below.
 *
 * The boundary is here rather than on the section entry in
 * `settings-sections.ts` because `adminOnly` is all-or-nothing — it hides the
 * control AND the panel — and cannot express "this section minus one card",
 * which is exactly what a merged Account/Users panel is. Same shape as
 * `<DataSection>`, whose Import card is admin-only inside a section both roles
 * open.
 *
 * Self card FIRST. A member's entire panel is the self card, so putting the
 * household table above it would open the two roles on different content.
 */
function AccountSection({ admin }: { admin: boolean }) {
  return (
    <div className="flex flex-col gap-6">
      <SelfAccountCard />
      {admin && <HouseholdUsersCard />}
    </div>
  );
}

const SELF_ACCOUNT_HEADING_ID = 'self-account-heading';
const HOUSEHOLD_USERS_HEADING_ID = 'household-users-heading';

function SelfAccountCard() {
  const { user, logout, refreshUser } = useAuth();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [savingName, setSavingName] = useState(false);

  const nameForm = useForm<DisplayNameValues>({
    resolver: zodResolver(displayNameSchema),
    defaultValues: { display_name: user?.display_name ?? '' },
  });

  // The auth context is the source of truth for this value, and it moves
  // underneath the form twice: once when `/auth/me` first resolves after a
  // reload, and once when our own save calls `refreshUser`. Resetting on the
  // value (not the object) keeps the box showing what the server holds and
  // drops the dirty state after a successful save.
  const storedName = user?.display_name ?? '';
  // `reset` destructured rather than depending on `nameForm`: react-hook-form
  // guarantees it is referentially stable, so the effect keys on the VALUE and
  // nothing else. (Holding the form in a ref and writing it during render is
  // what this used to do, and `react-hooks/refs` rejects it.)
  const { reset: resetNameForm } = nameForm;
  useEffect(() => {
    resetNameForm({ display_name: storedName });
  }, [resetNameForm, storedName]);

  async function onSaveDisplayName(values: DisplayNameValues) {
    setSavingName(true);
    try {
      // PATCH /auth/me, NOT PUT /users/{id}: the admin route is behind
      // RequireAdmin, so it is not a path a member has, and this endpoint takes
      // its target from the session rather than from a URL segment. An admin
      // renaming THEMSELVES goes through here too — this card is role-blind on
      // purpose, and the endpoint is open to every authenticated role.
      await api.patch<User>('auth/me', { display_name: values.display_name });
      toast.success(`Display name updated to ${values.display_name}`);
      // The shell renders this name (Sidebar and MobileNav both read
      // `user.display_name`), and nothing else refreshes the auth context.
      void refreshUser();
      // Display names are resolved server-side into every transaction's
      // `created_by`, and this PATCH emits no SSE event. Without it the
      // Transactions table and the /quick recently-added panel keep serving the
      // old name from cache. Same reason the admin rename does it.
      void queryClient.invalidateQueries({ queryKey: ['transactions'] });
    } catch (err) {
      // A 400 is the server judging THIS VALUE — too long after its own rune
      // count, empty after its own trim, or carrying a codepoint it refuses
      // because that character can forge structure in a push notification body
      // (control characters, U+2028/9, bidi overrides). All three are sentences
      // the person has to read next to the box they must fix, so the server's
      // own words go on the field rather than into a toast that swallows them.
      if (err instanceof ApiError && err.status === 400) {
        nameForm.setError('display_name', { message: err.message });
      } else {
        toast.error(
          err instanceof Error ? err.message : 'Failed to update display name',
        );
      }
    } finally {
      setSavingName(false);
    }
  }

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
    // `role="group"` + `aria-labelledby`, on both cards in this panel. It is
    // the only Settings section that holds TWO independent cards, so without a
    // name a screen-reader user arriving at the password fields has nothing to
    // tell them whether they are in their own account or in somebody's row of
    // the household table. `group` rather than `region`: these are sets of
    // related controls inside a section that is already a landmark, and two
    // more landmarks here would be noise.
    //
    // Static ids rather than `useId`, because both cards are singletons within
    // the page — there is no second instance for them to collide with.
    <Card role="group" aria-labelledby={SELF_ACCOUNT_HEADING_ID}>
      <CardHeader>
        <CardTitle id={SELF_ACCOUNT_HEADING_ID} className="text-base">
          Your account
        </CardTitle>
        <CardDescription>
          Who SpenDrop shows you as, and the password you sign in with.
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-6">
        {/* Identity, read-only. The username is the login and cannot be
            changed by anybody, including an admin — there is no write path for
            `users.username` anywhere in the API — and the role is a fact about
            this account that the person needs in order to make sense of what
            the rest of Settings does and does not offer them.

            The role appears here as TEXT and nothing on this card branches on
            it. That is what keeps this component prop-less: a gate here would
            have to be written as a gate. */}
        <dl className="flex flex-col gap-3 text-sm sm:flex-row sm:gap-8">
          <div className="flex flex-col gap-1">
            <dt className="text-muted-foreground">Username</dt>
            <dd className="font-mono">{user?.username ?? '—'}</dd>
          </div>
          <div className="flex flex-col gap-1">
            <dt className="text-muted-foreground">Role</dt>
            <dd>
              <Badge variant={isAdmin(user) ? 'default' : 'secondary'}>
                {isAdmin(user) ? 'Admin' : 'Member'}
              </Badge>
            </dd>
          </div>
        </dl>
        {/* A member's only route to this column. `PUT /api/users/{id}` is
            behind RequireAdmin, so before PATCH /auth/me existed a member was
            stuck with whatever name an admin typed at account creation — on a
            value that labels every transaction they enter, household-wide. */}
        <Form {...nameForm}>
          <form
            onSubmit={(e) => void nameForm.handleSubmit(onSaveDisplayName)(e)}
            className="flex max-w-md flex-col gap-4"
            noValidate
          >
            <DisplayNameField control={nameForm.control} />
            <p className="text-sm text-muted-foreground">
              This is the name SpenDrop shows for you, including on every
              transaction you enter. Your username and password are unchanged.
            </p>
            <Button type="submit" className="w-fit" disabled={savingName}>
              {savingName ? 'Saving…' : 'Save display name'}
            </Button>
          </form>
        </Form>
        <Separator />
        {/* A heading rather than a second Card. The whole panel is this one
            card for a member, and splitting "who you are" from "how you sign
            in" into two cards would put a card boundary where there is no
            change of subject. */}
        <div className="flex flex-col gap-4">
        <h3 className="text-base font-semibold leading-none tracking-tight">
          Change password
        </h3>
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
        </div>
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

/**
 * One currency as a stacked card — the below-`md` presentation of the row the
 * table renders.
 *
 * Five columns do not survive a 360px viewport. Measured on the built
 * container: the section panned 129px sideways, and what sat out there was the
 * `Rate to Base` input — 93% of it clipped. That column is the only editable
 * thing on this surface, so the pan was hiding the entire point of it.
 *
 * ANATOMY: the identity (code, then name and symbol) reads as the heading, the
 * `Base` badge sits opposite it because it is a property of the row rather
 * than a fourth fact about it, and the rate gets a full-width field of its own
 * underneath. Modelled on `<UserCard>` — inert card, one explicit control —
 * rather than `<TransactionCard>`, for the same reason: "what does tapping a
 * CURRENCY do" has no answer.
 *
 * The field's label reads "Rate for USD" rather than a bare "Rate to base",
 * and that is deliberate on two counts. It is the SAME string the table's
 * `aria-label` uses, so the two presentations name the same datum identically
 * and a query written against one finds the other; and it is unique per row,
 * where a repeated "Rate to base" would leave a screen-reader user tabbing
 * through several identically-named fields. Written as a real `<Label>` rather
 * than an `aria-label` so the visible text and the accessible name are one
 * string — an `aria-label` beside visible label text overrides it and orphans
 * what is on screen.
 */
function CurrencyCard({
  currency,
  rate,
  onRateChange,
}: {
  currency: Currency;
  rate: string;
  onRateChange: (code: string, value: string) => void;
}) {
  const rateFieldId = `currency-rate-${currency.code}`;
  return (
    <li className="flex flex-col gap-3 p-4">
      <div className="flex items-start justify-between gap-3">
        {/* min-w-0 is what lets `truncate` do anything on a flex child. */}
        <div className="flex min-w-0 flex-col gap-1">
          <div className="truncate font-mono font-medium">{currency.code}</div>
          <div className="truncate text-sm text-muted-foreground">
            {currency.name} &middot; {currency.symbol}
          </div>
        </div>
        {currency.is_base && (
          <Badge variant="secondary" className="shrink-0">
            Base
          </Badge>
        )}
      </div>
      {currency.is_base ? (
        // The base currency has no editable rate on either presentation; the
        // table renders the same fixed 1.0000 in its Rate column.
        <p className="text-sm text-muted-foreground">
          Rate to base:{' '}
          <span className="font-mono tabular-nums">1.0000</span>
        </p>
      ) : (
        <div className="flex flex-col gap-1.5">
          <Label htmlFor={rateFieldId}>
            Rate for <span className="font-mono">{currency.code}</span>
          </Label>
          {/* Full width, unlike the table's `max-w-[160px]`: that cap is what
              a five-column row can spare, and it is also what made this the
              column that got clipped. A card has the whole width to give. */}
          <Input
            id={rateFieldId}
            type="number"
            step="0.0001"
            min="0"
            // A rate is four decimals deep, so the decimal keypad, not the
            // digits one. `type` alone does not open it on Android — see
            // `<MonthlyBudgetCard>` in `pages/Budgets.tsx`.
            inputMode="decimal"
            value={rate}
            onChange={(e) => onRateChange(currency.code, e.target.value)}
            onFocus={selectAllOnFocus}
          />
        </div>
      )}
    </li>
  );
}

function CurrenciesSection() {
  const isMobile = useIsMobileViewport();
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
          {/* ONE tree or the other, chosen in JS — never `md:hidden` on two.
              Both presentations render the same rate FIELDS, so mounting both
              would put two controls named "Rate for USD" in the document and
              submit whichever the browser reached last; `display: none` drops
              the loser from the a11y tree but never from React or from the
              form. See `useIsMobileViewport`. */}
          {isMobile ? (
            /* Tailwind's preflight strips the list-style and Safari drops the
               list role with it — hence the explicit `role`. Inset rather than
               edge-to-edge (the treatment `<UserCard>`'s list gets): this list
               shares a `CardContent` with the Add Currency form below it, so
               reaching the card's edges would mean negative margins that the
               sibling form does not want. */
            <ul
              role="list"
              aria-label="Currencies"
              className="flex flex-col divide-y divide-border rounded-md border"
            >
              {currencies.map((c) => (
                <CurrencyCard
                  key={c.code}
                  currency={c}
                  rate={editRates[c.code] ?? ''}
                  onRateChange={(code, value) =>
                    setEditRates((prev) => ({ ...prev, [code]: value }))
                  }
                />
              ))}
            </ul>
          ) : (
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
                          // Same decimal keypad as the phone card above; inert
                          // where a physical keyboard is attached.
                          inputMode="decimal"
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
          )}
          {/* Full width on a phone, hugging its label once there is room. The
              submit is the one control that commits every rate on the card
              list above it, and a `w-fit` button under a column of full-width
              fields reads as belonging to the last one. */}
          <Button
            type="submit"
            className="w-full md:w-fit"
            disabled={saving}
          >
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
                        inputMode="decimal"
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

/* ---------- Household users (admin only, inside the Account panel) ---------- */

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

/**
 * One household member as a stacked card — the below-`md` presentation of the
 * table to the right of this file.
 *
 * Modelled on `<TrashCard>`, NOT `<TransactionCard>`, and the difference is the
 * interaction model rather than the styling. The ledger card makes the whole
 * row one tap target and adds long-press selection; neither fits here. There is
 * no selection on this surface, and "what does tapping a PERSON do" has no
 * obvious answer — rename? promote? delete? So the card is inert and the one
 * explicit button says what it opens.
 *
 * Username first and in `font-mono`, exactly as the table renders it: it is the
 * login, it is unique, and it is the string every aria-label on this surface is
 * built from. The display name sits under it as the mutable label.
 */
function UserCard({
  user,
  onManage,
}: {
  user: User;
  onManage: (u: User) => void;
}) {
  return (
    <li className="flex flex-col gap-3 p-4">
      {/* min-w-0 is what lets `truncate` do anything on a flex child. */}
      <div className="flex min-w-0 flex-col gap-1">
        <div className="truncate font-mono font-medium">{user.username}</div>
        <div className="truncate text-sm text-muted-foreground">
          {user.display_name}
        </div>
      </div>
      {/* `min-h-11` even though `Button` now floors itself on a coarse pointer:
          this card renders only below `md`, where 44px is right for a mouse
          too. Same reasoning, and the same deliberate redundancy, as
          <TrashCard>'s action row — and emphatically NOT paired with a
          `md:min-h-0`, which is emitted after the pointer variant and would
          defeat the primitive's floor on the tablet. */}
      <Button
        type="button"
        variant="outline"
        className="min-h-11 w-full"
        data-manage-user-id={user.id}
        aria-label={`Manage ${user.username}`}
        onClick={() => onManage(user)}
      >
        Manage
      </Button>
    </li>
  );
}

function HouseholdUsersCard() {
  const { user: currentUser, refreshUser } = useAuth();
  const queryClient = useQueryClient();
  const isMobile = useIsMobileViewport();
  const [users, setUsers] = useState<User[]>([]);
  const [addOpen, setAddOpen] = useState(false);
  // The row the phone's manage dialog is pointed at. Null = closed.
  const [managingUser, setManagingUser] = useState<User | null>(null);
  // Sticky copies, for the same reason every other dialog on this page keeps
  // one: the target flips to null the instant the close animation starts, and
  // reading the name off it would blank the title mid-animation. Two of them,
  // because the two strings answer different questions — the username labels
  // the controls (it is unique) and the display name is what the copy reads.
  const [managingUserName, setManagingUserName] = useState('');
  const [managingUsername, setManagingUsername] = useState('');
  const [savingManagedName, setSavingManagedName] = useState(false);
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

  // A SECOND instance rather than sharing `renameForm`: the two editors are
  // open at different times but hold different targets, and one shared
  // form would carry the last dialog's field-level 400 into the next one.
  const manageNameForm = useForm<DisplayNameValues>({
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

  // `{ role }` ALONE, for the mirror image of the reason the rename sends
  // `{ display_name }` alone. handleUpdateUser merges against the stored row,
  // so a payload carrying both makes the client authoritative on BOTH: a role
  // echoed back from a stale snapshot reverts a change made elsewhere and drops
  // that user's sessions, and a display name echoed back overwrites a rename
  // the other admin just made. Two facts, two writes, neither carrying the
  // other. This is why the manage dialog is deliberately NOT a "save
  // everything" form.
  async function handleRoleChange(userId: number, role: Role) {
    try {
      await api.put(`users/${userId}`, { role });
      toast.success('Role updated');
      refreshUsers();
    } catch (err) {
      toast.error(err instanceof Error ? err.message : 'Failed to update role');
    }
  }

  /* --- the phone's manage dialog, and its focus handoff --- */

  const usersListRef = useRef<HTMLUListElement>(null);
  const cardTitleRef = useRef<HTMLDivElement>(null);
  // Remembered separately from `managingUser`, which is already null by the
  // time the close-focus hook runs.
  const lastManagedIdRef = useRef<number | null>(null);
  // Set only on the two paths that close this dialog IN ORDER TO OPEN A
  // CONFIRM. Load-bearing, not a guard against Radix: without it the manage
  // dialog's close-focus hook fires on unmount and yanks focus back to the card
  // AFTER the AlertDialog has already focused its Cancel button.
  const confirmHandoffRef = useRef(false);
  // Which surface opened the confirm that is now showing, remembered so its
  // close can send focus back there: the phone path returns to the card's
  // Manage button (focusAfterManageClose resolves it), the desktop path to
  // the table-row button that opened it. A ref, not state, for the same
  // reason as `lastManagedIdRef`: the confirm's target state is already null
  // by the time `onCloseAutoFocus` runs.
  const confirmOpenerRef = useRef<
    | { source: 'manage' }
    | { source: 'table'; kind: 'reset' | 'delete'; userId: number }
    | null
  >(null);
  // Set only by a delete the server ACCEPTED: that row is gone, and both
  // per-row anchors go with it, so the close falls back to the card title. A
  // failed delete keeps the dialog open and never sets this.
  const deleteSucceededRef = useRef(false);
  // The rename dialog's opener. Only the table opens it — the phone edits the
  // name inside the manage dialog — so one id is enough, and unlike the
  // confirms there is no removed-row case: a rename never takes the row away.
  const lastRenamedIdRef = useRef<number | null>(null);

  // Resolved against the LIVE list rather than the snapshot the dialog opened
  // with: a role change refetches the list, and the Select has to show what the
  // server now holds instead of the value it was opened with. Falls back to the
  // snapshot so the dialog keeps its contents through the close animation after
  // a delete drops the row. Same pattern as Transactions' `editingTx`.
  const managingRow = managingUser
    ? (users.find((u) => u.id === managingUser.id) ?? managingUser)
    : null;

  function openManage(u: User) {
    manageNameForm.reset({ display_name: u.display_name });
    lastManagedIdRef.current = u.id;
    setManagingUserName(u.display_name);
    setManagingUsername(u.username);
    setManagingUser(u);
  }

  // Where focus goes when the manage dialog closes on save, cancel, Escape or
  // an overlay tap: back to the Manage button that opened it, which is where
  // the user's attention was. The card title is the fallback for the row that
  // is no longer there.
  //
  // Radix's own restore cannot do this. `DialogContentModal` composes
  // `preventDefault(); context.triggerRef.current?.focus()`, and `triggerRef`
  // is only populated by a `DialogTrigger` — this dialog is opened
  // programmatically from a card tap and has none, so every close would land on
  // `<body>`. (There is no `AlertDialogTrigger` anywhere in this app either,
  // which is the same reason the confirms below are page-level rather than
  // nested inside this one.)
  function focusAfterManageClose() {
    const id = lastManagedIdRef.current;
    const anchor =
      id === null
        ? null
        : usersListRef.current?.querySelector<HTMLElement>(
            `[data-manage-user-id="${id}"]`,
          );
    (anchor ?? cardTitleRef.current)?.focus();
  }

  // The confirms' counterpart to focusAfterManageClose, needed for the same
  // reason: no `AlertDialogTrigger` exists anywhere in this app, so Radix's
  // own restore runs `triggerRef.current?.focus()` against null and every
  // close would land focus on `<body>`. Anchors are re-queried at close time
  // rather than held as elements — a refetch can have re-rendered the row
  // since the confirm opened — and a missing anchor falls back to the card
  // title, the one node on this surface that outlives any row.
  function focusAfterConfirmClose(rowRemoved: boolean) {
    const opener = confirmOpenerRef.current;
    confirmOpenerRef.current = null;
    if (rowRemoved || opener === null) {
      cardTitleRef.current?.focus();
      return;
    }
    if (opener.source === 'manage') {
      focusAfterManageClose();
      return;
    }
    const anchor = document.querySelector<HTMLElement>(
      opener.kind === 'reset'
        ? `[data-reset-user-id="${opener.userId}"]`
        : `[data-delete-user-id="${opener.userId}"]`,
    );
    (anchor ?? cardTitleRef.current)?.focus();
  }

  // Same Trigger-less shape again, for the rename dialog: save, cancel, Escape
  // and an overlay tap all return to the row's own Edit button. Queried at
  // close time because a rename refetches the list, which re-renders the row
  // the dialog was opened from.
  function focusAfterRenameClose() {
    const id = lastRenamedIdRef.current;
    const anchor =
      id === null
        ? null
        : document.querySelector<HTMLElement>(`[data-rename-user-id="${id}"]`);
    (anchor ?? cardTitleRef.current)?.focus();
  }

  // Reset and Delete CLOSE this dialog and open the page-level confirm.
  // Sequential, single-layer — never nested. Nesting stacks two `bg-black/80`
  // scrims (~4% of the page still visible), and Radix's `hideOthers()` puts
  // `aria-hidden` on the outer dialog, so the manage dialog would stop being
  // reachable by assistive tech and unqueryable in tests, while the confirm's
  // own close would restore focus to a null triggerRef and drop it on `<body>`.
  function confirmFromManage(open: (u: User) => void, u: User) {
    confirmHandoffRef.current = true;
    confirmOpenerRef.current = { source: 'manage' };
    setManagingUser(null);
    open(u);
  }

  // The table-row openers record themselves before opening, so the confirm's
  // close can find its way back to the exact button that started the chain.
  function openResetFromTable(u: User) {
    confirmOpenerRef.current = { source: 'table', kind: 'reset', userId: u.id };
    openReset(u);
  }

  function openDeleteFromTable(u: User) {
    confirmOpenerRef.current = { source: 'table', kind: 'delete', userId: u.id };
    openDelete(u);
  }

  async function onSaveManagedName(values: DisplayNameValues) {
    if (!managingUser) return;
    setSavingManagedName(true);
    try {
      await submitRename(managingUser, values.display_name);
      setManagingUser(null);
    } catch (err) {
      reportRenameError(err, manageNameForm);
    } finally {
      setSavingManagedName(false);
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
      // Before the state flip: the close this triggers must know the row is
      // gone so its focus restore skips the row anchors.
      deleteSucceededRef.current = true;
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
    lastRenamedIdRef.current = u.id;
    setRenamingUserName(u.display_name);
    setRenamingSelf(currentUser !== null && u.id === currentUser.id);
    setRenamingUser(u);
  }

  // The write itself, shared by the table's rename dialog and the phone's
  // manage dialog. Throws on failure so each caller decides what to close and
  // which form gets the field-level message.
  async function submitRename(target: User, displayName: string) {
    // display_name ALONE. The handler merges, so omitting `role` preserves
    // it; sending the row's current role back would turn every rename into a
    // role write, and a role write that differs from the stored one drops
    // that user's sessions.
    await api.put(`users/${target.id}`, { display_name: displayName });
    toast.success(`Display name updated to ${displayName}`);
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
    if (currentUser && target.id === currentUser.id) {
      void refreshUser();
    }
  }

  // Two different failures, two different places to say so.
  //
  // A 400 is the server judging THIS VALUE — too long, empty after its own
  // trim, or carrying a codepoint it refuses — so it belongs on the field, next
  // to the box the user has to fix. Anything else (a dropped request, a 500) is
  // not about the name at all, and rendering "Failed to fetch" as field
  // validation tells the admin their name is wrong when it is fine. That goes
  // to a toast, where the page's other transient failures already live.
  function reportRenameError(
    err: unknown,
    target: ReturnType<typeof useForm<DisplayNameValues>>,
  ) {
    if (err instanceof ApiError && err.status === 400) {
      target.setError('display_name', { message: err.message });
    } else {
      toast.error(
        err instanceof Error ? err.message : 'Failed to update display name',
      );
    }
  }

  async function onRenameUser(values: DisplayNameValues) {
    if (!renamingUser) return;
    setRenaming(true);
    try {
      await submitRename(renamingUser, values.display_name);
      setRenamingUser(null);
    } catch (err) {
      reportRenameError(err, renameForm);
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
    <Card role="group" aria-labelledby={HOUSEHOLD_USERS_HEADING_ID}>
      <CardHeader className="flex flex-row items-center justify-between">
        {/* `tabIndex={-1}` makes this a focus anchor, not a tab stop: it is
            where the manage dialog parks focus when the row it was pointed at
            has left the list. */}
        <CardTitle
          id={HOUSEHOLD_USERS_HEADING_ID}
          className="text-base"
          ref={cardTitleRef}
          tabIndex={-1}
        >
          Household users
        </CardTitle>
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
      {/* ONE tree or the other, chosen in JS. Not `md:hidden` on two: two trees
          for the same people would put every row's accessible name in the
          document twice ("Manage alice" and the table's three per-row buttons),
          and `display: none` removes the loser from the a11y tree but never
          from React. See `useIsMobileViewport`. */}
      {isMobile ? (
        <CardContent className="px-0 pb-2">
          {/* Tailwind's preflight strips the list-style and Safari drops the
              list role with it — hence the explicit `role`. */}
          <ul
            ref={usersListRef}
            role="list"
            aria-label="Household users"
            className="flex flex-col divide-y divide-border border-t"
          >
            {users.map((u) => (
              <UserCard key={u.id} user={u} onManage={openManage} />
            ))}
          </ul>
        </CardContent>
      ) : (
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
                  {/* `flex-wrap` is a WIDTH fix, not a cosmetic one. A
                      non-wrapping row of three `whitespace-nowrap` buttons
                      contributes the SUM of their widths to this cell's
                      min-content, and this is the last column, so the table's
                      min-content lands past the right edge of its scroll box:
                      measured at 1130px with the sidebar expanded (240px of
                      `md:pl-60`, 80px of page gutter, 48px of card padding),
                      the trailing Actions cell was reachable only by panning.
                      Allowing the row to wrap drops that contribution to the
                      WIDEST single button, which is roughly 200px less, and
                      the whole table fits again. `justify-end` keeps the
                      buttons hugging the right edge on both one and two lines.

                      NOT the `min-w-0` shape of this repo's grid min-content
                      latch: no grid or SVG is involved, and the ancestors here
                      (`main` > centred block > flex COLUMN) already bound this
                      card's width — a column flex item takes its cross size
                      from the container, so nothing upstream is inflating. The
                      overflow is generated inside the cell and has to be
                      solved inside it. */}
                  <div className="flex flex-wrap items-center justify-end gap-2">
                    {/* Offered on EVERY row, own included: the case this
                        exists for is an admin shortening their own name now
                        that it labels every transaction they enter. */}
                    <Button
                      type="button"
                      variant="outline"
                      size="sm"
                      data-rename-user-id={u.id}
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
                        data-reset-user-id={u.id}
                        onClick={() => openResetFromTable(u)}
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
                      data-delete-user-id={u.id}
                      onClick={() => openDeleteFromTable(u)}
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
      )}
    </Card>
      <AlertDialog
        open={resettingUser !== null}
        onOpenChange={(open) => {
          if (resetting) return;
          if (!open) setResettingUser(null);
        }}
      >
        <AlertDialogContent
          onCloseAutoFocus={(e) => {
            // Same Trigger-less shape as the manage dialog below — see
            // focusAfterConfirmClose. `false` on every close: a confirmed
            // reset keeps the row, so even that path returns to the button
            // that opened the chain.
            e.preventDefault();
            focusAfterConfirmClose(false);
          }}
        >
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
        <AlertDialogContent
          onCloseAutoFocus={(e) => {
            e.preventDefault();
            // Consumed here rather than inside the helper: only this
            // dialog's close can follow a successful delete, and reading the
            // flag anywhere shared would let one confirm's outcome leak into
            // the next one's focus restore.
            const removed = deleteSucceededRef.current;
            deleteSucceededRef.current = false;
            focusAfterConfirmClose(removed);
          }}
        >
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
        <DialogContent
          onCloseAutoFocus={(e) => {
            e.preventDefault();
            focusAfterRenameClose();
          }}
        >
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
              <DisplayNameField control={renameForm.control} />
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
      {/* The phone's one-and-only per-user surface. The table offers four
          controls on one row; a 360px card cannot, so the card offers one
          button and this dialog holds everything behind it.

          CONTROLLED, WITH NO `DialogTrigger` — matching the five other
          Trigger-less overlays in this app. That is not a style choice: the
          trigger is per-row and the dialog is one, so a trigger would have to
          be rendered inside every card and the last one mounted would win.
          The cost is that Radix's focus restore has nothing to restore to,
          which `onCloseAutoFocus` below pays.

          Not mounted behind `isMobile`, deliberately: rotating a phone
          mid-edit would otherwise discard what has been typed. Only a card can
          open it, so it cannot appear on a desktop that has no cards. */}
      <Dialog
        open={managingUser !== null}
        onOpenChange={(open) => {
          if (savingManagedName) return;
          if (!open) setManagingUser(null);
        }}
      >
        <DialogContent onCloseAutoFocus={(e) => {
          // `preventDefault` is belt-and-braces — Radix already calls it — so
          // the outcome does not depend on that continuing to be true.
          e.preventDefault();
          if (confirmHandoffRef.current) {
            // A confirm is opening. Its own FocusScope has already taken
            // focus; moving it back to the card now would steal it from the
            // Cancel button the user is about to reach for.
            confirmHandoffRef.current = false;
            return;
          }
          focusAfterManageClose();
        }}>
          <DialogHeader>
            <DialogTitle>
              Manage <span className="font-mono">{managingUsername}</span>
            </DialogTitle>
            {/* Says the two-writes rule out loud, because the dialog LOOKS
                like a form with a single Save and is not one. */}
            <DialogDescription>
              Everything you can change about {managingUserName}. The display
              name and the role save separately.
            </DialogDescription>
          </DialogHeader>
          <div className="flex flex-col gap-6">
            <Form {...manageNameForm}>
              <form
                onSubmit={(e) =>
                  void manageNameForm.handleSubmit(onSaveManagedName)(e)
                }
                className="flex flex-col gap-4"
                noValidate
              >
                <DisplayNameField control={manageNameForm.control} />
                <Button
                  type="submit"
                  className="w-full"
                  disabled={savingManagedName}
                >
                  {savingManagedName ? 'Saving…' : 'Save display name'}
                </Button>
              </form>
            </Form>
            <Separator />
            {/* Offered on your own row exactly as the table offers it. The
                server refuses a self-demotion with a 400 that arrives as a
                toast, and adding a frontend gate here would be a behaviour
                change nobody asked for — the two surfaces would then disagree
                about what is even attemptable. */}
            <div className="flex flex-col gap-2">
              <Label htmlFor="manage-user-role">Role</Label>
              <Select
                value={managingRow?.role ?? ROLE_MEMBER}
                onValueChange={(v) => {
                  if (v !== ROLE_ADMIN && v !== ROLE_MEMBER) return;
                  if (!managingRow) return;
                  void handleRoleChange(managingRow.id, v);
                }}
              >
                <SelectTrigger
                  id="manage-user-role"
                  aria-label={`Role for ${managingUsername}`}
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
            </div>
            <Separator />
            <div className="flex flex-col gap-2">
              {/* Hidden on your own row, exactly as in the table: you rotate
                  your own password in the account card above, which runs the
                  same cascade WITH a current-password check. The admin reset
                  deliberately has none. */}
              {managingRow && currentUser && managingRow.id !== currentUser.id && (
                <Button
                  type="button"
                  variant="outline"
                  className="w-full"
                  aria-label={`Reset password for ${managingUsername}`}
                  onClick={() => confirmFromManage(openReset, managingRow)}
                >
                  Reset password
                </Button>
              )}
              {managingRow && (
                <Button
                  type="button"
                  variant="destructive"
                  className="w-full"
                  aria-label={`Delete ${managingUsername}`}
                  onClick={() => confirmFromManage(openDelete, managingRow)}
                >
                  Delete
                </Button>
              )}
            </div>
          </div>
        </DialogContent>
      </Dialog>
    </>
  );
}

/* ---------- API tokens tab ---------- */

/**
 * The Alert body inside the reveal, referenced by the dialog's
 * `aria-describedby`.
 *
 * The reveal deliberately renders no `DialogDescription`: the amber Alert
 * already carries the one-time warning under a visible title, and a
 * DialogDescription would restate it verbatim to a screen reader. Pointing
 * `aria-describedby` at the Alert body is what gives the dialog a real
 * accessible description AND silences Radix's missing-description warning.
 *
 * (The comment that used to sit on `ShowOnceReveal` claimed this wiring was
 * already in place. It was not — `aria-describedby` appeared nowhere in this
 * file, and the id it named did not exist.)
 */
const NEW_TOKEN_REVEAL_DESCRIPTION_ID = 'new-token-reveal-description';

/**
 * Marks the button the reveal returns focus to on close. An attribute rather
 * than a ref because the reveal lives above the layout swap and the button
 * below it — see `NewTokenRevealProvider`.
 */
const CREATE_TOKEN_TRIGGER_ATTR = 'data-create-token-trigger';

/**
 * Marks a per-token Revoke button, so a closing confirm can find its way back
 * to the control that opened it.
 *
 * On the BUTTON rather than the row, and on both presentations, because the
 * card list and the table render different elements for the same token — one
 * query has to find whichever is mounted.
 */
const REVOKE_TOKEN_ID_ATTR = 'data-revoke-token-id';

/** The same, for the one Revoke all button. */
const REVOKE_ALL_TRIGGER_ATTR = 'data-revoke-all-trigger';

function ShowOnceReveal({
  token,
  onClose,
}: {
  /** Live state — null from the moment the user dismisses. See `shown`. */
  token: CreateTokenResponse | null;
  onClose: () => void;
}) {
  const revealRef = useRef<HTMLTextAreaElement>(null);

  // THE EXIT LATCH. Radix keeps this mounted through its ~200ms exit
  // animation, but `token` is already null on the commit that starts it — so
  // rendering straight from the prop faded out an empty bordered box instead
  // of the dialog the user just dismissed.
  //
  // The latch lives HERE, in the component the dialog unmounts, rather than in
  // the provider that outlives it. That is the whole point: the copy dies with
  // the dialog automatically, so the plaintext's lifetime is exactly the
  // dialog's, with no cleanup line to write, to forget, or to leave untested.
  // A ref in the provider was the first shape and is not available anyway —
  // reading one during render is what `react-hooks/refs` forbids.
  const [shown, setShown] = useState(token);
  // React's sanctioned adjust-state-during-render, not an effect: it covers
  // the create → dismiss → create-again-within-200ms case, where this instance
  // is re-shown without ever unmounting and a mount-time capture would display
  // the PREVIOUS token.
  if (token !== null && token !== shown) setShown(token);

  // Unreachable in practice — Radix does not mount this until `open` is true,
  // which requires a token — and present so the type narrows without a cast.
  if (shown === null) return null;

  return (
    <>
      <DialogHeader>
        <DialogTitle>Save your new token</DialogTitle>
      </DialogHeader>
      <div className="grid gap-4">
        <Alert variant="warning">
          <AlertTriangle className="h-4 w-4" />
          <AlertTitle>Copy it now</AlertTitle>
          <AlertDescription id={NEW_TOKEN_REVEAL_DESCRIPTION_ID}>
            This is the only time your token will be shown. We hash
            tokens at rest — if you lose it, revoke and create a new
            one.
          </AlertDescription>
        </Alert>
        <div className="grid gap-2">
          <Label htmlFor="api-token-reveal">Your new API token</Label>
          {/*
            A WRAPPING field, and stacked above its Copy button rather than
            sharing a row with it. Both halves are the same defect: a token is
            `spdr_` + 26 + `_` + 6 = 38 unbreakable characters, an `<input>`
            cannot wrap, and on a 360px phone the field was a window onto the
            middle of the string with 188px of it scrolled out of sight — the
            Copy button beside it having taken another ~90px off that window.

            This matters more here than clipping usually would. SpenDrop is
            self-hosted, and `navigator.clipboard` rejects outright on
            `http://192.168.x.x`, so on the household's own LAN the Copy button
            is the path that DOESN'T work and reading the token off the screen
            is the path that does. A token that cannot be read has to be
            revoked and re-minted.

            `<textarea readOnly>` rather than a styled `<div>`: it stays a
            labelable form control, so `<Label htmlFor>` still names it and
            every existing query for it still resolves, and `.focus()` +
            `.select()` keep working verbatim for the clipboard-blocked path
            below. `rows={2}` holds all 38 characters at 360px; a longer future
            token would scroll VERTICALLY, which is still reachable.

            CLASS FIDELITY, stated exactly rather than as "tracks input.tsx":
            it carries that primitive's border, radius, background, padding,
            ring and `md:text-sm` type ramp, plus its `coarse:min-h-11` touch
            floor — this is a field a thumb selects text in. It deliberately
            does NOT carry `h-10` (an auto-height box must not be pinned to one
            row) and does not carry the `placeholder:`, `file:` or `disabled:`
            variants, none of which a read-only, always-populated field can
            ever reach.

            The credential attributes are the same class of fix as the display
            name field's `autoComplete="off"` above, but they matter MORE here,
            and for a reason specific to the element: a textarea's value is DOM
            TEXT CONTENT, not a value attribute, so page translators, spell
            checkers and scrapers walk it the way they walk any prose. The
            plaintext must not be sent to a translation service, offered to a
            password manager, or "corrected".
          */}
          <textarea
            id="api-token-reveal"
            ref={revealRef}
            value={shown.token}
            readOnly
            rows={2}
            spellCheck={false}
            autoComplete="off"
            autoCorrect="off"
            autoCapitalize="off"
            translate="no"
            data-1p-ignore
            data-lpignore="true"
            onFocus={(e) => e.currentTarget.select()}
            className="flex w-full resize-none rounded-md border border-input bg-background px-3 py-2 font-mono text-base ring-offset-background coarse:min-h-11 [overflow-wrap:anywhere] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 md:text-sm"
          />
          <Button
            type="button"
            variant="outline"
            className="w-full sm:w-fit sm:justify-self-start"
            onClick={async () => {
              try {
                await navigator.clipboard.writeText(shown.token);
                toast.success('Copied to clipboard');
              } catch {
                // navigator.clipboard.writeText rejects on insecure
                // contexts (common for self-hosted SpenDrop served
                // over HTTP on a LAN IP — localhost is treated as
                // secure, but `http://192.168.x.x` is not) and on
                // explicit permission denial. Either way the user
                // cannot rely on the button alone. Focus + select
                // the reveal field so one Ctrl/Cmd+C still copies,
                // and tell them so via a toast. The dialog stays
                // open and the plaintext stays on screen — the user
                // does not have to re-trigger the Create flow.
                revealRef.current?.focus();
                revealRef.current?.select();
                toast.info(
                  'Press Ctrl/Cmd+C to copy \u2014 clipboard blocked in this context.',
                );
              }
            }}
          >
            {/* No icon margin: `Button`'s base already lays its children
                out with `gap-2`, so `mr-2` here would add a second gap.
                This was the only icon-and-text Button in the app that
                carried one. */}
            <Copy className="h-4 w-4" aria-hidden="true" />
            Copy
          </Button>
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

interface NewTokenReveal {
  /** Put a freshly minted plaintext token on screen. */
  show: (token: CreateTokenResponse) => void;
}

const NewTokenRevealContext = createContext<NewTokenReveal | null>(null);

function useNewTokenReveal(): NewTokenReveal {
  const reveal = useContext(NewTokenRevealContext);
  if (reveal === null) {
    // Not defensive padding. The whole point of this context is that the
    // reveal outlives `<ApiTokensSection>`, so a section rendered outside the
    // provider would compile, run, mint a token and then drop it on the floor
    // with no visible symptom. Fail at first render instead.
    throw new Error(
      'useNewTokenReveal must be used inside <NewTokenRevealProvider>',
    );
  }
  return reveal;
}

/**
 * Holds the one-time plaintext token, and renders the dialog that shows it.
 *
 * WHY IT LIVES ABOVE THE PAGE'S LAYOUT FORK. `<Settings>` mounts either
 * `<SettingsSectionPicker>` or `<Tabs>`, never both, and crossing `md` swaps
 * one for the other. Those are different component types in the same position,
 * so React unmounts the whole subtree and every section's state with it. For
 * four of the five sections that costs a scroll position or a half-typed form.
 * For this one it costs a secret the server hashed at rest and will never hand
 * out again: rotating a phone while the token was on screen left the user
 * holding a token they now have to revoke and re-mint, and nothing said so.
 *
 * So the state is lifted to the one place above the swap — and the DIALOG is
 * lifted with it, rather than left in the section to be re-opened from
 * surviving state. That second half is what makes the guarantee structural
 * instead of a recovery: the reveal's DOM never unmounts, so a rotation in
 * either direction is a no-op for it — no flicker, no re-entry animation, no
 * dropped focus. It also closes the window between POST and response, where a
 * rotation would otherwise unmount the component holding the setState that
 * receives the token.
 *
 * Chosen over threading the token through `renderSettingsSection`: that
 * function is deliberately a pure (tab, role) → component map, so that "the
 * two surfaces cannot render different things for the same value", and a third
 * parameter only one section reads would put an API-token concern into every
 * section's signature. Chosen over `sessionStorage` or a module-scope variable
 * for the obvious reason — neither is a home for a plaintext credential.
 */
function NewTokenRevealProvider({
  children,
  fallbackFocusRef,
}: {
  children: ReactNode;
  /**
   * Where focus goes on close when the button that opened the flow is no
   * longer in the document. Owned by the page rather than looked up here,
   * because it has to be something that outlives every section — see the
   * close handler below for why `<body>` is not an acceptable answer.
   */
  fallbackFocusRef: RefObject<HTMLElement | null>;
}) {
  const [token, setToken] = useState<CreateTokenResponse | null>(null);

  // Referentially stable, which is load-bearing rather than an optimisation:
  // an in-flight create whose section unmounts mid-request still calls this
  // from a stale closure, and it has to reach the live provider.
  const value = useMemo<NewTokenReveal>(() => ({ show: setToken }), []);

  return (
    <NewTokenRevealContext.Provider value={value}>
      {children}
      <Dialog
        open={token !== null}
        onOpenChange={(open) => {
          // Close requests are ignored on purpose, carried over verbatim from
          // the dialog this replaces: once the plaintext is on screen, Escape
          // and a backdrop tap must not silently destroy it. The only exit is
          // "I've saved my token".
          if (!open) return;
        }}
      >
        <DialogContent
          aria-describedby={NEW_TOKEN_REVEAL_DESCRIPTION_ID}
          // The built-in X would call the `onOpenChange` above, which refuses
          // — so it rendered, took a tab stop, and did nothing. A control that
          // looks operable and is not is worse than no control, and on the one
          // surface holding an unrecoverable secret it is worse still: the
          // user taps the affordance every other dialog on this page honours
          // and concludes the token is saved. Removed rather than made to
          // work, because "the only exit is the explicit button" is the
          // deliberate design here.
          showCloseButton={false}
          onCloseAutoFocus={(e) => {
            // No DialogTrigger sits above this content — it opens
            // programmatically from the create flow — so Radix's own restore
            // would run `triggerRef.current?.focus()` against null and drop
            // focus on `<body>`. Same shape, and same reason, as the four
            // handoffs in `<HouseholdUsersCard>`.
            //
            // Queried at close time rather than held as a ref because the
            // button it returns to lives BELOW the layout swap: a rotation
            // while the reveal was open replaced that button with a new one.
            const anchor = document.querySelector<HTMLElement>(
              `[${CREATE_TOKEN_TRIGGER_ATTR}]`,
            );
            // The fallback is a real target, not Radix's default. Radix's
            // default here IS focus-to-body — the restore runs against a null
            // triggerRef — which strands a keyboard or screen-reader user at
            // the top of the document with no announcement. The page heading
            // is the honest place to land: it always exists (the provider and
            // the heading are siblings) and it says where you now are.
            //
            // This arm is believed unreachable today: `activeTab` survives a
            // rotation and both layouts render `<ApiTokensSection>`, so the
            // Create token button is always in the document while the reveal
            // is up. It is written for the reachable version of tomorrow.
            e.preventDefault();
            (anchor ?? fallbackFocusRef.current)?.focus();
          }}
        >
          {/* Handed the LIVE state, null and all. The component latches its
              own copy for the exit animation — see `ShowOnceReveal` — so the
              plaintext's lifetime is bounded by this dialog's mount rather
              than by a cleanup line here that could be forgotten. */}
          <ShowOnceReveal token={token} onClose={() => setToken(null)} />
        </DialogContent>
      </Dialog>
    </NewTokenRevealContext.Provider>
  );
}

/**
 * One API token as a stacked card — the below-`md` presentation of the row the
 * table renders.
 *
 * Six columns do not survive a 360px viewport: measured on the built
 * container, this section panned 308px, and the column that ends up furthest
 * out is Actions — the Revoke button, which is the only thing you can DO to a
 * token from this surface.
 *
 * ANATOMY: identity on top (name, then the prefix that identifies the token in
 * a log line), the three table columns that are facts rather than actions as a
 * definition list beneath it, and Revoke as one full-width button. The `<dl>`
 * is not decoration — a table's values are named by column headers, and a card
 * that drops the headers leaves "Never" sitting on its own with nothing saying
 * which of the three dates it is. Names and values as `<dt>`/`<dd>` pairs put
 * that back for sighted and screen-reader users with one piece of markup,
 * which an `sr-only` prefix would have done for only one of them.
 *
 * Register follows the page's other `<dl>` (the account card's): the name is
 * muted and unweighted, the value takes the foreground. Naming the value with
 * a bolder term than the value itself inverts the emphasis — the fact is what
 * you came to read. Left-aligned rather than justified, which is the one place
 * this diverges: it mirrors the column alignment of the table it replaces.
 */
function ApiTokenCard({
  token,
  lastUsed,
  expires,
  created,
  createdAbsolute,
  onRevoke,
}: {
  token: ApiToken;
  lastUsed: string;
  expires: string;
  created: string;
  createdAbsolute: string;
  onRevoke: (token: ApiToken) => void;
}) {
  return (
    <li className="flex flex-col gap-3 p-4">
      <div className="flex min-w-0 flex-col gap-1">
        {/* A token name is user-supplied and can be one long unbroken word
            ("HomepageDashboardIntegration"). `overflow-wrap:anywhere` rather
            than `truncate`: this is the string the Revoke confirmation quotes
            back, so it has to be readable in full before you tap Revoke. */}
        <div className="font-medium [overflow-wrap:anywhere]">
          {token.name}
        </div>
        <div className="font-mono text-sm text-muted-foreground [overflow-wrap:anywhere]">
          {token.token_prefix}
        </div>
      </div>
      <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-xs">
        <dt className="text-muted-foreground">Last used</dt>
        <dd className="[overflow-wrap:anywhere]">{lastUsed}</dd>
        <dt className="text-muted-foreground">Expires</dt>
        <dd>{expires}</dd>
        <dt className="text-muted-foreground">Created</dt>
        {/* The absolute date as TEXT, not a `title`. The table can afford a
            hover tooltip; this card renders only below `md`, where there is no
            hover and a `title` is simply unreachable — the exact date would
            have been visible on the one presentation that has a mouse and
            invisible on the one that does not. The relative phrase stays
            first because it is what you scan for; the date follows it, muted,
            for when "2 months ago" is not precise enough. */}
        <dd>
          {created}{' '}
          <span className="text-muted-foreground">({createdAbsolute})</span>
        </dd>
      </dl>
      {/* `min-h-11` even though `Button` floors itself on a coarse pointer:
          this card renders only below `md`, where 44px is right for a mouse
          too. Same deliberate redundancy as `<UserCard>`'s action row, and
          emphatically NOT paired with a `md:min-h-0` — that is emitted after
          the pointer variant and would defeat the primitive's floor on the
          tablet. */}
      <Button
        type="button"
        variant="destructive"
        className="min-h-11 w-full"
        {...{ [REVOKE_TOKEN_ID_ATTR]: token.id }}
        aria-label={`Revoke ${token.name}`}
        onClick={() => onRevoke(token)}
      >
        Revoke
      </Button>
    </li>
  );
}

function ApiTokensSection() {
  const isMobile = useIsMobileViewport();
  // The plaintext is NOT held here. This component is unmounted by the page's
  // md layout swap; the reveal is not. See `NewTokenRevealProvider`.
  const reveal = useNewTokenReveal();
  const [tokens, setTokens] = useState<ApiToken[]>([]);
  const [createOpen, setCreateOpen] = useState(false);
  const [creating, setCreating] = useState(false);
  // Set for exactly the one close that hands off to the reveal. The create
  // dialog HAS a trigger, so Radix restores focus to that button when it
  // closes — and Radix's `Presence` does not unmount the content until the
  // 200ms exit animation finishes, so that restore lands AFTER the reveal has
  // mounted and focused itself, yanking focus out of the dialog the user now
  // has to read. Same shape, and the same failure, as `confirmHandoffRef` in
  // `<HouseholdUsersCard>`.
  //
  // DISCLOSED AS UNPROVEN: this line cannot be mutation-killed here. happy-dom
  // runs no exit animation, so the create dialog unmounts synchronously and
  // its restore fires BEFORE the reveal mounts — measured both ways, focus
  // ends on the reveal field either way. The suppression is kept for the
  // browser's ordering, not the test environment's.
  const revealHandoffRef = useRef(false);
  const [revokingToken, setRevokingToken] = useState<ApiToken | null>(null);
  // Sticky display-name for the revoke-one dialog title, captured when the
  // user clicks a Revoke button. Kept as state (not derived from
  // revokingToken) so Radix's close-exit animation can still show the name
  // while revokingToken has already flipped to null.
  const [revokingTokenName, setRevokingTokenName] = useState('');
  const [revokeAllOpen, setRevokeAllOpen] = useState(false);
  const [revoking, setRevoking] = useState(false);

  // Which control opened the confirm that is currently up, so its close can
  // send focus back there. A ref rather than state for the same reason
  // `<HouseholdUsersCard>` uses one: the confirm's target is already null by
  // the time `onCloseAutoFocus` runs.
  //
  // `{ kind: 'one' }` carries the token id rather than an element, because the
  // anchor is re-queried at close time — the two presentations render
  // different Revoke buttons for the same token, and a rotation while the
  // confirm is open swaps one for the other. Both carry
  // `data-revoke-token-id`, so one query serves both.
  const revokeOpenerRef = useRef<
    { kind: 'one'; id: number } | { kind: 'all' } | null
  >(null);

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

  /**
   * Where focus goes when either revoke confirm closes.
   *
   * Neither confirm has an `AlertDialogTrigger` — none exists anywhere in this
   * app — so Radix's own restore runs `triggerRef.current?.focus()` against
   * null and every close, cancel and confirm alike, dropped focus on `<body>`.
   * Same defect and same remedy as `focusAfterConfirmClose` in
   * `<HouseholdUsersCard>`.
   *
   * The opener is re-queried here rather than captured as an element: a
   * refetch, a revoke or a rotation across `md` can all have replaced the row
   * since the confirm opened. A missing anchor is the normal case after a
   * SUCCESSFUL revoke — that token's button is gone with it — so the fallback
   * is not an error path; it is where a confirmed revoke always lands. The
   * Create token button is the right destination for it: it is the one control
   * on this card that survives every list state, including the empty one that
   * a revoke-all produces.
   */
  function focusAfterRevokeClose() {
    const opener = revokeOpenerRef.current;
    revokeOpenerRef.current = null;
    const anchor =
      opener === null
        ? null
        : document.querySelector<HTMLElement>(
            opener.kind === 'one'
              ? `[${REVOKE_TOKEN_ID_ATTR}="${opener.id}"]`
              : `[${REVOKE_ALL_TRIGGER_ATTR}]`,
          );
    (
      anchor ??
      document.querySelector<HTMLElement>(`[${CREATE_TOKEN_TRIGGER_ATTR}]`)
    )?.focus();
  }

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
      // Hand the plaintext to the provider ABOVE the layout swap, then close
      // this dialog. Sequential, single-layer — never nested; the same
      // handoff shape `confirmFromManage` uses, and for the same reasons
      // (stacked scrims, and Radix's `hideOthers()` making the outer dialog
      // unreachable to assistive tech).
      revealHandoffRef.current = true;
      reveal.show(body);
      setCreateOpen(false);
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
      // The server WRITES the token before it responds, so a timeout, a
      // dropped connection or a 5xx on the way back can leave a live token
      // the user never saw and cannot revoke — it is absent from this list
      // until something else remounts the section. Re-read the list so an
      // orphan shows up immediately, next to its own Revoke button.
      //
      // Swallowed separately from the toast above: the user has already been
      // told the create failed, and a second error toast about the refresh
      // would describe a request they did not make.
      void fetchTokens().catch(() => {
        /* Nothing further to offer — the list is already stale, and the
           create failure has been reported. */
      });
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
      {/* Stacked below `md`, side by side above it. The description and the
          two nowrap buttons are each about half of a 360px viewport, so as one
          row their min-content ran past the card and the header panned along
          with the table beneath it. This is layout only — no duplicated
          controls, no second a11y tree — so it is a CSS gate rather than the
          JS one the row lists use. */}
      <CardHeader className="flex flex-col items-start gap-4 md:flex-row md:justify-between">
        <div className="grid gap-1.5">
          <CardTitle className="text-base">API tokens</CardTitle>
          <CardDescription>
            Personal access tokens for scripts, dashboards, and
            third-party integrations. Each token has full access to
            your account.
          </CardDescription>
        </div>
        <div className="flex flex-wrap gap-2">
          <Dialog
            open={createOpen}
            onOpenChange={(open) => {
              // Guard close-while-submitting: if the POST is in flight and we
              // let the user close the dialog here, the create form is torn
              // down under a request that is still going to succeed — the user
              // gets a token they never saw asked for. Mirror the
              // revoke-dialog busy guard.
              //
              // There is no longer a close-while-revealed guard here, because
              // the reveal is no longer inside this dialog: the plaintext is
              // shown by `NewTokenRevealProvider`, which is above the page's
              // layout swap and refuses its own close for exactly that reason.
              if (creating) return;
              if (open) {
                setCreateOpen(true);
                return;
              }
              setCreateOpen(false);
              createForm.reset();
            }}
          >
            <DialogTrigger asChild>
              <Button size="sm" {...{ [CREATE_TOKEN_TRIGGER_ATTR]: '' }}>
                Create token
              </Button>
            </DialogTrigger>
            <DialogContent
              onCloseAutoFocus={(e) => {
                // Only on the handoff close. Every other close (Escape,
                // backdrop, the X) should restore focus to this dialog's
                // trigger exactly as Radix intends.
                if (!revealHandoffRef.current) return;
                revealHandoffRef.current = false;
                e.preventDefault();
              }}
            >
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
            </DialogContent>
          </Dialog>
          {tokens.length > 0 && (
            <Button
              size="sm"
              variant="destructive"
              {...{ [REVOKE_ALL_TRIGGER_ATTR]: '' }}
              onClick={() => {
                revokeOpenerRef.current = { kind: 'all' };
                setRevokeAllOpen(true);
              }}
            >
              Revoke all ({tokens.length})
            </Button>
          )}
        </div>
      </CardHeader>
      {/* The empty state is shared and already width-agnostic, so the fork
          sits INSIDE it: one presentation or the other, chosen in JS, never
          `md:hidden` on two. Two trees would put a second "Revoke <name>"
          button in the document for every token — and on a surface whose only
          action destroys credentials, an ambiguous Revoke is not a cosmetic
          problem. See `useIsMobileViewport`. */}
      {tokens.length === 0 ? (
        <CardContent>
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
        </CardContent>
      ) : isMobile ? (
        <CardContent className="px-0 pb-2">
          {/* Tailwind's preflight strips the list-style and Safari drops the
              list role with it — hence the explicit `role`. Edge to edge, as
              `<UserCard>`'s list is: `CardContent`'s `p-6` would spend 48px of
              a 360px viewport on gutters these rows do not need, and each card
              restores a 16px one of its own. */}
          <ul
            role="list"
            aria-label="API tokens"
            className="flex flex-col divide-y divide-border border-t"
          >
            {tokens.map((t) => (
              <ApiTokenCard
                key={t.id}
                token={t}
                lastUsed={formatLastUsed(t)}
                expires={formatExpires(t)}
                created={formatDistanceToNowStrict(new Date(t.created_at), {
                  addSuffix: true,
                })}
                createdAbsolute={new Date(t.created_at).toLocaleDateString()}
                onRevoke={(target) => {
                  // Capture the name at click time — the AlertDialog title
                  // reads revokingTokenName, which stays set through the
                  // close-exit animation after revokingToken flips to null.
                  revokeOpenerRef.current = { kind: 'one', id: target.id };
                  setRevokingTokenName(target.name);
                  setRevokingToken(target);
                }}
              />
            ))}
          </ul>
        </CardContent>
      ) : (
        <CardContent>
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
                      {...{ [REVOKE_TOKEN_ID_ATTR]: t.id }}
                      onClick={() => {
                        // Capture the name at click time — the AlertDialog
                        // title reads revokingTokenName, which stays set
                        // through the close-exit animation after
                        // revokingToken flips back to null.
                        revokeOpenerRef.current = { kind: 'one', id: t.id };
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
        </CardContent>
      )}
    </Card>
      <AlertDialog
        open={revokingToken !== null}
        onOpenChange={(open) => {
          if (revoking) return;
          if (!open) setRevokingToken(null);
        }}
      >
        <AlertDialogContent
          onCloseAutoFocus={(e) => {
            e.preventDefault();
            focusAfterRevokeClose();
          }}
        >
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
        <AlertDialogContent
          onCloseAutoFocus={(e) => {
            e.preventDefault();
            focusAfterRevokeClose();
          }}
        >
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
  sign_mismatch: 'amount signs disagree',
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
  // The RAW STRING is the state, and the number is derived. A numeric state
  // could not represent "empty": `Number('')` is 0, so the moment the user
  // cleared the field to retype it, the controlled input snapped to "0" and
  // the next keystroke appended to that — backspace-and-retype was impossible.
  // The numeric keypad this field now asks for makes clear-and-retype the
  // normal way to change a year, so the trap got easier to hit.
  //
  // `Number(yearInput)` reproduces the old coercion EXACTLY, including
  // `Number('') === 0`, so every export URL this section builds is unchanged
  // for every input — the fix is confined to what the box displays. (An empty
  // field still exports year 0; that is pre-existing and deliberately left
  // alone here rather than folded into a typing fix.)
  const [yearInput, setYearInput] = useState(String(now.getFullYear()));
  const year = Number(yearInput);
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
          {/* `flex-wrap`, and `max-w-md` stays. Without the wrap this row's
              natural width is 355px — Year `w-28` (112) + gap 12 + Month
              `w-36` (144, mounted by default since exportMode starts
              'monthly') + gap 12 + the Export button (~75, whitespace-nowrap
              from buttonVariants) — against 278px of content box at a 360px
              viewport (360 − 32 AppShell px-4 − 2 card border − 48
              CardContent p-6). It panned the whole page 36px.
              Wrapped, line 1 is 268 of 278 and no single child exceeds 144px,
              so the row cannot overflow above roughly a 226px viewport.
              Desktop is bit-identical: 355 natural is under `max-w-md`'s 448,
              so it never wraps once there is room — and REMOVING max-w-md
              instead would throw the Export button to the far right of a
              1400px column. Same idiom as `Budgets.tsx`'s filter row. */}
          <div className="flex max-w-md flex-wrap items-end gap-3">
            <div className="flex flex-col gap-2">
              <Label htmlFor="export-year">Year</Label>
              <Input
                id="export-year"
                type="number"
                // A year has no fraction, so the digits keypad rather than the
                // decimal one — the only `numeric` hint on this page.
                inputMode="numeric"
                value={yearInput}
                onChange={(e) => setYearInput(e.target.value)}
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
            {/* `coarse:gap-6` on the switch rows ONLY, and the number is
                derived rather than chosen. `Switch` grows its hit area with a
                `coarse:before:-inset-y-3` pseudo-element; an absolute pseudo
                resolves its insets against the PADDING box, which `border-2`
                leaves at 20px, so the band is 20 + 2×12 = 44px and reaches
                (44 − 24) / 2 = 10px past the border box above and below. A
                one-line `Label` is 14px against the switch's 24px, so these
                rows are exactly switch-height and all 10px of that reach lands
                in the gap. The pitch therefore has to exceed 24 + 10 + 10 =
                44px: at `gap-4` the bands overlap by 4px and a tap in the seam
                goes to whichever row paints its pseudo last rather than the one
                under the thumb, and at `gap-5` they merely TILE — a shared
                boundary is not clearance, it resolves the same way. 24px leaves
                4px of dead space between them.

                Not the other lever: the band IS the 44px target, so trimming it
                to fit a smaller gap puts the switch under the floor. The rows
                below are left at `gap-4` because their controls carry
                `coarse:min-h-11` on their own boxes and overhang nothing, so
                the worst seam there is 16 − 10 = 6px and still positive. The
                fine-pointer rhythm is untouched: no pseudo, no `coarse:` gap.
                Derived in `Settings.notifications.test.tsx` from the rendered
                class strings rather than restated as a constant. */}
            <div className="flex flex-col gap-4 coarse:gap-6">
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
            </div>

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
                {/* `w-36`, not `w-28`, on all three time inputs: the 12h
                    "hh:mm AM" rendering needs ~140px at the phone's 16px
                    font, and 112px clipped the value the field exists to
                    show (measured scrollWidth 128-134 vs clientWidth 110). */}
                <Input
                  id="digest-time"
                  type="time"
                  className="w-36"
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
                className="w-36"
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
                className="w-36"
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
//
// A `Map`, not a `Record`, because the key comes straight off the query string
// and the two failure modes it removes are both real. A plain object literal
// answers for every key on `Object.prototype`, so `?tab=constructor` was a
// truthy hit that toasted "undefined has its own page now" with an Open button
// wired to `navigate(undefined)`. And `Record<string, T>` lookups are typed as
// always-present without `noUncheckedIndexedAccess` (this project does not set
// it), so `moved.label` was reading through a guarantee the type system had no
// basis for. `Map.get` returns `T | undefined` and consults no prototype, which
// closes both — and closes them for the next lookup site too, rather than
// requiring an `Object.hasOwn` that a future reader has to remember.
const MOVED_TABS = new Map<string, { route: string; label: string }>([
  ['savings', { route: '/savings', label: 'Savings' }],
  ['budgets', { route: '/budgets', label: 'Budgets' }],
  // General split across Budgets and the remaining Settings tabs —
  // point at Budgets since that's where the monthly-budget editor lives.
  ['general', { route: '/budgets', label: 'Budgets' }],
]);

/**
 * One Settings section's body. The single place that maps a tab value to its
 * component, so the two surfaces cannot render different things for the same
 * value.
 */
function renderSettingsSection(value: SettingsTab, admin: boolean) {
  switch (value) {
    case 'account':
      return <AccountSection admin={admin} />;
    case 'currencies':
      return <CurrenciesSection />;
    case 'api-tokens':
      return <ApiTokensSection />;
    case 'notifications':
      return <NotificationsSection />;
    case 'data':
      return <DataSection admin={admin} />;
  }
}

/**
 * The phone-width replacement for the tab strip: a Select that chooses which
 * section is shown, and the section itself as a labelled region.
 *
 * NOT A HIDDEN TAB STRIP, and that is the whole design decision. The obvious
 * cheap move — keep `TabsList` with `sr-only` and let a Select drive the same
 * value — leaves TWO controls for one choice in the accessibility tree, since
 * `sr-only` hides from sight and not from a screen reader. A phone user would
 * meet a five-item tablist AND a combobox that do the same thing.
 *
 * The other obvious move is worse, and it is measured rather than assumed:
 * dropping `TabsList` while keeping `Tabs`/`TabsContent` leaves every panel
 * with `aria-labelledby` pointing at a trigger id that no longer exists —
 * probed on this Radix version, the panels still mount and switch correctly,
 * `role="tabpanel"` is still emitted, and the IDREF resolves to NOTHING. So
 * every section silently loses its accessible name, and `role="tabpanel"`
 * survives with no `tablist` anywhere in the document, which is not a valid
 * ARIA structure. It looks perfect on screen.
 *
 * So the phone does not use the tab pattern at all. A combobox that swaps a
 * region is what this actually is, and it is named as such.
 */
function SettingsSectionPicker({
  admin,
  value,
  onChange,
}: {
  admin: boolean;
  value: SettingsTab;
  onChange: (next: string) => void;
}) {
  const sections = visibleSettingsSections(admin);
  const label = settingsSectionLabel(value, admin);
  return (
    <div className="flex flex-col gap-6">
      <Select value={value} onValueChange={onChange}>
        {/* `h-11`, not the stock `h-10`: this is the only way to reach four of
            the five sections on a phone, so it carries the 44px touch floor the
            rest of the phone shell keeps. */}
        <SelectTrigger aria-label="Settings section" className="h-11 w-full">
          <SelectValue />
        </SelectTrigger>
        {/* No local `onCloseAutoFocus` and no per-option `min-h-11`: both were
            workarounds for defects that now live fixed in `ui/select.tsx` —
            the wrapper's blanket `preventDefault()` that cancelled Radix's
            focus restore, and the 32px stock option row. Restoring either here
            would only re-do what the primitive already does. */}
        <SelectContent>
          {sections.map((section) => (
            <SelectItem key={section.value} value={section.value}>
              {section.label(admin)}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      {/* A named region, so the section is a landmark a screen-reader user can
          jump to — the job the tabpanel did on the other surface. The name is
          the section's own label, which is also what the closed Select reads,
          so the control and the content agree. */}
      <section aria-label={label}>{renderSettingsSection(value, admin)}</section>
    </div>
  );
}

export function Settings() {
  const { user } = useAuth();
  const admin = isAdmin(user);
  const pageHeadingRef = useRef<HTMLHeadingElement>(null);
  // WIDTH, not pointer capability — unlike the heatmap's year grid, which
  // needs a pointer that can hit a sub-24px target. Nothing here depends on
  // hover or precision; the section labels simply do not fit a narrow page —
  // measured when there were six of them, see the `TabsList` note below. A
  // touch tablet at ~720px portrait getting the Select is correct.
  const isMobile = useIsMobileViewport();
  const [searchParams, setSearchParams] = useSearchParams();
  const navigate = useNavigate();
  const tabParam = searchParams.get('tab');
  // Role-clamped, not merely validated — see `resolveSettingsTab`. A member
  // arriving on `?tab=users` lands on `account`, because the phone surface
  // renders whatever value it is handed.
  const initialTab = resolveSettingsTab(tabParam, admin);
  const [activeTab, setActiveTab] = useState<SettingsTab>(initialTab);

  // One-shot forwarding toast for `?tab=savings|budgets|general`
  // bookmarks left over from before the page split. Runs on mount
  // only — re-toasting on subsequent in-app tab clicks would be
  // noisy.
  useEffect(() => {
    const moved = tabParam === null ? undefined : MOVED_TABS.get(tabParam);
    if (moved) {
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
    // Clamped here too, not just on the initial value: this is the second way
    // a `?tab=` reaches state, so validating without the role check would let
    // a later in-app navigation reopen the hole the initial clamp closes.
    const resolved = resolveSettingsTab(tabParam, admin);
    if (tabParam !== null && resolved !== activeTab) {
      setActiveTab(resolved);
    }
    // activeTab is intentionally excluded from deps: this effect is a
    // one-way URL → state sync. Including activeTab would re-run the
    // effect every time the user clicks a tab and could race with the
    // history listener. The guard above already prevents redundant
    // setState on equal values, so the only meaningful trigger is a
    // fresh tabParam coming in from the router.
    //
    // `admin` IS a dep, though the effect body is the only thing that reads it
    // and no section is `adminOnly` today. It costs exactly nothing now — the
    // extra run happens only when `admin` changes value, and while none is
    // `adminOnly` both roles reach every section, so `resolved` would come back
    // identical and the guard would refuse the setState anyway. What it buys is
    // the day the first `adminOnly` section lands: a role that flips true →
    // false while this page is mounted (a `refreshUser` echoing a demotion is
    // the live path) would otherwise leave `activeTab` sitting on a section the
    // clamp has started refusing, on the phone surface that renders whatever
    // value it is handed. Listing it now rather than leaving a note is
    // deliberate — a note is only read if someone happens to look here at
    // exactly the right moment, which is how the comment this page's switch
    // stack carried went two values out of date.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tabParam, admin]);

  /**
   * The single funnel for BOTH surfaces — the desktop strip's `onValueChange`
   * and the phone picker's — which is what keeps them from writing different
   * URLs for the same choice.
   *
   * REPLACE, NEVER PUSH, and that is the whole design decision. Pushing would
   * give a bookmarkable URL too, but it also makes Back walk backwards through
   * every section the user tried on the way here, and this page is a thing to
   * browse: five sections, one page, and a Back button the user presses to
   * LEAVE. Replacing rewrites the entry the user is standing on, so a later
   * push (any link off this page) still leaves a Settings entry behind that
   * carries the section — Back lands on the section they were reading, and the
   * address bar is shareable the whole time.
   *
   * NOTHING IS WRITTEN ON MOUNT, deliberately: `?tab=` means "a section was
   * chosen", so a bare `/settings` stays bare and keeps resolving to `account`
   * exactly as before. A canonicalising mount write would also rewrite a
   * retired `?tab=savings` bookmark out from under its own forwarding toast —
   * the toast fires once from the URL, and replacing that URL with the resolved
   * value means reloading the page the user just shared no longer reproduces
   * it.
   *
   * It cannot fight the inbound handling either. The URL → state effect re-runs
   * with the value this just wrote and its guard finds `resolved === activeTab`,
   * so it no-ops rather than looping; the forwarding toast runs on mount only,
   * so a write can never re-toast.
   *
   * `resolveSettingsTab` rather than `v as SettingsTab`: this is where a value
   * enters BOTH state and the URL, and it should be the same clamp the inbound
   * path uses, so the round trip is closed by construction — whatever is
   * written here is a value the loader hands back unchanged. Both surfaces
   * render from `visibleSettingsSections(admin)` and so cannot offer an invalid
   * or role-refused value today; the clamp is here to delete an unchecked cast,
   * not because a reachable caller violates it.
   */
  function handleTabChange(v: string) {
    const next = resolveSettingsTab(v, admin);
    // CHOSEN, not an oversight: re-picking the section that is already open
    // writes nothing, so arriving on a raw or retired value (`?tab=users`,
    // `?tab=savings` — both resolve to `account`) and then tapping Account
    // leaves that value in the URL until a DIFFERENT section is picked. It
    // follows from the same rule as the absent mount write: `?tab=` records a
    // choice that changed something, and the raw value is what the forwarding
    // toast reads on reload. It is also unreachable through either control —
    // neither Radix Tabs nor Radix Select emits a change for a click on the
    // value they already hold — so this is a note for a reader, not a branch.
    if (next === activeTab) return;
    setActiveTab(next);
    setSearchParams(
      (prev) => {
        // Rebuilt from the live params rather than a bare `{ tab }`, so a
        // param this page does not own survives the write.
        const params = new URLSearchParams(prev);
        params.set('tab', next);
        return params;
      },
      { replace: true },
    );
  }

  return (
    // ABOVE the fork below, deliberately. Everything inside the `isMobile`
    // ternary is torn down and rebuilt when the viewport crosses `md`; the
    // one-time token reveal is the one piece of state on this page whose loss
    // cannot be undone, so it is held out here instead.
    //
    // The heading doubles as the reveal's last-resort focus target, which is
    // why the ref is created here and handed down: it is the only element on
    // this page guaranteed to be mounted whenever the provider is.
    <NewTokenRevealProvider fallbackFocusRef={pageHeadingRef}>
    <div className="flex flex-col gap-6">
      {/* `tabIndex={-1}` makes this a focus anchor, not a tab stop — the same
          role `<HouseholdUsersCard>`'s CardTitle plays for its own dialogs. */}
      <h1
        ref={pageHeadingRef}
        tabIndex={-1}
        className="text-2xl font-semibold tracking-tight"
      >
        Settings
      </h1>
      {isMobile ? (
        <SettingsSectionPicker
          admin={admin}
          value={activeTab}
          onChange={handleTabChange}
        />
      ) : (
        <Tabs
          value={activeTab}
          onValueChange={handleTabChange}
          activationMode="manual"
          className="w-full"
        >
          {/* The strip scrolls sideways rather than widening the page — see
              the note on `scrollableTabsList`, which explains why
              `justify-start` is the half that must not be lost. Below `md`
              this is not rendered at all.

              THE FIGURES BELOW ARE A DATED MEASUREMENT, kept as recorded
              rather than rescaled. Taken 2026-08-09 (`cbaa4d0`), when this
              page still had SIX sections — `users` merged into `account` on
              2026-08-14 (`a8c484f`), so the strip carries five labels today.
              At a 360px viewport those six labels came to 568px of scrollWidth
              against 313 of client, and the Reports treatment (9px tick font,
              px-2) still came to 461px. Four short words fit that way; six
              long ones did not at any size worth reading. Five long ones have
              not been re-measured — dropping one label of six does not close a
              568-to-313 gap. */}
          <TabsList className={scrollableTabsList}>
            {visibleSettingsSections(admin).map((section) => (
              <TabsTrigger key={section.value} value={section.value}>
                {section.label(admin)}
              </TabsTrigger>
            ))}
          </TabsList>
          {visibleSettingsSections(admin).map((section) => (
            <TabsContent
              key={section.value}
              value={section.value}
              className="mt-6"
            >
              {renderSettingsSection(section.value, admin)}
            </TabsContent>
          ))}
        </Tabs>
      )}
      {/* Outside the Tabs on purpose: "which build am I on" is a property of
          the app, not of a tab, so it stays put wherever the user has
          navigated rather than hiding on four of the five panels. */}
      <AppVersion className="pt-2" />
    </div>
    </NewTokenRevealProvider>
  );
}
