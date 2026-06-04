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
  it('maps snake_case wire to camelCase + narrows mode', async () => {
    globalThis.fetch = vi.fn(async () =>
      jsonResp({ mode: 'basic', local_bypass: true }),
    ) as typeof fetch;
    await expect(getAuthConfig()).resolves.toEqual({
      mode: 'basic', localBypass: true, oidcReady: false,
    });
  });

  it('falls back to forms on unknown mode', async () => {
    globalThis.fetch = vi.fn(async () =>
      jsonResp({ mode: 'unknown_mode', local_bypass: false }),
    ) as typeof fetch;
    await expect(getAuthConfig()).resolves.toEqual({
      mode: 'forms', localBypass: false, oidcReady: false,
    });
  });

  it('maps mode=oidc and includes loginUrl when present', async () => {
    globalThis.fetch = vi.fn(async () =>
      jsonResp({ mode: 'oidc', local_bypass: false, oidc_ready: true, login_url: '/api/v1/auth/oidc/start' }),
    ) as typeof fetch;
    await expect(getAuthConfig()).resolves.toEqual({
      mode: 'oidc', localBypass: false, oidcReady: true, loginUrl: '/api/v1/auth/oidc/start',
    });
  });

  it('defaults localBypass to false when field absent', async () => {
    globalThis.fetch = vi.fn(async () =>
      jsonResp({ mode: 'none' }),
    ) as typeof fetch;
    await expect(getAuthConfig()).resolves.toEqual({
      mode: 'none', localBypass: false, oidcReady: false,
    });
  });

  it('decodes oidc_ready from wire', async () => {
    globalThis.fetch = vi.fn(async () =>
      jsonResp({ mode: 'forms', local_bypass: false, oidc_ready: true, login_url: '/api/v1/auth/oidc/start' }),
    ) as typeof fetch;
    await expect(getAuthConfig()).resolves.toMatchObject({
      oidcReady: true, loginUrl: '/api/v1/auth/oidc/start',
    });
  });

  it('defaults oidcReady to false when absent', async () => {
    globalThis.fetch = vi.fn(async () =>
      jsonResp({ mode: 'forms', local_bypass: false }),
    ) as typeof fetch;
    await expect(getAuthConfig()).resolves.toMatchObject({ oidcReady: false });
  });
});

describe('useAuthConfig()', () => {
  it('resolves to AuthConfig on success', async () => {
    globalThis.fetch = vi.fn(async () =>
      jsonResp({ mode: 'forms', local_bypass: false }),
    ) as typeof fetch;
    const qc = makeQC();
    const { result } = renderHook(() => useAuthConfig(), { wrapper: wrap(qc) });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(result.current.data).toEqual({ mode: 'forms', localBypass: false, oidcReady: false });
  });

  it('exposes error state on 5xx', async () => {
    globalThis.fetch = vi.fn(async () =>
      jsonResp({ error: 'boom' }, 503),
    ) as typeof fetch;
    const qc = makeQC();
    const { result } = renderHook(() => useAuthConfig(), { wrapper: wrap(qc) });
    await waitFor(() => expect(result.current.isError).toBe(true));
  });
});
