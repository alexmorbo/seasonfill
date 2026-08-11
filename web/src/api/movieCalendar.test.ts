import { describe, it, expect, vi, beforeEach } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { ReactNode } from 'react';
import { createElement } from 'react';
import { ApiError } from '@/lib/api';
import { useMovieCalendar, type MovieCalendarReport } from './movieCalendar';

const mockApi = vi.fn();
vi.mock('@/lib/api', async () => {
  const actual = await vi.importActual<typeof import('@/lib/api')>('@/lib/api');
  return { ...actual, api: (...args: unknown[]) => mockApi(...args) };
});

function wrapper() {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  });
  return ({ children }: { children: ReactNode }) =>
    createElement(QueryClientProvider, { client: qc }, children);
}

const report: MovieCalendarReport = {
  generated_at: '2026-08-11T00:00:00Z',
  from: '2026-08-01',
  to: '2026-08-31',
  days: [
    {
      date: '2026-08-15',
      events: [
        { date: '2026-08-15', milestone: 'theatrical', tmdb_id: 438631, movie_id: 7, title: 'Dune', poster: 'abc' },
      ],
    },
  ],
};

beforeEach(() => mockApi.mockReset());

describe('useMovieCalendar', () => {
  it('fetches /movies/calendar with the from/to querystring and parses the envelope', async () => {
    mockApi.mockResolvedValueOnce(report);
    const { result } = renderHook(
      () => useMovieCalendar({ from: '2026-08-01', to: '2026-08-31' }),
      { wrapper: wrapper() },
    );
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(mockApi).toHaveBeenCalledWith('/movies/calendar?from=2026-08-01&to=2026-08-31');
    expect(result.current.data?.days?.[0]?.events?.[0]?.title).toBe('Dune');
  });

  it('omits absent from/to (bare endpoint)', async () => {
    mockApi.mockResolvedValueOnce({ days: [] });
    const { result } = renderHook(() => useMovieCalendar({}), { wrapper: wrapper() });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(mockApi).toHaveBeenCalledWith('/movies/calendar');
  });

  it('does not fetch when enabled=false', () => {
    renderHook(() => useMovieCalendar({ enabled: false }), { wrapper: wrapper() });
    expect(mockApi).not.toHaveBeenCalled();
  });

  it('surfaces a 500 as an error', async () => {
    mockApi.mockRejectedValueOnce(new ApiError(500, 'boom'));
    const { result } = renderHook(() => useMovieCalendar({}), { wrapper: wrapper() });
    await waitFor(() => expect(result.current.isError).toBe(true));
    expect(result.current.error?.status).toBe(500);
  });
});
