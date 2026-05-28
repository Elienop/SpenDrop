import { useState, useEffect, useCallback } from 'react';
import { zodResolver } from '@hookform/resolvers/zod';
import { useForm } from 'react-hook-form';
import * as z from 'zod';
import { PiggyBank } from 'lucide-react';
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
import { MIN_YEAR, MAX_YEAR } from '@/lib/dates';
import { formatCurrency } from '@/lib/format';
import { useBaseCurrency } from '@/hooks/useBaseCurrency';

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
        {goals.length === 0 ? (
          <div className="flex flex-col items-center gap-3 py-8 text-center">
            <PiggyBank
              className="h-8 w-8 text-muted-foreground/60"
              aria-hidden="true"
            />
            <div className="grid gap-1">
              <p className="text-sm font-medium">No savings goals yet</p>
              <p className="text-sm text-muted-foreground">
                Add one to set a yearly target.
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
        )}
      </CardContent>
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
