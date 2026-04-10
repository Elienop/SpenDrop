import { describe, test, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { ButtonGroup, ButtonGroupText } from './button-group';

describe('ButtonGroup', () => {
  test('renders a group role container', () => {
    render(<ButtonGroup data-testid="bg">content</ButtonGroup>);
    expect(screen.getByRole('group')).toBeInTheDocument();
  });

  test('sets data-slot attribute', () => {
    render(<ButtonGroup data-testid="bg">content</ButtonGroup>);
    expect(screen.getByTestId('bg')).toHaveAttribute(
      'data-slot',
      'button-group',
    );
  });

  test('defaults to horizontal orientation', () => {
    render(<ButtonGroup data-testid="bg">content</ButtonGroup>);
    expect(screen.getByTestId('bg')).toHaveAttribute(
      'data-orientation',
      'horizontal',
    );
  });

  test('accepts vertical orientation', () => {
    render(<ButtonGroup data-testid="bg" orientation="vertical">content</ButtonGroup>);
    expect(screen.getByTestId('bg')).toHaveAttribute(
      'data-orientation',
      'vertical',
    );
  });

  test('applies vertical flex-col class when vertical', () => {
    render(<ButtonGroup data-testid="bg" orientation="vertical">content</ButtonGroup>);
    expect(screen.getByTestId('bg').className).toContain('flex-col');
  });

  test('does not apply flex-col when horizontal', () => {
    render(<ButtonGroup data-testid="bg">content</ButtonGroup>);
    expect(screen.getByTestId('bg').className).not.toContain('flex-col');
  });

  test('merges custom className', () => {
    render(<ButtonGroup data-testid="bg" className="my-class">content</ButtonGroup>);
    expect(screen.getByTestId('bg').className).toContain('my-class');
  });

  test('passes through extra div props', () => {
    render(<ButtonGroup data-testid="bg" id="grp">content</ButtonGroup>);
    expect(screen.getByTestId('bg')).toHaveAttribute('id', 'grp');
  });

  test('renders children', () => {
    render(
      <ButtonGroup>
        <button>A</button>
        <button>B</button>
      </ButtonGroup>,
    );
    expect(screen.getByText('A')).toBeInTheDocument();
    expect(screen.getByText('B')).toBeInTheDocument();
  });
});

describe('ButtonGroupText', () => {
  test('renders a div by default', () => {
    render(<ButtonGroupText data-testid="bgt">Label</ButtonGroupText>);
    const el = screen.getByTestId('bgt');
    expect(el.tagName).toBe('DIV');
    expect(el).toHaveTextContent('Label');
  });

  test('renders as child element when asChild is true', () => {
    render(
      <ButtonGroupText asChild data-testid="bgt">
        <span>Label</span>
      </ButtonGroupText>,
    );
    const el = screen.getByTestId('bgt');
    expect(el.tagName).toBe('SPAN');
  });

  test('merges custom className', () => {
    render(<ButtonGroupText data-testid="bgt" className="extra">Label</ButtonGroupText>);
    expect(screen.getByTestId('bgt').className).toContain('extra');
  });
});
