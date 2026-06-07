import { describe, it, expect } from 'vitest';
import { screen } from '@testing-library/react';
import { renderWithProviders } from '@/test-utils';
import { QueueSweepCTA } from './QueueSweepCTA';

describe('<QueueSweepCTA />', () => {
  it('renders the heading + button when backlog > 0', () => {
    renderWithProviders(<QueueSweepCTA backlogCount={10} />);
    expect(screen.getByText(/sweep every series/i)).toBeInTheDocument();
    expect(screen.getByRole('link', { name: /scan whole instance/i }))
      .toHaveAttribute('href', '/scans?new=1');
  });

  it('renders nothing when backlog is 0', () => {
    renderWithProviders(<QueueSweepCTA backlogCount={0} />);
    expect(screen.queryByTestId('queue-sweep-cta')).not.toBeInTheDocument();
  });
});
