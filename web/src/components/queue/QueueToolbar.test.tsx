import { describe, it, expect, vi } from 'vitest';
import { screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders } from '@/test-utils';
import { QueueToolbar } from './QueueToolbar';

describe('<QueueToolbar />', () => {
  it('renders the search input with the provided value', () => {
    renderWithProviders(
      <QueueToolbar q="severance" sort="debt" onQChange={vi.fn()} onSortChange={vi.fn()} />,
    );
    expect(screen.getByDisplayValue('severance')).toBeInTheDocument();
  });

  it('calls onQChange on input', async () => {
    const onQChange = vi.fn();
    renderWithProviders(
      <QueueToolbar q="" sort="debt" onQChange={onQChange} onSortChange={vi.fn()} />,
    );
    await userEvent.type(screen.getByPlaceholderText(/search by series/i), 'a');
    expect(onQChange).toHaveBeenLastCalledWith('a');
  });

  it('opens the sort menu and triggers onSortChange', async () => {
    const onSortChange = vi.fn();
    renderWithProviders(
      <QueueToolbar q="" sort="debt" onQChange={vi.fn()} onSortChange={onSortChange} />,
    );
    await userEvent.click(screen.getByRole('combobox'));
    await userEvent.click(await screen.findByText(/sort: a→z/i));
    expect(onSortChange).toHaveBeenCalledWith('title');
  });
});
