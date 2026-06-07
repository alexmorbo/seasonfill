import { describe, expect, it } from 'vitest';
import { screen } from '@testing-library/react';
import { renderWithProviders } from '@/test-utils';
import { InstanceStatsBlock } from './InstanceStatsBlock';

describe('<InstanceStatsBlock />', () => {
  it('colors fails red when > 0', () => {
    renderWithProviders(
      <InstanceStatsBlock
        grabs={47} imports={39} fails={2}
        windowLabelKey="instances.hero.stats.7d.label"
      />,
    );
    const fails = screen.getByTestId('stats-fails');
    expect(fails).toHaveTextContent('2');
    expect(fails.className).toMatch(/text-status-danger/);
  });

  it('colors fails green when zero', () => {
    renderWithProviders(
      <InstanceStatsBlock
        grabs={12} imports={8} fails={0}
        windowLabelKey="instances.hero.stats.24h.label"
      />,
    );
    expect(screen.getByTestId('stats-fails').className).toMatch(/text-status-ok/);
  });
});
