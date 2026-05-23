import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest';
import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Route, Routes } from 'react-router-dom';
import { renderWithProviders } from '@/test-utils';
import { ScanDetail } from './ScanDetail';
import { InstanceFilterCtx } from '@/lib/instance-filter-context-internal';

const origFetch = globalThis.fetch;
const ctxValue = { filter: null, setFilter: vi.fn() };
const json = (b: unknown, status = 200) =>
  new Response(JSON.stringify(b), { status, headers: { 'Content-Type': 'application/json' } });

// Array-form capture (010b r1 / 011b r1 idiom): useScan polls every 2 s
// while running and useDecisions polls every 30 s — single-slot URL
// captures race against refetches.
type Captured = { urls: string[]; methods: string[] };
function fetchStub(map: Record<string, () => Response>, captured?: Captured) {
  return vi.fn(async (url: RequestInfo | URL, init?: RequestInit) => {
    const path = typeof url === 'string' ? url : url.toString();
    captured?.urls.push(path);
    captured?.methods.push(init?.method ?? 'GET');
    for (const k of Object.keys(map)) if (path.includes(k)) return map[k]!();
    return json({});
  });
}

beforeEach(() => {
  Object.defineProperty(window, 'location', {
    writable: true, value: { pathname: '/scans/abc', search: '', assign: vi.fn() },
  });
});
afterEach(() => { globalThis.fetch = origFetch; });

const wrap = () => (
  <InstanceFilterCtx.Provider value={ctxValue}>
    <Routes><Route path="/scans/:id" element={<ScanDetail />} /></Routes>
  </InstanceFilterCtx.Provider>
);

const runningScan = {
  id: 'abc', instance: 'alpha', trigger: 'manual', status: 'running',
  started_at: new Date(Date.now() - 60_000).toISOString(), series_scanned: 7,
};
const completedScan = {
  ...runningScan, status: 'completed', finished_at: new Date().toISOString(),
  series_scanned: 12, candidates_found: 4, grabs_performed: 2, grabs_failed: 0,
};
const mkDec = (id: string, sid: number, title: string, season: number, cat: string, outcome = 'skip') => ({
  id, scan_run_id: 'abc', series_id: sid, series_title: title, season_number: season,
  decision: outcome, category: cat,
  reason: outcome === 'grab' ? 'grab_selected_dry_run' : 'skip_no_missing',
  candidates_count: outcome === 'grab' ? 3 : 0,
});
const mixedDecisions = { items: [
  mkDec('d-a-1', 1, 'Severance', 1, 'action_taken', 'grab'),
  mkDec('d-b-1', 2, 'Andor', 1, 'all_complete'),
  mkDec('d-b-2', 2, 'Andor', 2, 'all_complete'),
] };

const emptyLists = { '/decisions': () => json({ items: [] }), '/grabs': () => json({ items: [] }) };

describe('<ScanDetail /> rebuild', () => {
  it('renders the progress bar in indeterminate running mode + 2 s poll indicator', async () => {
    globalThis.fetch = fetchStub({ '/scans/abc': () => json(runningScan), ...emptyLists }) as typeof fetch;
    renderWithProviders(wrap(), { route: '/scans/abc' });
    const bar = await screen.findByRole('progressbar');
    expect(bar).toHaveAttribute('data-status', 'running');
    expect(bar).toHaveAttribute('data-determinate', 'false');
    expect(screen.getByText(/7 series scanned/i)).toBeInTheDocument();
    expect(screen.getByTestId('poll-indicator')).toHaveTextContent(/polling every 2s/i);
  });

  it('renders decisions grouped by series with worst-category-first sort', async () => {
    globalThis.fetch = fetchStub({
      '/scans/abc': () => json(completedScan),
      '/decisions': () => json(mixedDecisions),
      '/grabs': () => json({ items: [] }),
    }) as typeof fetch;
    renderWithProviders(wrap(), { route: '/scans/abc' });
    // Severance (action_taken=5) before Andor (all_complete=0).
    const titles = await screen.findAllByTestId('series-title');
    expect(titles.map((n) => n.textContent)).toEqual(['Severance', 'Andor']);
    // Severance default-expanded; Andor collapsed.
    expect(screen.getByLabelText(/seasons for severance/i)).toBeInTheDocument();
    expect(screen.queryByLabelText(/seasons for andor/i)).not.toBeInTheDocument();
  });

  it('toggling a group writes ?expanded= and round-trips display state', async () => {
    globalThis.fetch = fetchStub({
      '/scans/abc': () => json(completedScan),
      '/decisions': () => json(mixedDecisions),
      '/grabs': () => json({ items: [] }),
    }) as typeof fetch;
    renderWithProviders(wrap(), { route: '/scans/abc' });
    await screen.findAllByTestId('series-title');
    // Collapse default-expanded Severance.
    await userEvent.click(screen.getAllByRole('button', { expanded: true })[0]!);
    await waitFor(() => expect(screen.queryByLabelText(/seasons for severance/i)).not.toBeInTheDocument());
    // Expand Andor → user's explicit set persists in URL.
    const andor = screen.getAllByRole('button', { expanded: false })
      .find((b) => b.textContent?.includes('Andor'))!;
    await userEvent.click(andor);
    await waitFor(() => expect(screen.getByLabelText(/seasons for andor/i)).toBeInTheDocument());
  });

  it('stops polling once status transitions to completed', async () => {
    const captured: Captured = { urls: [], methods: [] };
    let scanBody: unknown = runningScan;
    globalThis.fetch = fetchStub({ '/scans/abc': () => json(scanBody), ...emptyLists }, captured) as typeof fetch;
    renderWithProviders(wrap(), { route: '/scans/abc' });
    await waitFor(() => expect(screen.getByText(/polling every 2s/i)).toBeInTheDocument());
    // Flip response; next 2 s tick lands new status; refetchInterval cb → false.
    scanBody = completedScan;
    await waitFor(() => expect(screen.queryByText(/polling every 2s/i)).not.toBeInTheDocument(), { timeout: 4000 });
    // Sample two ticks; no further /scans/abc GETs queue.
    const before = captured.urls.filter((u) => u.includes('/scans/abc')).length;
    await new Promise((r) => setTimeout(r, 2_500));
    const after = captured.urls.filter((u) => u.includes('/scans/abc')).length;
    expect(after).toBe(before);
  });

  it('renders failure alert + counters strip on terminal failure', async () => {
    globalThis.fetch = fetchStub({
      '/scans/xyz': () => json({
        id: 'xyz', instance: 'beta', trigger: 'cron', status: 'failed',
        error_message: 'sonarr: 401 Unauthorized',
        started_at: new Date().toISOString(), finished_at: new Date().toISOString(),
      }),
      ...emptyLists,
    }) as typeof fetch;
    renderWithProviders(wrap(), { route: '/scans/xyz' });
    expect(await screen.findByText(/Scan failed/i)).toBeInTheDocument();
    expect(screen.getByText(/401 Unauthorized/i)).toBeInTheDocument();
    expect(screen.getByText(/0 decisions · 0 grabs/i)).toBeInTheDocument();
  });
});
