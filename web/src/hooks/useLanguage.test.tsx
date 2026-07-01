import { describe, it, expect, vi, beforeEach } from 'vitest';
import { renderHook, act, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { PropsWithChildren } from 'react';
import i18n from '@/i18n';
import { useLanguage } from './useLanguage';
import { ME_QUERY_KEY } from './useMe';
import type { MeResponse } from '@/lib/me-types';

const BASE_ME: MeResponse = {
  id: 1,
  username: 'admin',
  email: 'admin@example.com',
  role: 'admin',
  auth_mode: 'forms',
  avatar_mode: 'auto',
  avatar_resolved_mode: 'gravatar',
  avatar_hash: 'abc',
  preferred_language: 'en-US',
  idp_profile_url: null,
  oidc_subject: null,
  last_login_at: null,
};

function mkClient() {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: Infinity, staleTime: 0 } },
  });
  qc.setQueryData(ME_QUERY_KEY, BASE_ME);
  return qc;
}

function wrapper(qc: QueryClient) {
  return function Wrapper({ children }: PropsWithChildren) {
    return <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
  };
}

beforeEach(() => {
  vi.restoreAllMocks();
  void i18n.changeLanguage('en-US');
  window.localStorage.clear();
});

describe('useLanguage', () => {
  it('reads current from useMe cache (preferred_language)', () => {
    const qc = mkClient();
    const { result } = renderHook(() => useLanguage(), { wrapper: wrapper(qc) });
    expect(result.current.current).toBe('en-US');
  });

  it('setLanguage optimistically updates cache + calls i18n + writes localStorage + fires PATCH', async () => {
    const qc = mkClient();
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ ...BASE_ME, preferred_language: 'ru-RU' }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    );
    const { result } = renderHook(() => useLanguage(), { wrapper: wrapper(qc) });

    await act(async () => {
      await result.current.setLanguage('ru-RU');
    });

    // Optimistic cache update
    const cached = qc.getQueryData<MeResponse>(ME_QUERY_KEY);
    expect(cached?.preferred_language).toBe('ru-RU');
    // i18n switched
    expect(i18n.resolvedLanguage).toBe('ru-RU');
    // localStorage
    expect(window.localStorage.getItem('seasonfill.lang')).toBe('ru-RU');
    // PATCH wire
    const call = fetchSpy.mock.calls.find(
      (c) => String(c[0]).endsWith('/me/settings'),
    );
    expect(call).toBeDefined();
    expect((call?.[1] as RequestInit | undefined)?.method).toBe('PATCH');
    expect((call?.[1] as RequestInit | undefined)?.body).toContain(
      '"preferred_language":"ru-RU"',
    );
  });

  it('rolls back optimistic state + reverts i18n on PATCH failure', async () => {
    const qc = mkClient();
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ error: 'boom' }), {
        status: 500,
        headers: { 'Content-Type': 'application/json' },
      }),
    );
    const { result } = renderHook(() => useLanguage(), { wrapper: wrapper(qc) });

    await act(async () => {
      await result.current.setLanguage('ru-RU');
    });

    // Optimistic update was reverted
    const cached = qc.getQueryData<MeResponse>(ME_QUERY_KEY);
    expect(cached?.preferred_language).toBe('en-US');
    // i18n was reverted
    await waitFor(() => expect(i18n.resolvedLanguage).toBe('en-US'));
  });

  it('ignores unsupported language codes', async () => {
    const qc = mkClient();
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response('{}', { status: 200 }),
    );
    const { result } = renderHook(() => useLanguage(), { wrapper: wrapper(qc) });

    await act(async () => {
      await result.current.setLanguage('xx-XX');
    });
    expect(fetchSpy).not.toHaveBeenCalled();
    expect(i18n.resolvedLanguage).toBe('en-US');
  });
});
