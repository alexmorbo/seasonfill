import { describe, it, expect, vi } from 'vitest';
import { screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders } from '@/test-utils';
import { QueueHeader } from './QueueHeader';

describe('<QueueHeader />', () => {
  const baseProps = {
    name: 'alpha',
    mode: 'auto' as const,
    updatedAtMs: Date.now(),
    isFetching: false,
    onRefresh: vi.fn(),
  };

  it('renders the instance name and back link', () => {
    renderWithProviders(<QueueHeader {...baseProps} />);
    expect(screen.getByRole('heading', { name: 'alpha' })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: /instances/i })).toHaveAttribute('href', '/instances');
  });

  it('renders the mode chip with auto styling', () => {
    renderWithProviders(<QueueHeader {...baseProps} mode="auto" />);
    expect(screen.getByTestId('queue-mode-chip')).toHaveTextContent('auto');
  });

  it('renders the manual mode chip', () => {
    renderWithProviders(<QueueHeader {...baseProps} mode="manual" />);
    expect(screen.getByTestId('queue-mode-chip')).toHaveTextContent('manual');
  });

  it('shows "updated just now" for fresh dataUpdatedAt', () => {
    renderWithProviders(<QueueHeader {...baseProps} updatedAtMs={Date.now()} />);
    expect(screen.getByText(/updated just now/i)).toBeInTheDocument();
  });

  it('fires onRefresh when the refresh button is clicked', async () => {
    const onRefresh = vi.fn();
    renderWithProviders(<QueueHeader {...baseProps} onRefresh={onRefresh} />);
    await userEvent.click(screen.getByRole('button', { name: /refresh queue/i }));
    expect(onRefresh).toHaveBeenCalledTimes(1);
  });

  it('disables the refresh button while fetching', () => {
    renderWithProviders(<QueueHeader {...baseProps} isFetching />);
    expect(screen.getByRole('button', { name: /refresh queue/i })).toBeDisabled();
  });
});
