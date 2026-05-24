import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import {
  runtimeConfigKey, useRuntimeConfig, useUpdateRuntimeConfig,
  type RuntimeConfigWithMeta,
} from './runtime-config';

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

describe('useRuntimeConfig()', () => {
  it('parses payload and captures Last-Modified', async () => {
    globalThis.fetch = vi.fn(async () =>
      jsonResp({ dry_run: true }, 200, { 'Last-Modified': 'Wed, 21 Oct 2025 07:28:00 GMT' }),
    ) as typeof fetch;
    const qc = makeQC();
    const { result } = renderHook(() => useRuntimeConfig(), { wrapper: wrap(qc) });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(result.current.data?.lastModified).toBe(
      'Wed, 21 Oct 2025 07:28:00 GMT',
    );
  });
});

describe('useUpdateRuntimeConfig()', () => {
  it('sends If-Unmodified-Since from cached Last-Modified and toasts on success', async () => {
    const captured: { headers?: Record<string, string> } = {};
    globalThis.fetch = vi.fn(async (_u: RequestInfo | URL, init?: RequestInit) => {
      captured.headers = init?.headers as Record<string, string>;
      return jsonResp({ dry_run: false }, 200);
    }) as typeof fetch;

    const qc = makeQC();
    const seed: RuntimeConfigWithMeta = {
      config: { dry_run: true } as never,
      lastModified: 'Wed, 21 Oct 2025 07:28:00 GMT',
    };
    qc.setQueryData(runtimeConfigKey, seed);

    const { result } = renderHook(() => useUpdateRuntimeConfig(), { wrapper: wrap(qc) });
    result.current.mutate({ dry_run: false } as never);
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(captured.headers?.['If-Unmodified-Since']).toBe(
      'Wed, 21 Oct 2025 07:28:00 GMT',
    );
    expect(toastSuccess).toHaveBeenCalledWith('Settings saved');
  });

  it('412 surfaces stale-write message and invalidates the query', async () => {
    globalThis.fetch = vi.fn(async () =>
      jsonResp({ error: 'stale', code: 'STALE_WRITE' }, 412),
    ) as typeof fetch;
    const qc = makeQC();
    const invalidateSpy = vi.spyOn(qc, 'invalidateQueries');
    const { result } = renderHook(() => useUpdateRuntimeConfig(), { wrapper: wrap(qc) });
    result.current.mutate({ dry_run: false } as never);
    await waitFor(() => expect(result.current.isError).toBe(true));
    expect(toastMessage).toHaveBeenCalledWith(
      'Settings changed by another tab — reloaded',
    );
    expect(invalidateSpy).toHaveBeenCalled();
  });

  it('400 surfaces the server message verbatim', async () => {
    globalThis.fetch = vi.fn(async () =>
      jsonResp({ error: 'invalid cron: foo', code: 'BAD_REQUEST' }, 400),
    ) as typeof fetch;
    const qc = makeQC();
    const { result } = renderHook(() => useUpdateRuntimeConfig(), { wrapper: wrap(qc) });
    result.current.mutate({} as never);
    await waitFor(() => expect(result.current.isError).toBe(true));
    expect(toastError).toHaveBeenCalledWith('invalid cron: foo');
  });

  it('PUT response Last-Modified is cached synchronously before invalidate', async () => {
    let putCalls = 0;
    const captured: { lastIUS?: string | undefined } = {};
    globalThis.fetch = vi.fn(async (_u: RequestInfo | URL, init?: RequestInit) => {
      if (init?.method === 'PUT') {
        putCalls += 1;
        const hdrs = (init.headers ?? {}) as Record<string, string>;
        captured.lastIUS = hdrs['If-Unmodified-Since'];
        const newLM = putCalls === 1
          ? 'Wed, 21 Oct 2025 07:30:00 GMT'
          : 'Wed, 21 Oct 2025 07:31:00 GMT';
        return jsonResp({ dry_run: putCalls === 1 }, 200, { 'Last-Modified': newLM });
      }
      // Background refetch — return the latest cached body.
      return jsonResp({ dry_run: false }, 200, {
        'Last-Modified': 'Wed, 21 Oct 2025 07:30:00 GMT',
      });
    }) as typeof fetch;

    const qc = makeQC();
    const seed: RuntimeConfigWithMeta = {
      config: { dry_run: true } as never,
      lastModified: 'Wed, 21 Oct 2025 07:28:00 GMT',
    };
    qc.setQueryData(runtimeConfigKey, seed);

    // Render both hooks so runtimeConfigKey has an active observer —
    // required to prevent gcTime=0 from evicting setQueryData writes
    // before the second mutate reads them.
    const { result } = renderHook(
      () => ({ update: useUpdateRuntimeConfig(), q: useRuntimeConfig() }),
      { wrapper: wrap(qc) },
    );

    // First PUT — should send seed's IUS (07:28).
    result.current.update.mutate({ dry_run: false } as never);
    await waitFor(() => expect(result.current.update.isSuccess).toBe(true));
    expect(captured.lastIUS).toBe('Wed, 21 Oct 2025 07:28:00 GMT');

    // setQueryData in onSuccess ran synchronously, so the cache must
    // already carry the PUT's Last-Modified before the background GET
    // from invalidateQueries can overwrite it.
    await waitFor(() => {
      const after = qc.getQueryData<RuntimeConfigWithMeta>(runtimeConfigKey);
      expect(after?.lastModified).toBe('Wed, 21 Oct 2025 07:30:00 GMT');
    });

    // Second PUT — should now use 07:30, not 07:28.
    result.current.update.mutate({ dry_run: true } as never);
    await waitFor(() => expect(putCalls).toBe(2));
    expect(captured.lastIUS).toBe('Wed, 21 Oct 2025 07:30:00 GMT');
  });
});
