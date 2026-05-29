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
  it('POSTs /decisions/:id/rescan and invalidates decisions+scans+instances on success', async () => {
    const captured: { url?: string; method?: string } = {};
    globalThis.fetch = vi.fn(async (u: RequestInfo | URL, init?: RequestInit) => {
      captured.url = typeof u === 'string' ? u : u.toString();
      if (init?.method) captured.method = init.method;
      return jsonResp(
        [{
          scan_run_id: 'run-new',
          instance: 'alpha',
          status: 'running',
          started_at: new Date().toISOString(),
        }],
        202,
      );
    }) as typeof fetch;

    const { Wrapper, invalidations } = wrap();
    const { result } = renderHook(() => useRescanDecision(), { wrapper: Wrapper });
    result.current.mutate({ decisionId: 'dec-old' });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));

    expect(captured.url).toBe('/api/v1/decisions/dec-old/rescan');
    expect(captured.method).toBe('POST');
    expect(toastSuccess).toHaveBeenCalledWith('Rescan started');
    // The mutation now returns the raw items[] (matching POST /scan).
    expect(result.current.data?.[0]?.scan_run_id).toBe('run-new');
    // Invalidations mirror useTriggerScan: decisions, scans, instances.
    expect(invalidations.flat()).toEqual(
      expect.arrayContaining(['decisions', 'scans', 'instances']),
    );
  });

  it('does NOT mutate the decisions infinite-cache (no in-place seeding under the new contract)', async () => {
    globalThis.fetch = vi.fn(async () =>
      jsonResp(
        [{
          scan_run_id: 'run-x',
          instance: 'alpha',
          status: 'running',
          started_at: new Date().toISOString(),
        }],
        202,
      ),
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

    // The pre-seed should remain untouched in shape — invalidation may
    // refetch lazily, but the rescan hook itself MUST NOT prepend a
    // synthetic decision row anymore (no Decision returned by the new
    // backend contract).
    const cached = qc.getQueryData<{
      readonly pages: readonly { readonly items: readonly { readonly id: string }[] }[];
    }>(['decisions', null, {}]);
    expect(cached?.pages[0]?.items).toHaveLength(1);
    expect(cached?.pages[0]?.items[0]?.id).toBe('dec-old');
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

  it('surfaces SCAN_IN_PROGRESS conflict envelope as a toast', async () => {
    globalThis.fetch = vi.fn(async () =>
      jsonResp(
        { code: 'SCAN_IN_PROGRESS', error: 'scan already running', instance: 'alpha' },
        409,
      ),
    ) as typeof fetch;
    const { Wrapper } = wrap();
    const { result } = renderHook(() => useRescanDecision(), { wrapper: Wrapper });
    result.current.mutate({ decisionId: 'dec-old' });
    await waitFor(() => expect(result.current.isError).toBe(true));
    // ApiError.message is the parsed `error` field (per lib/api.ts).
    expect(toastError).toHaveBeenCalledWith('scan already running');
  });
});
