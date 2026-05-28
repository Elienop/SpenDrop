import { useState, useEffect, useCallback, useMemo } from 'react';
import { zodResolver } from '@hookform/resolvers/zod';
import { useForm, useWatch } from 'react-hook-form';
import * as z from 'zod';
import { AlertCircle, PiggyBank, Trash2 } from 'lucide-react';
import { toast } from 'sonner';
import { api } from '../api/client';
import type { SavingsGoal } from '../api/types';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { selectAllOnFocus } from '@/lib/utils';
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card';
import {
  Dialog,
  DialogClose,
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
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { MIN_YEAR, MAX_YEAR } from '@/lib/dates';
import { formatCurrency } from '@/lib/format';
import { useBaseCurrency } from '@/hooks/useBaseCurrency';
import { destructiveActionClass } from '@/lib/styles';

const goalSchema = z.object({
  year: z.number().int().min(MIN_YEAR).max(MAX_YEAR),
  // The backend treats `target_amount: 0` as a delete (there is no
  // separate DELETE route). A user pressing Enter on the default form
  // would silently destroy an existing goal — so reject empty/zero at
  // the schema level. Real deletion goes through the row's Delete
  // button + AlertDialog instead.
  target_amount: z
    .number({ error: 'Enter a target greater than 0' })
    .positive('Target must be greater than 0'),
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
      // `undefined` (not `0`) so the field renders blank and a no-op
      // submit triggers the schema validator, not a silent
      // PUT target_amount=0 (which would delete an existing goal).
      target_amount: undefined as unknown as number,
    },
  });

  const [confirmDelete, setConfirmDelete] = useState<SavingsGoal | null>(null);

  // The form's `year` field. Watched so the dialog can surface a
  // replace-existing warning when the picked year already has a goal.
  // PUT /savings-goals/{year} is upsert — without this clue the user
  // would silently overwrite a prior target. `useWatch` (vs
  // `form.watch`) is React-Compiler-safe and doesn't trigger the
  // `react-hooks/incompatible-library` advisory.
  const watchedYear = useWatch({ control: form.control, name: 'year' });
  const existingGoal = useMemo(
    () =>
      // Require the watched year to be in-range before scanning goals.
      // The year input's onChange writes `0` for an empty field (so the
      // form's number type stays consistent), which would otherwise
      // make this memo run `goals.find(g => g.year === 0)` on every
      // keystroke that produces a sub-MIN_YEAR transient (typing
      // "2026" passes through 2 → 20 → 202 → 2026). The schema
      // rejects sub-MIN_YEAR on submit; matching it here keeps the
      // warning consistent.
      typeof watchedYear === 'number' &&
      Number.isFinite(watchedYear) &&
      watchedYear >= MIN_YEAR
        ? goals.find((g) => g.year === watchedYear)
        : undefined,
    [goals, watchedYear],
  );

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
    // Capture the clash flag at submit time — the warning was visible
    // when the user committed, so the toast/UX should reflect the
    // action they actually took (replace vs add), not whatever the
    // post-refetch state turns out to be.
    const isReplace = goals.some((g) => g.year === values.year);
    try {
      await api.put(`savings-goals/${values.year}`, {
        target_amount: values.target_amount,
      });
      form.reset();
      setAddOpen(false);
      toast.success(
        isReplace ? 'Savings goal replaced' : 'Savings goal added',
      );
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
        <div className="flex flex-col gap-1.5">
          <CardTitle className="text-base">Savings Goals</CardTitle>
          <CardDescription>
            Set a yearly savings target. Reports → Savings tracks progress
            against it.
          </CardDescription>
        </div>
        <Dialog open={addOpen} onOpenChange={(open) => {
          setAddOpen(open);
          if (!open) form.reset();
        }}>
          <DialogTrigger asChild>
            <Button size="sm">Add Goal</Button>
          </DialogTrigger>
          <DialogContent>
            <DialogHeader>
              <DialogTitle>
                {existingGoal ? 'Replace Savings Goal' : 'Add Savings Goal'}
              </DialogTitle>
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
                {existingGoal && (
                  <Alert>
                    <AlertCircle className="h-4 w-4" aria-hidden="true" />
                    <AlertTitle>
                      You already have a goal for {existingGoal.year}
                    </AlertTitle>
                    <AlertDescription>
                      Saving will replace your current{' '}
                      {formatCurrency(
                        existingGoal.target_amount,
                        baseCurrency,
                      )}{' '}
                      target for {existingGoal.year}.
                    </AlertDescription>
                  </Alert>
                )}
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
                                ? undefined
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
                  {/* DialogClose calls onOpenChange(false), which the
                      parent already wires to form.reset() — so Cancel
                      cleanly resets the form. Important when the
                      submit flips to destructive "Replace Goal", at
                      which point the only visible mouse action would
                      otherwise be destructive. */}
                  <DialogClose asChild>
                    <Button type="button" variant="ghost">
                      Cancel
                    </Button>
                  </DialogClose>
                  <Button type="submit">
                    {existingGoal ? 'Replace Goal' : 'Add Goal'}
                  </Button>
                </DialogFooter>
              </form>
            </Form>
          </DialogContent>
        </Dialog>
      </CardHeader>
      <CardContent>
        {goals.length === 0 ? (
          <div className="flex flex-col items-center gap-3 py-8 text-center">
            <PiggyBank
              className="h-8 w-8 text-muted-foreground/60"
              aria-hidden="true"
            />
            <div className="grid gap-1">
              <p className="text-sm font-medium">No savings goals yet</p>
              <p className="text-sm text-muted-foreground">
                Add a goal to track your savings progress in Reports.
              </p>
            </div>
          </div>
        ) : (
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
                  {/* Match Budgets' Annual total cell: tabular-nums alone
                      gives column alignment without the mono typeface. */}
                  <TableCell className="tabular-nums">
                    {formatCurrency(g.target_amount, baseCurrency)}
                  </TableCell>
                  <TableCell className="text-right">
                    {/*
                      Icon-only delete to match the row-action pattern
                      used elsewhere (Categories uses a MoreHorizontal
                      dropdown, but Savings rows have only one action,
                      so the dropdown wrapper would be overkill). aria-label
                      keeps the assertion-by-name selector intact for tests.
                    */}
                    <Button
                      type="button"
                      variant="ghost"
                      size="icon"
                      onClick={() => setConfirmDelete(g)}
                      aria-label={`Delete ${g.year} goal`}
                    >
                      <Trash2
                        className="h-4 w-4 text-destructive"
                        aria-hidden="true"
                      />
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </CardContent>
      <AlertDialog
        open={confirmDelete !== null}
        onOpenChange={(open) => {
          if (!open) setConfirmDelete(null);
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete savings goal?</AlertDialogTitle>
            <AlertDialogDescription>
              Remove the{' '}
              {confirmDelete
                ? formatCurrency(confirmDelete.target_amount, baseCurrency)
                : ''}{' '}
              target for {confirmDelete?.year}. You can add a new goal for{' '}
              {confirmDelete?.year} later.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              className={destructiveActionClass}
              onClick={() => {
                const goal = confirmDelete;
                setConfirmDelete(null);
                if (goal) void handleDelete(goal);
              }}
            >
              Delete
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </Card>
  );
}

export function Savings() {
  return (
    <div className="flex flex-col gap-6">
      <h1 className="text-2xl font-semibold tracking-tight">Savings</h1>
      <SavingsSection />
    </div>
  );
}
