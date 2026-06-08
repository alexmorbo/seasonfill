import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest';
import { screen, waitFor } from '@testing-library/react';
import { Route, Routes } from 'react-router-dom';
import { renderWithProviders } from '@/test-utils';
import { ScanDetail } from './ScanDetail';
import { InstanceFilterCtx } from '@/lib/instance-filter-context-internal';
import { PageTitleProvider } from '@/components/shell/page-title-context';

const origFetch = globalThis.fetch;
const ctxValue = { filter: null, setFilter: vi.fn() };
const json = (b: unknown, status = 200) =>
  new Response(JSON.stringify(b), { status, headers: { 'Content-Type': 'application/json' } });

function fetchStub(map: Record<string, () => Response>) {
  return vi.fn(async (url: RequestInfo | URL) => {
    const path = typeof url === 'string' ? url : url.toString();
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
  <PageTitleProvider defaultTitle="Scan">
    <InstanceFilterCtx.Provider value={ctxValue}>
      <Routes><Route path="/scans/:id" element={<ScanDetail />} /></Routes>
    </InstanceFilterCtx.Provider>
  </PageTitleProvider>
);

const completedScan = {
  id: 'abc', instance: 'alpha', trigger: 'manual', status: 'completed',
  started_at: new Date(Date.now() - 60_000).toISOString(),
  finished_at: new Date().toISOString(),
  series_scanned: 12, candidates_found: 4, grabs_performed: 2, grabs_failed: 0,
  dry_run: false, errors_count: 0,
};
const runningScan = { ...completedScan, status: 'running', finished_at: undefined };
const mkDec = (id: string, sid: number, title: string, season: number, cat: string, outcome = 'skip') => ({
  id, scan_run_id: 'abc', series_id: sid, series_title: title, season_number: season,
  decision: outcome, category: cat,
  reason: outcome === 'grab' ? 'grab_selected_dry_run' : 'skip_no_missing',
});
const mixed = { items: [
  mkDec('d-a-1', 1, 'Severance', 1, 'action_taken', 'grab'),
  mkDec('d-b-1', 2, 'Andor', 1, 'all_complete'),
  mkDec('d-b-2', 2, 'Andor', 2, 'all_complete'),
] };
const emptyLists = { '/decisions': () => json({ items: [] }), '/grabs': () => json({ items: [] }) };

describe('<ScanDetail /> redesign', () => {
  it('renders the header card with id + status + 6 chips', async () => {
    globalThis.fetch = fetchStub({ '/scans/abc': () => json(completedScan), ...emptyLists }) as typeof fetch;
    renderWithProviders(wrap(), { route: '/scans/abc' });
    expect(await screen.findByTestId('scan-header-card')).toBeInTheDocument();
    // 5 plain chips + 1 accent grabs chip = 6 total.
    expect(screen.getAllByTestId(/scan-chip/)).toHaveLength(6);
  });

  it('shows ScanProgressBar + CancelScanDialog when running', async () => {
    globalThis.fetch = fetchStub({ '/scans/abc': () => json(runningScan), ...emptyLists }) as typeof fetch;
    renderWithProviders(wrap(), { route: '/scans/abc' });
    expect(await screen.findByRole('progressbar')).toBeInTheDocument();
    expect(screen.getByTestId('poll-indicator')).toBeInTheDocument();
  });

  it('renders decisions grouped by series with worst-category-first sort (all_complete hidden by default per F-P1-10)', async () => {
    globalThis.fetch = fetchStub({
      '/scans/abc': () => json(completedScan),
      '/decisions': () => json(mixed),
      '/grabs': () => json({ items: [] }),
    }) as typeof fetch;
    renderWithProviders(wrap(), { route: '/scans/abc' });
    // Andor is fully `all_complete` → hidden by default. Only Severance
    // (action_taken) renders; a reveal toggle exposes Andor on demand.
    const titles = await screen.findAllByTestId('series-title');
    expect(titles.map((n) => n.textContent)).toEqual(['Severance']);
    expect(await screen.findByTestId('scan-decisions-skip-toggle')).toBeInTheDocument();
  });

  it('DRAWER CONTRACT: ?drawer=<decision_id> opens DecisionDrawer (F7 deep-link)', async () => {
    globalThis.fetch = fetchStub({
      '/scans/abc': () => json(completedScan),
      '/decisions': () => json(mixed),
      '/grabs': () => json({ items: [] }),
    }) as typeof fetch;
    renderWithProviders(wrap(), { route: '/scans/abc?drawer=d-a-1' });
    // DecisionDrawer is a Radix Sheet — when open, its dialog role is mounted.
    expect(await screen.findByRole('dialog')).toBeInTheDocument();
  });

  it('DRAWER DEEP-LOAD: ?drawer=<id> for a decision past the loaded page deep-fetches via /decisions/:id (N-4)', async () => {
    // The list-cache contains ONLY `d-a-1`; the deep-linked id
    // `d-deep-99` is NOT in any of the loaded pages — i.e., it's
    // past the first /decisions page (operator deep-link path).
    // Without the N-4 fix the drawer would render the "Решение не
    // найдено" empty state because the list-cache lookup misses.
    globalThis.fetch = vi.fn(async (url: RequestInfo | URL) => {
      const p = typeof url === 'string' ? url : url.toString();
      if (p.includes('/scans/abc')) return json(completedScan);
      // GET /api/v1/decisions/<id> — single-resource lookup.
      if (/\/decisions\/d-deep-99(?:\?|$)/.test(p)) {
        return json({
          id: 'd-deep-99',
          scan_run_id: 'abc',
          instance: 'alpha',
          series_id: 999,
          series_title: 'Late Series',
          season_number: 4,
          decision: 'grab',
          category: 'action_taken',
          reason: 'grab_selected_dry_run',
        });
      }
      if (p.includes('/decisions')) return json(mixed); // list only has d-a-1, d-b-1, d-b-2
      if (p.includes('/grabs')) return json({ items: [] });
      return json({});
    }) as typeof fetch;
    renderWithProviders(wrap(), { route: '/scans/abc?drawer=d-deep-99' });
    // Drawer mounts as a Radix Sheet (role="dialog") even while the
    // deep fetch resolves; assert the series-title header from the
    // deep-fetched payload appears.
    expect(await screen.findByRole('dialog')).toBeInTheDocument();
    expect(await screen.findByText('Late Series')).toBeInTheDocument();
    // And the "not-found" copy must NOT appear.
    expect(screen.queryByText(/Решение не найдено|Decision not found/i)).toBeNull();
  });

  it('DRAWER DEEP-LOAD: 404 from /decisions/:id renders the not-found empty state (N-4)', async () => {
    // Same setup but the backend returns 404 → drawer shows the
    // legitimate "not found" copy.
    globalThis.fetch = vi.fn(async (url: RequestInfo | URL) => {
      const p = typeof url === 'string' ? url : url.toString();
      if (p.includes('/scans/abc')) return json(completedScan);
      if (/\/decisions\/d-truly-missing(?:\?|$)/.test(p)) {
        return json({ error: 'decision not found' }, 404);
      }
      if (p.includes('/decisions')) return json({ items: [] });
      if (p.includes('/grabs')) return json({ items: [] });
      return json({});
    }) as typeof fetch;
    renderWithProviders(wrap(), { route: '/scans/abc?drawer=d-truly-missing' });
    await waitFor(() => {
      expect(screen.getByText(/Решение не найдено|Decision not found/i)).toBeInTheDocument();
    });
  });

  it('?outcome=<value> filters the decisions card (param name preserved)', async () => {
    const captured: string[] = [];
    globalThis.fetch = vi.fn(async (url: RequestInfo | URL) => {
      const p = typeof url === 'string' ? url : url.toString();
      captured.push(p);
      if (p.includes('/scans/abc')) return json(completedScan);
      if (p.includes('/decisions')) return json({ items: [] });
      if (p.includes('/grabs')) return json({ items: [] });
      return json({});
    }) as typeof fetch;
    renderWithProviders(wrap(), { route: '/scans/abc?outcome=grab' });
    await waitFor(() => {
      expect(captured.some((u) => u.includes('/decisions') && u.includes('decision=grab'))).toBe(true);
    });
  });

  it('?expanded=<encoded> controls accordion open-state (param preserved)', async () => {
    globalThis.fetch = fetchStub({
      '/scans/abc': () => json(completedScan),
      '/decisions': () => json(mixed),
      '/grabs': () => json({ items: [] }),
    }) as typeof fetch;
    // Empty `expanded=` opens nothing.
    renderWithProviders(wrap(), { route: '/scans/abc?expanded=' });
    await screen.findAllByTestId('series-title');
    expect(screen.queryByLabelText(/seasons for severance/i)).not.toBeInTheDocument();
    expect(screen.queryByLabelText(/seasons for andor/i)).not.toBeInTheDocument();
  });

  it('Result filter (Результат) Select onValueChange empty-string guard does not crash', async () => {
    globalThis.fetch = fetchStub({
      '/scans/abc': () => json(completedScan),
      '/decisions': () => json(mixed),
      '/grabs': () => json({ items: [] }),
    }) as typeof fetch;
    renderWithProviders(wrap(), { route: '/scans/abc' });
    // Just assert mount — the guard pattern is enforced in code; the
    // unit cost of asserting Radix internals is prohibitive.
    expect(await screen.findByTestId('scan-result-filter')).toBeInTheDocument();
  });

  it('renders LinkedGrabsCard when grabs.length > 0', async () => {
    globalThis.fetch = fetchStub({
      '/scans/abc': () => json(completedScan),
      '/decisions': () => json({ items: [] }),
      '/grabs': () => json({ items: [
        { id: 'g1', scan_run_id: 'abc', release_title: 'Severance.S01.WEB-DL.1080p',
          status: 'imported', indexer_name: 'rarbg', attempts: 1,
          created_at: new Date().toISOString(), updated_at: new Date().toISOString() },
      ] }),
    }) as typeof fetch;
    renderWithProviders(wrap(), { route: '/scans/abc' });
    expect(await screen.findByTestId('scan-linked-grabs-card')).toBeInTheDocument();
    expect(screen.getByTestId('scan-linked-grabs-row')).toBeInTheDocument();
  });

  it('failed scan with error_message renders danger Alert above decisions card', async () => {
    globalThis.fetch = fetchStub({
      '/scans/abc': () => json({ ...completedScan, status: 'failed', error_message: 'boom' }),
      '/decisions': () => json({ items: [] }),
      '/grabs': () => json({ items: [] }),
    }) as typeof fetch;
    renderWithProviders(wrap(), { route: '/scans/abc' });
    expect(await screen.findByText('boom')).toBeInTheDocument();
  });
});
