import { describe, test, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { TagInput } from './TagInput';

describe('TagInput', () => {
  test('renders empty input with placeholder when no tags', () => {
    render(<TagInput value="" onChange={() => {}} />);
    expect(screen.getByPlaceholderText('Add tag...')).toBeInTheDocument();
  });

  test('renders custom placeholder', () => {
    render(<TagInput value="" onChange={() => {}} placeholder="Type here..." />);
    expect(screen.getByPlaceholderText('Type here...')).toBeInTheDocument();
  });

  test('displays existing tags as pills', () => {
    render(<TagInput value="food,rent,travel" onChange={() => {}} />);
    expect(screen.getByText('food')).toBeInTheDocument();
    expect(screen.getByText('rent')).toBeInTheDocument();
    expect(screen.getByText('travel')).toBeInTheDocument();
  });

  test('hides placeholder when tags exist', () => {
    render(<TagInput value="food" onChange={() => {}} />);
    const input = screen.getByRole('textbox');
    expect(input).not.toHaveAttribute('placeholder', 'Add tag...');
  });

  test('adds tag on Enter key', async () => {
    const onChange = vi.fn();
    const user = userEvent.setup();
    render(<TagInput value="" onChange={onChange} />);

    const input = screen.getByRole('textbox');
    await user.type(input, 'groceries{Enter}');

    expect(onChange).toHaveBeenCalledWith('groceries');
  });

  test('adds tag on comma key', async () => {
    const onChange = vi.fn();
    const user = userEvent.setup();
    render(<TagInput value="" onChange={onChange} />);

    const input = screen.getByRole('textbox');
    await user.type(input, 'groceries,');

    expect(onChange).toHaveBeenCalledWith('groceries');
  });

  test('appends tag to existing tags', async () => {
    const onChange = vi.fn();
    const user = userEvent.setup();
    render(<TagInput value="food,rent" onChange={onChange} />);

    const input = screen.getByRole('textbox');
    await user.type(input, 'travel{Enter}');

    expect(onChange).toHaveBeenCalledWith('food,rent,travel');
  });

  test('does not add duplicate tags', async () => {
    const onChange = vi.fn();
    const user = userEvent.setup();
    render(<TagInput value="food,rent" onChange={onChange} />);

    const input = screen.getByRole('textbox');
    await user.type(input, 'food{Enter}');

    expect(onChange).not.toHaveBeenCalled();
  });

  test('does not add empty/whitespace tags', async () => {
    const onChange = vi.fn();
    const user = userEvent.setup();
    render(<TagInput value="" onChange={onChange} />);

    const input = screen.getByRole('textbox');
    await user.type(input, '   {Enter}');

    expect(onChange).not.toHaveBeenCalled();
  });

  test('removes tag when remove button clicked', async () => {
    const onChange = vi.fn();
    const user = userEvent.setup();
    render(<TagInput value="food,rent,travel" onChange={onChange} />);

    const removeButton = screen.getByRole('button', { name: /remove tag rent/i });
    await user.click(removeButton);

    expect(onChange).toHaveBeenCalledWith('food,travel');
  });

  test('removes last tag on Backspace when input is empty', async () => {
    const onChange = vi.fn();
    const user = userEvent.setup();
    render(<TagInput value="food,rent" onChange={onChange} />);

    const input = screen.getByRole('textbox');
    await user.click(input);
    await user.keyboard('{Backspace}');

    expect(onChange).toHaveBeenCalledWith('food');
  });

  test('adds tag on blur', async () => {
    const onChange = vi.fn();
    const user = userEvent.setup();
    render(
      <div>
        <TagInput value="" onChange={onChange} />
        <button>other</button>
      </div>
    );

    const input = screen.getByRole('textbox');
    await user.type(input, 'groceries');
    await user.click(screen.getByRole('button', { name: 'other' }));

    expect(onChange).toHaveBeenCalledWith('groceries');
  });

  test('clears input after adding a tag', async () => {
    const user = userEvent.setup();
    render(<TagInput value="" onChange={() => {}} />);

    const input = screen.getByRole('textbox') as HTMLInputElement;
    await user.type(input, 'groceries{Enter}');

    expect(input.value).toBe('');
  });

  test('trims whitespace from tags', async () => {
    const onChange = vi.fn();
    const user = userEvent.setup();
    render(<TagInput value="" onChange={onChange} />);

    const input = screen.getByRole('textbox');
    await user.type(input, '  groceries  {Enter}');

    expect(onChange).toHaveBeenCalledWith('groceries');
  });

  test('each tag pill has an accessible remove button', () => {
    render(<TagInput value="food,rent" onChange={() => {}} />);
    expect(screen.getByRole('button', { name: /remove tag food/i })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /remove tag rent/i })).toBeInTheDocument();
  });

  test('handles value with extra whitespace around commas', () => {
    render(<TagInput value=" food , rent , travel " onChange={() => {}} />);
    expect(screen.getByText('food')).toBeInTheDocument();
    expect(screen.getByText('rent')).toBeInTheDocument();
    expect(screen.getByText('travel')).toBeInTheDocument();
  });

  test('applies custom className', () => {
    const { container } = render(<TagInput value="" onChange={() => {}} className="custom-class" />);
    expect(container.firstElementChild).toHaveClass('custom-class');
  });
});
