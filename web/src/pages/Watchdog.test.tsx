import { describe, expect, it, vi, beforeEach } from 'vitest';
import { screen, waitFor } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';
import { renderWithProviders } from '@/test-utils';
import { Watchdog } from '@/pages/Watchdog';
import type { WatchdogRollupAggregate } from '@/lib/api/watchdogRollups';
import { renderPageWithTitle } from '@/test-utils-title';

vi.mock('@/lib/api', async () => {
  const actual = await vi.importActual<typeof import('@/lib/api')>('@/lib/api');
  return { ...actual, api: vi.fn() };
});

import { api } from '@/lib/api';

function wrap(ui: React.ReactNode) {
  return <I18nextProvider i18n={i18n}>{ui}</I18nextProvider>;
}

// Route the mock to a path-based handler so each fixture branch only
// has to set the rollup shape; sub-queries (blacklist + grabs) return
// empty lists by default.
function routeApi(handler: (path: string) => unknown) {
  vi.mocked(api).mockImplementation((path: string) => {
    return Promise.resolve(handler(path) ?? { items: [] });
  });
}

const baseRollup = {
  instance_name: 'homelab',
  enabled: true,
  active: true,
  watched: 12,
  unregistered: 2,
  regrabs_24h: 1,
  regrabs_7d: 5,
  blacklist_size: 1,
  last_poll_at: new Date(Date.now() - 60_000).toISOString(),
  last_poll_result: 'ok' as const,
  qbit_reachable: true,
  poll_interval_seconds: 1800,
  cooldown_hours: 120,
  no_better_max: 3,
};

const activeRollups: WatchdogRollupAggregate = {
  items: [baseRollup],
};

const totalZeroRollups: WatchdogRollupAggregate = {
  items: [],
};

const disabledRollups: WatchdogRollupAggregate = {
  items: [{ ...baseRollup, enabled: false, active: false, blacklist_size: 0 }],
};

// `delta` repro: operator has watchdog ON, qBit configured, but the
// first poll has not stamped a snapshot yet → backend reports
// qbit_reachable=false, active=false. The page MUST still render
// the panel so the operator can see the toggle and the warn chip.
const unreachableRollups: WatchdogRollupAggregate = {
  items: [
    {
      ...baseRollup,
      enabled: true,
      active: false,
      qbit_reachable: false,
      last_poll_at: undefined,
      last_poll_result: undefined,
    },
  ],
};

describe('<Watchdog /> (integration)', () => {
  beforeEach(() => {
    vi.mocked(api).mockReset();
  });

  it('renders the active page (strip + feed + panel + blacklist slot)', async () => {
    routeApi((path) => {
      if (path === '/watchdog/rollups') return activeRollups;
      if (path.includes('/watchdog/blacklist')) return { items: [] };
      if (path.startsWith('/grabs')) return { items: [] };
      return {};
    });
    renderWithProviders(wrap(<Watchdog />), { route: '/watchdog' });

    await waitFor(() =>
      expect(screen.getByTestId('watchdog-page')).toBeInTheDocument(),
    );
    await waitFor(() =>
      expect(screen.getByTestId('watchdog-grid')).toBeInTheDocument(),
    );
    await waitFor(() =>
      expect(
        screen.getByTestId('watchdog-blacklist-slot'),
      ).toBeInTheDocument(),
    );
    expect(
      screen.queryByTestId('watchdog-not-configured'),
    ).not.toBeInTheDocument();
  });

  it('renders the not-configured empty when items.length === 0', async () => {
    routeApi((path) => {
      if (path === '/watchdog/rollups') return totalZeroRollups;
      return {};
    });
    renderWithProviders(wrap(<Watchdog />), { route: '/watchdog' });

    await waitFor(() =>
      expect(
        screen.getByTestId('watchdog-not-configured'),
      ).toBeInTheDocument(),
    );
    expect(screen.queryByTestId('watchdog-grid')).not.toBeInTheDocument();
    expect(
      screen.queryByTestId('watchdog-blacklist-slot'),
    ).not.toBeInTheDocument();
  });

  it('renders the panel (not onboarding) when the only instance is disabled', async () => {
    routeApi((path) => {
      if (path === '/watchdog/rollups') return disabledRollups;
      if (path.includes('/watchdog/blacklist')) return { items: [] };
      if (path.startsWith('/grabs')) return { items: [] };
      return {};
    });
    renderWithProviders(wrap(<Watchdog />), { route: '/watchdog' });

    await waitFor(() =>
      expect(screen.getByTestId('watchdog-grid')).toBeInTheDocument(),
    );
    await waitFor(() => {
      expect(
        screen.getByTestId('watchdog-panel-homelab'),
      ).toBeInTheDocument();
    });
    expect(
      screen.getByTestId('watchdog-panel-toggle-homelab'),
    ).toBeInTheDocument();
    expect(
      screen.queryByTestId('watchdog-not-configured'),
    ).not.toBeInTheDocument();
  });

  it('renders the panel when enabled=true but qbit_reachable=false (no first poll yet)', async () => {
    routeApi((path) => {
      if (path === '/watchdog/rollups') return unreachableRollups;
      if (path.includes('/watchdog/blacklist')) return { items: [] };
      if (path.startsWith('/grabs')) return { items: [] };
      return {};
    });
    renderWithProviders(wrap(<Watchdog />), { route: '/watchdog' });

    await waitFor(() =>
      expect(screen.getByTestId('watchdog-grid')).toBeInTheDocument(),
    );
    await waitFor(() => {
      expect(
        screen.getByTestId('watchdog-panel-homelab'),
      ).toBeInTheDocument();
    });
    expect(
      screen.getByTestId('watchdog-panel-toggle-homelab'),
    ).toBeInTheDocument();
    expect(
      screen.queryByTestId('watchdog-not-configured'),
    ).not.toBeInTheDocument();
  });

  it('sets the topbar page title via useSetPageTitle', async () => {
    routeApi(() => ({ items: [] }));
    const { getTitle } = renderPageWithTitle(wrap(<Watchdog />), { route: '/watchdog' });
    await waitFor(() => {
      expect(getTitle()).toBe(i18n.t('watchdog.title'));
    });
  });
});
