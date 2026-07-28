import * as React from 'react';
import { Eye, EyeOff } from 'lucide-react';

import { cn } from '@/lib/utils';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';

export interface PasswordInputProps
  extends Omit<React.ComponentProps<'input'>, 'type'> {
  /**
   * Field-scoped noun for the toggle's accessible name, e.g. "current
   * password" produces "Show current password".
   *
   * Settings renders three password fields in one form and two more in the
   * reset dialog; five buttons all named "Show password" would be ambiguous
   * in a screen-reader rotor and would make name-based test queries match
   * more than one element.
   */
  toggleLabel?: string;
}

/**
 * A password field with a show/hide toggle.
 *
 * Two details are load-bearing and easy to get wrong:
 *
 * 1. `{...props}` must land on the inner `<input>`, never on the wrapper
 *    `<div>`. shadcn's `FormControl` is a Radix `Slot` that clones this
 *    component and injects `id`, `aria-describedby` and `aria-invalid`. Spread
 *    those onto the wrapper and `FormLabel`'s `htmlFor` points at a `div`, so
 *    clicking the label stops focusing the field and `getByLabelText` no
 *    longer resolves to the input.
 *
 * 2. `onMouseDown={e => e.preventDefault()}` on the toggle is what preserves
 *    the caret. Without it the browser moves focus to the button on mousedown,
 *    so revealing mid-edit drops the user's cursor position.
 *
 * The field is `type="password"` until the user explicitly reveals it, so
 * password managers still recognise it on first render.
 */
const PasswordInput = React.forwardRef<HTMLInputElement, PasswordInputProps>(
  ({ className, toggleLabel = 'password', id, disabled, ...props }, ref) => {
    const [visible, setVisible] = React.useState(false);
    const Icon = visible ? EyeOff : Eye;

    return (
      <div className="relative">
        <Input
          {...props}
          id={id}
          ref={ref}
          disabled={disabled}
          type={visible ? 'text' : 'password'}
          // pr-10 keeps the value clear of the button; [&::-ms-reveal]:hidden
          // suppresses Edge's built-in reveal so there is only one eye.
          className={cn('pr-10 [&::-ms-reveal]:hidden', className)}
        />
        <Button
          type="button"
          variant="ghost"
          size="icon"
          disabled={disabled}
          aria-label={visible ? `Hide ${toggleLabel}` : `Show ${toggleLabel}`}
          aria-controls={id}
          onMouseDown={(e) => e.preventDefault()}
          onClick={() => setVisible((v) => !v)}
          // h-8 inside the h-10 input leaves clearance; ring-offset-0 stops the
          // focus ring landing on the input's border and reading as a glitch.
          className="absolute right-1 top-1/2 h-8 w-8 -translate-y-1/2 text-muted-foreground focus-visible:ring-offset-0"
        >
          <Icon aria-hidden="true" />
        </Button>
        {/* The toggle deliberately does not take focus on click, so the
            changing button name is never announced to a mouse + SR user. */}
        <span className="sr-only" aria-live="polite">
          {visible ? `${toggleLabel} is visible` : `${toggleLabel} is hidden`}
        </span>
      </div>
    );
  },
);
PasswordInput.displayName = 'PasswordInput';

export { PasswordInput };
