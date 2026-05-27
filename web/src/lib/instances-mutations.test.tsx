import { describe, expect, it, vi, afterEach, beforeEach } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import {
  instanceDetailKey,
  useCreateInstance,
  useDeleteInstance,
  useTestInstance,
  useUpdateInstance,
} from './instances-mutations';

const toastSuccess = vi.fn();
const toastError = vi.fn();
const toastMessage = vi.fn();
vi.mock('sonner', () => ({
  toast: {
    success: (m: string) => toastSuccess(m),
    error: (m: string) => toastError(m),
    message: (m: string) => toastMessage(m),
  },
}));

function makeQC() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0, staleTime: 0 },
      mutations: { retry: false },
    },
  });
}

function wrap(qc: QueryClient) {
  return ({ children }: { children: React.ReactNode }) => (
    <QueryClientProvider client={qc}>{children}</QueryClientProvider>
  );
}

const jsonResp = (body: unknown, status = 200, headers: Record<string, string> = {}) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json', ...headers },
  });

const origFetch = globalThis.fetch;
beforeEach(() => {
  toastSuccess.mockClear();
  toastError.mockClear();
  toastMessage.mockClear();
  Object.defineProperty(window, 'location', {
    writable: true, value: { pathname: '/settings', assign: vi.fn() },
  });
});
afterEach(() => { globalThis.fetch = origFetch; });

describe('useCreateInstance()', () => {
  it('POSTs /instances and toasts on success', async () => {
    const captured: { url?: string | undefined; method?: string | undefined } = {};
    globalThis.fetch = vi.fn(async (u: RequestInfo | URL, init?: RequestInit) => {
      captured.url = typeof u === 'string' ? u : u.toString();
      captured.method = init?.method;
      return jsonResp({ name: 'alpha', api_key: '***' }, 201);
    }) as typeof fetch;

    const qc = makeQC();
    const { result } = renderHook(() => useCreateInstance(), { wrapper: wrap(qc) });
    result.current.mutate({
      body: { name: 'alpha', url: 'http://x', api_key: 'k' } as never,
    });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(captured.url).toBe('/api/v1/instances');
    expect(captured.method).toBe('POST');
    expect(toastSuccess).toHaveBeenCalledWith('Instance created');
  });
});

describe('useUpdateInstance()', () => {
  it('sends If-Unmodified-Since from cached Last-Modified', async () => {
    const captured: { headers?: Record<string, string> | undefined } = {};
    globalThis.fetch = vi.fn(async (_u: RequestInfo | URL, init?: RequestInit) => {
      captured.headers = init?.headers as Record<string, string> | undefined;
      return jsonResp({ name: 'alpha', api_key: '***' }, 200);
    }) as typeof fetch;

    const qc = makeQC();
    qc.setQueryData(instanceDetailKey('alpha'), {
      detail: { name: 'alpha' },
      lastModified: 'Wed, 21 Oct 2025 07:28:00 GMT',
    });
    const { result } = renderHook(() => useUpdateInstance(), { wrapper: wrap(qc) });
    result.current.mutate({
      name: 'alpha',
      body: { name: 'alpha', url: 'http://x', api_key: '' } as never,
    });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(captured.headers?.['If-Unmodified-Since']).toBe(
      'Wed, 21 Oct 2025 07:28:00 GMT',
    );
  });

  it('412 triggers stale-write toast + invalidates both queries', async () => {
    globalThis.fetch = vi.fn(async () =>
      jsonResp({ error: 'stale', code: 'STALE_WRITE' }, 412),
    ) as typeof fetch;
    const qc = makeQC();
    const invalidateSpy = vi.spyOn(qc, 'invalidateQueries');
    const { result } = renderHook(() => useUpdateInstance(), { wrapper: wrap(qc) });
    result.current.mutate({
      name: 'alpha',
      body: { name: 'alpha', url: 'http://x', api_key: '' } as never,
    });
    await waitFor(() => expect(result.current.isError).toBe(true));
    expect(toastMessage).toHaveBeenCalledWith(
      'Settings changed by another tab — reloaded',
    );
    expect(invalidateSpy).toHaveBeenCalled();
  });
});

describe('useDeleteInstance()', () => {
  it('DELETEs and toasts on 204', async () => {
    const captured: { method?: string | undefined } = {};
    globalThis.fetch = vi.fn(async (_u: RequestInfo | URL, init?: RequestInit) => {
      captured.method = init?.method;
      return new Response(null, { status: 204 });
    }) as typeof fetch;
    const qc = makeQC();
    const { result } = renderHook(() => useDeleteInstance(), { wrapper: wrap(qc) });
    result.current.mutate({ name: 'alpha' });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(captured.method).toBe('DELETE');
    expect(toastSuccess).toHaveBeenCalledWith('Instance deleted');
  });

  it('invalidates scans/decisions/grabs caches on success', async () => {
    globalThis.fetch = vi.fn(async () => new Response(null, { status: 204 })) as typeof fetch;
    const qc = makeQC();
    const invalidateSpy = vi.spyOn(qc, 'invalidateQueries');
    const { result } = renderHook(() => useDeleteInstance(), { wrapper: wrap(qc) });
    result.current.mutate({ name: 'alpha' });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    const keys = invalidateSpy.mock.calls.map((c) => (c[0] as { queryKey: unknown[] }).queryKey[0]);
    expect(keys).toEqual(expect.arrayContaining(['instances', 'scans', 'decisions', 'grabs']));
  });

  it('non-204 error surfaces a delete-failed toast', async () => {
    globalThis.fetch = vi.fn(async () =>
      jsonResp({ error: 'internal error' }, 500),
    ) as typeof fetch;
    const qc = makeQC();
    const { result } = renderHook(() => useDeleteInstance(), { wrapper: wrap(qc) });
    result.current.mutate({ name: 'alpha' });
    await waitFor(() => expect(result.current.isError).toBe(true));
    expect(toastError).toHaveBeenCalledWith(expect.stringContaining('Delete failed'));
  });
});

describe('useTestInstance()', () => {
  it('resolves with the probe response and fires NO toast on ok=true', async () => {
    globalThis.fetch = vi.fn(async () =>
      jsonResp({ ok: true, version: '4.0.0.999' }, 200),
    ) as typeof fetch;
    const qc = makeQC();
    const { result } = renderHook(() => useTestInstance(), { wrapper: wrap(qc) });
    result.current.mutate({ url: 'http://x', api_key: 'k' });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    // Happy path: NO toast — the dialog owns the inline feedback channel.
    expect(toastSuccess).not.toHaveBeenCalled();
    expect(toastError).not.toHaveBeenCalled();
  });

  it('resolves with the probe response and fires NO toast on ok=false', async () => {
    globalThis.fetch = vi.fn(async () =>
      jsonResp({ ok: false, reason: 'authentication failed' }, 200),
    ) as typeof fetch;
    const qc = makeQC();
    const { result } = renderHook(() => useTestInstance(), { wrapper: wrap(qc) });
    result.current.mutate({ url: 'http://x', api_key: 'wrong' });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    // ok=false is still a successful HTTP call; no toast — inline only.
    expect(toastSuccess).not.toHaveBeenCalled();
    expect(toastError).not.toHaveBeenCalled();
  });

  it('toasts timeout on 504', async () => {
    globalThis.fetch = vi.fn(async () =>
      jsonResp({ error: 'timeout', code: 'PROBE_TIMEOUT' }, 504),
    ) as typeof fetch;
    const qc = makeQC();
    const { result } = renderHook(() => useTestInstance(), { wrapper: wrap(qc) });
    result.current.mutate({ url: 'http://x', api_key: 'k' });
    await waitFor(() => expect(result.current.isError).toBe(true));
    expect(toastError).toHaveBeenCalledWith(
      'Timed out — Sonarr did not respond',
    );
  });
});
