import { describe, test, expect, afterEach } from 'vitest';
import { render, screen, cleanup } from '@testing-library/react';
import { useEffect } from 'react';
import { useForm } from 'react-hook-form';

import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from './form';

afterEach(cleanup);

// `FormField` and `FormItem` memoise their context values (SonarQube S6481).
// A memo is only as good as its dependency list, and the failure mode of a
// wrong one is silent: the provider keeps serving the FIRST value forever
// while the props under it carry on changing. Nothing throws, nothing warns —
// the field just describes itself with a stale name.
//
// These tests are the dependency list, expressed as behaviour. They are the
// mutants to watch: `[]` in place of `[props.name]`, and `[]` in place of
// `[id]`.
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

    // Two FormItems in one form must not collide on that root.
    expect(container.querySelectorAll('[id$="-form-item"]')).toHaveLength(1);
  });
});
