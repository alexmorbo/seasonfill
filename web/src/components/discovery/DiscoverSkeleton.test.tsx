import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { DiscoverSkeleton } from './DiscoverSkeleton';

describe('<DiscoverSkeleton />', () => {
  it('renders 20 placeholder cards by default', () => {
    render(<DiscoverSkeleton />);
    expect(
      screen.getAllByTestId('discovery-warming-skeleton-card'),
    ).toHaveLength(20);
  });

  it('honors a custom testId and count', () => {
    render(<DiscoverSkeleton testId="x-warming" count={5} />);
    expect(screen.getByTestId('x-warming')).toBeInTheDocument();
    expect(
      screen.getAllByTestId('discovery-warming-skeleton-card'),
    ).toHaveLength(5);
  });
});
