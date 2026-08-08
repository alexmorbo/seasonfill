import type { ReactElement } from 'react';
import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest';
import userEvent from '@testing-library/user-event';
import { screen, waitFor } from '@testing-library/react';
import { renderWithProviders } from '@/test-utils';
import { renderPageWithTitle } from '@/test-utils-title';
import { InstanceFilterCtx } from '@/lib/instance-filter-context-internal';
import i18n from '@/i18n';
import { Gaps } from './Gaps';

const origFetch = globalThis.fetch;
// filter=null → "all instances" scope, the report renders one section each.
const ctxValue = { filter: null, setFilter: vi.fn() };

const wrap = (ui: ReactElement) => (
  <InstanceFilterCtx.Provider value={ctxValue}>{ui}</InstanceFilterCtx.Provider>
);

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });

function report(over: Record<string, unknown> = {}) {
  return {
    generated_at: new Date().toISOString(),
    instances: [
      {
        instance_name: 'homelab',
        missing_episode_count: 5,
        whole_season_missing_count: 1,
        series: [
          {
            series_id: 42,
            title: 'The Expanse',
            missing_count: 5,
            seasons: [
              {
                season_number: 2,
                missing_count: 3,
                aired_monitored_count: 10,
                whole_season_missing: false,
                episodes: [
                  { episode_id: 1, season_number: 2, episode_number: 4, air_date: new Date().toISOString() },
                  { episode_id: 2, season_number: 2, episode_number: 5, air_date: new Date().toISOString() },
                  { episode_id: 3, season_number: 2, episode_number: 6, air_date: new Date().toISOString() },
                ],
              },
              {
                season_number: 3,
                missing_count: 2,
                aired_monitored_count: 2,
                whole_season_missing: true,
                episodes: [
                  { episode_id: 4, season_number: 3, episode_number: 1, air_date: new Date().toISOString() },
                  { episode_id: 5, season_number: 3, episode_number: 2, air_date: new Date().toISOString() },
                ],
              },
            ],
          },
        ],
      },
    ],
    ...over,
  };
}

const healthy = () => ({
  generated_at: new Date().toISOString(),
  instances: [
    {
      instance_name: 'homelab',
      missing_episode_count: 0,
      whole_season_missing_count: 0,
      series: [],
    },
  ],
});

beforeEach(() => {
  Object.defineProperty(window, 'location', {
    writable: true,
    value: { pathname: '/gaps', search: '', assign: vi.fn() },
  });
});
afterEach(() => {
  globalThis.fetch = origFetch;
  vi.restoreAllMocks();
});

describe('<Gaps />', () => {
  it('renders per-instance counters from the mocked endpoint', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue(json(report())) as typeof fetch;
    renderWithProviders(wrap(<Gaps />), { route: '/gaps' });

    expect(await screen.findByTestId('gaps-page')).toBeInTheDocument();
    expect(await screen.findByTestId('gaps-instance-homelab')).toBeInTheDocument();
    expect(screen.getByTestId('gaps-instance-homelab-missing')).toHaveTextContent('5');
    expect(screen.getByTestId('gaps-instance-homelab-whole-season')).toHaveTextContent('1');
  });

  it('surfaces the whole-season-missing badge distinctly', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue(json(report())) as typeof fetch;
    renderWithProviders(wrap(<Gaps />), { route: '/gaps' });

    // expand the series to reveal its seasons
    const toggle = await screen.findByTestId('gaps-series-42-toggle');
    await userEvent.click(toggle);

    // season 3 carries a danger badge; season 2 (scattered gaps) does not
    expect(await screen.findByTestId('gaps-season-42-3-whole')).toBeInTheDocument();
    expect(screen.queryByTestId('gaps-season-42-2-whole')).toBeNull();
  });

  it('drills down series → season → episodes', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue(json(report())) as typeof fetch;
    renderWithProviders(wrap(<Gaps />), { route: '/gaps' });

    // episodes not mounted until both collapsibles open
    expect(screen.queryByText('S02E04')).not.toBeInTheDocument();
    await userEvent.click(await screen.findByTestId('gaps-series-42-toggle'));
    await userEvent.click(await screen.findByTestId('gaps-season-42-2-toggle'));
    expect(await screen.findByText('S02E04')).toBeInTheDocument();
    expect(screen.getByTestId('gaps-season-42-2-episodes')).toBeInTheDocument();
  });

  it('shows the loading skeleton while the request is in flight', () => {
    globalThis.fetch = vi.fn().mockReturnValue(new Promise(() => {})) as typeof fetch;
    renderWithProviders(wrap(<Gaps />), { route: '/gaps' });
    expect(screen.getByTestId('gaps-loading')).toBeInTheDocument();
  });

  it('shows the all-healthy state when every instance is gap-free', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue(json(healthy())) as typeof fetch;
    renderWithProviders(wrap(<Gaps />), { route: '/gaps' });

    expect(await screen.findByTestId('gaps-all-healthy')).toBeInTheDocument();
    // no series rows are rendered in the healthy branch
    expect(screen.queryByTestId('gaps-series-42')).toBeNull();
  });

  it('renders the error state with a retry action on failure', async () => {
    globalThis.fetch = vi
      .fn()
      .mockResolvedValue(json({ error: 'boom' }, 500)) as typeof fetch;
    renderWithProviders(wrap(<Gaps />), { route: '/gaps' });

    expect(await screen.findByTestId('gaps-error')).toBeInTheDocument();
    expect(screen.getByText(i18n.t('common.retry'))).toBeInTheDocument();
  });

  it('sets the topbar page title via useSetPageTitle', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue(json(healthy())) as typeof fetch;
    const { getTitle } = renderPageWithTitle(<Gaps />, { route: '/gaps' });
    await waitFor(() => {
      expect(getTitle()).toBe(i18n.t('gaps.title'));
    });
  });
});
