import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest';
import userEvent from '@testing-library/user-event';
import { screen, waitFor } from '@testing-library/react';
import { Route, Routes } from 'react-router-dom';
import { renderWithProviders } from '@/test-utils';
import { InstanceQueue } from './InstanceQueue';
import { InstanceFilterCtx } from '@/lib/instance-filter-context-internal';

const origFetch = globalThis.fetch;
const ctxValue = { filter: null, setFilter: vi.fn() };

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });

// `url`/`body` = last-write (used by other tests); `urls`/`methods`/`bodies`
// = per-call arrays (used by the scan-now race-proof assertion below).
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

describe('<InstanceQueue />', () => {
  it('renders empty state when items=[]', async () => {
    globalThis.fetch = fetchStub({
      '/instances/alpha/missing': () => json({ items: [], total: 0 }),
      '/instances': () =>
        json({ instances: [{ name: 'alpha', mode: 'manual', health: 'available' }] }),
    }) as typeof fetch;

    renderWithProviders(wrap(), { route: '/instances/alpha/queue' });
    expect(
      await screen.findByText(/no missing-aired episodes/i),
    ).toBeInTheDocument();
  });

  it('renders rows with title, missing count, and season chips', async () => {
    globalThis.fetch = fetchStub({
      '/instances/alpha/missing': () =>
        json({
          items: [
            {
              series_id: 122,
              title: 'Severance',
              monitored: true,
              total_missing_aired: 8,
              seasons: [
                { season_number: 2, missing_aired_count: 8 },
              ],
            },
            {
              series_id: 9,
              title: 'Andor',
              monitored: true,
              total_missing_aired: 3,
              seasons: [
                { season_number: 1, missing_aired_count: 1 },
                { season_number: 2, missing_aired_count: 2 },
              ],
            },
          ],
          total: 2,
        }),
      '/instances': () =>
        json({ instances: [{ name: 'alpha', mode: 'manual', health: 'available' }] }),
    }) as typeof fetch;

    renderWithProviders(wrap(), { route: '/instances/alpha/queue' });
    expect(await screen.findByText('Severance')).toBeInTheDocument();
    expect(screen.getByText('Andor')).toBeInTheDocument();
    expect(
      screen.getByLabelText(/Season 2: 8 missing/i),
    ).toBeInTheDocument();
    expect(
      screen.getByLabelText(/Season 1: 1 missing/i),
    ).toBeInTheDocument();
  });

  it('Scan now → POST /scan with series_ids and navigates to /scans/:id', async () => {
    const captured: Captured = {};
    globalThis.fetch = fetchStub(
      {
        '/instances/alpha/missing': () =>
          json({
            items: [
              {
                series_id: 122,
                title: 'Severance',
                monitored: true,
                total_missing_aired: 8,
                seasons: [{ season_number: 2, missing_aired_count: 8 }],
              },
            ],
            total: 1,
          }),
        '/instances': () =>
          json({ instances: [{ name: 'alpha', mode: 'manual', health: 'available' }] }),
        '/scan': () =>
          json(
            [{ scan_run_id: 'run-77', instance: 'alpha', status: 'running' }],
            202,
          ),
      },
      captured,
    ) as typeof fetch;

    renderWithProviders(wrap(), { route: '/instances/alpha/queue' });
    const btn = await screen.findByRole('button', { name: /scan severance now/i });
    await userEvent.click(btn);

    // The POST `/scan` fires, but onSuccess invalidates ['missing'], which
    // triggers another GET `/missing` AFTER the POST. A single `captured.url`
    // would be overwritten by the refetch and fail. Walk the full call list.
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
      '/instances/ghost/missing': () =>
        json({ error: 'unknown instance: ghost' }, 404),
      '/instances': () => json({ instances: [] }),
    }) as typeof fetch;

    renderWithProviders(wrap(), { route: '/instances/ghost/queue' });
    expect(await screen.findByText(/unknown instance ghost/i)).toBeInTheDocument();
  });
});
