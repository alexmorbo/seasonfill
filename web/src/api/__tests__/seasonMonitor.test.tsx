import { describe, it, expect, vi, beforeEach } from 'vitest';
import { renderHook, act } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import i18n from '@/i18n';
import { ApiError } from '@/lib/api';
import { useMonitorSeason } from '../seasonMonitor';

const mockApi = vi.fn();
vi.mock('@/lib/api', async () => {
  const actual = await vi.importActual<typeof import('@/lib/api')>('@/lib/api');
  return {
    ...actual,
    api: (path: string, init?: RequestInit) =>
      init === undefined ? mockApi(path) : mockApi(path, init),
  };
});

const toastSuccess = vi.fn();
const toastError = vi.fn();
vi.mock('sonner', () => ({
  toast: {
    success: (...a: unknown[]) => toastSuccess(...a),
    error: (...a: unknown[]) => toastError(...a),
  },
}));

const makeQc = () =>
  new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0 },
      mutations: { retry: false },
    },
  });

const wrapWith = (qc: QueryClient) =>
  ({ children }: { children: React.ReactNode }) =>
    <QueryClientProvider client={qc}>{children}</QueryClientProvider>;

const successResp = {
  instance: 'main', monitored: true, searched: true,
  season_number: 4, series_id: 42,
};

beforeEach(() => {
  mockApi.mockReset();
  toastSuccess.mockReset();
  toastError.mockReset();
});

describe('useMonitorSeason', () => {
  it('POSTs the monitor URL with { method:POST, body:{ search:true } }', async () => {
    mockApi.mockResolvedValueOnce(successResp);
    const { result } = renderHook(() => useMonitorSeason(), {
      wrapper: wrapWith(makeQc()),
    });
    await act(async () => {
      await result.current.mutateAsync({ instance: 'main', seriesId: 42, seasonNumber: 4 });
    });
    expect(mockApi).toHaveBeenCalledWith(
      '/instances/main/series/42/seasons/4/monitor',
      { method: 'POST', body: { search: true } },
    );
  });

  it('URL-encodes the instance name', async () => {
    mockApi.mockResolvedValueOnce(successResp);
    const { result } = renderHook(() => useMonitorSeason(), {
      wrapper: wrapWith(makeQc()),
    });
    await act(async () => {
      await result.current.mutateAsync({
        instance: 'my sonarr/4k', seriesId: 7, seasonNumber: 2,
      });
    });
    expect(mockApi).toHaveBeenCalledWith(
      '/instances/my%20sonarr%2F4k/series/7/seasons/2/monitor',
      { method: 'POST', body: { search: true } },
    );
  });

  it('invalidates the per-instance library + per-series seasons keys and toasts success', async () => {
    mockApi.mockResolvedValueOnce(successResp);
    const qc = makeQc();
    const spy = vi.spyOn(qc, 'invalidateQueries');
    const { result } = renderHook(() => useMonitorSeason(), {
      wrapper: wrapWith(qc),
    });
    await act(async () => {
      await result.current.mutateAsync({ instance: 'main', seriesId: 42, seasonNumber: 4 });
    });
    expect(spy).toHaveBeenCalledWith({ queryKey: ['series-library', 42, 'main'] });
    expect(spy).toHaveBeenCalledWith({ queryKey: ['series-seasons', 42] });
    expect(toastSuccess).toHaveBeenCalledWith(
      i18n.t('seriesDetail.seasons.requestQueued'),
    );
  });

  it('toasts the requestFailed message on an ApiError(502)', async () => {
    mockApi.mockRejectedValueOnce(
      new ApiError(502, 'sonarr_unreachable',
        { error: 'sonarr_unreachable', message: 'unreachable' }),
    );
    const { result } = renderHook(() => useMonitorSeason(), {
      wrapper: wrapWith(makeQc()),
    });
    await act(async () => {
      try {
        await result.current.mutateAsync({ instance: 'main', seriesId: 42, seasonNumber: 4 });
      } catch { /* mutation rejects — the hook surfaces the toast */ }
    });
    expect(toastError).toHaveBeenCalledWith(
      i18n.t('seriesDetail.seasons.requestFailed', { error: 'sonarr_unreachable' }),
    );
  });
});
