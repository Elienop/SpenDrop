import {
  act,
  render as rtlRender,
  screen,
  waitFor,
  within,
  type RenderOptions,
} from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, it, expect, vi } from 'vitest';
import { MemoryRouter } from 'react-router-dom';
import type { ReactElement } from 'react';
import { ImportPreviewTable } from './ImportPreviewTable';
import type { ImportPreview } from '@/api/types';

/**
 * Every render goes through a router, because the unknown-currency
 * detail row carries a `<Link>` to Settings → Currencies — the remedy
 * for that flag is a route change, not anything inside this table. A
 * bare render throws on the first such row and, worse, would let the
 * link degrade into a full page reload in production unnoticed.
 */
function render(ui: ReactElement, options?: Omit<RenderOptions, 'wrapper'>) {
  return rtlRender(ui, { ...options, wrapper: MemoryRouter });
}

function makePreview(overrides: Partial<ImportPreview> = {}): ImportPreview {
  return {
    import_id: 'preview-abc',
    row_count: 3,
    columns: ['Date', 'Description', 'Amount', 'Category'],
    unique_categories: ['Food'],
    collision_groups: [],
    expires_at: '2099-01-01T00:00:00Z',
    rows: [
      {
        row_id: 0,
        skip: false,
        content_hash: 'h0',
        date: '2025-01-07',
        description: 'Starbucks',
        amount: 5,
        category: 'Food',
      },
      {
        row_id: 1,
        skip: false,
        content_hash: 'h1',
        date: '2025-01-08',
        description: "Trader Joe's",
        amount: 42.1,
        category: 'Food',
      },
      {
        row_id: 2,
        skip: false,
        content_hash: 'h2',
        date: '2025-01-09',
        description: 'Amazon',
        amount: 29.99,
        category: 'Food',
      },
    ],
    ...overrides,
  };
}

const noopProps = {
  cellErrors: {},
  unresolvedCount: 0,
  unresolvedCategoryCount: 0,
  canImport: true,
  pendingPatchCount: 0,
  onPatchRow: vi.fn(),
  onApplyRate: vi.fn(),
  onConfirm: vi.fn(),
};

/**
 * The strings `importFieldLengthMessage` emits in
 * internal/api/import_handlers.go. Mirrored here as test INPUT only —
 * the component renders whatever the wire carries and composes no
 * wording of its own, which is the property most of these tests exist
 * to hold.
 */
const SERVER_FIELD_MESSAGES: Record<string, string> = {
  description:
    'Too long for SpenDrop, which stores 500 characters. Shorten it here, or skip this row.',
  tags: "This row's tags are longer than the 500 characters SpenDrop stores. Skip this row, or shorten them in your spreadsheet and upload again.",
  notes:
    "This row's note is longer than the 2,000 characters SpenDrop stores. Skip this row, or shorten the note in your spreadsheet and upload again.",
};

/** Field error in the shape the server actually sends — message included. */
function fieldError(row_id: number, field: 'description' | 'tags' | 'notes') {
  return { row_id, field, message: SERVER_FIELD_MESSAGES[field] };
}

describe('ImportPreviewTable — over-length fields', () => {
  it('flags the row and blocks import straight from the preview payload', () => {
    render(
      <ImportPreviewTable
        preview={makePreview({
          field_errors: [fieldError(1, 'notes')],
        })}
        {...noopProps}
        canImport={false}
      />,
    );

    const flagged = screen.getByText("Trader Joe's").closest('tr')!;
    expect(flagged.getAttribute('data-field-error')).toBe('true');
    // Clean rows are explicitly not flagged — asserting "not 'true'"
    // rather than "null" so the attribute merely changing shape does
    // not silently pass.
    expect(
      screen.getByText('Starbucks').closest('tr')!.getAttribute('data-field-error'),
    ).not.toBe('true');
    expect(screen.getByRole('button', { name: /^Import 3$/ })).toBeDisabled();
  });

  it("renders the server's sentence verbatim under the row it describes", () => {
    render(
      <ImportPreviewTable
        preview={makePreview({ field_errors: [fieldError(1, 'notes')] })}
        {...noopProps}
        canImport={false}
      />,
    );

    // Verbatim, not reworded: the backend owns this string and reuses it
    // for the PATCH 400, so any rephrasing here would make the same
    // condition read two ways depending on how the user reached it.
    const detail = screen.getByText(SERVER_FIELD_MESSAGES.notes);
    expect(detail).toBeInTheDocument();
    // Attached to the flagged row, not floating at the top of the table
    // — the sentence says "This row's note", so it has to sit against a
    // row for that to mean anything.
    const detailRow = detail.closest('tr')!;
    expect(detailRow.getAttribute('data-field-error-detail')).toBe('1');
    expect(detailRow.previousElementSibling?.getAttribute('data-row-id')).toBe(
      '1',
    );
  });

  it('states only a count and an action in the bulk bar, never an explanation', () => {
    render(
      <ImportPreviewTable
        preview={makePreview({ field_errors: [fieldError(1, 'notes')] })}
        {...noopProps}
        canImport={false}
      />,
    );

    const bar = screen.getByRole('heading', {
      name: '1 row is too long to import',
    });
    // The bar is ours; the sentences are the server's. A limit or a
    // remedy restated here would be a second copy of the server's
    // wording, free to drift from it.
    expect(bar.textContent).not.toMatch(/\d+ characters|shorten|skip/i);
  });

  it('does not repeat a description error that its own cell already shows', () => {
    // Description has a cell, so `cellErrors` renders the message there.
    // A row-level copy as well would print the same sentence twice.
    render(
      <ImportPreviewTable
        preview={makePreview({ field_errors: [fieldError(0, 'description')] })}
        {...noopProps}
        cellErrors={{
          '0:description': {
            field: 'description',
            message: SERVER_FIELD_MESSAGES.description,
          },
        }}
        canImport={false}
      />,
    );

    expect(
      screen.getAllByText(SERVER_FIELD_MESSAGES.description),
    ).toHaveLength(1);
    expect(
      document.querySelector('[data-field-error-detail]'),
    ).toBeNull();
  });

  it('offers a bulk skip that fires one PATCH per flagged row and no others', async () => {
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    const onPatchRow = vi.fn().mockResolvedValue(undefined);
    render(
      <ImportPreviewTable
        preview={makePreview({
          field_errors: [fieldError(0, 'description'), fieldError(2, 'notes')],
        })}
        {...noopProps}
        onPatchRow={onPatchRow}
        canImport={false}
      />,
    );

    await user.click(screen.getByRole('button', { name: 'Skip these 2 rows' }));

    expect(onPatchRow).toHaveBeenCalledTimes(2);
    expect(onPatchRow).toHaveBeenCalledWith(0, 'skip', true);
    expect(onPatchRow).toHaveBeenCalledWith(2, 'skip', true);
    // Row 1 is clean and must be left alone — a bulk escape that skipped
    // healthy rows would silently drop data the user wanted.
    expect(onPatchRow).not.toHaveBeenCalledWith(1, 'skip', true);
  });

  it('says "Skip this row" when only one row is flagged', () => {
    render(
      <ImportPreviewTable
        preview={makePreview({
          field_errors: [fieldError(0, 'description')],
        })}
        {...noopProps}
        canImport={false}
      />,
    );

    expect(
      screen.getByRole('button', { name: 'Skip this row' }),
    ).toBeInTheDocument();
  });

  it('drops the flag and the banner once the row is skipped', () => {
    const preview = makePreview({
      field_errors: [fieldError(0, 'description')],
    });
    preview.rows[0].skip = true;

    render(
      <ImportPreviewTable preview={preview} {...noopProps} />,
    );

    expect(
      screen.getByText('Starbucks').closest('tr')!.getAttribute('data-field-error'),
    ).not.toBe('true');
    expect(screen.queryByRole('button', { name: /^Skip th/ })).not.toBeInTheDocument();
    expect(screen.getByText(/Ready to import/)).toBeInTheDocument();
  });

  it("points a flagged row at its own explanation, not at the bulk bar", () => {
    render(
      <ImportPreviewTable
        preview={makePreview({ field_errors: [fieldError(0, 'notes')] })}
        {...noopProps}
        canImport={false}
      />,
    );

    const cell = screen.getByText('Starbucks').closest('td')!;
    expect(cell.getAttribute('aria-describedby')).toContain(
      'import-field-error-0',
    );
    // The IDREF must resolve to the sentence about THIS row. Pointing at
    // the bulk bar would tell a screen-reader user only that some number
    // of rows are too long, without saying which field or what to do.
    const target = document.getElementById('import-field-error-0');
    expect(target).not.toBeNull();
    expect(target!.textContent).toBe(SERVER_FIELD_MESSAGES.notes);
  });

  it('names both blockers in the status line when a preview has each', () => {
    render(
      <ImportPreviewTable
        preview={makePreview({
          collision_groups: [
            { group_id: 'g1', reason: 'intra_file', member_row_ids: [0, 1] },
          ],
          field_errors: [fieldError(2, 'description')],
        })}
        {...noopProps}
        unresolvedCount={1}
        unresolvedCategoryCount={0}
        canImport={false}
      />,
    );

    // Fixing only the one the status named would leave the button
    // disabled with no explanation.
    expect(
      screen.getByText('Fix or skip 1 collision and 1 too-long row to enable import'),
    ).toBeInTheDocument();
  });

  it('lets a row carry both flags at once rather than choosing one section', () => {
    // Two rows with the same over-long description collide AND are both
    // too long. Pulling them into a section of their own would leave the
    // collision header counting rows it no longer shows.
    render(
      <ImportPreviewTable
        preview={makePreview({
          collision_groups: [
            { group_id: 'g1', reason: 'intra_file', member_row_ids: [0, 1] },
          ],
          field_errors: [fieldError(0, 'description'), fieldError(1, 'description')],
        })}
        {...noopProps}
        unresolvedCount={1}
        unresolvedCategoryCount={0}
        canImport={false}
      />,
    );

    const row0 = screen.getByText('Starbucks').closest('tr')!;
    expect(row0.getAttribute('data-collision')).toBe('true');
    expect(row0.getAttribute('data-field-error')).toBe('true');
    // Both group members still render under their collision header.
    expect(screen.getByText("Trader Joe's").closest('tr')).not.toBeNull();
    expect(screen.getByText(/2 rows collide/)).toBeInTheDocument();
  });

  // The rows this feature flags are exactly the rows that would wreck
  // the preview: a 5,500-character description rendered unbounded
  // measured 2,211px tall in the browser, pushing the banner asking the
  // user to act on it two screens off the top.
  //
  // Note what these can and cannot prove. happy-dom does no layout, so
  // none of them measures a pixel — they pin the mechanism that bounds
  // the height (the classes and the title), and the behaviour that
  // mechanism must not break. The height itself needs a browser.
  describe('unbounded cell growth', () => {
    const longDescription = 'x'.repeat(5500);

    function previewWithLongDescription() {
      const preview = makePreview({
        field_errors: [fieldError(0, 'description')],
      });
      preview.rows[0].description = longDescription;
      return preview;
    }

    it('bounds the description cell and keeps the full text reachable', () => {
      render(
        <ImportPreviewTable
          preview={previewWithLongDescription()}
          {...noopProps}
          canImport={false}
        />,
      );

      const value = screen.getByText(longDescription);
      expect(value.className).toContain('truncate');
      expect(value.className).toContain('max-w-[28rem]');
      // truncate needs a block box to have anything to overflow.
      expect(value.className).toContain('block');
      // Hover reveals the whole value without entering the editor.
      expect(value.getAttribute('title')).toBe(longDescription);
    });

    it('keeps truncation visual — the editor still opens on the whole value', async () => {
      // The remedy the error message points at is "shorten it here", so
      // a bound that fed the editor the ellipsised text would quietly
      // destroy 5,000 characters on the next commit.
      const user = userEvent.setup({ pointerEventsCheck: 0 });
      render(
        <ImportPreviewTable
          preview={previewWithLongDescription()}
          {...noopProps}
          canImport={false}
        />,
      );

      await user.dblClick(screen.getByText(longDescription));

      const editor = screen.getByRole('textbox');
      expect((editor as HTMLInputElement).value).toBe(longDescription);
      expect((editor as HTMLInputElement).value).toHaveLength(5500);
    });

    it('does not clip the error sentence while bounding the value', () => {
      // `truncate` on the CELL — the obvious way to copy TransactionRow —
      // sets white-space:nowrap on the error paragraph too and collapses
      // the server's sentence to one ellipsised line.
      render(
        <ImportPreviewTable
          preview={previewWithLongDescription()}
          {...noopProps}
          cellErrors={{
            '0:description': {
              field: 'description',
              message: SERVER_FIELD_MESSAGES.description,
            },
          }}
          canImport={false}
        />,
      );

      const message = screen.getByText(SERVER_FIELD_MESSAGES.description);
      expect(message.className).not.toContain('truncate');
      const cell = message.closest('td')!;
      expect(cell.className).not.toContain('truncate');
      // The bound is still applied, just at the level that does not
      // swallow the sentence.
      expect(cell.className).toContain('max-w-[28rem]');
    });

    it('bounds the category cell, which the backend never length-checks', () => {
      const preview = makePreview();
      preview.rows[0].category = 'y'.repeat(3000);

      render(<ImportPreviewTable preview={preview} {...noopProps} />);

      const cell = screen.getByText('y'.repeat(3000));
      expect(cell.className).toContain('truncate');
      expect(cell.className).toContain('max-w-[28rem]');
      expect(cell.getAttribute('title')).toBe('y'.repeat(3000));
    });

    it('leaves the short parsed columns unbounded', () => {
      // Date and amount are parsed before they reach the table, so they
      // cannot be pathological — bounding them would be noise, and a
      // future reader should see that the omission is deliberate.
      render(<ImportPreviewTable preview={makePreview()} {...noopProps} />);

      expect(screen.getByText('2025-01-07').className).not.toContain(
        'truncate',
      );
      expect(screen.getByText('5.00').className).not.toContain('truncate');
    });
  });

  it('disables the bulk skip while a PATCH is in flight', () => {
    render(
      <ImportPreviewTable
        preview={makePreview({
          field_errors: [fieldError(0, 'description')],
        })}
        {...noopProps}
        pendingPatchCount={1}
        canImport={false}
      />,
    );

    expect(screen.getByRole('button', { name: 'Skip this row' })).toBeDisabled();
  });
});

describe('ImportPreviewTable', () => {
  it('renders one row per preview.rows entry', () => {
    render(<ImportPreviewTable preview={makePreview()} {...noopProps} />);
    // Every row rendered — descriptions are a stable proxy.
    expect(screen.getByText('Starbucks')).toBeInTheDocument();
    expect(screen.getByText("Trader Joe's")).toBeInTheDocument();
    expect(screen.getByText('Amazon')).toBeInTheDocument();
  });

  it('applies amber collision class to rows in collision_groups and nothing to clean rows', () => {
    const preview = makePreview({
      collision_groups: [
        { group_id: 'g1', reason: 'intra_file', member_row_ids: [0, 1] },
      ],
    });
    render(<ImportPreviewTable preview={preview} {...noopProps} />);

    const row0 = screen.getByText('Starbucks').closest('tr')!;
    const row1 = screen.getByText("Trader Joe's").closest('tr')!;
    const row2 = screen.getByText('Amazon').closest('tr')!;

    // Collision rows carry the amber marker (asserted via data-collision so
    // we're not coupled to a specific Tailwind utility class).
    expect(row0.getAttribute('data-collision')).toBe('true');
    expect(row1.getAttribute('data-collision')).toBe('true');
    // Clean row is explicitly NOT collision — checking "!== 'true'" rather
    // than null because the attribute could be absent OR set to 'false'.
    expect(row2.getAttribute('data-collision')).not.toBe('true');
  });

  it('stale-style regression: a row that flips collision → clean loses data-collision on re-render', () => {
    // Start: row 0 is in a collision group.
    const before = makePreview({
      collision_groups: [
        { group_id: 'g1', reason: 'intra_file', member_row_ids: [0, 1] },
      ],
    });
    const { rerender } = render(<ImportPreviewTable preview={before} {...noopProps} />);

    expect(screen.getByText('Starbucks').closest('tr')!.getAttribute('data-collision')).toBe('true');

    // Simulate the PATCH response: row 0 is now clean. The hook's merge
    // hands us a fresh preview with collision_groups that no longer include
    // row 0. This is the EXACT case importcsv #16 blew — if the component
    // carries collision state in useState/useEffect-derived state instead of
    // deriving from props, row 0 keeps its amber class until the next state
    // update and the user sees a stale "unresolved" marker.
    const after = makePreview({
      collision_groups: [
        { group_id: 'g1', reason: 'intra_file', member_row_ids: [1] }, // row 0 removed
      ],
    });
    rerender(<ImportPreviewTable preview={after} {...noopProps} />);

    expect(screen.getByText('Starbucks').closest('tr')!.getAttribute('data-collision')).not.toBe('true');
    // And row 1 still carries it.
    expect(screen.getByText("Trader Joe's").closest('tr')!.getAttribute('data-collision')).toBe('true');
  });

  it('Import button reflects canImport and pendingPatchCount', () => {
    const onConfirm = vi.fn();
    const preview = makePreview();

    // Case 1: canImport=false → disabled, label shows unresolved count.
    const { rerender } = render(
      <ImportPreviewTable
        preview={preview}
        cellErrors={{}}
        unresolvedCount={3}
        unresolvedCategoryCount={0}
        canImport={false}
        pendingPatchCount={0}
        onPatchRow={vi.fn()}
        onApplyRate={vi.fn()}
        onConfirm={onConfirm}
      />,
    );
    const btn = screen.getByRole('button', { name: /import/i });
    expect(btn).toBeDisabled();
    // Status message reflects unresolved count.
    expect(screen.getByText(/3.*collision/i)).toBeInTheDocument();

    // Case 2: canImport=true, pendingPatchCount=0 → enabled.
    rerender(
      <ImportPreviewTable
        preview={preview}
        cellErrors={{}}
        unresolvedCount={0}
        unresolvedCategoryCount={0}
        canImport={true}
        pendingPatchCount={0}
        onPatchRow={vi.fn()}
        onApplyRate={vi.fn()}
        onConfirm={onConfirm}
      />,
    );
    expect(btn).toBeEnabled();

    // Case 3: pendingPatchCount > 0 → disabled even though canImport is
    // nominally true. This is the PATCH/confirm race guard.
    rerender(
      <ImportPreviewTable
        preview={preview}
        cellErrors={{}}
        unresolvedCount={0}
        unresolvedCategoryCount={0}
        canImport={true}
        pendingPatchCount={2}
        onPatchRow={vi.fn()}
        onApplyRate={vi.fn()}
        onConfirm={onConfirm}
      />,
    );
    expect(btn).toBeDisabled();

    // Case 4: click back in case-2 state fires onConfirm once.
    rerender(
      <ImportPreviewTable
        preview={preview}
        cellErrors={{}}
        unresolvedCount={0}
        unresolvedCategoryCount={0}
        canImport={true}
        pendingPatchCount={0}
        onPatchRow={vi.fn()}
        onApplyRate={vi.fn()}
        onConfirm={onConfirm}
      />,
    );
    btn.click();
    expect(onConfirm).toHaveBeenCalledTimes(1);
  });

  it('double-click cell + type + Enter fires onPatchRow with correct payload, and inline 400 renders when cellErrors is non-empty', async () => {
    const user = userEvent.setup();
    const onPatchRow = vi.fn().mockResolvedValue(undefined);

    const { rerender } = render(
      <ImportPreviewTable
        preview={makePreview()}
        cellErrors={{}}
        unresolvedCount={0}
        unresolvedCategoryCount={0}
        canImport={true}
        pendingPatchCount={0}
        onPatchRow={onPatchRow}
        onApplyRate={vi.fn()}
        onConfirm={vi.fn()}
      />,
    );

    // Double-click row 0's description cell (Starbucks).
    const cell = screen.getByText('Starbucks');
    await user.dblClick(cell);

    // Edit input must exist, focused, and have the current value.
    const input = await screen.findByDisplayValue('Starbucks');
    expect(input).toHaveFocus();

    // Clear + type new value + Enter.
    await user.clear(input);
    await user.type(input, 'Starbucks NYC{Enter}');

    expect(onPatchRow).toHaveBeenCalledTimes(1);
    expect(onPatchRow).toHaveBeenCalledWith(0, 'description', 'Starbucks NYC');

    // Rerender with the 400 error injected for (row_id=0, field='description').
    rerender(
      <ImportPreviewTable
        preview={makePreview()}
        cellErrors={{ '0:description': { field: 'description', message: 'INVALID_DESCRIPTION' } }}
        unresolvedCount={0}
        unresolvedCategoryCount={0}
        canImport={true}
        pendingPatchCount={0}
        onPatchRow={onPatchRow}
        onApplyRate={vi.fn()}
        onConfirm={vi.fn()}
      />,
    );

    // Inline error message rendered.
    expect(screen.getByText('INVALID_DESCRIPTION')).toBeInTheDocument();
    // The cell has an error marker (data attribute to avoid coupling to classes).
    const row0 = screen.getByText('Starbucks').closest('tr')!;
    const errorCell = within(row0).getAllByText(/INVALID_DESCRIPTION|Starbucks/i)[0].closest('td')!;
    expect(errorCell.getAttribute('data-cell-error')).toBe('true');
  });

  /**
   * One `<Input>` serves three columns here, so the keypad hint cannot live on
   * the element — it has to be derived from `field`. Both halves are asserted
   * because a hint applied unconditionally would look identical on the amount
   * cell and silently open a digits pad on free text. The shape is explained
   * once, on AmountCurrencyInput's `_PairsTypeNumberWithInputModeDecimal`:
   * no test environment raises a soft keyboard, so this pins the ATTRIBUTE.
   */
  it('the shared cell editor asks for a decimal keypad on amount and none on description', async () => {
    const user = userEvent.setup();
    render(<ImportPreviewTable preview={makePreview()} {...noopProps} />);

    // `29.99` is row 2's amount, rendered via `row.amount.toFixed(2)`.
    await user.dblClick(screen.getByText('29.99'));
    const amountEditor = await screen.findByDisplayValue('29.99');
    expect(amountEditor).toHaveAttribute('inputmode', 'decimal');
  });

  it('the shared cell editor leaves a description cell with no keypad hint', async () => {
    const user = userEvent.setup();
    render(<ImportPreviewTable preview={makePreview()} {...noopProps} />);

    // The negative half of the pair above. This is the assertion that fails if
    // the hint is applied to every field rather than derived from `field`.
    await user.dblClick(screen.getByText('Amazon'));
    const descriptionEditor = await screen.findByDisplayValue('Amazon');
    expect(descriptionEditor).not.toHaveAttribute('inputmode');
  });

  it('Escape during edit cancels without firing onPatchRow', async () => {
    const user = userEvent.setup();
    const onPatchRow = vi.fn();

    render(
      <ImportPreviewTable
        preview={makePreview()}
        cellErrors={{}}
        unresolvedCount={0}
        unresolvedCategoryCount={0}
        canImport={true}
        pendingPatchCount={0}
        onPatchRow={onPatchRow}
        onApplyRate={vi.fn()}
        onConfirm={vi.fn()}
      />,
    );

    await user.dblClick(screen.getByText('Starbucks'));
    const input = await screen.findByDisplayValue('Starbucks');
    await user.clear(input);
    await user.type(input, 'Nope{Escape}');

    expect(onPatchRow).not.toHaveBeenCalled();
    // The cell shows the original value again.
    expect(screen.getByText('Starbucks')).toBeInTheDocument();
  });

  it('Skip checkbox toggles in both directions: unskipped row → skip=true, pre-skipped row → skip=false', async () => {
    const user = userEvent.setup();
    const onPatchRow = vi.fn().mockResolvedValue(undefined);

    // Fixture intentionally mixes skip states so a single render exercises
    // BOTH transitions. The un-skip case is the one most likely to regress
    // silently — after a "Skip all in group" bulk action the user needs a
    // way to un-skip individual rows, and that's the only UI surface for
    // it. If this test only checked the skip direction, a bug where the
    // checkbox was hard-coded to always emit `true` would pass.
    const preview = makePreview({
      rows: [
        {
          row_id: 0,
          skip: false,
          content_hash: 'h0',
          date: '2025-01-07',
          description: 'Starbucks',
          amount: 5,
          category: 'Food',
        },
        {
          row_id: 1,
          skip: true,
          content_hash: 'h1',
          date: '2025-01-08',
          description: "Trader Joe's",
          amount: 42.1,
          category: 'Food',
        },
      ],
    });

    render(
      <ImportPreviewTable
        preview={preview}
        cellErrors={{}}
        unresolvedCount={0}
        unresolvedCategoryCount={0}
        canImport={true}
        pendingPatchCount={0}
        onPatchRow={onPatchRow}
        onApplyRate={vi.fn()}
        onConfirm={vi.fn()}
      />,
    );

    // Unskipped row → checkbox unchecked, click emits skip=true.
    const row0 = screen.getByText('Starbucks').closest('tr')!;
    const checkbox0 = within(row0).getByRole('checkbox');
    expect(checkbox0).not.toBeChecked();
    await user.click(checkbox0);
    expect(onPatchRow).toHaveBeenNthCalledWith(1, 0, 'skip', true);

    // Pre-skipped row → checkbox checked, click emits skip=false (un-skip path).
    const row1 = screen.getByText("Trader Joe's").closest('tr')!;
    const checkbox1 = within(row1).getByRole('checkbox');
    expect(checkbox1).toBeChecked();
    await user.click(checkbox1);
    expect(onPatchRow).toHaveBeenNthCalledWith(2, 1, 'skip', false);

    expect(onPatchRow).toHaveBeenCalledTimes(2);
  });

  it('renders a collision group header with "Skip all" that fires N onPatchRow calls', async () => {
    const user = userEvent.setup();
    const onPatchRow = vi.fn().mockResolvedValue(undefined);

    render(
      <ImportPreviewTable
        preview={makePreview({
          collision_groups: [
            { group_id: 'g1', reason: 'intra_file', member_row_ids: [0, 1, 2] },
          ],
        })}
        cellErrors={{}}
        unresolvedCount={3}
        unresolvedCategoryCount={0}
        canImport={false}
        pendingPatchCount={0}
        onPatchRow={onPatchRow}
        onApplyRate={vi.fn()}
        onConfirm={vi.fn()}
      />,
    );

    // Header row present and labelled.
    expect(screen.getByText(/3 rows collide/i)).toBeInTheDocument();

    // Button label MUST embed the member count so the user sees the
    // destructive scope before clicking — matches "Skip all 3 in group",
    // not the looser "Skip all" pattern that would silently accept a
    // label regression like "Skip all".
    const skipAll = screen.getByRole('button', { name: /skip all 3 in group/i });
    await user.click(skipAll);

    // One PATCH per member row, in member-row-id order, each with skip=true.
    expect(onPatchRow).toHaveBeenCalledTimes(3);
    expect(onPatchRow).toHaveBeenNthCalledWith(1, 0, 'skip', true);
    expect(onPatchRow).toHaveBeenNthCalledWith(2, 1, 'skip', true);
    expect(onPatchRow).toHaveBeenNthCalledWith(3, 2, 'skip', true);
  });
});

describe('ImportPreviewTable — unresolved categories', () => {
  it('names the blocker and points at where it is fixed', () => {
    render(
      <ImportPreviewTable
        preview={makePreview()}
        {...noopProps}
        unresolvedCategoryCount={2}
        canImport={false}
      />,
    );

    // "Fix or skip" would point at the table; the remedy is the mapping
    // panel below it, so this blocker carries its own verb.
    expect(
      screen.getByText(/2 category choices still needed below/),
    ).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /^Import 3$/ })).toBeDisabled();
  });

  it('composes with the row-level blockers rather than replacing them', () => {
    render(
      <ImportPreviewTable
        preview={makePreview({ field_errors: [fieldError(1, 'notes')] })}
        {...noopProps}
        unresolvedCount={1}
        unresolvedCategoryCount={1}
        canImport={false}
      />,
    );

    // All three at once. Naming only one would send the user to fix a
    // third of the problem and watch the button stay disabled.
    const status = screen.getByText(/Fix or skip 1 collision and 1 too-long row/);
    expect(status.textContent).toMatch(
      /1 category choice still needed below/,
    );
  });

  it('reports ready only when the category blocker is clear too', () => {
    const { rerender } = render(
      <ImportPreviewTable
        preview={makePreview()}
        {...noopProps}
        unresolvedCategoryCount={1}
        canImport={false}
      />,
    );
    expect(screen.queryByText(/Ready to import/)).toBeNull();

    rerender(
      <ImportPreviewTable
        preview={makePreview()}
        {...noopProps}
        unresolvedCategoryCount={0}
        canImport
      />,
    );
    expect(screen.getByText(/Ready to import 3 rows/)).toBeInTheDocument();
  });
});

/**
 * The strings the backend's money resolver emits — mirrored here as test
 * INPUT only, exactly like SERVER_FIELD_MESSAGES above. The table
 * renders whatever the wire carries; if any of these sentences appear in
 * the component, that is the bug these fixtures exist to catch.
 */
const SERVER_MONEY_MESSAGES = {
  rateMissing:
    'no rate for 1,500,000 LBP — enter one, or apply today’s 89,000',
  unknownCurrency: "`LBX` isn't set up — add it under Settings → Currencies",
  amountDisagrees: '16.00 ≠ 1,500,000 ÷ 89,000 = 16.85',
} as const;

/** The currencies table as the preview reports it. */
const PREVIEW_CURRENCIES = [
  { code: 'USD', rate_to_base: 1, is_base: true },
  { code: 'LBP', rate_to_base: 89000, is_base: false },
];

describe('ImportPreviewTable — money', () => {
  it('renders a Rate column between Amount and Skip', () => {
    render(<ImportPreviewTable preview={makePreview()} {...noopProps} />);

    const headers = screen
      .getAllByRole('columnheader')
      .map((th) => th.textContent);
    // Order is the contract, not just membership: the rate reads as the
    // divisor of the Amount beside it, and putting it anywhere else
    // leaves the two halves of one figure apart.
    //
    // "Rate to base", not "Rate": the same quantity is labelled "Rate to
    // Base" in Settings → Currencies, and a bare "Rate" leaves its
    // DIRECTION to be guessed — the one ambiguity the upload copy spends
    // a sentence closing.
    expect(headers).toEqual([
      'Date',
      'Description',
      'Category',
      'Amount',
      'Rate to base',
      'Skip',
    ]);
  });

  it('shows the sheet rate in its cell and nothing at all when there is none', () => {
    const preview = makePreview();
    preview.rows[0].rate = 89000;
    preview.rows[0].original_amount = 1500000;
    preview.rows[0].original_currency = 'LBP';
    render(<ImportPreviewTable preview={preview} {...noopProps} />);

    const rated = screen.getByText('Starbucks').closest('tr')!;
    const withoutRate = screen.getByText('Amazon').closest('tr')!;
    expect(
      rated.querySelector('[data-import-col="rate"]')!.textContent,
    ).toBe('89000');
    // Empty, not "0" and not "—": the cell is an editor, and a
    // placeholder character would be the value the editor opened on.
    expect(
      withoutRate.querySelector('[data-import-col="rate"]')!.textContent,
    ).toBe('');
  });

  it('asks for a decimal keypad on the rate cell and PATCHes the typed string', async () => {
    const user = userEvent.setup();
    const onPatchRow = vi.fn().mockResolvedValue(undefined);
    const preview = makePreview();
    preview.rows[0].rate = 89000;
    render(
      <ImportPreviewTable
        preview={preview}
        {...noopProps}
        onPatchRow={onPatchRow}
      />,
    );

    await user.dblClick(screen.getByText('89000'));
    const editor = await screen.findByDisplayValue('89000');
    // A rate is money-shaped for the keypad's purposes — see the
    // numeric-input rule in the design guide. No test environment
    // raises a soft keyboard, so this pins the ATTRIBUTE.
    expect(editor).toHaveAttribute('inputmode', 'decimal');

    await user.clear(editor);
    await user.type(editor, '90000{Enter}');

    expect(onPatchRow).toHaveBeenCalledWith(0, 'rate', '90000');
  });

  it('lets an empty rate cell be edited, which is how a rate is cleared', async () => {
    const user = userEvent.setup();
    const onPatchRow = vi.fn().mockResolvedValue(undefined);
    const preview = makePreview();
    preview.rows[0].rate = 89000;
    render(
      <ImportPreviewTable
        preview={preview}
        {...noopProps}
        onPatchRow={onPatchRow}
      />,
    );

    await user.dblClick(screen.getByText('89000'));
    const editor = await screen.findByDisplayValue('89000');
    await user.clear(editor);
    await user.type(editor, '{Enter}');

    // The empty string is the CLEAR instruction on the wire, so it has to
    // reach the server rather than being swallowed as "no change".
    expect(onPatchRow).toHaveBeenCalledWith(0, 'rate', '');
  });

  it('shows a foreign row’s original and the rate it was divided by', () => {
    const preview = makePreview({ currencies: PREVIEW_CURRENCIES });
    preview.rows[0].amount = 16.85;
    preview.rows[0].amount_derived = true;
    preview.rows[0].original_amount = 1500000;
    preview.rows[0].original_currency = 'LBP';
    preview.rows[0].rate = 89000;
    render(<ImportPreviewTable preview={preview} {...noopProps} />);

    const row = screen.getByText('Starbucks').closest('tr')!;
    // The stored value on the first line, what it came from on the
    // second. Without the second line the user is asked to accept a
    // number with no way to check it.
    expect(within(row).getByText('16.85')).toBeInTheDocument();
    expect(
      within(row).getByText('1,500,000.00 LBP @ 89,000'),
    ).toBeInTheDocument();
  });

  it('drops the "@ rate" for a label-only row, which quoted none', () => {
    const preview = makePreview({ currencies: PREVIEW_CURRENCIES });
    preview.rows[0].original_amount = 1500000;
    preview.rows[0].original_currency = 'LBP';
    render(<ImportPreviewTable preview={preview} {...noopProps} />);

    const row = screen.getByText('Starbucks').closest('tr')!;
    const secondary = within(row).getByTestId('import-original-money');
    // "@ " with nothing after it, or a rate the sheet never quoted, would
    // both be claims about how this row was priced. It was not priced —
    // the USD came off the sheet and the original is a label.
    expect(secondary.textContent).toBe('1,500,000.00 LBP');
    expect(secondary.textContent).not.toContain('@');
  });

  it('says nothing extra on a base-currency row', () => {
    const preview = makePreview({ currencies: PREVIEW_CURRENCIES });
    preview.rows[0].original_amount = 5;
    preview.rows[0].original_currency = 'usd';
    render(<ImportPreviewTable preview={preview} {...noopProps} />);

    // The backend collapses a base-currency label (matrix #7), storing no
    // original at all — so echoing "5.00 USD" under a $5.00 row would
    // describe a row the import is not going to write. Case-insensitive,
    // like every currency lookup on the server.
    expect(screen.queryByTestId('import-original-money')).toBeNull();
  });

  it('routes a rate flag to the rate cell and an amount flag to the amount cell', () => {
    const preview = makePreview({
      currencies: PREVIEW_CURRENCIES,
      field_errors: [
        { row_id: 0, field: 'rate', message: SERVER_MONEY_MESSAGES.rateMissing },
        {
          row_id: 1,
          field: 'amount',
          message: SERVER_MONEY_MESSAGES.amountDisagrees,
        },
      ],
    });
    render(
      <ImportPreviewTable
        preview={preview}
        {...noopProps}
        cellErrors={{
          '0:rate': { field: 'rate', message: SERVER_MONEY_MESSAGES.rateMissing },
          '1:amount': {
            field: 'amount',
            message: SERVER_MONEY_MESSAGES.amountDisagrees,
          },
        }}
        canImport={false}
      />,
    );

    const rateCell = screen
      .getByText(SERVER_MONEY_MESSAGES.rateMissing)
      .closest('td')!;
    expect(rateCell.getAttribute('data-import-col')).toBe('rate');
    const amountCell = screen
      .getByText(SERVER_MONEY_MESSAGES.amountDisagrees)
      .closest('td')!;
    // Identified by POSITION and by what it holds, because the Amount
    // column carries no marker of its own: only the new Rate column
    // does, and only so the positive control can subtract it. The
    // amount error has to land in the cell showing the amount it
    // disputes — row 1's 42.10 — not merely somewhere in the row.
    expect(
      Array.from(amountCell.parentElement!.children).indexOf(amountCell),
    ).toBe(3);
    expect(amountCell.textContent).toContain('42.10');
    // Both rows are blocked, and marked as such rather than only counted.
    expect(
      screen.getByText('Starbucks').closest('tr')!.getAttribute('data-money-error'),
    ).toBe('true');
    expect(
      screen.getByText('Amazon').closest('tr')!.getAttribute('data-money-error'),
    ).not.toBe('true');
  });

  it('puts an unknown currency under the row with a way to go and add it', () => {
    const preview = makePreview({
      currencies: PREVIEW_CURRENCIES,
      field_errors: [
        {
          row_id: 1,
          field: 'original_currency',
          message: SERVER_MONEY_MESSAGES.unknownCurrency,
        },
      ],
    });
    preview.rows[1].original_currency = 'LBX';
    preview.rows[1].original_amount = 1500000;
    render(
      <ImportPreviewTable preview={preview} {...noopProps} canImport={false} />,
    );

    // Verbatim, and at row level: there is no currency cell to hang it
    // on, and the remedy is not in this table at all.
    const detail = screen.getByText(SERVER_MONEY_MESSAGES.unknownCurrency, {
      exact: false,
    });
    const detailRow = detail.closest('tr')!;
    expect(detailRow.getAttribute('data-field-error-detail')).toBe('1');
    expect(detailRow.previousElementSibling?.getAttribute('data-row-id')).toBe(
      '1',
    );

    // A route change, not a modal: the currency is added on another
    // Settings section, and the session survives the trip.
    const link = within(detailRow).getByRole('link');
    expect(link).toHaveAttribute('href', '/settings?tab=currencies');
  });

  it('names the money blocker in the status line and disables Import', () => {
    render(
      <ImportPreviewTable
        preview={makePreview({
          currencies: PREVIEW_CURRENCIES,
          field_errors: [
            {
              row_id: 0,
              field: 'rate',
              message: SERVER_MONEY_MESSAGES.rateMissing,
            },
            {
              row_id: 1,
              field: 'amount',
              message: SERVER_MONEY_MESSAGES.amountDisagrees,
            },
          ],
        })}
        {...noopProps}
        canImport={false}
      />,
    );

    expect(
      screen.getByText(
        'Fix or skip 2 rows with money problems to enable import',
      ),
    ).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /^Import 3$/ })).toBeDisabled();
  });

  it('keeps the singular when exactly one row has money trouble', () => {
    render(
      <ImportPreviewTable
        preview={makePreview({
          currencies: PREVIEW_CURRENCIES,
          field_errors: [
            {
              row_id: 0,
              field: 'rate',
              message: SERVER_MONEY_MESSAGES.rateMissing,
            },
          ],
        })}
        {...noopProps}
        canImport={false}
      />,
    );

    expect(
      screen.getByText(
        'Fix or skip 1 row with a money problem to enable import',
      ),
    ).toBeInTheDocument();
  });

  it('composes with the other two blockers instead of replacing them', () => {
    render(
      <ImportPreviewTable
        preview={makePreview({
          currencies: PREVIEW_CURRENCIES,
          collision_groups: [
            { group_id: 'g1', reason: 'intra_file', member_row_ids: [0, 1] },
          ],
          field_errors: [
            fieldError(2, 'notes'),
            {
              row_id: 0,
              field: 'rate',
              message: SERVER_MONEY_MESSAGES.rateMissing,
            },
          ],
        })}
        {...noopProps}
        unresolvedCount={1}
        canImport={false}
      />,
    );

    // Three blockers, one sentence, and a comma before the last "and" —
    // "a and b and c" is what a straight join produces and it reads as a
    // mistake. Naming only one would send the user to fix a third of the
    // problem and watch the button stay disabled.
    expect(
      screen.getByText(
        'Fix or skip 1 collision, 1 too-long row and 1 row with a money problem to enable import',
      ),
    ).toBeInTheDocument();
  });

  it('offers today’s rate for exactly the rows that quoted none', async () => {
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    const onApplyRate = vi.fn().mockResolvedValue(undefined);
    const preview = makePreview({
      currencies: PREVIEW_CURRENCIES,
      field_errors: [
        { row_id: 0, field: 'rate', message: SERVER_MONEY_MESSAGES.rateMissing },
        { row_id: 1, field: 'rate', message: SERVER_MONEY_MESSAGES.rateMissing },
        {
          row_id: 2,
          field: 'amount',
          message: SERVER_MONEY_MESSAGES.amountDisagrees,
        },
      ],
    });
    for (const row of preview.rows) {
      row.original_amount = 1500000;
      row.original_currency = 'LBP';
    }
    preview.rows[2].rate = 89000;
    // Matrix #10 — a currency and a rate with NOTHING to apply them to —
    // is flagged on `rate` like the rows above, and a rate cannot fix it.
    preview.rows.push({
      row_id: 3,
      skip: false,
      content_hash: 'h3',
      date: '2025-01-10',
      description: 'Blank row',
      amount: 0,
      category: 'Food',
      original_currency: 'LBP',
      rate: 89000,
    });
    preview.field_errors = [
      ...(preview.field_errors ?? []),
      { row_id: 3, field: 'rate', message: SERVER_MONEY_MESSAGES.rateMissing },
    ];
    render(
      <ImportPreviewTable
        preview={preview}
        {...noopProps}
        onApplyRate={onApplyRate}
        canImport={false}
      />,
    );

    // The rate on the button is the rate the PATCH carries — the user
    // accepts a number they can read, and that number becomes the row's
    // booked_rate.
    await user.click(
      screen.getByRole('button', {
        name: "Apply today's 89,000 LBP to 2 rows",
      }),
    );
    // The count on the button is the count of rows it can actually fix.


    expect(onApplyRate).toHaveBeenCalledTimes(1);
    // Row 2's problem is a disagreeing amount, not a missing rate:
    // overwriting the rate it already quoted would re-price money the
    // sheet had already decided. Row 3 has no original amount, so the
    // rate has nothing to convert — including it would promise a fix
    // that lands as a successful PATCH against a row that stays blocked.
    expect(onApplyRate).toHaveBeenCalledWith([0, 1], 89000);
  });

  it('names the currency when more than one is waiting on a rate', async () => {
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    const onApplyRate = vi.fn().mockResolvedValue(undefined);
    const preview = makePreview({
      currencies: [
        ...PREVIEW_CURRENCIES,
        { code: 'EUR', rate_to_base: 0.92, is_base: false },
      ],
      field_errors: [
        { row_id: 0, field: 'rate', message: SERVER_MONEY_MESSAGES.rateMissing },
        { row_id: 1, field: 'rate', message: SERVER_MONEY_MESSAGES.rateMissing },
      ],
    });
    preview.rows[0].original_amount = 1500000;
    preview.rows[0].original_currency = 'LBP';
    preview.rows[1].original_amount = 20;
    preview.rows[1].original_currency = 'EUR';
    render(
      <ImportPreviewTable
        preview={preview}
        {...noopProps}
        onApplyRate={onApplyRate}
        canImport={false}
      />,
    );

    // One button per currency, each naming which rows it means — a single
    // "apply today's rate" would have to pick one of two numbers.
    await user.click(
      screen.getByRole('button', { name: "Apply today's 0.92 EUR to 1 row" }),
    );

    expect(onApplyRate).toHaveBeenCalledWith([1], 0.92);
    expect(
      screen.getByRole('button', { name: "Apply today's 89,000 LBP to 1 row" }),
    ).toBeInTheDocument();
  });

  it('offers no rate at all for a currency the household does not have', () => {
    const preview = makePreview({
      currencies: PREVIEW_CURRENCIES,
      field_errors: [
        {
          row_id: 0,
          field: 'original_currency',
          message: SERVER_MONEY_MESSAGES.unknownCurrency,
        },
      ],
    });
    preview.rows[0].original_amount = 1500000;
    preview.rows[0].original_currency = 'LBX';
    render(
      <ImportPreviewTable preview={preview} {...noopProps} canImport={false} />,
    );

    // There is no "today's rate" for a currency that does not exist, and
    // inventing one from the base row would record a rate of 1.
    expect(screen.queryByRole('button', { name: /Apply today's/ })).toBeNull();
  });

  it('accepts the computed amount for the rows whose sheet disagreed', async () => {
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    const onPatchRow = vi.fn().mockResolvedValue(undefined);
    const preview = makePreview({
      currencies: PREVIEW_CURRENCIES,
      field_errors: [
        {
          row_id: 0,
          field: 'amount',
          message: SERVER_MONEY_MESSAGES.amountDisagrees,
        },
      ],
    });
    preview.rows[0].amount = 16;
    preview.rows[0].original_amount = 1500000;
    preview.rows[0].original_currency = 'LBP';
    preview.rows[0].rate = 89000;
    render(
      <ImportPreviewTable
        preview={preview}
        {...noopProps}
        onPatchRow={onPatchRow}
        canImport={false}
      />,
    );

    await user.click(
      screen.getByRole('button', {
        name: 'Use the computed 16.85 for this row',
      }),
    );

    // 1,500,000 ÷ 89,000 = 16.853932..., which is 16.85 to the cent —
    // rounded the way the wire edge rounds, not the way JS rounds, so the
    // value the server stores is the value the button promised.
    expect(onPatchRow).toHaveBeenCalledTimes(1);
    expect(onPatchRow).toHaveBeenCalledWith(0, 'amount', '16.85');
  });

  it('always offers a skip, even when nothing else on the bar can be automated', async () => {
    // A bar of unknown-currency rows has no rate to apply and no amount
    // to accept — every remedy is somewhere else. The status line still
    // says "Fix or SKIP", so the bar has to carry the skip half or it is
    // a heading with no way out.
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    const onPatchRow = vi.fn().mockResolvedValue(undefined);
    const preview = makePreview({
      currencies: PREVIEW_CURRENCIES,
      field_errors: [
        {
          row_id: 0,
          field: 'original_currency',
          message: SERVER_MONEY_MESSAGES.unknownCurrency,
        },
        {
          row_id: 1,
          field: 'original_currency',
          message: SERVER_MONEY_MESSAGES.unknownCurrency,
        },
      ],
    });
    for (const row of preview.rows.slice(0, 2)) {
      row.original_amount = 1500000;
      row.original_currency = 'LBX';
    }
    render(
      <ImportPreviewTable
        preview={preview}
        {...noopProps}
        onPatchRow={onPatchRow}
        canImport={false}
      />,
    );

    const bar = document.querySelector('[data-money-error-bar="true"]')!;
    expect(within(bar as HTMLElement).queryByRole('button', {
      name: /Apply today's/,
    })).toBeNull();

    await user.click(
      within(bar as HTMLElement).getByRole('button', {
        name: 'Skip these 2 rows',
      }),
    );

    expect(onPatchRow).toHaveBeenCalledTimes(2);
    expect(onPatchRow).toHaveBeenNthCalledWith(1, 0, 'skip', true);
    expect(onPatchRow).toHaveBeenNthCalledWith(2, 1, 'skip', true);
  });

  it('describes each bar’s skip button by its own heading', () => {
    // Both bars can be on screen at once offering "Skip these 2 rows".
    // The visible labels stay short because they read in context; a
    // screen reader gets that context from the description rather than
    // from the column of amber.
    render(
      <ImportPreviewTable
        preview={makePreview({
          currencies: PREVIEW_CURRENCIES,
          field_errors: [
            fieldError(0, 'notes'),
            {
              row_id: 1,
              field: 'rate',
              message: SERVER_MONEY_MESSAGES.rateMissing,
            },
          ],
        })}
        {...noopProps}
        canImport={false}
      />,
    );

    const lengthBar = document.querySelector(
      '[data-field-error-bar="true"]',
    ) as HTMLElement;
    const moneyBar = document.querySelector(
      '[data-money-error-bar="true"]',
    ) as HTMLElement;
    const lengthSkip = within(lengthBar).getByRole('button', {
      name: 'Skip this row',
    });
    const moneySkip = within(moneyBar).getByRole('button', {
      name: 'Skip this row',
    });

    expect(
      document.getElementById(lengthSkip.getAttribute('aria-describedby')!)
        ?.textContent,
    ).toMatch(/too long to import/);
    expect(
      document.getElementById(moneySkip.getAttribute('aria-describedby')!)
        ?.textContent,
    ).toMatch(/money problem/);
  });

  it('lands focus on the bar heading when the bar survives the burst', async () => {
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    const onApplyRate = vi.fn().mockResolvedValue(undefined);
    const preview = makePreview({
      currencies: PREVIEW_CURRENCIES,
      field_errors: [
        { row_id: 0, field: 'rate', message: SERVER_MONEY_MESSAGES.rateMissing },
      ],
    });
    preview.rows[0].original_amount = 1500000;
    preview.rows[0].original_currency = 'LBP';
    render(
      <ImportPreviewTable
        preview={preview}
        {...noopProps}
        onApplyRate={onApplyRate}
        canImport={false}
      />,
    );

    await user.click(
      screen.getByRole('button', { name: "Apply today's 89,000 LBP to 1 row" }),
    );

    // The button that was clicked can be gone by the time the response
    // lands; focus must not fall to document.body, and must not land on
    // the aria-live status line either.
    await waitFor(() => {
      expect(
        document.querySelector('[data-bulk-heading="money"]'),
      ).toHaveFocus();
    });
  });

  it('lands focus on Import when the burst clears the bar away', async () => {
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    // The PATCH is held open so the preview can be replaced BEFORE the
    // burst settles — which is the real order of events: the bar
    // disappears BECAUSE the response landed, so by the time focus is
    // placed there is nothing left of the bar to place it on.
    let settlePatch!: () => void;
    const onPatchRow = vi.fn().mockReturnValue(
      new Promise<void>((resolve) => {
        settlePatch = () => resolve();
      }),
    );
    const { rerender } = render(
      <ImportPreviewTable
        preview={makePreview({ field_errors: [fieldError(0, 'notes')] })}
        {...noopProps}
        onPatchRow={onPatchRow}
        canImport={false}
      />,
    );

    await user.click(screen.getByRole('button', { name: 'Skip this row' }));
    rerender(
      <ImportPreviewTable
        preview={makePreview()}
        {...noopProps}
        onPatchRow={onPatchRow}
      />,
    );
    await act(async () => {
      settlePatch();
    });

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /^Import 3$/ })).toHaveFocus();
    });
  });

  it('keeps the Settings link out of the row description it sits beside', () => {
    // Every editable cell in the row points at the detail id, so anything
    // inside it is announced once per cell — four times over for one
    // link.
    render(
      <ImportPreviewTable
        preview={makePreview({
          currencies: PREVIEW_CURRENCIES,
          field_errors: [
            {
              row_id: 0,
              field: 'original_currency',
              message: SERVER_MONEY_MESSAGES.unknownCurrency,
            },
          ],
        })}
        {...noopProps}
        canImport={false}
      />,
    );

    const description = document.getElementById('import-field-error-0')!;
    expect(description.textContent).toBe(SERVER_MONEY_MESSAGES.unknownCurrency);
    expect(within(description).queryByRole('link')).toBeNull();
    // Still present, still one Tab away — just not part of the sentence.
    const detailRow = description.closest('tr')!;
    expect(within(detailRow).getByRole('link')).toHaveAttribute(
      'href',
      '/settings?tab=currencies',
    );
  });

  it('keeps going when one row of a computed-amount burst is refused', async () => {
    // The out-of-range case (matrix #12) comes back 400, and `onPatchRow`
    // re-throws after recording it. An unguarded `await` in the loop would
    // drop every LATER row silently — the user would see one message and
    // have no way to know which rows the click reached — and leak the
    // rejection out of a `void`-ed promise on the way.
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    const onPatchRow = vi
      .fn()
      .mockRejectedValueOnce(new Error('Amount is out of range.'))
      .mockResolvedValue(undefined);
    const preview = makePreview({
      currencies: PREVIEW_CURRENCIES,
      field_errors: [
        {
          row_id: 0,
          field: 'amount',
          message: SERVER_MONEY_MESSAGES.amountDisagrees,
        },
        {
          row_id: 1,
          field: 'amount',
          message: SERVER_MONEY_MESSAGES.amountDisagrees,
        },
      ],
    });
    for (const row of preview.rows) {
      row.original_amount = 1500000;
      row.original_currency = 'LBP';
      row.rate = 89000;
    }
    render(
      <ImportPreviewTable
        preview={preview}
        {...noopProps}
        onPatchRow={onPatchRow}
        canImport={false}
      />,
    );

    await user.click(
      screen.getByRole('button', {
        name: 'Use the computed amounts for 2 rows',
      }),
    );

    expect(onPatchRow).toHaveBeenCalledTimes(2);
    expect(onPatchRow).toHaveBeenNthCalledWith(1, 0, 'amount', '16.85');
    expect(onPatchRow).toHaveBeenNthCalledWith(2, 1, 'amount', '16.85');
  });

  it('keeps going when one row of a skip burst is refused', async () => {
    // Same policy on the other burst: a session that expires mid-skip
    // must not leave the rows after the first failure untouched and
    // unexplained.
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    const onPatchRow = vi
      .fn()
      .mockRejectedValueOnce(new Error('Import session not found'))
      .mockResolvedValue(undefined);
    render(
      <ImportPreviewTable
        preview={makePreview({
          field_errors: [fieldError(0, 'description'), fieldError(2, 'notes')],
        })}
        {...noopProps}
        onPatchRow={onPatchRow}
        canImport={false}
      />,
    );

    await user.click(screen.getByRole('button', { name: 'Skip these 2 rows' }));

    expect(onPatchRow).toHaveBeenCalledTimes(2);
    expect(onPatchRow).toHaveBeenNthCalledWith(1, 0, 'skip', true);
    expect(onPatchRow).toHaveBeenNthCalledWith(2, 2, 'skip', true);
  });

  it('leaves a preview with no money problems exactly as it was', async () => {
    // THE POSITIVE CONTROL. The snapshot file was written by the
    // component BEFORE this feature existed; the only thing removed from
    // the current render is the new Rate column, so anything else that
    // moved — a class, an attribute, a cell, the status line — fails
    // here rather than being noticed in a browser later.
    const { container } = render(
      <ImportPreviewTable
        preview={{
          import_id: 'preview-abc',
          row_count: 1,
          columns: ['Date', 'Description', 'Amount', 'Category'],
          unique_categories: ['Food'],
          collision_groups: [],
          expires_at: '2099-01-01T00:00:00Z',
          rows: [
            {
              row_id: 0,
              skip: false,
              content_hash: 'h0',
              date: '2025-01-07',
              description: 'Starbucks',
              amount: 5,
              category: 'Food',
            },
          ],
        }}
        {...noopProps}
      />,
    );

    const stripped = container.cloneNode(true) as HTMLElement;
    stripped
      .querySelectorAll('[data-import-col="rate"]')
      .forEach((el) => el.remove());

    await expect(stripped.innerHTML).toMatchFileSnapshot(
      './__snapshots__/import-preview-clean.html',
    );
  });
});
