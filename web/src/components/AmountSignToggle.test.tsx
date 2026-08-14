import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, it, expect, vi } from 'vitest';
import { AmountSignToggle } from './AmountSignToggle';
import { AmountSignNote } from './AmountSignNote';

describe('AmountSignToggle', () => {
  it('_ExpenseReadsRefund: the word for money coming back', () => {
    render(
      <AmountSignToggle checked={false} onCheckedChange={vi.fn()} type="expense" />,
    );
    expect(screen.getByRole('switch', { name: 'Refund' })).toBeInTheDocument();
  });

  it('_IncomeReadsReversal: the same control must not call an income reversal a refund', () => {
    // v1 promotes the expense case, but the income one is reachable — a
    // negative income is defined, not forbidden — and a control that called it
    // "Refund" would be actively wrong about the row it is creating.
    render(
      <AmountSignToggle checked={false} onCheckedChange={vi.fn()} type="income" />,
    );
    expect(screen.getByRole('switch', { name: 'Reversal' })).toBeInTheDocument();
    expect(screen.queryByText('Refund')).not.toBeInTheDocument();
  });

  it('_ReflectsCheckedState: on and off are distinguishable', () => {
    const { rerender } = render(
      <AmountSignToggle checked={false} onCheckedChange={vi.fn()} type="expense" />,
    );
    expect(screen.getByRole('switch')).not.toBeChecked();

    rerender(
      <AmountSignToggle checked={true} onCheckedChange={vi.fn()} type="expense" />,
    );
    expect(screen.getByRole('switch')).toBeChecked();
  });

  it('_ClickingReportsTheNewState: hands the caller the value, not a toggle event', () => {
    const onCheckedChange = vi.fn();
    render(
      <AmountSignToggle
        checked={false}
        onCheckedChange={onCheckedChange}
        type="expense"
      />,
    );
    // fireEvent-free on purpose: this is a Radix control and the pointer path
    // is what the phone actually runs.
    return userEvent.click(screen.getByRole('switch')).then(() => {
      expect(onCheckedChange).toHaveBeenCalledWith(true);
    });
  });

  it('_LabelFocusesTheSwitch: the visible word is bound to the control', async () => {
    const user = userEvent.setup();
    render(
      <AmountSignToggle checked={false} onCheckedChange={vi.fn()} type="expense" />,
    );
    await user.click(screen.getByText('Refund'));
    expect(screen.getByRole('switch')).toHaveFocus();
  });

  it('_HintIsOptOut: the explanation renders only where a surface asks for it', () => {
    const { rerender } = render(
      <AmountSignToggle checked={false} onCheckedChange={vi.fn()} type="expense" />,
    );
    expect(screen.queryByText(/came back to you/i)).not.toBeInTheDocument();

    rerender(
      <AmountSignToggle
        checked={false}
        onCheckedChange={vi.fn()}
        type="expense"
        showHint
      />,
    );
    expect(screen.getByText(/came back to you/i)).toBeInTheDocument();
    // The hint must not become the control's name — "Refund Money that came
    // back to you." is not something anyone would say out loud, and it is
    // exactly what a two-line <Label> produces without the explicit aria-label.
    expect(screen.getByRole('switch', { name: 'Refund' })).toBeInTheDocument();
  });

  it('_HintDescribesTheSwitch: the explanation reaches a screen reader too', () => {
    // The `aria-label` that keeps the NAME to one word also cuts the hint out
    // of the accessible tree entirely — the label element is no longer the
    // name source, and nothing else pointed at the sentence. So a sighted user
    // got the definition of "Refund" and a screen-reader user got the word on
    // its own, on the one control whose whole job is to say which way the
    // money went. Description, not name: the sentence follows the word instead
    // of replacing it.
    //
    // Both directions in ONE test, on purpose. An "it is absent without
    // showHint" test on its own passes just as happily when the wiring was
    // never added at all.
    const { rerender } = render(
      <AmountSignToggle
        checked={false}
        onCheckedChange={vi.fn()}
        type="expense"
        showHint
      />,
    );
    const withHint = screen.getByRole('switch');
    expect(withHint).toHaveAccessibleName('Refund');
    expect(withHint).toHaveAccessibleDescription('Money that came back to you.');
    // The id is generated, so this pins the WIRING rather than a value: the
    // attribute has to resolve to the element holding the hint.
    const described = document.getElementById(
      withHint.getAttribute('aria-describedby') ?? '',
    );
    expect(described).toBe(screen.getByText(/came back to you/i));

    rerender(
      <AmountSignToggle checked={false} onCheckedChange={vi.fn()} type="expense" />,
    );
    // No hint rendered, so no dangling reference to one: an aria-describedby
    // pointing at a missing id is announced as nothing at all in some screen
    // readers and as the whole document in others.
    expect(screen.getByRole('switch')).not.toHaveAttribute('aria-describedby');
  });

  it('_UndoesAMonoAncestor: the words render in the text face, not tabular figures', () => {
    // Two of the four mounts sit inside a `font-mono` amount block (the inline
    // edit row's amount cell, and the amount column of the entry row). Same
    // reason AmountSignNote carries the classes.
    render(
      <div className="font-mono">
        <AmountSignToggle checked={false} onCheckedChange={vi.fn()} type="expense" />
      </div>,
    );
    const root = screen.getByRole('switch').parentElement as HTMLElement;
    expect(root.className.split(/\s+/)).toContain('font-sans');
    expect(root.className.split(/\s+/)).toContain('font-normal');
  });

  it('_LabelKeepsTheRegisterItCites: the word is a label, not a hint', () => {
    // This is the `Label` + `Switch` + two-line register Settings' notification
    // toggles established, and the main line there carries Label's default
    // `font-medium` with only the hint demoted to `font-normal`. Demoting the
    // whole Label — which this shipped with, and which no other Label in
    // web/src does — flattens the two lines into one weight and leaves the
    // control's own name reading as secondary to the sentence explaining it.
    //
    // The root's `font-normal` is a DIFFERENT thing and must survive: it undoes
    // a `font-mono` ancestor on two of the four mounts (see the test above).
    render(
      <AmountSignToggle
        checked={false}
        onCheckedChange={vi.fn()}
        type="expense"
        showHint
      />,
    );
    const label = screen.getByText('Refund').closest('label') as HTMLElement;
    const tokens = label.className.split(/\s+/);
    expect(tokens).toContain('font-medium');
    expect(tokens).not.toContain('font-normal');
    // `gap-1`, the register's own spacing — `gap-0.5` was a third value for a
    // two-line label stack that already had one.
    expect(tokens).toContain('gap-1');
  });

  it('_SharesItsVocabularyWithTheSavedRow: toggle and note use one word per kind', () => {
    // The control that creates the state and the label that explains it
    // afterwards hold their copy in two files. If either drifts, the household
    // meets two names for one thing — this is the assertion that notices.
    for (const [type, amount] of [
      ['expense', -20],
      ['income', -20],
    ] as const) {
      const note = render(<AmountSignNote amount={amount} type={type} />);
      const noteWord = screen.getByTestId('amount-sign-note').textContent ?? '';
      note.unmount();

      const toggle = render(
        <AmountSignToggle checked onCheckedChange={vi.fn()} type={type} />,
      );
      expect(
        screen.getByRole('switch', { name: noteWord.trim() }),
      ).toBeInTheDocument();
      toggle.unmount();
    }
  });
});
