import { describe, it, expect, vi, beforeEach } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { PropsWithChildren } from 'react';
import { ApiError } from '@/lib/api';
import { useMe, ME_QUERY_KEY } from './useMe';
import type { MeResponse } from '@/lib/me-types';

const ADMIN_PAYLOAD: MeResponse = {
  id: 1,
  username: 'admin',
  email: 'admin@example.com',
  role: 'admin',
  auth_mode: 'forms',
  avatar_mode: 'auto',
  avatar_resolved_mode: 'gravatar',
  avatar_hash: '0bc83cb571cd1c50ba6f3e8a78ef1346',
  preferred_language: 'ru',
  idp_profile_url: null,
  oidc_subject: null,
  last_login_at: '2026-06-22T20:30:00Z',
};

function wrapper(qc: QueryClient) {
  return function Wrapper({ children }: PropsWithChildren) {
    return <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
  };
}

function mkClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0, staleTime: 0 } },
  });
}

beforeEach(() => {
  vi.restoreAllMocks();
});

describe('useMe', () => {
  it('returns the admin envelope on 200', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify(ADMIN_PAYLOAD), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    );
    const qc = mkClient();
    const { result } = renderHook(() => useMe(), { wrapper: wrapper(qc) });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(result.current.data).toEqual(ADMIN_PAYLOAD);
    // The cache must live under ["me"] — N-7c relies on it.
    expect(qc.getQueryData(ME_QUERY_KEY)).toEqual(ADMIN_PAYLOAD);
  });

  it('propagates 401 as an ApiError (the global wrapper handles the redirect)', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(null, { status: 401 }),
    );
    // Suppress the JSDOM "navigation not implemented" warning when api()
    // attempts window.location.assign — happy-dom no-ops it.
    const qc = mkClient();
    const { result } = renderHook(() => useMe(), { wrapper: wrapper(qc) });
    await waitFor(() => expect(result.current.isError).toBe(true));
    expect(result.current.error).toBeInstanceOf(ApiError);
    expect(result.current.error?.status).toBe(401);
  });

  it('refetch hits /api/v1/me again', async () => {
    const spy = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify(ADMIN_PAYLOAD), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    );
    const qc = mkClient();
    const { result } = renderHook(() => useMe(), { wrapper: wrapper(qc) });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    const callsBefore = spy.mock.calls.length;
    await result.current.refetch();
    expect(spy.mock.calls.length).toBeGreaterThan(callsBefore);
  });
});
