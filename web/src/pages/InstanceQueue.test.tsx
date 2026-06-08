import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import userEvent from '@testing-library/user-event';
import { screen, waitFor } from '@testing-library/react';
import { Route, Routes } from 'react-router-dom';
import { renderWithProviders } from '@/test-utils';
import { InstanceQueue } from './InstanceQueue';
import { InstanceFilterCtx } from '@/lib/instance-filter-context-internal';
import i18n from '@/i18n';
import { renderPageWithTitle } from '@/test-utils-title';

const origFetch = globalThis.fetch;
const ctxValue = { filter: null, setFilter: vi.fn() };

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });

type Captured = {
  url?: string; body?: string;
  urls?: string[]; methods?: string[]; bodies?: string[];
};

function fetchStub(
  perPath: Record<string, (init?: RequestInit) => Response>,
  captured: Captured = {},
) {
  captured.urls ??= []; captured.methods ??= []; captured.bodies ??= [];
  return vi.fn(async (url: RequestInfo | URL, init?: RequestInit) => {
    const path = typeof url === 'string' ? url : url.toString();
    const body = typeof init?.body === 'string' ? init.body : undefined;
    captured.url = path;
    if (body !== undefined) captured.body = body;
    captured.urls!.push(path);
    captured.methods!.push(init?.method ?? 'GET');
    captured.bodies!.push(body ?? '');
    for (const key of Object.keys(perPath)) {
      if (path.includes(key)) return perPath[key]!(init);
    }
    return json({});
  });
}

beforeEach(() => {
  Object.defineProperty(window, 'location', {
    writable: true,
    value: { pathname: '/instances/alpha/queue', search: '', assign: vi.fn() },
  });
});
afterEach(() => {
  globalThis.fetch = origFetch;
  vi.restoreAllMocks();
});

const wrap = () => (
  <InstanceFilterCtx.Provider value={ctxValue}>
    <Routes>
      <Route path="/instances/:name/queue" element={<InstanceQueue />} />
      <Route path="/scans/:id" element={<div>scan-detail-stub</div>} />
    </Routes>
  </InstanceFilterCtx.Provider>
);

const missingSeverance = {
  series_id: 122,
  title: 'Severance',
  title_slug: 'severance',
  year: 2022,
  monitored: true,
  total_missing_aired: 8,
  seasons: [{ season_number: 2, missing_aired_count: 8 }],
};

const missingAndor = {
  series_id: 9,
  title: 'Andor',
  monitored: true,
  total_missing_aired: 3,
  seasons: [
    { season_number: 1, missing_aired_count: 1 },
    { season_number: 2, missing_aired_count: 2 },
  ],
};

describe('<InstanceQueue /> (integration)', () => {
  it('renders empty state when items=[]', async () => {
    globalThis.fetch = fetchStub({
      '/instances/alpha/missing': () => json({ items: [], total: 0 }),
      '/instances': () =>
        json({ instances: [{ name: 'alpha', mode: 'auto', health: 'available' }] }),
    }) as typeof fetch;

    renderWithProviders(wrap(), { route: '/instances/alpha/queue' });
    expect(
      await screen.findByText(/no backlog/i),
    ).toBeInTheDocument();
  });

  it('renders rows with title + season chips and stats strip', async () => {
    globalThis.fetch = fetchStub({
      '/instances/alpha/missing': () =>
        json({
          items: [missingSeverance, missingAndor],
          total: 2,
        }),
      '/instances': () =>
        json({ instances: [{ name: 'alpha', mode: 'auto', health: 'available' }] }),
    }) as typeof fetch;

    renderWithProviders(wrap(), { route: '/instances/alpha/queue' });
    expect(await screen.findByText('Severance')).toBeInTheDocument();
    expect(screen.getByText('Andor')).toBeInTheDocument();
    // stats strip: 2 series, 11 episodes
    expect(screen.getByText('2')).toBeInTheDocument();
    expect(screen.getByText('11')).toBeInTheDocument();
  });

  it('opens the season drill on chip click and fetches episodes', async () => {
    globalThis.fetch = fetchStub({
      '/instances/alpha/series/122/seasons/2/episodes': () =>
        json({
          items: [
            { number: 1, monitored: true, has_file: true, aired: true, air_date_utc: '2024-01-01T00:00:00Z' },
            { number: 2, monitored: true, has_file: false, aired: true, air_date_utc: '2024-01-08T00:00:00Z' },
            { number: 3, monitored: true, has_file: false, aired: false, air_date_utc: '2099-01-01T00:00:00Z' },
          ],
          total: 3,
          have: 1,
          miss: 1,
        }),
      '/instances/alpha/missing': () =>
        json({ items: [missingSeverance], total: 1 }),
      '/instances': () =>
        json({ instances: [{ name: 'alpha', mode: 'auto', health: 'available' }] }),
    }) as typeof fetch;

    renderWithProviders(wrap(), { route: '/instances/alpha/queue' });
    const chip = await screen.findByLabelText(/Season 2: 8 missing/i);
    await userEvent.click(chip);

    expect(await screen.findByTestId('queue-drill')).toBeInTheDocument();
    expect(await screen.findByTestId('queue-episode-chips')).toBeInTheDocument();
    // E1 = have, E2 = miss, E3 = upcoming
    expect(screen.getByText('E1').getAttribute('data-state')).toBe('have');
    expect(screen.getByText('E2').getAttribute('data-state')).toBe('miss');
    expect(screen.getByText('E3').getAttribute('data-state')).toBe('upcoming');
  });

  it('row Scan → POST /scan with series_ids and navigates', async () => {
    const captured: Captured = {};
    globalThis.fetch = fetchStub(
      {
        '/instances/alpha/missing': () =>
          json({ items: [missingSeverance], total: 1 }),
        '/scan': () =>
          json([{ scan_run_id: 'run-77', instance: 'alpha', status: 'running' }], 202),
        '/instances': () =>
          json({ instances: [{ name: 'alpha', mode: 'auto', health: 'available' }] }),
      },
      captured,
    ) as typeof fetch;

    renderWithProviders(wrap(), { route: '/instances/alpha/queue' });
    const btn = await screen.findByRole('button', { name: /scan severance now/i });
    await userEvent.click(btn);

    const findScanPost = () =>
      (captured.urls ?? []).findIndex(
        (u, i) => u.includes('/scan') && (captured.methods ?? [])[i] === 'POST',
      );
    await waitFor(() => expect(findScanPost()).toBeGreaterThanOrEqual(0));
    const idx = findScanPost();
    expect(JSON.parse((captured.bodies ?? [])[idx] || '{}')).toEqual({
      instance: 'alpha', series_ids: [122],
    });
    expect(await screen.findByText(/scan-detail-stub/i)).toBeInTheDocument();
  });

  it('drill Scan-season fires POST /scan with series_ids only (no season_numbers)', async () => {
    const captured: Captured = {};
    globalThis.fetch = fetchStub(
      {
        '/instances/alpha/series/122/seasons/2/episodes': () =>
          json({
            items: [{ number: 1, monitored: true, has_file: false, aired: true, air_date_utc: '2024-01-01T00:00:00Z' }],
            total: 1, have: 0, miss: 1,
          }),
        '/instances/alpha/missing': () =>
          json({ items: [missingSeverance], total: 1 }),
        '/scan': () =>
          json([{ scan_run_id: 'run-78', instance: 'alpha', status: 'running' }], 202),
        '/instances': () =>
          json({ instances: [{ name: 'alpha', mode: 'auto', health: 'available' }] }),
      },
      captured,
    ) as typeof fetch;

    renderWithProviders(wrap(), { route: '/instances/alpha/queue' });
    await userEvent.click(await screen.findByLabelText(/Season 2: 8 missing/i));
    const scanSeasonBtn = await screen.findByTestId('queue-drill-scan-season');
    await userEvent.click(scanSeasonBtn);

    const findScanPost = () =>
      (captured.urls ?? []).findIndex(
        (u, i) => u.includes('/scan') && (captured.methods ?? [])[i] === 'POST',
      );
    await waitFor(() => expect(findScanPost()).toBeGreaterThanOrEqual(0));
    const idx = findScanPost();
    const body = JSON.parse((captured.bodies ?? [])[idx] || '{}');
    expect(body).toEqual({ instance: 'alpha', series_ids: [122] });
    expect(body).not.toHaveProperty('season_numbers');
  });

  it('surfaces 404 when the instance is unknown', async () => {
    globalThis.fetch = fetchStub({
      '/instances/ghost/missing': () =>
        json({ error: 'unknown instance: ghost' }, 404),
      '/instances': () => json({ instances: [] }),
    }) as typeof fetch;

    renderWithProviders(wrap(), { route: '/instances/ghost/queue' });
    expect(await screen.findByText(/unknown instance ghost/i)).toBeInTheDocument();
  });

  it('renders title as a Sonarr link when ui_url + title_slug are present', async () => {
    globalThis.fetch = fetchStub({
      '/instances/alpha/missing': () =>
        json({ items: [missingSeverance], total: 1 }),
      '/api/v1/instances/alpha': () =>
        json({
          name: 'alpha',
          url: 'http://sonarr:8989',
          public_url: 'https://sonarr.example.com',
          ui_url: 'https://sonarr.example.com',
        }),
      '/instances': () =>
        json({ instances: [{ name: 'alpha', mode: 'auto', health: 'available' }] }),
    }) as typeof fetch;

    renderWithProviders(wrap(), { route: '/instances/alpha/queue' });
    const link = await screen.findByRole('link', { name: /Severance/i });
    expect(link).toHaveAttribute(
      'href',
      'https://sonarr.example.com/series/severance',
    );
    expect(link).toHaveAttribute('target', '_blank');
  });

  it('search filter narrows the row list', async () => {
    globalThis.fetch = fetchStub({
      '/instances/alpha/missing': () =>
        json({ items: [missingSeverance, missingAndor], total: 2 }),
      '/instances': () =>
        json({ instances: [{ name: 'alpha', mode: 'auto', health: 'available' }] }),
    }) as typeof fetch;

    renderWithProviders(wrap(), { route: '/instances/alpha/queue' });
    await screen.findByText('Severance');
    const search = screen.getByPlaceholderText(/search by series/i);
    await userEvent.type(search, 'and');
    expect(screen.queryByText('Severance')).not.toBeInTheDocument();
    expect(screen.getByText('Andor')).toBeInTheDocument();
  });

  it('sets the topbar page title via useSetPageTitle', async () => {
    const { getTitle } = renderPageWithTitle(<InstanceQueue />, { route: '/instances/homelab/queue' });
    await waitFor(() => {
      expect(getTitle()).toBe(i18n.t('instanceQueue.title'));
    });
  });
});
