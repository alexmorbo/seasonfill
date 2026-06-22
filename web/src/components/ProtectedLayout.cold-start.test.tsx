import { describe, it, expect, vi, beforeEach } from 'vitest';
import { renderHook, waitFor, act } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { PropsWithChildren } from 'react';
import { useEffect, useRef } from 'react';
import i18n from '@/i18n';
import { useMe, ME_QUERY_KEY } from '@/hooks/useMe';
import type { MeResponse } from '@/lib/me-types';

// This file tests the cold-start hydration behaviour without rendering
// the whole ProtectedLayout (which pulls in router + shell + every
// banner). The hydration logic is a single useEffect on
// `me.data?.preferred_language`. We exercise it via the hook + a small
// test component.

const BASE_ME: MeResponse = {
  id: 1,
  username: 'admin',
  email: 'admin@example.com',
  role: 'admin',
  auth_mode: 'forms',
  avatar_mode: 'auto',
  avatar_resolved_mode: 'gravatar',
  avatar_hash: 'abc',
  preferred_language: 'ru',
  idp_profile_url: null,
  oidc_subject: null,
  last_login_at: null,
};

function mkClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0, staleTime: 0 } },
  });
}

function wrapper(qc: QueryClient) {
  return function Wrapper({ children }: PropsWithChildren) {
    return <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
  };
}

// Mirror the hydration effect from ProtectedLayout so the unit test
// stays close to the real implementation while not pulling in the
// rest of the shell. If ProtectedLayout's effect drifts, this test
// should be updated alongside.

function useColdStartHydrate() {
  const me = useMe();
  const hydratedRef = useRef(false);
  useEffect(() => {
    if (hydratedRef.current) return;
    const pref = me.data?.preferred_language;
    if (!pref) return;
    if (i18n.resolvedLanguage === pref) {
      hydratedRef.current = true;
      return;
    }
    hydratedRef.current = true;
    void i18n.changeLanguage(pref);
  }, [me.data?.preferred_language]);
  return me;
}

beforeEach(() => {
  vi.restoreAllMocks();
  void i18n.changeLanguage('en');
});

describe('cold-start i18n hydration', () => {
  it('switches i18n.language to preferred_language on first /me resolve', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify(BASE_ME), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    );
    const qc = mkClient();
    const { result } = renderHook(() => useColdStartHydrate(), { wrapper: wrapper(qc) });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    await waitFor(() => expect(i18n.resolvedLanguage).toBe('ru'));
  });

  it('does not switch when preferred_language matches i18n.language', async () => {
    void i18n.changeLanguage('ru');
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ ...BASE_ME, preferred_language: 'ru' }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    );
    const qc = mkClient();
    const spy = vi.spyOn(i18n, 'changeLanguage');
    const { result } = renderHook(() => useColdStartHydrate(), { wrapper: wrapper(qc) });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    // changeLanguage should NOT have been called from the effect path
    // (only the manual beforeEach call counts, which happens before
    // the spy attaches).
    expect(spy).not.toHaveBeenCalledWith('ru');
  });

  it('does not switch when preferred_language is null', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ ...BASE_ME, preferred_language: null }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    );
    const qc = mkClient();
    const { result } = renderHook(() => useColdStartHydrate(), { wrapper: wrapper(qc) });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(i18n.resolvedLanguage).toBe('en');
  });

  it('only hydrates once per mount (subsequent /me updates do not re-switch)', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify(BASE_ME), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    );
    const qc = mkClient();
    const { result } = renderHook(() => useColdStartHydrate(), { wrapper: wrapper(qc) });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    await waitFor(() => expect(i18n.resolvedLanguage).toBe('ru'));

    // Operator manually switches back to 'en' (simulates header
    // switcher use). A subsequent cache update with preferred=ru must
    // NOT re-hydrate (user just chose 'en').
    await act(async () => {
      await i18n.changeLanguage('en');
      qc.setQueryData<MeResponse>(ME_QUERY_KEY, { ...BASE_ME, preferred_language: 'ru' });
    });
    expect(i18n.resolvedLanguage).toBe('en');
  });
});
