import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { RatioPill } from '../RatioPill';

describe('<RatioPill />', () => {
  it.each([
    [0.43, 'low'],
    [1.5,  'mid'],
    [2.31, 'high'],
  ] as const)('classifies %s as %s', (v, tier) => {
    render(<RatioPill value={v} />);
    expect(screen.getByTestId('ratio-pill').getAttribute('data-tier')).toBe(tier);
  });

  it('shows em-dash for null', () => {
    render(<RatioPill />);
    expect(screen.getByTestId('ratio-pill').textContent).toBe('—');
  });

  it('renders two decimals', () => {
    render(<RatioPill value={0.4321} />);
    expect(screen.getByTestId('ratio-pill').textContent).toBe('0.43');
  });
});
