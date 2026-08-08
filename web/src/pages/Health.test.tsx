import type { ReactElement } from 'react';
import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest';
import userEvent from '@testing-library/user-event';
import { screen, waitFor } from '@testing-library/react';
import { renderWithProviders } from '@/test-utils';
import { renderPageWithTitle } from '@/test-utils-title';
import i18n from '@/i18n';
import { Health } from './Health';

const origFetch = globalThis.fetch;

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });

function dashboard(over: Record<string, unknown> = {}) {
  return {
    generated_at: new Date().toISOString(),
    missing_tvdb_id: {
      count: 3,
      items: [
        { series_id: 42, title: 'The Expanse' },
        { series_id: 7, title: 'Andor' },
        { series_id: 9, title: 'Severance' },
      ],
    },
    missing_poster: { count: 1, items: [{ series_id: 11, title: 'Dark' }] },
    stale_enrichment: {
      count: 2,
      items: [
        { series_id: 42, title: 'The Expanse', tier: 'hot', synced_at: new Date().toISOString() },
        { series_id: 7, title: 'Andor', tier: 'cold', synced_at: new Date().toISOString() },
      ],
    },
    stuck_grabs: {
      count: 1,
      items: [
        {
          id: 'a1b2c3d4-0000-0000-0000-000000000000',
          instance_name: 'main',
          series_title: 'Hijack',
          season_number: 2,
          created_at: new Date().toISOString(),
        },
      ],
    },
    dead_letters: {
      count: 1,
      items: [
        {
          id: 1001,
          instance_name: 'main',
          event_type: 'Download',
          attempts: 6,
          last_error: 'sonarr 500',
          created_at: new Date().toISOString(),
        },
      ],
    },
    rate_limit_pressure: {
      deferred: true,
      reason: 'tracked as a metric, not a row count',
      metric: 'seasonfill_sonarr_rate_oversubscribed',
    },
    ...over,
  };
}

const healthy = () => ({
  generated_at: new Date().toISOString(),
  missing_tvdb_id: { count: 0, items: [] },
  missing_poster: { count: 0, items: [] },
  stale_enrichment: { count: 0, items: [] },
  stuck_grabs: { count: 0, items: [] },
  dead_letters: { count: 0, items: [] },
  rate_limit_pressure: {
    deferred: true,
    reason: 'tracked as a metric',
    metric: 'seasonfill_sonarr_rate_oversubscribed',
  },
});

const wrap = (ui: ReactElement) => ui;

beforeEach(() => {
  Object.defineProperty(window, 'location', {
    writable: true,
    value: { pathname: '/health', search: '', assign: vi.fn() },
  });
});
afterEach(() => {
  globalThis.fetch = origFetch;
  vi.restoreAllMocks();
});

describe('<Health />', () => {
  it('renders a counter card per signal from the mocked endpoint', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue(json(dashboard())) as typeof fetch;
    renderWithProviders(wrap(<Health />), { route: '/health' });

    expect(await screen.findByTestId('health-page')).toBeInTheDocument();
    expect(await screen.findByTestId('health-missing-tvdb-count')).toHaveTextContent('3');
    expect(screen.getByTestId('health-missing-poster-count')).toHaveTextContent('1');
    expect(screen.getByTestId('health-stale-count')).toHaveTextContent('2');
    expect(screen.getByTestId('health-stuck-grabs-count')).toHaveTextContent('1');
    expect(screen.getByTestId('health-dead-letters-count')).toHaveTextContent('1');
  });

  it('expands a drill-down and reveals the offending rows', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue(json(dashboard())) as typeof fetch;
    renderWithProviders(wrap(<Health />), { route: '/health' });

    const toggle = await screen.findByTestId('health-missing-tvdb-toggle');
    // collapsed → drill items not mounted yet
    expect(screen.queryByText('The Expanse')).not.toBeInTheDocument();
    await userEvent.click(toggle);
    expect(await screen.findByText('The Expanse')).toBeInTheDocument();
    expect(screen.getByTestId('health-missing-tvdb-items')).toBeInTheDocument();
  });

  it('renders the deferred rate-limit signal as a deferred/metric state, not a count', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue(json(dashboard())) as typeof fetch;
    renderWithProviders(wrap(<Health />), { route: '/health' });

    expect(await screen.findByTestId('health-rate-limit')).toBeInTheDocument();
    expect(screen.getByTestId('health-rate-limit-deferred')).toBeInTheDocument();
    expect(
      screen.getByText(/seasonfill_sonarr_rate_oversubscribed/),
    ).toBeInTheDocument();
    // deferred envelope must NOT render a numeric count badge
    expect(screen.queryByTestId('health-rate-limit-count')).toBeNull();
  });

  it('shows the loading skeleton while the request is in flight', () => {
    globalThis.fetch = vi.fn().mockReturnValue(new Promise(() => {})) as typeof fetch;
    renderWithProviders(wrap(<Health />), { route: '/health' });
    expect(screen.getByTestId('health-loading')).toBeInTheDocument();
  });

  it('shows the positive all-healthy state when every signal is zero', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue(json(healthy())) as typeof fetch;
    renderWithProviders(wrap(<Health />), { route: '/health' });

    expect(await screen.findByTestId('health-all-healthy')).toBeInTheDocument();
    // all DB cards read 0
    expect(screen.getByTestId('health-missing-tvdb-count')).toHaveTextContent('0');
    // and never offer a drill toggle
    expect(screen.queryByTestId('health-missing-tvdb-toggle')).toBeNull();
  });

  it('renders the error state with a retry action on failure', async () => {
    globalThis.fetch = vi
      .fn()
      .mockResolvedValue(json({ error: 'boom' }, 500)) as typeof fetch;
    renderWithProviders(wrap(<Health />), { route: '/health' });

    expect(await screen.findByTestId('health-error')).toBeInTheDocument();
    expect(screen.getByText(i18n.t('common.retry'))).toBeInTheDocument();
  });

  it('sets the topbar page title via useSetPageTitle', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue(json(healthy())) as typeof fetch;
    const { getTitle } = renderPageWithTitle(<Health />, { route: '/health' });
    await waitFor(() => {
      expect(getTitle()).toBe(i18n.t('health.title'));
    });
  });
});
