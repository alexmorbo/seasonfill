import { describe, it, expect, vi, beforeEach } from 'vitest';
import { renderHook, waitFor, act } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { useAddToRadarr, type AddToRadarrRequest } from './addToRadarr';

const mockApi = vi.fn();
vi.mock('@/lib/api', async () => {
  const actual = await vi.importActual<typeof import('@/lib/api')>('@/lib/api');
  return {
    ...actual,
    api: (path: string, init?: RequestInit) =>
      init === undefined ? mockApi(path) : mockApi(path, init),
  };
});

const wrap = () => {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 }, mutations: { retry: false } },
  });
  return ({ children }: { children: React.ReactNode }) =>
    <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
};

beforeEach(() => mockApi.mockReset());

describe('useAddToRadarr', () => {
  it('POSTs /discovery/add-to-radarr with the exact wire body', async () => {
    mockApi.mockResolvedValueOnce({
      radarr_movie_id: 42, instance_name: 'radarr-main', already_added: false,
    });
    const { result } = renderHook(() => useAddToRadarr(), { wrapper: wrap() });

    const body: AddToRadarrRequest = {
      instance_name: 'radarr-main',
      tmdb_id: 438631,
      quality_profile_id: 4,
      root_folder_path: '/movies',
      minimum_availability: 'released',
      search_on_add: true,
    };
    await act(async () => { await result.current.mutateAsync(body); });

    await waitFor(() => expect(mockApi).toHaveBeenCalled());
    expect(mockApi).toHaveBeenCalledWith('/discovery/add-to-radarr', {
      method: 'POST', body,
    });
  });

  it('surfaces the typed response', async () => {
    mockApi.mockResolvedValueOnce({
      radarr_movie_id: 7, instance_name: 'radarr-main', already_added: true,
    });
    const { result } = renderHook(() => useAddToRadarr(), { wrapper: wrap() });
    let res: unknown;
    await act(async () => {
      res = await result.current.mutateAsync({
        instance_name: 'radarr-main', tmdb_id: 1, quality_profile_id: 1,
        root_folder_path: '/movies',
      });
    });
    expect(res).toEqual({
      radarr_movie_id: 7, instance_name: 'radarr-main', already_added: true,
    });
  });
});
