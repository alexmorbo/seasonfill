import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest';
import { screen, waitFor } from '@testing-library/react';
import { Route, Routes } from 'react-router-dom';
import { renderWithProviders } from '@/test-utils';
import { ScanDetail } from './ScanDetail';
import { InstanceFilterCtx } from '@/lib/instance-filter-context-internal';

const origFetch = globalThis.fetch;
const ctxValue = { filter: null, setFilter: vi.fn() };

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });

function fetchStub(map: Record<string, unknown>) {
  return vi.fn(async (url: RequestInfo | URL) => {
    const path = typeof url === 'string' ? url : url.toString();
    for (const k of Object.keys(map)) if (path.includes(k)) return json(map[k]);
    return json({});
  });
}

beforeEach(() => {
  Object.defineProperty(window, 'location', {
    writable: true,
    value: { pathname: '/scans/abc', search: '', assign: vi.fn() },
  });
});
afterEach(() => {
  globalThis.fetch = origFetch;
});

const wrap = () => (
  <InstanceFilterCtx.Provider value={ctxValue}>
    <Routes>
      <Route path="/scans/:id" element={<ScanDetail />} />
    </Routes>
  </InstanceFilterCtx.Provider>
);

describe('<ScanDetail />', () => {
  it('renders header, stats, and linked grabs', async () => {
    globalThis.fetch = fetchStub({
      '/scans/abc': {
        id: 'abc',
        instance: 'alpha',
        trigger: 'manual',
        status: 'completed',
        started_at: new Date(Date.now() - 60_000).toISOString(),
        finished_at: new Date().toISOString(),
        series_scanned: 10,
        candidates_found: 4,
        grabs_performed: 2,
        grabs_failed: 0,
      },
      '/decisions': {
        items: [
          {
            id: 'd1',
            scan_run_id: 'abc',
            series_title: 'Severance',
            season_number: 1,
            decision: 'grab',
            reason: 'upgrade_available',
            candidates_count: 3,
          },
        ],
      },
      '/grabs': {
        items: [
          {
            id: 'g1',
            scan_run_id: 'abc',
            series_title: 'Severance',
            release_title: 'Severance.S01E01.1080p',
            status: 'imported',
            instance: 'alpha',
            indexer_name: 'tracker.x',
            attempts: 1,
            created_at: new Date().toISOString(),
          },
        ],
      },
    }) as typeof fetch;
    renderWithProviders(wrap(), { route: '/scans/abc' });
    expect(await screen.findByText(/Scan/)).toBeInTheDocument();
    expect(await screen.findByText('Severance')).toBeInTheDocument();
    await waitFor(() =>
      expect(screen.getByText(/Severance.S01E01/)).toBeInTheDocument(),
    );
  });

  it('shows failure alert when status=failed', async () => {
    globalThis.fetch = fetchStub({
      '/scans/xyz': {
        id: 'xyz',
        instance: 'beta',
        trigger: 'cron',
        status: 'failed',
        error_message: 'sonarr: 401 Unauthorized',
        started_at: new Date().toISOString(),
      },
      '/decisions': { items: [] },
      '/grabs': { items: [] },
    }) as typeof fetch;
    renderWithProviders(wrap(), { route: '/scans/xyz' });
    expect(await screen.findByText(/Scan failed/i)).toBeInTheDocument();
    expect(screen.getByText(/401 Unauthorized/i)).toBeInTheDocument();
  });

  it('renders "Scan not found" on 404', async () => {
    globalThis.fetch = vi
      .fn()
      .mockResolvedValue(json({ error: 'not found' }, 404)) as typeof fetch;
    renderWithProviders(wrap(), { route: '/scans/nope' });
    expect(await screen.findByText(/Scan not found/i)).toBeInTheDocument();
  });
});
