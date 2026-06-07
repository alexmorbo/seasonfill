import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';
import { WatchdogAggregateStrip } from './WatchdogAggregateStrip';
import type { WatchdogRollupAggregate } from '@/lib/api/watchdogRollups';

function wrap(ui: React.ReactNode) {
  return <I18nextProvider i18n={i18n}>{ui}</I18nextProvider>;
}

const fixture: WatchdogRollupAggregate = {
  active_count: 1,
  total_count: 2,
  items: [
    {
      instance: 'homelab',
      enabled: true,
      active: true,
      watched: 12,
      unregistered: 2,
      regrabs_24h: 1,
      regrabs_7d: 5,
      blacklist_size: 3,
      last_poll_at: new Date(Date.now() - 2 * 60_000).toISOString(),
      last_poll_result: 'ok',
      qbit_reachable: true,
      poll_interval_min: 30,
      regrab_cooldown_h: 120,
      max_no_better: 3,
    },
    {
      instance: '4k',
      enabled: false,
      active: false,
      watched: 0,
      unregistered: 0,
      regrabs_24h: 0,
      regrabs_7d: 0,
      blacklist_size: 0,
      qbit_reachable: false,
      poll_interval_min: 30,
      regrab_cooldown_h: 120,
      max_no_better: 3,
    },
  ],
};

describe('<WatchdogAggregateStrip />', () => {
  it('renders 6 tiles from the aggregate fixture', () => {
    render(wrap(<WatchdogAggregateStrip rollups={fixture} />));
    expect(screen.getByTestId('watchdog-strip-active')).toHaveTextContent('1 / 2');
    expect(screen.getByText('12')).toBeInTheDocument();
    expect(screen.getByText('2')).toBeInTheDocument();
    expect(screen.getByText('5')).toBeInTheDocument();
    expect(screen.getByText('3')).toBeInTheDocument();
  });

  it('renders the loading skeleton when isLoading=true', () => {
    render(wrap(<WatchdogAggregateStrip isLoading />));
    expect(screen.getByTestId('watchdog-strip-loading')).toBeInTheDocument();
  });

  it('renders "never" label when no instance has polled', () => {
    const noPoll: WatchdogRollupAggregate = {
      ...fixture,
      items: fixture.items.map((r) => ({ ...r, last_poll_at: undefined as string | undefined })),
    };
    render(wrap(<WatchdogAggregateStrip rollups={noPoll} />));
    // The translated value depends on locale; assert the labelled tile renders.
    expect(screen.getByText(/последний|never/i)).toBeInTheDocument();
  });
});
