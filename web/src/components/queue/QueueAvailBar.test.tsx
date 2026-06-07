import { describe, it, expect } from 'vitest';
import { screen } from '@testing-library/react';
import { renderWithProviders } from '@/test-utils';
import { QueueAvailBar } from './QueueAvailBar';

describe('<QueueAvailBar />', () => {
  it('renders both segments when have>0 and miss>0', () => {
    renderWithProviders(<QueueAvailBar have={11} miss={13} />);
    const have = screen.getByTestId('queue-avail-have') as HTMLElement;
    const miss = screen.getByTestId('queue-avail-miss') as HTMLElement;
    expect(have.style.width).toBe('46%');
    expect(miss.style.width).toBe('54%');
  });

  it('renders only the miss segment when have=0', () => {
    renderWithProviders(<QueueAvailBar have={0} miss={10} />);
    expect(screen.queryByTestId('queue-avail-have')).not.toBeInTheDocument();
    expect((screen.getByTestId('queue-avail-miss') as HTMLElement).style.width).toBe('100%');
  });

  it('renders no segments when both are 0', () => {
    renderWithProviders(<QueueAvailBar have={0} miss={0} />);
    expect(screen.queryByTestId('queue-avail-have')).not.toBeInTheDocument();
    expect(screen.queryByTestId('queue-avail-miss')).not.toBeInTheDocument();
  });

  it('exposes have/miss via aria-label', () => {
    renderWithProviders(<QueueAvailBar have={11} miss={13} />);
    expect(screen.getByRole('progressbar')).toHaveAttribute(
      'aria-label',
      'have 11, missing 13',
    );
  });
});
