import { describe, test, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { useForm } from 'react-hook-form';

import { PasswordInput } from './password-input';
import {
  Form,
  FormControl,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from './form';

/**
 * Renders PasswordInput inside a real Form/FormField/FormItem/FormLabel/
 * FormControl tree. That is deliberate: FormControl is a Radix Slot that
 * clones the child and injects id/aria-describedby/aria-invalid, and getting
 * that spread wrong is the whole risk of this component. Rendering it bare
 * would test nothing interesting.
 */
function Harness({
  label = 'Password',
  toggleLabel,
  autoComplete,
  onSubmit,
}: {
  label?: string;
  toggleLabel?: string;
  autoComplete?: string;
  onSubmit?: () => void;
}) {
  const form = useForm<{ password: string }>({
    defaultValues: { password: '' },
  });
  return (
    <Form {...form}>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          onSubmit?.();
        }}
      >
        <FormField
          control={form.control}
          name="password"
          render={({ field }) => (
            <FormItem>
              <FormLabel>{label}</FormLabel>
              <FormControl>
                <PasswordInput
                  toggleLabel={toggleLabel}
                  autoComplete={autoComplete}
                  {...field}
                />
              </FormControl>
              <FormMessage />
            </FormItem>
          )}
        />
      </form>
    </Form>
  );
}

describe('PasswordInput', () => {
  test('defaults to type=password so password managers recognise it', () => {
    render(<Harness />);
    expect(screen.getByLabelText('Password')).toHaveAttribute(
      'type',
      'password',
    );
  });

  test('toggles to text and back', async () => {
    const user = userEvent.setup();
    render(<Harness />);
    const input = screen.getByLabelText('Password');

    await user.click(screen.getByRole('button', { name: 'Show password' }));
    expect(input).toHaveAttribute('type', 'text');

    await user.click(screen.getByRole('button', { name: 'Hide password' }));
    expect(input).toHaveAttribute('type', 'password');
  });

  test('accessible name flips between Show and Hide', async () => {
    const user = userEvent.setup();
    render(<Harness />);
    expect(
      screen.getByRole('button', { name: 'Show password' }),
    ).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Show password' }));
    expect(
      screen.getByRole('button', { name: 'Hide password' }),
    ).toBeInTheDocument();
  });

  test('toggleLabel scopes the accessible name per field', async () => {
    render(<Harness label="Current password" toggleLabel="current password" />);
    expect(
      screen.getByRole('button', { name: 'Show current password' }),
    ).toBeInTheDocument();
  });

  test('the typed value survives a toggle', async () => {
    const user = userEvent.setup();
    render(<Harness />);
    const input = screen.getByLabelText('Password') as HTMLInputElement;

    await user.type(input, 'hunter2');
    await user.click(screen.getByRole('button', { name: 'Show password' }));

    expect(input.value).toBe('hunter2');
  });

  // Mutation test: delete onMouseDown={e => e.preventDefault()} from
  // password-input.tsx and this must fail — without it the browser moves focus
  // to the button on mousedown and the caret is lost.
  test('keeps focus and caret position when toggled with the mouse', async () => {
    const user = userEvent.setup();
    render(<Harness />);
    const input = screen.getByLabelText('Password') as HTMLInputElement;

    await user.type(input, 'hunter2');
    input.setSelectionRange(3, 3);

    await user.click(screen.getByRole('button', { name: 'Show password' }));

    expect(document.activeElement).toBe(input);
    expect(input.selectionStart).toBe(3);
  });

  // Mutation test: remove type="button" and this must fail — an untyped button
  // inside a <form> defaults to type=submit, so every reveal would submit.
  test('toggling does not submit the surrounding form', async () => {
    const user = userEvent.setup();
    const onSubmit = vi.fn();
    render(<Harness onSubmit={onSubmit} />);

    await user.click(screen.getByRole('button', { name: 'Show password' }));

    expect(onSubmit).not.toHaveBeenCalled();
  });

  test('the toggle is keyboard reachable and operable', async () => {
    const user = userEvent.setup();
    render(<Harness />);
    const input = screen.getByLabelText('Password');

    input.focus();
    await user.tab();
    expect(document.activeElement).toBe(
      screen.getByRole('button', { name: 'Show password' }),
    );

    await user.keyboard('{Enter}');
    expect(input).toHaveAttribute('type', 'text');
  });

  // Mutation test: move the {...props} spread onto the wrapper <div> and this
  // must fail — the Slot's injected id would land on the div, breaking
  // label-click focus and every getByLabelText query in the suite.
  test('Slot-injected props land on the input, not the wrapper', () => {
    render(<Harness />);
    const input = screen.getByLabelText('Password');

    expect(input.tagName).toBe('INPUT');
    expect(input).toHaveAttribute('id');
    expect(input).toHaveAttribute('aria-describedby');
  });

  test('passes autoComplete through to the input', () => {
    render(<Harness autoComplete="current-password" />);
    expect(screen.getByLabelText('Password')).toHaveAttribute(
      'autocomplete',
      'current-password',
    );
  });

  test('each instance holds its own visibility state', async () => {
    const user = userEvent.setup();
    render(
      <>
        <Harness label="First password" toggleLabel="first password" />
        <Harness label="Second password" toggleLabel="second password" />
      </>,
    );

    await user.click(
      screen.getByRole('button', { name: 'Show first password' }),
    );

    expect(screen.getByLabelText('First password')).toHaveAttribute(
      'type',
      'text',
    );
    expect(screen.getByLabelText('Second password')).toHaveAttribute(
      'type',
      'password',
    );
  });
});
