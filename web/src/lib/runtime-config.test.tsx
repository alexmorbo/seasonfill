import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import {
  runtimeConfigKey, useRuntimeConfig, useUpdateRuntimeConfig,
  type RuntimeConfigWithMeta,
} from './runtime-config';
import { authConfigQueryKey } from './auth-config';

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
  it('PUTs unconditionally without If-Unmodified-Since (last-write-wins)', async () => {
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
    expect(captured.headers?.['If-Unmodified-Since']).toBeUndefined();
    expect(toastSuccess).toHaveBeenCalledWith('Settings saved');
  });

  it('invalidates authConfigQueryKey on save (mode change propagation)', async () => {
    globalThis.fetch = vi.fn(async () => jsonResp({ dry_run: false }, 200)) as typeof fetch;
    const qc = makeQC();
    // Seed the auth-config cache with a stale value so we can verify it
    // gets invalidated; we test via QueryClient state rather than a spy on
    // invalidateQueries because the latter is more brittle.
    qc.setQueryData(authConfigQueryKey, { oidcReady: false });
    const invalidateSpy = vi.spyOn(qc, 'invalidateQueries');
    const { result } = renderHook(() => useUpdateRuntimeConfig(), { wrapper: wrap(qc) });
    result.current.mutate({} as never);
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    const calls = invalidateSpy.mock.calls.map((c) => c[0]);
    expect(calls).toContainEqual({ queryKey: authConfigQueryKey });
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
});
