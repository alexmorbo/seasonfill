import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { useGrabDecision } from './grab-mutation';

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
      queries: { retry: false, gcTime: 0, staleTime: 0 },
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

describe('useGrabDecision()', () => {
  it('POSTs /decisions/:id/grab and invalidates 4 query keys on success', async () => {
    const captured: { url?: string; method?: string } = {};
    globalThis.fetch = vi.fn(async (u: RequestInfo | URL, init?: RequestInit) => {
      captured.url = typeof u === 'string' ? u : u.toString();
      if (init?.method) captured.method = init.method;
      return jsonResp({ id: 'g-1', instance: 'alpha', status: 'grabbed' });
    }) as typeof fetch;

    const { Wrapper, invalidations } = wrap();
    const { result } = renderHook(() => useGrabDecision(), { wrapper: Wrapper });
    result.current.mutate({ decisionId: 'dec-77' });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));

    expect(captured.url).toBe('/api/v1/decisions/dec-77/grab');
    expect(captured.method).toBe('POST');
    expect(toastSuccess).toHaveBeenCalledWith('Grab dispatched');
    expect(invalidations.flat()).toEqual(
      expect.arrayContaining(['decisions', 'grabs', 'scans', 'scan']),
    );
  });

  it.each([
    ['already grabbed: g-1', 409, 'Already grabbed'],
    ['blocked by cooldown: series:alpha/122/2', 409, 'On cooldown — try again later'],
    ['sonarr unavailable', 502, 'Grab failed: sonarr unavailable'],
  ])('error body %s → toast %s', async (body, status, expected) => {
    globalThis.fetch = vi.fn(async () => jsonResp({ error: body }, status)) as typeof fetch;
    const { Wrapper } = wrap();
    const { result } = renderHook(() => useGrabDecision(), { wrapper: Wrapper });
    result.current.mutate({ decisionId: 'dec-77' });
    await waitFor(() => expect(result.current.isError).toBe(true));
    expect(toastError).toHaveBeenCalledWith(expected);
  });
});
