import { describe, test, expect, beforeAll } from 'vitest';
import { render, screen, act } from '@testing-library/react';
import type { ComponentProps } from 'react';
import { toast } from 'sonner';
import { Toaster } from './sonner';
import { ThemeProviderContext } from '@/hooks/useTheme';

// What this file can and cannot prove:
//
// happy-dom applies no stylesheets, so NOTHING here can tell you what the
// button looks like on screen — that is a browser measurement, and a class-based
// version of these styles passed every unit test while rendering at sonner's own
// 24px in the real thing. What it CAN prove is that the styles reach the element
// as inline style properties, which is the whole reason they are inline: an
// inline style needs no cascade to win, so "it is on the element" and "it is in
// effect" stop being different questions.

beforeAll(() => {
  // sonner reads prefers-color-scheme on mount; happy-dom ships no matchMedia.
  Object.defineProperty(window, 'matchMedia', {
    configurable: true,
    value: (query: string) => ({
      matches: false,
      media: query,
      onchange: null,
      addEventListener: () => {},
      removeEventListener: () => {},
      addListener: () => {},
      removeListener: () => {},
      dispatchEvent: () => false,
    }),
  });
});

function renderToaster(props: ComponentProps<typeof Toaster> = {}) {
  return render(
    <ThemeProviderContext.Provider
      value={{
        theme: 'dark',
        setTheme: () => {},
        colorTheme: null,
        setColorTheme: () => {},
      }}
    >
      <Toaster {...props} />
    </ThemeProviderContext.Provider>,
  );
}

async function actionButton(
  props: ComponentProps<typeof Toaster> = {},
): Promise<HTMLElement> {
  renderToaster(props);
  act(() => {
    toast.error('Could not save', {
      action: { label: 'Retry', onClick: () => {} },
    });
  });
  return screen.findByRole('button', { name: 'Retry' });
}

describe('Toaster action button', () => {
  test('is sized inline for a touch target, not left at sonner’s 24px', async () => {
    const button = await actionButton();
    // 2.5rem = 40px, the minimum comfortable touch target. The toast is also a
    // swipe-to-dismiss surface, so a missed tap on a failed save dismisses the
    // only path that cannot duplicate the entry.
    expect(button.style.height).toBe('2.5rem');
    // Per-side rather than the shorthand: the DOM normalises `0 1rem` to
    // `0px 1rem`, which is a serialisation detail, not the thing being pinned.
    expect(button.style.paddingLeft).toBe('1rem');
    expect(button.style.paddingRight).toBe('1rem');
    // 12px is sonner's default and is below the legible floor.
    expect(button.style.fontSize).toBe('0.875rem');
  });

  test('wears the theme’s primary colour rather than sonner’s default', async () => {
    const button = await actionButton();
    // Tokens are HSL components (`--primary: 240 5.9% 10%`), so a bare var()
    // would produce no colour at all.
    expect(button.style.background).toBe('hsl(var(--primary))');
    expect(button.style.color).toBe('hsl(var(--primary-foreground))');
  });

  test('carries the styles inline, where no stylesheet can outrank them', async () => {
    const button = await actionButton();
    // The point of the mechanism, stated as an assertion: these live in the
    // style attribute, not in a class whose rule has to win a cascade fight
    // with sonner's runtime-injected `[data-button]`.
    expect(button.getAttribute('style')).toMatch(/height:\s*2\.5rem/);
  });

  test('survives a caller that passes its own toastOptions', async () => {
    // The trap this closes: toastOptions used to be overridable wholesale, so
    // a caller setting one unrelated option silently reverted every action
    // button in the app to sonner's 24px.
    const button = await actionButton({ toastOptions: { duration: 1234 } });
    expect(button.style.height).toBe('2.5rem');
    expect(button.style.background).toBe('hsl(var(--primary))');
  });

  test('lets a caller override one style property without losing the rest', async () => {
    const button = await actionButton({
      toastOptions: { actionButtonStyle: { fontWeight: 700 } },
    });
    expect(button.style.fontWeight).toBe('700');
    // Everything the caller did not mention keeps the shared floor.
    expect(button.style.height).toBe('2.5rem');
  });

  test('keeps the rest of classNames when a caller sets one of them', async () => {
    const button = await actionButton({
      toastOptions: { classNames: { description: 'custom-description' } },
    });
    const li = button.closest('li');
    // `toast` is untouched by the caller, so the theme classes stay.
    expect(li?.getAttribute('class')).toMatch(/group-\[\.toaster\]:bg-background/);
  });
});
