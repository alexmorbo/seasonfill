import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { PopularityMeter } from '../PopularityMeter';

describe('<PopularityMeter />', () => {
  it('renders a number with two decimals', () => {
    render(<PopularityMeter value={1.234} />);
    expect(screen.getByTestId('popularity-meter').textContent).toBe('1.23');
  });

  it('shows em-dash for zero (older qBit)', () => {
    render(<PopularityMeter value={0} />);
    expect(screen.getByTestId('popularity-meter').textContent).toBe('—');
  });

  it('shows em-dash for null', () => {
    render(<PopularityMeter />);
    expect(screen.getByTestId('popularity-meter').textContent).toBe('—');
  });
});
