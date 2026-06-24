import type React from 'react';
import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import { TooltipProvider } from '@/components/ui/tooltip';
import i18n from '@/i18n';
import { DegradedChip } from './DegradedChip';

function wrap(node: React.ReactNode) {
  return render(
    <I18nextProvider i18n={i18n}>
      <TooltipProvider delayDuration={0}>{node}</TooltipProvider>
    </I18nextProvider>,
  );
}

describe('DegradedChip', () => {
  it('renders nothing when sources are empty', () => {
    const { container } = wrap(<DegradedChip sources={[]} />);
    expect(container.firstChild).toBeNull();
  });

  it('shows count + data-sources when sources are non-empty', () => {
    wrap(<DegradedChip sources={['tmdb_series', 'omdb']} />);
    const chip = screen.getByTestId('series-degraded-chip');
    expect(chip.getAttribute('data-sources')).toBe('tmdb_series,omdb');
    expect(chip.textContent).toMatch(/2/);
  });

  it('renders a single source without comma separator', () => {
    wrap(<DegradedChip sources={['tmdb_series']} />);
    const chip = screen.getByTestId('series-degraded-chip');
    expect(chip.getAttribute('data-sources')).toBe('tmdb_series');
    expect(chip.textContent).toMatch(/1/);
  });
});
