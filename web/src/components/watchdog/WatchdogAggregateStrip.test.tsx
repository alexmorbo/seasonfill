import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';
import { WatchdogAggregateStrip } from './WatchdogAggregateStrip';
import type { WatchdogRollupAggregate } from '@/lib/api/watchdogRollups';

function wrap(ui: React.ReactNode) {
  return <I18nextProvider i18n={i18n}>{ui}</I18nextProvider>;
}

const baseRollup = {
  enabled: true, active: true, watched: 12, unregistered: 2,
  regrabs_24h: 1, regrabs_7d: 5, blacklist_size: 3,
  qbit_reachable: true, poll_interval_seconds: 1800,
  cooldown_hours: 120, no_better_max: 3,
} as const;

const fixture: WatchdogRollupAggregate = {
  items: [
    { instance_name: 'homelab', ...baseRollup,
      last_poll_at: new Date(Date.now() - 2 * 60_000).toISOString(),
      last_poll_result: 'ok' },
    { instance_name: '4k', ...baseRollup, enabled: false, active: false,
      watched: 0, unregistered: 0, regrabs_24h: 0, regrabs_7d: 0,
      blacklist_size: 0, qbit_reachable: false },
  ],
};

describe('<WatchdogAggregateStrip />', () => {
  it('derives active/total from items locally; never renders undefined', () => {
    render(wrap(<WatchdogAggregateStrip rollups={fixture} />));
    const active = screen.getByTestId('watchdog-strip-active');
    expect(active).toHaveTextContent('1 / 2');
    expect(active.textContent).not.toMatch(/undefined/);
  });

  it('handles an empty items array without rendering "undefined"', () => {
    render(wrap(<WatchdogAggregateStrip rollups={{ items: [] }} />));
    const active = screen.getByTestId('watchdog-strip-active');
    expect(active).toHaveTextContent('0 / 0');
    expect(active.textContent).not.toMatch(/undefined/);
  });

  it('aggregates watched/unregistered/regrabs/blacklist tiles', () => {
    render(wrap(<WatchdogAggregateStrip rollups={fixture} />));
    expect(screen.getByText('12')).toBeInTheDocument();
    expect(screen.getByText('5')).toBeInTheDocument();
  });

  it('renders skeleton when isLoading', () => {
    render(wrap(<WatchdogAggregateStrip isLoading />));
    expect(screen.getByTestId('watchdog-strip-loading')).toBeInTheDocument();
  });
});
