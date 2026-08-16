import { describe, test, expect, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent } from '@testing-library/react';
import { memo, useEffect, useState } from 'react';
import { useForm } from 'react-hook-form';

import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
  useFormField,
} from './form';

afterEach(cleanup);

// `FormField` and `FormItem` memoise their context values (SonarQube S6481).
// A memo is only as good as its dependency list, and the failure mode of a
// wrong one is silent: the provider keeps serving the FIRST value forever
// while the props under it carry on changing. Nothing throws, nothing warns —
// the field just describes itself with a stale name.
//
// These tests are the dependency list, expressed as behaviour. The mutants
// they watch are `[]` in place of `[props.name]` on FormField's memo, and
// `React.useId()` replaced by a constant in FormItem — the second one is what
// makes every field after the first share field #1's id, so `<label for>` and
// `aria-describedby` resolve to the first input for the whole form.
//
// `[]` in place of `[id]` on FormItem's memo is deliberately NOT on that list:
// `useId` is stable for the lifetime of the instance, so the first value is
// also the only value and that mutant is equivalent, not surviving (the
// FormItem comment in form.tsx says the same).
describe('FormField context freshness', () => {
  // Two fields with different errors. `useFormField` looks the error up BY
  // NAME off the context, so a frozen context value keeps rendering the first
  // field's message after the name prop has moved on.
  function Harness({ name }: { name: 'first' | 'second' }) {
    const form = useForm<{ first: string; second: string }>({
      defaultValues: { first: '', second: '' },
    });
    // In an effect, not during render: `setError` triggers a re-render, and
    // calling it in the render body loops.
    useEffect(() => {
      form.setError('first', { type: 'manual', message: 'First is wrong' });
      form.setError('second', { type: 'manual', message: 'Second is wrong' });
    }, [form]);

    return (
      <Form {...form}>
        <FormField
          control={form.control}
          name={name}
          render={({ field }) => (
            <FormItem>
              <FormLabel>Field</FormLabel>
              <FormControl>
                <input {...field} />
              </FormControl>
              <FormDescription>Description</FormDescription>
              <FormMessage />
            </FormItem>
          )}
        />
      </Form>
    );
  }

  test('a changed name prop reaches useFormField instead of being cached', () => {
    const { rerender } = render(<Harness name="first" />);
    // Positive control: the first name is genuinely what rendered, so the
    // assertion below is a change and not merely an absence.
    expect(screen.getByText('First is wrong')).toBeInTheDocument();

    rerender(<Harness name="second" />);
    expect(screen.getByText('Second is wrong')).toBeInTheDocument();
    expect(screen.queryByText('First is wrong')).toBeNull();
  });
});

describe('FormItem id wiring', () => {
  // `FormItem`'s context carries the `useId` value that every id in the group
  // derives from. It is stable for the instance's lifetime, so the memo can
  // never go stale — what CAN break is the wiring itself, which is what the
  // aria plumbing below depends on.
  function Harness() {
    const form = useForm<{ nickname: string }>({
      defaultValues: { nickname: '' },
    });
    useEffect(() => {
      form.setError('nickname', {
        type: 'manual',
        message: 'Nickname is wrong',
      });
    }, [form]);

    return (
      <Form {...form}>
        <FormField
          control={form.control}
          name="nickname"
          render={({ field }) => (
            <FormItem>
              <FormLabel>Nickname</FormLabel>
              <FormControl>
                <input {...field} />
              </FormControl>
              <FormDescription>What the household calls you.</FormDescription>
              <FormMessage />
            </FormItem>
          )}
        />
      </Form>
    );
  }

  test('label, control, description and message all share one id root', () => {
    const { container } = render(<Harness />);

    const control = screen.getByLabelText('Nickname');
    const description = screen.getByText('What the household calls you.');
    const message = screen.getByText('Nickname is wrong');

    // The control's id is `${id}-form-item`; strip the suffix to recover the
    // root every sibling has to agree on.
    const root = control.id.replace(/-form-item$/, '');
    expect(root).not.toBe(control.id); // the suffix was really there
    expect(description.id).toBe(`${root}-form-item-description`);
    expect(message.id).toBe(`${root}-form-item-message`);

    // An invalid field points at BOTH the description and the message.
    expect(control).toHaveAttribute(
      'aria-describedby',
      `${root}-form-item-description ${root}-form-item-message`,
    );
    expect(control).toHaveAttribute('aria-invalid', 'true');

    // Sanity: this harness renders exactly one FormItem, so the ids above are
    // the only ones in play. The COLLISION case is the test below, which needs
    // a second FormItem to exist at all.
    expect(container.querySelectorAll('[id$="-form-item"]')).toHaveLength(1);
  });

  // The same field with nothing wrong with it. Every other harness in this file
  // calls `setError` first, which leaves `FormControl`'s valid-field branch
  // — `!error ? formDescriptionId : `${formDescriptionId} ${formMessageId}`` —
  // unexercised: replacing it with `undefined` drops the description link from
  // every healthy field in the app and nothing notices. A valid field is also
  // the state a form spends most of its life in.
  function ValidHarness() {
    const form = useForm<{ nickname: string }>({
      defaultValues: { nickname: '' },
    });

    return (
      <Form {...form}>
        <FormField
          control={form.control}
          name="nickname"
          render={({ field }) => (
            <FormItem>
              <FormLabel>Nickname</FormLabel>
              <FormControl>
                <input {...field} />
              </FormControl>
              <FormDescription>What the household calls you.</FormDescription>
              <FormMessage />
            </FormItem>
          )}
        />
      </Form>
    );
  }

  test('a valid field points at its description and nothing else', () => {
    render(<ValidHarness />);

    const control = screen.getByLabelText('Nickname');
    const description = screen.getByText('What the household calls you.');

    // Positive control: the description element the attribute names really
    // exists, so the assertion below is a link and not just a string.
    expect(description.id).toBe(`${control.id}-description`);

    expect(control).toHaveAttribute(
      'aria-describedby',
      `${control.id}-description`,
    );
    expect(control).toHaveAttribute('aria-invalid', 'false');
    // And there is no message element for it to have pointed at.
    expect(document.getElementById(`${control.id}-message`)).toBeNull();
  });

  // Two fields in one form, which is what every real form here renders
  // (Settings change-password, Login, Register, Savings, TransactionEntryRow).
  // `useId` is the only thing keeping their id roots apart: replace it with a
  // constant and both fields answer to the same id, so both labels point at
  // the first input and both `aria-describedby` chains resolve to the first
  // field's description and message. Nothing throws — the second field just
  // stops being reachable by its own label.
  function TwoFieldHarness() {
    const form = useForm<{ nickname: string; household: string }>({
      defaultValues: { nickname: '', household: '' },
    });
    useEffect(() => {
      form.setError('nickname', {
        type: 'manual',
        message: 'Nickname is wrong',
      });
      form.setError('household', {
        type: 'manual',
        message: 'Household is wrong',
      });
    }, [form]);

    return (
      <Form {...form}>
        <FormField
          control={form.control}
          name="nickname"
          render={({ field }) => (
            <FormItem>
              <FormLabel>Nickname</FormLabel>
              <FormControl>
                <input {...field} />
              </FormControl>
              <FormDescription>What the household calls you.</FormDescription>
              <FormMessage />
            </FormItem>
          )}
        />
        <FormField
          control={form.control}
          name="household"
          render={({ field }) => (
            <FormItem>
              <FormLabel>Household</FormLabel>
              <FormControl>
                <input {...field} />
              </FormControl>
              <FormDescription>Which ledger you write to.</FormDescription>
              <FormMessage />
            </FormItem>
          )}
        />
      </Form>
    );
  }

  test('two FormItems in one form get distinct id roots', () => {
    const { container } = render(<TwoFieldHarness />);

    const roots = Array.from(
      container.querySelectorAll('[id$="-form-item"]'),
    ).map((el) => el.id);
    // Positive control: both FormItems really rendered, so the distinctness
    // check below is comparing two ids rather than passing over a short list.
    expect(roots).toHaveLength(2);
    expect(new Set(roots).size).toBe(2);

    // Each label therefore resolves to its OWN control; a shared id root makes
    // both of these the first input.
    const nickname = screen.getByLabelText('Nickname');
    const household = screen.getByLabelText('Household');
    expect(nickname).not.toBe(household);

    // Each control describes itself with its own description and message, not
    // with the first field's.
    expect(nickname).toHaveAttribute(
      'aria-describedby',
      `${nickname.id}-description ${nickname.id}-message`,
    );
    expect(household).toHaveAttribute(
      'aria-describedby',
      `${household.id}-description ${household.id}-message`,
    );

    // And those ids point at the right text, so the aria chain above is not
    // merely self-consistent.
    expect(screen.getByText('What the household calls you.').id).toBe(
      `${nickname.id}-description`,
    );
    expect(screen.getByText('Nickname is wrong').id).toBe(
      `${nickname.id}-message`,
    );
    expect(screen.getByText('Which ledger you write to.').id).toBe(
      `${household.id}-description`,
    );
    expect(screen.getByText('Household is wrong').id).toBe(
      `${household.id}-message`,
    );
  });
});

describe('FormField and FormItem context identity', () => {
  // The memos exist to stop these two contexts handing out a NEW value on every
  // parent render. The dependency-list mutants are caught above; deleting the
  // memo outright is not, because it changes no output — only how often the
  // consumers under it re-render. That is measurable directly: a `React.memo`
  // component with no props re-renders for exactly one reason, a context it
  // reads changing identity, so its render count IS the memo.
  //
  // Measured against a memoless build: 5 parent re-renders produce 5 extra
  // probe renders. Every real form in this app renders several fields inside
  // one provider, so that multiplies by the field count on every keystroke.
  function makeProbe() {
    const counter = { renders: 0 };
    const Probe = memo(function Probe() {
      counter.renders += 1;
      // Subscribes to BOTH contexts, so either memo going away moves the count.
      useFormField();
      return null;
    });
    return { counter, Probe };
  }

  function Harness({
    Probe,
    name,
  }: {
    Probe: React.ComponentType;
    name: 'first' | 'second';
  }) {
    const [tick, setTick] = useState(0);
    const form = useForm<{ first: string; second: string }>({
      defaultValues: { first: '', second: '' },
    });

    return (
      <Form {...form}>
        <button type="button" onClick={() => setTick((n) => n + 1)}>
          bump {tick}
        </button>
        <FormField
          control={form.control}
          name={name}
          render={() => (
            <FormItem>
              <Probe />
            </FormItem>
          )}
        />
      </Form>
    );
  }

  test('a parent re-render does not re-render the fields under it', () => {
    const { counter, Probe } = makeProbe();
    render(<Harness Probe={Probe} name="first" />);

    // Positive control: the probe is really mounted and counting, so the
    // comparison below is not 0 against 0.
    const afterMount = counter.renders;
    expect(afterMount).toBeGreaterThan(0);

    for (let i = 0; i < 5; i += 1) {
      fireEvent.click(screen.getByText(/^bump/));
    }
    // Positive control: the parent really re-rendered five times.
    expect(screen.getByText('bump 5')).toBeInTheDocument();

    expect(counter.renders).toBe(afterMount);
  });

  test('a changed dependency still reaches the field', () => {
    // The other half, and what stops the test above being satisfied by a probe
    // that never re-renders at all: when `name` really changes, the memo has to
    // hand out a new value and the consumer has to see it.
    const { counter, Probe } = makeProbe();
    const { rerender } = render(<Harness Probe={Probe} name="first" />);
    const afterMount = counter.renders;

    rerender(<Harness Probe={Probe} name="second" />);
    expect(counter.renders).toBeGreaterThan(afterMount);
  });
});
