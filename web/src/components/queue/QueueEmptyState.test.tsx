import { describe, it, expect } from 'vitest';
import { screen } from '@testing-library/react';
import { renderWithProviders } from '@/test-utils';
import { QueueEmptyState } from './QueueEmptyState';

describe('<QueueEmptyState />', () => {
  it('renders the success title and CTA links', () => {
    renderWithProviders(<QueueEmptyState />);
    expect(screen.getByText(/no backlog/i)).toBeInTheDocument();
    expect(screen.getByRole('link', { name: /scan history/i })).toHaveAttribute('href', '/scans');
    expect(screen.getByRole('link', { name: /back to instances/i })).toHaveAttribute('href', '/instances');
  });
});
