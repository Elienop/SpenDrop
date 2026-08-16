import { describe, test, expect, afterEach } from 'vitest';
import { render, screen, cleanup } from '@testing-library/react';

import { Alert, AlertTitle, AlertDescription } from './alert';

afterEach(cleanup);

describe('AlertTitle renders its children as the heading', () => {
  // `AlertTitle` destructures `children` and places it explicitly instead of
  // letting it ride in on `{...props}`. The rendered DOM is identical either
  // way — React puts a spread `children` in exactly this position — so the
  // change was made for static analysis (SonarQube S6850: a heading with no
  // content has no accessible name), and "the DOM is identical" was only ever
  // proven in throwaway probes. This file is the in-repo version of that
  // proof: whatever the mechanism, the heading has to keep having a name and
  // the spread has to keep reaching the element.
  function tree() {
    return render(
      <Alert>
        <AlertTitle data-testid="title" id="pinned-title">
          Budget exceeded
        </AlertTitle>
        <AlertDescription>You are over by $12.50.</AlertDescription>
      </Alert>,
    );
  }

  test('the children land inside the h5 itself', () => {
    const { container } = tree();

    const heading = container.querySelector('h5');
    expect(heading).not.toBeNull();
    // Inside the h5, not merely somewhere in the alert: a title rendered as a
    // sibling would still satisfy a `getByText` on the container.
    expect(heading).toHaveTextContent('Budget exceeded');
  });

  test('the heading has an accessible name at level 5', () => {
    tree();

    // The whole point of the change. An `<h5>` with no content resolves to no
    // accessible name, so this query is what fails if `children` ever stops
    // being rendered.
    expect(
      screen.getByRole('heading', { level: 5, name: 'Budget exceeded' }),
    ).toBeInTheDocument();
  });

  test('spread props still reach the heading element', () => {
    const { container } = tree();

    // Destructuring `children` out of `props` must not have disturbed the rest
    // of the spread — every call site relies on it for ids and test hooks.
    const heading = container.querySelector('h5');
    expect(heading).toHaveAttribute('id', 'pinned-title');
    expect(heading).toHaveAttribute('data-testid', 'title');
    // And the component's own className is still merged, not replaced.
    expect(heading).toHaveClass('font-medium', 'leading-none');
  });

  test('the alert wrapper and description are unchanged around it', () => {
    // Positive control for the fixture: the heading assertions above are being
    // made inside a real alert, not a bare h5.
    const { container } = tree();

    expect(screen.getByRole('alert')).toBe(container.firstElementChild);
    expect(screen.getByText('You are over by $12.50.')).toBeInTheDocument();
  });
});
