import { describe, expect, it, vi, beforeEach } from 'vitest';
import { renderHook, act, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { useUpdateTimezone } from './useTimezoneSetting';

function wrapper() {
  const qc = new QueryClient({ defaultOptions: { mutations: { retry: false } } });
  return ({ children }: { children: React.ReactNode }) => (
    <QueryClientProvider client={qc}>{children}</QueryClientProvider>
  );
}

beforeEach(() => {
  vi.restoreAllMocks();
});

describe('useUpdateTimezone', () => {
  it('PATCHes and returns the parsed state', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(
        JSON.stringify({ timezone: 'America/New_York', source: 'db', requires_restart: true }),
        { status: 200, headers: { 'Content-Type': 'application/json' } },
      ),
    );
    const { result } = renderHook(() => useUpdateTimezone(), { wrapper: wrapper() });
    let returned: unknown;
    await act(async () => {
      returned = await result.current.mutateAsync('America/New_York');
    });
    expect(fetchSpy).toHaveBeenCalledWith(
      '/api/v1/settings/timezone',
      expect.objectContaining({ method: 'PATCH' }),
    );
    expect(returned).toEqual({
      timezone: 'America/New_York', source: 'db', requiresRestart: true,
    });
    await waitFor(() => {
      expect(result.current.data).toEqual({
        timezone: 'America/New_York', source: 'db', requiresRestart: true,
      });
    });
  });

  it('surfaces backend 400 as the error message', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(
        JSON.stringify({ error: 'INVALID_TIMEZONE' }),
        { status: 400, headers: { 'Content-Type': 'application/json' } },
      ),
    );
    const { result } = renderHook(() => useUpdateTimezone(), { wrapper: wrapper() });
    await act(async () => {
      await expect(result.current.mutateAsync('Not/A/Zone')).rejects.toMatchObject({ status: 400 });
    });
  });
});
