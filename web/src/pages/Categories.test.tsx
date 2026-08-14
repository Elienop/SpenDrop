import { describe, test, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';

vi.mock('../hooks/useAuth', () => ({
  useAuth: vi.fn(),
}));

vi.mock('../api/client', () => ({
  api: {
    get: vi.fn(),
    post: vi.fn(),
    put: vi.fn(),
    patch: vi.fn(),
    del: vi.fn(),
  },
}));

vi.mock('sonner', () => ({
  toast: {
    success: vi.fn(),
    error: vi.fn(),
    info: vi.fn(),
  },
}));

import { useAuth } from '../hooks/useAuth';
import { api } from '../api/client';
import { toast } from 'sonner';
import { Categories } from './Categories';

const mockedUseAuth = vi.mocked(useAuth);
const mockedApi = vi.mocked(api);
const mockedToast = vi.mocked(toast);

const mockCategories = [
  {
    id: 1,
    name: 'Food',
    type: 'expense' as const,
    icon: null,
    sort_order: 0,
    is_active: true,
    created_at: '2024-01-01',
  },
  {
    id: 2,
    name: 'Salary',
    type: 'income' as const,
    icon: null,
    sort_order: 0,
    is_active: true,
    created_at: '2024-01-01',
  },
  {
    id: 3,
    name: 'Transport',
    type: 'expense' as const,
    icon: null,
    sort_order: 1,
    is_active: false,
    created_at: '2024-01-01',
  },
];

function renderCategories() {
  return render(
    <MemoryRouter>
      <Categories />
    </MemoryRouter>,
  );
}

function asAdmin() {
  mockedUseAuth.mockReturnValue({
    user: {
      id: 1,
      username: 'alice',
      display_name: 'Alice',
      role: 'admin',
      created_at: '2024-01-01',
    },
    loading: false,
    unverified: false,
    refreshUser: vi.fn(),
    login: vi.fn(),
    register: vi.fn(),
    logout: vi.fn(),
  });
}

function asMember() {
  mockedUseAuth.mockReturnValue({
    user: {
      id: 2,
      username: 'bob',
      display_name: 'Bob',
      role: 'member',
      created_at: '2024-01-01',
    },
    loading: false,
    unverified: false,
    refreshUser: vi.fn(),
    login: vi.fn(),
    register: vi.fn(),
    logout: vi.fn(),
  });
}

describe('Categories', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockedApi.get.mockResolvedValue(mockCategories);
  });

  describe('as admin', () => {
    beforeEach(asAdmin);

    test('renders the Categories heading', async () => {
      renderCategories();
      expect(
        screen.getByRole('heading', { level: 1, name: /categories/i }),
      ).toBeInTheDocument();
    });

    test('renders category rows from API', async () => {
      renderCategories();
      await waitFor(() => {
        expect(screen.getByText('Food')).toBeInTheDocument();
        expect(screen.getByText('Salary')).toBeInTheDocument();
        expect(screen.getByText('Transport')).toBeInTheDocument();
      });
      // 1 header row + 3 data rows
      expect(screen.getAllByRole('row')).toHaveLength(4);
    });

    test('renders a type badge for each category', async () => {
      renderCategories();
      await waitFor(() => {
        expect(screen.getByText('Food')).toBeInTheDocument();
      });
      const table = screen.getByRole('table');
      // Assert the exact badge labels, not the descriptive copy above the table
      expect(within(table).getAllByText('Expense').length).toBeGreaterThan(0);
      expect(within(table).getAllByText('Income').length).toBeGreaterThan(0);
    });

    test('renders Add category button for admin', async () => {
      renderCategories();
      await waitFor(() => {
        expect(
          screen.getByRole('button', { name: /add category/i }),
        ).toBeInTheDocument();
      });
    });

    test('clicking Add category opens a Sheet with a name input and type select', async () => {
      const user = userEvent.setup();
      renderCategories();
      await waitFor(() => {
        expect(screen.getByText('Food')).toBeInTheDocument();
      });

      await user.click(screen.getByRole('button', { name: /add category/i }));

      // Sheet dialog with form fields
      expect(screen.getByRole('dialog')).toBeInTheDocument();
      expect(screen.getByLabelText(/name/i)).toBeInTheDocument();
      expect(screen.getByLabelText(/type/i)).toBeInTheDocument();
      // Icon input is optional
      expect(screen.getByLabelText(/icon/i)).toBeInTheDocument();
      // No color input anywhere in the form
      expect(screen.queryByLabelText(/color/i)).not.toBeInTheDocument();
    });

    // B28. The title is what says whether this sheet creates or edits, and
    // the description carries the "type can't be changed after creation"
    // rule — both used to sit inside the body scroller and leave with it.
    // happy-dom lays nothing out, so what is pinned is the structure that
    // produces the fix: which side of the scroll container they are on.
    test('the editor header stays OUT of the body scroller', async () => {
      const user = userEvent.setup();
      renderCategories();
      await waitFor(() => {
        expect(screen.getByText('Food')).toBeInTheDocument();
      });

      await user.click(screen.getByRole('button', { name: /add category/i }));
      const dialog = screen.getByRole('dialog');
      const scroller = dialog.querySelector('.overflow-y-auto');
      expect(scroller).not.toBeNull();

      // Through the heading role, not the text: "Add category" is also the
      // page button that opened this sheet.
      expect(
        scroller!.contains(
          within(dialog).getByRole('heading', { name: 'Add category' }),
        ),
      ).toBe(false);
      expect(
        scroller!.contains(
          within(dialog).getByText(
            'Create a new expense or income category.',
          ),
        ),
      ).toBe(false);

      // The positive control: "is outside the scroller" is equally true of a
      // header that never rendered, and of a sheet with no scroller at all.
      expect(scroller!.contains(screen.getByLabelText(/name/i))).toBe(true);
    });

    test('submitting the Add Sheet posts to categories without a color field', async () => {
      const user = userEvent.setup();
      mockedApi.post.mockResolvedValue({});
      renderCategories();
      await waitFor(() => {
        expect(screen.getByText('Food')).toBeInTheDocument();
      });

      await user.click(screen.getByRole('button', { name: /add category/i }));
      await user.type(screen.getByLabelText(/name/i), 'Rent');
      // Type defaults to 'expense' — leave as-is
      await user.click(screen.getByRole('button', { name: /save/i }));

      await waitFor(() => {
        expect(mockedApi.post).toHaveBeenCalledWith(
          'categories',
          expect.objectContaining({ name: 'Rent', type: 'expense' }),
        );
      });
      // Explicit: no color field in the payload
      const payload = mockedApi.post.mock.calls[0][1] as Record<string, unknown>;
      expect(payload).not.toHaveProperty('color');
    });

    test('row kebab menu opens Edit and Activate/Deactivate actions', async () => {
      const user = userEvent.setup();
      renderCategories();
      await waitFor(() => {
        expect(screen.getByText('Food')).toBeInTheDocument();
      });

      // Each row has an "Actions for {name}" trigger
      await user.click(
        screen.getByRole('button', { name: /actions for food/i }),
      );
      expect(screen.getByRole('menuitem', { name: /edit/i })).toBeInTheDocument();
      expect(
        screen.getByRole('menuitem', { name: /deactivate/i }),
      ).toBeInTheDocument();
    });

    test('inactive category kebab shows Activate', async () => {
      const user = userEvent.setup();
      renderCategories();
      await waitFor(() => {
        expect(screen.getByText('Transport')).toBeInTheDocument();
      });

      await user.click(
        screen.getByRole('button', { name: /actions for transport/i }),
      );
      expect(
        screen.getByRole('menuitem', { name: /activate/i }),
      ).toBeInTheDocument();
    });

    test('inactive rows render an (inactive) label', async () => {
      renderCategories();
      await waitFor(() => {
        expect(screen.getByText('Transport')).toBeInTheDocument();
      });
      // "(inactive)" only appears on deactivated rows
      const transportRow = screen.getByText('Transport').closest('tr');
      expect(transportRow).not.toBeNull();
      expect(transportRow).toHaveTextContent(/\(inactive\)/);
    });

    test('Edit menu item opens a Sheet with existing category values', async () => {
      const user = userEvent.setup();
      renderCategories();
      await waitFor(() => {
        expect(screen.getByText('Food')).toBeInTheDocument();
      });

      await user.click(
        screen.getByRole('button', { name: /actions for food/i }),
      );
      await user.click(screen.getByRole('menuitem', { name: /edit/i }));

      expect(screen.getByRole('dialog')).toBeInTheDocument();
      expect(screen.getByLabelText(/name/i)).toHaveValue('Food');
      // No color input in edit form either
      expect(screen.queryByLabelText(/color/i)).not.toBeInTheDocument();
      // Type is immutable after creation — no Type select in edit mode
      expect(screen.queryByLabelText(/type/i)).not.toBeInTheDocument();
    });

    test('saving edits posts PUT without color or type fields', async () => {
      const user = userEvent.setup();
      mockedApi.put.mockResolvedValue({});
      renderCategories();
      await waitFor(() => {
        expect(screen.getByText('Food')).toBeInTheDocument();
      });

      await user.click(
        screen.getByRole('button', { name: /actions for food/i }),
      );
      await user.click(screen.getByRole('menuitem', { name: /edit/i }));

      const nameInput = screen.getByLabelText(/name/i);
      await user.clear(nameInput);
      await user.type(nameInput, 'Groceries');
      await user.click(screen.getByRole('button', { name: /save/i }));

      await waitFor(() => {
        expect(mockedApi.put).toHaveBeenCalledWith(
          'categories/1',
          expect.objectContaining({ name: 'Groceries' }),
        );
      });
      const payload = mockedApi.put.mock.calls[0][1] as Record<string, unknown>;
      // Backend accepts {name, icon}; type is immutable after creation.
      expect(payload).not.toHaveProperty('color');
      expect(payload).not.toHaveProperty('type');
    });

    test('Deactivate menu item PATCHes is_active=false', async () => {
      const user = userEvent.setup();
      mockedApi.patch.mockResolvedValue({});
      renderCategories();
      await waitFor(() => {
        expect(screen.getByText('Food')).toBeInTheDocument();
      });

      await user.click(
        screen.getByRole('button', { name: /actions for food/i }),
      );
      await user.click(screen.getByRole('menuitem', { name: /deactivate/i }));

      await waitFor(() => {
        expect(mockedApi.patch).toHaveBeenCalledWith('categories/1', {
          is_active: false,
        });
      });
    });

    test('Delete opens a confirm naming the category, and only confirming DELETEs', async () => {
      // pointerEventsCheck: 0 — the open AlertDialog sets
      // `pointer-events: none` on <body>, and happy-dom has no layout engine
      // to tell the portalled content apart. Same setup Settings' confirm
      // tests use.
      const user = userEvent.setup({ pointerEventsCheck: 0 });
      mockedApi.del.mockResolvedValue(undefined);
      renderCategories();
      await waitFor(() => {
        expect(screen.getByText('Food')).toBeInTheDocument();
      });

      await user.click(
        screen.getByRole('button', { name: /actions for food/i }),
      );
      await user.click(screen.getByRole('menuitem', { name: /delete/i }));

      // Opening the confirmation is not the deletion.
      const confirm = await screen.findByRole('alertdialog');
      expect(confirm).toHaveTextContent('Delete Food?');
      // …and the name carries the same register Settings' confirms give a
      // display name. Exact token, not a substring: `toContain('font-mono')`
      // would also match a hypothetical `md:font-mono`.
      const named = within(confirm).getByText('Food');
      expect(named.className.split(/\s+/)).toContain('font-mono');
      expect(mockedApi.del).not.toHaveBeenCalled();

      await user.click(
        within(confirm).getByRole('button', { name: /delete category/i }),
      );
      await waitFor(() => {
        expect(mockedApi.del).toHaveBeenCalledWith('categories/1');
      });
    });

    test('Cancel closes the confirm without deleting anything', async () => {
      const user = userEvent.setup({ pointerEventsCheck: 0 });
      mockedApi.del.mockResolvedValue(undefined);
      renderCategories();
      await waitFor(() => {
        expect(screen.getByText('Food')).toBeInTheDocument();
      });

      await user.click(
        screen.getByRole('button', { name: /actions for food/i }),
      );
      await user.click(screen.getByRole('menuitem', { name: /delete/i }));
      const confirm = await screen.findByRole('alertdialog');

      await user.click(within(confirm).getByRole('button', { name: /cancel/i }));

      await waitFor(() => {
        expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument();
      });
      expect(mockedApi.del).not.toHaveBeenCalled();
      // The row is untouched.
      expect(screen.getByText('Food')).toBeInTheDocument();
    });

    test('a delete refusal reaches a toast and the confirm stays open', async () => {
      // A toast, not the page banner: the banner renders BEHIND the confirm's
      // overlay, where the 409's sentence would never be seen. The dialog
      // staying open is the Settings delete-user precedent — a failed delete
      // needs somewhere to report back to.
      const user = userEvent.setup({ pointerEventsCheck: 0 });
      const refusal =
        'cannot delete category that has transactions — deactivate it instead, or reassign its transactions first';
      mockedApi.del.mockRejectedValueOnce(new Error(refusal));
      renderCategories();
      await waitFor(() => {
        expect(screen.getByText('Food')).toBeInTheDocument();
      });

      await user.click(
        screen.getByRole('button', { name: /actions for food/i }),
      );
      await user.click(screen.getByRole('menuitem', { name: /delete/i }));
      const confirm = await screen.findByRole('alertdialog');
      await user.click(
        within(confirm).getByRole('button', { name: /delete category/i }),
      );

      await waitFor(() => {
        expect(mockedToast.error).toHaveBeenCalledWith(refusal);
      });
      expect(screen.getByRole('alertdialog')).toBeInTheDocument();
    });

    test('a save refusal reaches a toast and the sheet stays open', async () => {
      // Same reasoning as the delete refusal above, and the same trap: the
      // page banner sits BEHIND the open sheet's overlay, so a duplicate-name
      // 409 reported there reads as "Save does nothing".
      const user = userEvent.setup({ pointerEventsCheck: 0 });
      const refusal = 'category name already exists';
      mockedApi.post.mockRejectedValueOnce(new Error(refusal));
      renderCategories();
      await waitFor(() => {
        expect(screen.getByText('Food')).toBeInTheDocument();
      });

      await user.click(screen.getByRole('button', { name: /add category/i }));
      await user.type(screen.getByLabelText(/name/i), 'Food');
      await user.click(screen.getByRole('button', { name: 'Save' }));

      await waitFor(() => {
        expect(mockedToast.error).toHaveBeenCalledWith(refusal);
      });
      // Still open, with the typed name intact — there is a value to correct.
      const sheet = screen.getByRole('dialog');
      expect(within(sheet).getByLabelText(/name/i)).toHaveValue('Food');
      // And the refusal did NOT go to the banner it would have been invisible in.
      expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    });

    test('Save goes disabled and reads Saving… while the write is in flight', async () => {
      // A live Save button through the round-trip is a double-POST waiting for
      // an impatient second tap.
      const user = userEvent.setup({ pointerEventsCheck: 0 });
      mockedApi.post.mockReturnValue(new Promise(() => {}));
      renderCategories();
      await waitFor(() => {
        expect(screen.getByText('Food')).toBeInTheDocument();
      });

      await user.click(screen.getByRole('button', { name: /add category/i }));
      await user.type(screen.getByLabelText(/name/i), 'Rent');
      await user.click(screen.getByRole('button', { name: 'Save' }));

      const busy = await screen.findByRole('button', { name: 'Saving…' });
      expect(busy).toBeDisabled();
      expect(busy).toHaveAttribute('aria-busy', 'true');
      // The idle label is gone, not merely duplicated.
      expect(
        screen.queryByRole('button', { name: 'Save' }),
      ).not.toBeInTheDocument();
      expect(mockedApi.post).toHaveBeenCalledTimes(1);
    });

    test('surfaces load failure in an alert banner', async () => {
      mockedApi.get.mockRejectedValueOnce(new Error('boom'));
      renderCategories();
      const banner = await screen.findByRole('alert');
      expect(banner).toHaveTextContent('boom');
    });
  });

  describe('as member', () => {
    beforeEach(asMember);

    test('hides Add category button for non-admin', async () => {
      renderCategories();
      await waitFor(() => {
        expect(screen.getByText('Food')).toBeInTheDocument();
      });
      expect(
        screen.queryByRole('button', { name: /add category/i }),
      ).not.toBeInTheDocument();
    });

    test('hides row kebab menus for non-admin', async () => {
      renderCategories();
      await waitFor(() => {
        expect(screen.getByText('Food')).toBeInTheDocument();
      });
      expect(
        screen.queryByRole('button', { name: /actions for food/i }),
      ).not.toBeInTheDocument();
    });
  });
});
