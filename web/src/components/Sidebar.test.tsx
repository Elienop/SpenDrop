import { describe, test, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';

vi.mock('../hooks/useAuth', () => ({
  useAuth: vi.fn(),
}));

vi.mock('../hooks/useTrashCount', () => ({
  useTrashCount: vi.fn(),
}));

import { useAuth } from '../hooks/useAuth';
import { useTrashCount } from '../hooks/useTrashCount';
import { ThemeProvider } from './theme-provider';
import { Sidebar } from './Sidebar';

const mockedUseAuth = vi.mocked(useAuth);
const mockedUseTrashCount = vi.mocked(useTrashCount);

const mockUser = {
  id: 1,
  username: 'alice',
  display_name: 'Alice',
  role: 'admin' as const,
  created_at: '2024-01-01',
};

function renderSidebar(currentPath = '/') {
  return render(
    <ThemeProvider>
      <MemoryRouter initialEntries={[currentPath]}>
        <Sidebar />
      </MemoryRouter>
    </ThemeProvider>,
  );
}

describe('Sidebar', () => {
  const mockLogout = vi.fn();

  beforeEach(() => {
    localStorage.clear();
    vi.clearAllMocks();
    mockedUseAuth.mockReturnValue({
      user: mockUser,
      loading: false,
      login: vi.fn(),
      register: vi.fn(),
      logout: mockLogout,
    });
    mockedUseTrashCount.mockReturnValue({
      count: 0,
      loading: false,
      refetch: vi.fn(),
    });
  });

  test('renders all navigation links', () => {
    renderSidebar();
    expect(screen.getByRole('link', { name: /dashboard/i })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: /reports/i })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: /transactions/i })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: /categories/i })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: /settings/i })).toBeInTheDocument();
  });

  test('marks the current route as active via aria-current', () => {
    renderSidebar('/transactions');
    const link = screen.getByRole('link', { name: /transactions/i });
    expect(link).toHaveAttribute('aria-current', 'page');
  });

  test('active route gets the bg-muted styling (not just aria-current)', () => {
    // aria-current is set by NavLink internally — it can be present
    // without our manual `isActive` className branch firing (e.g. if
    // a future minifier mangles the className function). Assert on
    // the actual class so we catch a regression in the styling
    // contract, not just the ARIA contract.
    renderSidebar('/transactions');
    const link = screen.getByRole('link', { name: /transactions/i });
    expect(link.className).toContain('bg-muted');
  });

  test('displays user display name when expanded', async () => {
    const user = userEvent.setup();
    renderSidebar();
    await user.click(screen.getByLabelText('Toggle sidebar'));
    expect(screen.getByText('Alice')).toBeInTheDocument();
  });

  test('displays user avatar initial', () => {
    renderSidebar();
    expect(screen.getByText('A')).toBeInTheDocument();
  });

  test('calls logout when logout button is clicked', async () => {
    const user = userEvent.setup();
    renderSidebar();
    await user.click(screen.getByRole('button', { name: /log\s*out/i }));
    expect(mockLogout).toHaveBeenCalledTimes(1);
  });

  test('has semantic nav element', () => {
    renderSidebar();
    expect(screen.getByRole('navigation')).toBeInTheDocument();
  });

  test('renders collapsed by default', () => {
    renderSidebar();
    expect(screen.getByLabelText('Toggle sidebar')).toHaveAttribute(
      'aria-expanded',
      'false',
    );
  });

  test('expands when toggle button is clicked', async () => {
    const user = userEvent.setup();
    renderSidebar();
    await user.click(screen.getByLabelText('Toggle sidebar'));
    expect(screen.getByLabelText('Toggle sidebar')).toHaveAttribute(
      'aria-expanded',
      'true',
    );
  });

  test('persists expanded state to localStorage', async () => {
    const user = userEvent.setup();
    renderSidebar();
    await user.click(screen.getByLabelText('Toggle sidebar'));
    expect(localStorage.getItem('spendrop-sidebar')).toBe('true');
  });

  test('reads initial state from localStorage', () => {
    localStorage.setItem('spendrop-sidebar', 'true');
    renderSidebar();
    expect(screen.getByLabelText('Toggle sidebar')).toHaveAttribute(
      'aria-expanded',
      'true',
    );
  });

  test('dispatches sidebar-toggle event on toggle', async () => {
    const listener = vi.fn();
    window.addEventListener('sidebar-toggle', listener);
    try {
      const user = userEvent.setup();
      renderSidebar();
      await user.click(screen.getByLabelText('Toggle sidebar'));
      expect(listener).toHaveBeenCalled();
    } finally {
      window.removeEventListener('sidebar-toggle', listener);
    }
  });

  describe('Trash count badge', () => {
    test('hides the badge entirely when trash count is 0 (expanded)', async () => {
      mockedUseTrashCount.mockReturnValue({
        count: 0,
        loading: false,
        refetch: vi.fn(),
      });
      const user = userEvent.setup();
      renderSidebar();
      await user.click(screen.getByLabelText('Toggle sidebar'));

      const trashLink = screen.getByRole('link', { name: /trash/i });
      // No number rendered next to the label.
      expect(trashLink).not.toHaveTextContent(/\d+/);
    });

    test('renders the count next to the Trash label when admin and count > 0', async () => {
      mockedUseTrashCount.mockReturnValue({
        count: 7,
        loading: false,
        refetch: vi.fn(),
      });
      const user = userEvent.setup();
      renderSidebar();
      await user.click(screen.getByLabelText('Toggle sidebar'));

      const trashLink = screen.getByRole('link', { name: /trash/i });
      // accessible name includes the count via the visible badge text.
      expect(trashLink).toHaveTextContent('7');
    });

    test('passes enabled=true to useTrashCount for admin users', () => {
      renderSidebar();
      expect(mockedUseTrashCount).toHaveBeenCalledWith(true);
    });

    test('passes enabled=false to useTrashCount for non-admin users', () => {
      mockedUseAuth.mockReturnValue({
        user: { ...mockUser, role: 'member' },
        loading: false,
        login: vi.fn(),
        register: vi.fn(),
        logout: mockLogout,
      });
      renderSidebar();
      expect(mockedUseTrashCount).toHaveBeenCalledWith(false);
    });

    test('does not render the Trash link for non-admin users (regression check)', () => {
      mockedUseAuth.mockReturnValue({
        user: { ...mockUser, role: 'member' },
        loading: false,
        login: vi.fn(),
        register: vi.fn(),
        logout: mockLogout,
      });
      // Even if the hook returned a positive count for some reason,
      // a non-admin must not see the link.
      mockedUseTrashCount.mockReturnValue({
        count: 99,
        loading: false,
        refetch: vi.fn(),
      });
      renderSidebar();
      expect(
        screen.queryByRole('link', { name: /trash/i }),
      ).not.toBeInTheDocument();
    });

    test('hides the badge text in the collapsed sidebar even when count > 0', () => {
      // Sidebar starts collapsed (default localStorage). In this state
      // the link is icon-only and there is no room for a number next
      // to the label, so the badge is suppressed.
      mockedUseTrashCount.mockReturnValue({
        count: 5,
        loading: false,
        refetch: vi.fn(),
      });
      renderSidebar();
      const trashLink = screen.getByRole('link', { name: /trash/i });
      expect(trashLink).not.toHaveTextContent(/5/);
    });

    test('uses an aria-label that names the count for screen readers (plural)', async () => {
      mockedUseTrashCount.mockReturnValue({
        count: 7,
        loading: false,
        refetch: vi.fn(),
      });
      const user = userEvent.setup();
      renderSidebar();
      await user.click(screen.getByLabelText('Toggle sidebar'));

      expect(
        screen.getByRole('link', { name: 'Trash, 7 items' }),
      ).toBeInTheDocument();
    });

    test('uses a singular aria-label when the count is exactly 1', async () => {
      mockedUseTrashCount.mockReturnValue({
        count: 1,
        loading: false,
        refetch: vi.fn(),
      });
      const user = userEvent.setup();
      renderSidebar();
      await user.click(screen.getByLabelText('Toggle sidebar'));

      expect(
        screen.getByRole('link', { name: 'Trash, 1 item' }),
      ).toBeInTheDocument();
    });

    test('caps the visible badge text at "99+" while the aria-label keeps the real count', async () => {
      mockedUseTrashCount.mockReturnValue({
        count: 150,
        loading: false,
        refetch: vi.fn(),
      });
      const user = userEvent.setup();
      renderSidebar();
      await user.click(screen.getByLabelText('Toggle sidebar'));

      // aria-label uses the un-capped count for screen-reader fidelity.
      const link = screen.getByRole('link', { name: 'Trash, 150 items' });
      // Visible text is capped — the raw "150" is NOT in the rendered
      // text, but "99+" is.
      expect(link).toHaveTextContent('99+');
      expect(link.textContent).not.toContain('150');
    });

    test('shows the literal "99" at exactly 99 (cap kicks in at 100)', async () => {
      mockedUseTrashCount.mockReturnValue({
        count: 99,
        loading: false,
        refetch: vi.fn(),
      });
      const user = userEvent.setup();
      renderSidebar();
      await user.click(screen.getByLabelText('Toggle sidebar'));
      expect(
        screen.getByRole('link', { name: 'Trash, 99 items' }),
      ).toHaveTextContent('99');
    });

    test('shows "99+" at exactly 100', async () => {
      mockedUseTrashCount.mockReturnValue({
        count: 100,
        loading: false,
        refetch: vi.fn(),
      });
      const user = userEvent.setup();
      renderSidebar();
      await user.click(screen.getByLabelText('Toggle sidebar'));
      expect(
        screen.getByRole('link', { name: 'Trash, 100 items' }),
      ).toHaveTextContent('99+');
    });

    test('collapsed sidebar shows a dot indicator on Trash when count > 0', () => {
      // Collapsed by default (localStorage cleared in beforeEach).
      mockedUseTrashCount.mockReturnValue({
        count: 3,
        loading: false,
        refetch: vi.fn(),
      });
      renderSidebar();
      const trashLink = screen.getByRole('link', { name: /trash/i });
      // The dot is a positioned span with data-testid for direct
      // assertion (the number is suppressed in collapsed mode).
      expect(
        trashLink.querySelector('[data-testid="trash-dot"]'),
      ).toBeInTheDocument();
    });

    test('collapsed sidebar does NOT show a dot when count is 0', () => {
      mockedUseTrashCount.mockReturnValue({
        count: 0,
        loading: false,
        refetch: vi.fn(),
      });
      renderSidebar();
      const trashLink = screen.getByRole('link', { name: /trash/i });
      expect(
        trashLink.querySelector('[data-testid="trash-dot"]'),
      ).not.toBeInTheDocument();
    });

    test('aria-label is set in collapsed mode too (so SR users still hear the count)', () => {
      mockedUseTrashCount.mockReturnValue({
        count: 4,
        loading: false,
        refetch: vi.fn(),
      });
      renderSidebar();
      // Even collapsed, the link's accessible name includes the count.
      expect(
        screen.getByRole('link', { name: 'Trash, 4 items' }),
      ).toBeInTheDocument();
    });
  });

  describe('layout', () => {
    test('renders a Separator between the nav sections', () => {
      // Separator divides the top section (nav + admin) from the
      // bottom section (Settings + Logout). Shadcn's Separator
      // renders role="none" (decorative) by default — query by the
      // primitive's data-orientation attribute instead.
      renderSidebar();
      const separator = document.querySelector('[data-orientation="horizontal"]');
      expect(separator).not.toBeNull();
    });

    test('Reports sits between Transactions and Budgets in the nav order', async () => {
      const user = userEvent.setup();
      renderSidebar();
      await user.click(screen.getByLabelText('Toggle sidebar'));

      const nav = screen.getByRole('navigation');
      const links = Array.from(nav.querySelectorAll('a[href]')).map(
        (a) => (a as HTMLAnchorElement).getAttribute('href'),
      );
      const tx = links.indexOf('/transactions');
      const rep = links.indexOf('/reports');
      const bud = links.indexOf('/budgets');
      expect(tx).toBeGreaterThanOrEqual(0);
      expect(rep).toBeGreaterThan(tx);
      expect(bud).toBeGreaterThan(rep);
    });

    test('Settings sits in the bottom section AFTER the separator', async () => {
      const user = userEvent.setup();
      renderSidebar();
      await user.click(screen.getByLabelText('Toggle sidebar'));

      const nav = screen.getByRole('navigation');
      const separator = nav.querySelector('[data-orientation="horizontal"]');
      const settings = screen.getByRole('link', { name: /settings/i });
      // compareDocumentPosition: bit 4 = "follows" (separator precedes settings).
      const rel = separator!.compareDocumentPosition(settings);
      expect(rel & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    });

    test('no Menu / Admin / General section titles are rendered', async () => {
      const user = userEvent.setup();
      renderSidebar();
      await user.click(screen.getByLabelText('Toggle sidebar'));

      // The old grouping titles are gone — replaced by a single
      // separator. Members never saw them collapsed either.
      expect(screen.queryByText(/^Menu$/)).not.toBeInTheDocument();
      expect(screen.queryByText(/^Admin$/)).not.toBeInTheDocument();
      expect(screen.queryByText(/^General$/)).not.toBeInTheDocument();
    });
  });
});
