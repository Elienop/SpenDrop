import { vi, describe, test, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Tabs } from './Tabs';

const tabs = [
  { key: 'general', label: 'General' },
  { key: 'advanced', label: 'Advanced' },
  { key: 'data', label: 'Data' },
];

describe('Tabs', () => {
  test('renders all tab labels', () => {
    render(<Tabs tabs={tabs} activeKey="general" onTabChange={() => {}} />);
    expect(screen.getByText('General')).toBeInTheDocument();
    expect(screen.getByText('Advanced')).toBeInTheDocument();
    expect(screen.getByText('Data')).toBeInTheDocument();
  });

  test('marks active tab with aria-selected', () => {
    render(<Tabs tabs={tabs} activeKey="advanced" onTabChange={() => {}} />);
    expect(screen.getByRole('tab', { name: 'Advanced' })).toHaveAttribute('aria-selected', 'true');
    expect(screen.getByRole('tab', { name: 'General' })).toHaveAttribute('aria-selected', 'false');
  });

  test('calls onTabChange when a tab is clicked', async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(<Tabs tabs={tabs} activeKey="general" onTabChange={onChange} />);
    await user.click(screen.getByText('Data'));
    expect(onChange).toHaveBeenCalledWith('data');
  });
});
