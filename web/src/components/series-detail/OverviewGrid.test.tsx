import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { OverviewGrid } from './OverviewGrid';

describe('OverviewGrid', () => {
  it('renders left + right slots', () => {
    render(<OverviewGrid
      left={<div data-testid="left-slot">L</div>}
      right={<div data-testid="right-slot">R</div>}
    />);
    expect(screen.getByTestId('left-slot')).toBeInTheDocument();
    expect(screen.getByTestId('right-slot')).toBeInTheDocument();
    expect(screen.getByTestId('overview-grid').className).toMatch(/lg:grid-cols-\[minmax/);
  });
});
