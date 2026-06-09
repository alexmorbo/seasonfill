import { describe, expect, it, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter, Routes, Route, useLocation } from 'react-router-dom';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';
import { WatchdogSeasonsTable } from './WatchdogSeasonsTable';
import type { WatchdogSeasonsFilters } from '@/lib/api/watchdogSeasons';

vi.mock('@/lib/api', async () => {
  const actual = await vi.importActual<typeof import('@/lib/api')>('@/lib/api');
  return { ...actual, api: vi.fn() };
});

import { api } from '@/lib/api';

const baseFilters: WatchdogSeasonsFilters = {
  instance: null,
  q: '',
  cooldownOnly: false,
  blacklistedOnly: false,
};

function LocationProbe() {
  const loc = useLocation();
  return (
    <div data-testid="location">{loc.pathname + (loc.search || '')}</div>
  );
}

function wrap(ui: React.ReactNode) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0, staleTime: 0 } },
  });
  return (
    <QueryClientProvider client={qc}>
      <I18nextProvider i18n={i18n}>
        <MemoryRouter initialEntries={['/watchdog']}>
          <Routes>
            <Route
              path="/watchdog"
              element={
                <>
                  <LocationProbe />
                  {ui}
                </>
              }
            />
          </Routes>
        </MemoryRouter>
      </I18nextProvider>
    </QueryClientProvider>
  );
}

const fixtureRows = [
  {
    instance: 'homelab',
    series_id: 169,
    series_title: 'Your Friends & Neighbors',
    season_number: 2,
    monitored: true,
    origin: {
      indexer: 'RuTracker (Prowlarr)',
      first_seen_at: new Date(Date.now() - 86_400_000).toISOString(),
      last_seen_at: new Date(Date.now() - 3_600_000).toISOString(),
    },
    cooldown: {
      expires_at: new Date(Date.now() + 3_600_000).toISOString(),
      reason: 'series_after_grab',
    },
    no_better_counter: { consecutive: 2, max: 3 },
  },
  {
    instance: 'homelab',
    series_id: 22,
    series_title: 'Wednesday',
    season_number: 2,
    monitored: false,
    origin: {
      indexer: 'Kinozal',
      first_seen_at: new Date(Date.now() - 2 * 86_400_000).toISOString(),
      last_seen_at: new Date(Date.now() - 7_200_000).toISOString(),
    },
    blacklist: { expires_at: '', reason: 'manual' },
  },
];

beforeEach(() => {
  vi.mocked(api).mockReset();
});

describe('<WatchdogSeasonsTable />', () => {
  it('renders seeded rows with status and cooldown badges', async () => {
    vi.mocked(api).mockResolvedValueOnce({
      items: fixtureRows,
      next_cursor: '',
    });

    render(wrap(<WatchdogSeasonsTable filters={baseFilters} />));

    expect(await screen.findByText(/Your Friends & Neighbors/)).toBeInTheDocument();
    expect(screen.getByText(/Wednesday/)).toBeInTheDocument();
    // Status badges
    expect(screen.getAllByText(/monitored/i).length).toBeGreaterThan(0);
    expect(screen.getAllByText(/blacklist/i).length).toBeGreaterThan(0);
    expect(screen.getAllByText(/cooldown/i).length).toBeGreaterThan(0);
    // No-better badge present
    expect(screen.getByText('2/3')).toBeInTheDocument();
  });

  it('navigates to drawer URL params when a row is clicked', async () => {
    vi.mocked(api).mockResolvedValueOnce({
      items: fixtureRows,
      next_cursor: '',
    });

    render(wrap(<WatchdogSeasonsTable filters={baseFilters} />));

    const row = await screen.findByTestId(
      'watchdog-seasons-row-homelab-169-2',
    );
    fireEvent.click(row);
    await waitFor(() => {
      expect(screen.getByTestId('location').textContent).toContain(
        'series_id=169',
      );
    });
    expect(screen.getByTestId('location').textContent).toContain(
      'instance=homelab',
    );
  });

  it('renders the empty state when the list comes back empty', async () => {
    vi.mocked(api).mockResolvedValueOnce({ items: [], next_cursor: '' });

    render(wrap(<WatchdogSeasonsTable filters={baseFilters} />));

    expect(
      await screen.findByTestId('watchdog-seasons-empty'),
    ).toBeInTheDocument();
  });

  it('passes filter params to the backend query', async () => {
    vi.mocked(api).mockResolvedValueOnce({ items: [], next_cursor: '' });

    render(
      wrap(
        <WatchdogSeasonsTable
          filters={{
            instance: 'homelab',
            q: 'severance',
            cooldownOnly: true,
            blacklistedOnly: true,
          }}
        />,
      ),
    );

    await waitFor(() => {
      expect(vi.mocked(api)).toHaveBeenCalled();
    });
    const url = vi.mocked(api).mock.calls[0]![0] as string;
    expect(url).toContain('instance=homelab');
    expect(url).toContain('q=severance');
    expect(url).toContain('cooldown_only=true');
    expect(url).toContain('blacklisted_only=true');
  });
});
