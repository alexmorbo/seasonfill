import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { SpeedCell } from '../SpeedCell';

describe('<SpeedCell />', () => {
  it('renders human-formatted MB/s', () => {
    render(<SpeedCell down={2_200_000} up={800_000} />);
    expect(screen.getByTestId('speed-cell-down').textContent).toMatch(/MB\/s/);
    expect(screen.getByTestId('speed-cell-up').textContent).toMatch(/KB\/s|MB\/s/);
  });

  it('falls back to em-dash when both speeds are zero', () => {
    render(<SpeedCell down={0} up={0} />);
    expect(screen.getByTestId('speed-cell-down').textContent).toBe('—');
    expect(screen.getByTestId('speed-cell-up').textContent).toBe('—');
  });

  it('shows em-dash on both speeds when muted', () => {
    render(<SpeedCell down={2_200_000} up={800_000} muted />);
    expect(screen.getByTestId('speed-cell-down').textContent).toBe('—');
    expect(screen.getByTestId('speed-cell-up').textContent).toBe('—');
  });
});
