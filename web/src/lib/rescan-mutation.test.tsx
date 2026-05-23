import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { useRescanDecision } from './rescan-mutation';

const toastSuccess = vi.fn();
const toastError = vi.fn();
vi.mock('sonner', () => ({
  toast: {
    success: (m: string) => toastSuccess(m),
    error: (m: string) => toastError(m),
  },
}));

function wrap() {
  const qc = new QueryClient({
    defaultOptions: {
      // gcTime must be > 0: pre-seeded ['decisions', ...] caches have
      // no observers (no component is rendering useDecisions in this
      // hook-only test), so gcTime:0 would garbage-collect them the
      // microtask after setQueryData and setQueriesData wouldn't find
      // anything to update (019 cache-seed test).
      queries: { retry: false, gcTime: 5 * 60_000, staleTime: 0 },
      mutations: { retry: false },
    },
  });
  const invalidations: unknown[] = [];
  const orig = qc.invalidateQueries.bind(qc);
  qc.invalidateQueries = ((opts?: { queryKey?: readonly unknown[] }) => {
    if (opts?.queryKey) invalidations.push(opts.queryKey);
    return orig(opts);
  }) as typeof qc.invalidateQueries;
  const Wrapper = ({ children }: { children: React.ReactNode }) => (
    <QueryClientProvider client={qc}>{children}</QueryClientProvider>
  );
  return { qc, invalidations, Wrapper };
}

const origFetch = globalThis.fetch;
const jsonResp = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });

beforeEach(() => {
  toastSuccess.mockClear();
  toastError.mockClear();
  Object.defineProperty(window, 'location', {
    writable: true,
    value: { pathname: '/', assign: vi.fn() },
  });
});
afterEach(() => {
  globalThis.fetch = origFetch;
});

describe('useRescanDecision()', () => {
  it('POSTs /decisions/:id/rescan and invalidates decisions+scans on success', async () => {
    const captured: { url?: string; method?: string } = {};
    globalThis.fetch = vi.fn(async (u: RequestInfo | URL, init?: RequestInit) => {
      captured.url = typeof u === 'string' ? u : u.toString();
      if (init?.method) captured.method = init.method;
      return jsonResp({
        id: 'dec-new', instance: 'alpha', series_title: 'Severance',
        season_number: 2, decision: 'grab', reason: 'grab_selected',
        category: 'action_taken', scan_run_id: 'scan-1',
        created_at: new Date().toISOString(),
      });
    }) as typeof fetch;

    const { Wrapper, invalidations } = wrap();
    const { result } = renderHook(() => useRescanDecision(), { wrapper: Wrapper });
    result.current.mutate({ decisionId: 'dec-old' });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));

    expect(captured.url).toBe('/api/v1/decisions/dec-old/rescan');
    expect(captured.method).toBe('POST');
    expect(toastSuccess).toHaveBeenCalledWith(
      'Rescan complete — showing new decision',
    );
    expect(invalidations.flat()).toEqual(
      expect.arrayContaining(['decisions', 'scans', 'scan']),
    );
  });

  it('seeds existing decisions infinite-cache with the new row before invalidating', async () => {
    globalThis.fetch = vi.fn(async () =>
      jsonResp({
        id: 'dec-new', instance: 'alpha', series_title: 'Severance',
        season_number: 2, decision: 'grab', reason: 'grab_selected',
        category: 'action_taken', scan_run_id: 'scan-1',
        created_at: new Date().toISOString(),
      }),
    ) as typeof fetch;

    const { qc, Wrapper } = wrap();
    // Pre-seed an infinite-query cache as useDecisions would.
    qc.setQueryData(['decisions', null, {}], {
      pages: [{ items: [{ id: 'dec-old' }], next_cursor: '' }],
      pageParams: [''],
    });

    const { result } = renderHook(() => useRescanDecision(), { wrapper: Wrapper });
    result.current.mutate({ decisionId: 'dec-old' });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));

    // After success, the new row sits at index 0 of the first page.
    const cached = qc.getQueryData<{
      readonly pages: readonly { readonly items: readonly { readonly id: string }[] }[];
    }>(['decisions', null, {}]);
    expect(cached?.pages[0]?.items[0]?.id).toBe('dec-new');
    expect(cached?.pages[0]?.items[1]?.id).toBe('dec-old');
  });

  it('falls back to plain success toast when response is missing id', async () => {
    globalThis.fetch = vi.fn(async () =>
      jsonResp({
        // id intentionally absent — defensive branch for partial DTOs.
        instance: 'alpha', series_title: 'Severance', season_number: 2,
        decision: 'skip', reason: 'skip_no_releases',
        category: 'nothing_found', scan_run_id: 'scan-1',
        created_at: new Date().toISOString(),
      }),
    ) as typeof fetch;

    const { Wrapper } = wrap();
    const { result } = renderHook(() => useRescanDecision(), { wrapper: Wrapper });
    result.current.mutate({ decisionId: 'dec-old' });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));

    expect(toastSuccess).toHaveBeenCalledWith('Rescan complete');
    expect(toastSuccess).not.toHaveBeenCalledWith(
      'Rescan complete — showing new decision',
    );
  });

  it.each([
    [
      'decision already superseded; rescan the successor instead',
      409,
      'Already rescanned — open the successor',
    ],
    [
      'decision already executed; create a new scan instead',
      409,
      'Already grabbed against Sonarr — create a new scan',
    ],
    ['sonarr unavailable', 502, 'Rescan failed: sonarr unavailable'],
  ])('error body %s → toast %s', async (body, status, expected) => {
    globalThis.fetch = vi.fn(async () => jsonResp({ error: body }, status)) as typeof fetch;
    const { Wrapper } = wrap();
    const { result } = renderHook(() => useRescanDecision(), { wrapper: Wrapper });
    result.current.mutate({ decisionId: 'dec-old' });
    await waitFor(() => expect(result.current.isError).toBe(true));
    expect(toastError).toHaveBeenCalledWith(expected);
  });
});
