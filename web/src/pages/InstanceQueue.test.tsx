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
  perPath: Record<string, (init?: RequestInit) => Response | Promise<Response>>,
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

// 493 / N-1c §scope-9 + §H: useMissing now delegates to the global
// /series catalog endpoint and lossily projects each row to the
// MissingSeries shape. SeriesCacheItem (the new wire) carries
// `sonarr_series_id` and `missing_count` per row but no per-season
// stats. The projection sets `seasons[]` to `[]`. Tests that
// exercise the season chip / drill behaviour are skipped pending
// 494's queue rewrite (Open Note §5 in story 493).
const cacheSeverance = {
  sonarr_series_id: 122,
  instance_name: 'alpha',
  title: 'Severance',
  title_slug: 'severance',
  year: 2022,
  monitored: true,
  missing_count: 8,
  updated_at: '2025-01-01T00:00:00Z',
};

const cacheAndor = {
  sonarr_series_id: 9,
  instance_name: 'alpha',
  title: 'Andor',
  title_slug: 'andor',
  monitored: true,
  missing_count: 3,
  updated_at: '2025-01-01T00:00:00Z',
};

const cacheListResp = (items: readonly unknown[]) =>
  ({ items, total: items.length, has_more: false });

describe('<InstanceQueue /> (integration)', () => {
  it('renders empty state when items=[]', async () => {
    globalThis.fetch = fetchStub({
      'state=missing': () => json(cacheListResp([])),
      '/instances': () =>
        json({ instances: [{ name: 'alpha', mode: 'auto', health: 'available' }] }),
    }) as typeof fetch;

    renderWithProviders(wrap(), { route: '/instances/alpha/queue' });
    expect(
      await screen.findByText(/no backlog/i),
    ).toBeInTheDocument();
  });

  it('hides the stats strip while loading so 0/0 placeholders never show', async () => {
    let resolveMissing: ((r: Response) => void) | undefined;
    const pending = new Promise<Response>((resolve) => {
      resolveMissing = resolve;
    });
    globalThis.fetch = fetchStub({
      'state=missing': () => pending,
      '/instances': () =>
        json({ instances: [{ name: 'alpha', mode: 'auto', health: 'available' }] }),
    }) as typeof fetch;

    renderWithProviders(wrap(), { route: '/instances/alpha/queue' });
    // While the request is in flight: skeletons visible, stats strip hidden.
    expect(await screen.findByTestId('queue-loading')).toBeInTheDocument();
    expect(screen.queryByTestId('queue-stats')).not.toBeInTheDocument();
    // Resolve the request — stats strip materialises with the real numbers.
    resolveMissing?.(json(cacheListResp([cacheSeverance, cacheAndor])));
    expect(await screen.findByTestId('queue-stats')).toBeInTheDocument();
    expect(screen.getByText('11')).toBeInTheDocument();
  });

  it('renders rows from a live-shaped response with 111 items + counters', async () => {
    const items = Array.from({ length: 111 }).map((_, i) => ({
      sonarr_series_id: 1000 + i,
      instance_name: 'alpha',
      title: `Show ${i}`,
      title_slug: `show-${i}`,
      year: 2010 + (i % 15),
      monitored: true,
      missing_count: (i % 7) + 1,
      updated_at: '2025-01-01T00:00:00Z',
    }));
    const totalEpisodes = items.reduce((a, s) => a + s.missing_count, 0);

    globalThis.fetch = fetchStub({
      'state=missing': () => json(cacheListResp(items)),
      '/instances': () =>
        json({ instances: [{ name: 'alpha', mode: 'auto', health: 'available' }] }),
    }) as typeof fetch;

    renderWithProviders(wrap(), { route: '/instances/alpha/queue' });
    expect(await screen.findByText('Show 0')).toBeInTheDocument();
    const list = await screen.findByTestId('queue-list');
    expect(list.children.length).toBe(items.length);
    expect(screen.getByText(String(items.length))).toBeInTheDocument();
    expect(screen.getByText(totalEpisodes.toLocaleString())).toBeInTheDocument();
  });

  it('renders rows with title + missing pill + stats strip', async () => {
    globalThis.fetch = fetchStub({
      'state=missing': () => json(cacheListResp([cacheSeverance, cacheAndor])),
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

  // NOTE (story 499): Two `it.skip` placeholders for the season-chip → drill
  // flow were removed. The chip path depends on `MissingSeries.seasons[]`
  // populated by `useMissing` (web/src/lib/missing.ts §H), which currently
  // projects from the global /series catalog wire (`SeriesCacheItem`) and
  // emits `seasons: []` by design. Restoring per-season chips requires a BE
  // projection change (per-season counts on the cache row or a parallel
  // endpoint). When that lands, author fresh integration tests against the
  // new data shape — the prior placeholders carried no assertions to revive.

  it('row Scan → POST /scan with series_ids and navigates', async () => {
    const captured: Captured = {};
    globalThis.fetch = fetchStub(
      {
        'state=missing': () => json(cacheListResp([cacheSeverance])),
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

  it('surfaces 404 when the instance is unknown', async () => {
    globalThis.fetch = fetchStub({
      'state=missing': () =>
        json({ error: 'unknown instance: ghost' }, 404),
      '/instances': () => json({ instances: [] }),
    }) as typeof fetch;

    renderWithProviders(wrap(), { route: '/instances/ghost/queue' });
    expect(await screen.findByText(/unknown instance ghost/i)).toBeInTheDocument();
  });

  it('renders title as a Sonarr link when ui_url + title_slug are present', async () => {
    globalThis.fetch = fetchStub({
      'state=missing': () => json(cacheListResp([cacheSeverance])),
      '/api/v1/admin/instances/alpha': () =>
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
      'state=missing': () => json(cacheListResp([cacheSeverance, cacheAndor])),
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
