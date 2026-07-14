import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { ReactNode } from 'react';
import { useAuthConfig, getAuthConfig } from './auth-config';

const origFetch = globalThis.fetch;
afterEach(() => { globalThis.fetch = origFetch; vi.restoreAllMocks(); });
beforeEach(() => {
  Object.defineProperty(window, 'location', {
    writable: true, value: { pathname: '/', assign: vi.fn() },
  });
});

function makeQC() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0, staleTime: 0 } },
  });
}

// Mirrors production retry (query-client.ts): 5xx/network retry up to 2.
// retryDelay:0 skips react-query's exponential backoff so waitFor stays fast.
// useAuthConfig no longer overrides retry, so the provider must supply it.
function makeRetryQC() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: 2, retryDelay: 0, gcTime: 0, staleTime: Infinity },
    },
  });
}

function wrap(qc: QueryClient) {
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={qc}>{children}</QueryClientProvider>
  );
}

const jsonResp = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status, headers: { 'Content-Type': 'application/json' },
  });

describe('getAuthConfig()', () => {
  it('maps oidc_ready from wire; defaults to false when absent', async () => {
    globalThis.fetch = vi.fn(async () =>
      jsonResp({ oidc_ready: false }),
    ) as typeof fetch;
    await expect(getAuthConfig()).resolves.toEqual({ oidcReady: false });
  });

  it('includes loginUrl when present', async () => {
    globalThis.fetch = vi.fn(async () =>
      jsonResp({ oidc_ready: true, login_url: '/api/v1/auth/oidc/start' }),
    ) as typeof fetch;
    await expect(getAuthConfig()).resolves.toEqual({
      oidcReady: true, loginUrl: '/api/v1/auth/oidc/start',
    });
  });

  it('decodes oidc_ready from wire', async () => {
    globalThis.fetch = vi.fn(async () =>
      jsonResp({ oidc_ready: true, login_url: '/api/v1/auth/oidc/start' }),
    ) as typeof fetch;
    await expect(getAuthConfig()).resolves.toMatchObject({
      oidcReady: true, loginUrl: '/api/v1/auth/oidc/start',
    });
  });

  it('defaults oidcReady to false when absent', async () => {
    globalThis.fetch = vi.fn(async () =>
      jsonResp({}),
    ) as typeof fetch;
    await expect(getAuthConfig()).resolves.toEqual({ oidcReady: false });
  });
});

describe('useAuthConfig()', () => {
  it('resolves to AuthConfig on success', async () => {
    globalThis.fetch = vi.fn(async () =>
      jsonResp({ oidc_ready: false }),
    ) as typeof fetch;
    const qc = makeQC();
    const { result } = renderHook(() => useAuthConfig(), { wrapper: wrap(qc) });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(result.current.data).toEqual({ oidcReady: false });
  });

  it('exposes error state on 5xx', async () => {
    globalThis.fetch = vi.fn(async () =>
      jsonResp({ error: 'boom' }, 503),
    ) as typeof fetch;
    const qc = makeQC();
    const { result } = renderHook(() => useAuthConfig(), { wrapper: wrap(qc) });
    await waitFor(() => expect(result.current.isError).toBe(true));
  });

  it('recovers oidcReady after a transient 5xx (retries then succeeds)', async () => {
    let calls = 0;
    globalThis.fetch = vi.fn(async () => {
      calls += 1;
      // First call fails like a pod-roll blip; the retry must succeed.
      if (calls === 1) return jsonResp({ error: 'unavailable' }, 503);
      return jsonResp({ oidc_ready: true, login_url: '/api/v1/auth/oidc/start' });
    }) as typeof fetch;
    const qc = makeRetryQC();
    const { result } = renderHook(() => useAuthConfig(), { wrapper: wrap(qc) });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(result.current.data?.oidcReady).toBe(true);
    expect(result.current.data?.loginUrl).toBe('/api/v1/auth/oidc/start');
    expect(calls).toBeGreaterThanOrEqual(2);
  });
});
