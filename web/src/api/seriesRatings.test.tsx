import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import {
  useSeriesRatings,
  seriesRatingsQueryKey,
  isRatingsRevalidating,
  nextRatingsPollDelay,
  RATINGS_MAX_ATTEMPTS,
  type SeriesRatingsResponse,
} from './seriesRatings';

const mockApi = vi.fn();
vi.mock('@/lib/api', async () => {
  const actual = await vi.importActual<typeof import('@/lib/api')>('@/lib/api');
  return { ...actual, api: (path: string) => mockApi(path) };
});

function wrapper() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
  return ({ children }: { children: React.ReactNode }) => (
    <QueryClientProvider client={qc}>{children}</QueryClientProvider>
  );
}

describe('useSeriesRatings', () => {
  beforeEach(() => mockApi.mockReset());

  it('builds the ratings URL from seriesId (no lang param)', async () => {
    mockApi.mockResolvedValueOnce({ sources: { tmdb: 'fresh' } });
    const { result } = renderHook(
      () => useSeriesRatings({ seriesId: 42 }),
      { wrapper: wrapper() },
    );
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(mockApi).toHaveBeenCalledWith('/series/42/ratings');
  });

  it('disables the query when seriesId is missing', () => {
    renderHook(
      () => useSeriesRatings({ seriesId: undefined }),
      { wrapper: wrapper() },
    );
    expect(mockApi).not.toHaveBeenCalled();
  });

  it('exposes a stable query key (id only, no lang)', () => {
    expect(seriesRatingsQueryKey(42)).toEqual(['series-ratings', 42]);
  });
});

describe('isRatingsRevalidating', () => {
  it('is true when any source is revalidating', () => {
    expect(isRatingsRevalidating({ sources: { tmdb: 'revalidating', omdb: 'fresh' } })).toBe(true);
  });

  it('is true when any source is pending', () => {
    expect(isRatingsRevalidating({ sources: { tmdb: 'fresh', omdb: 'pending' } })).toBe(true);
  });

  it('is false when all sources are terminal (fresh/unavailable)', () => {
    expect(isRatingsRevalidating({ sources: { tmdb: 'fresh', omdb: 'unavailable' } })).toBe(false);
  });

  it('is false for undefined data / absent sources', () => {
    expect(isRatingsRevalidating(undefined)).toBe(false);
    expect(isRatingsRevalidating({})).toBe(false);
  });
});

describe('nextRatingsPollDelay (bounded backoff ladder)', () => {
  const revalidating: SeriesRatingsResponse = { sources: { tmdb: 'revalidating' } };

  it('walks the 3s → 6s → 12s ladder while revalidating', () => {
    expect(nextRatingsPollDelay(revalidating, 0)).toBe(3_000);
    expect(nextRatingsPollDelay(revalidating, 1)).toBe(6_000);
    expect(nextRatingsPollDelay(revalidating, 2)).toBe(12_000);
  });

  it('is BOUNDED — returns false once the cap is reached even while still revalidating', () => {
    expect(nextRatingsPollDelay(revalidating, RATINGS_MAX_ATTEMPTS)).toBe(false);
    expect(nextRatingsPollDelay(revalidating, RATINGS_MAX_ATTEMPTS + 5)).toBe(false);
  });

  it('stops immediately on a terminal status (all fresh/unavailable)', () => {
    const terminal: SeriesRatingsResponse = { sources: { tmdb: 'fresh', omdb: 'unavailable' } };
    expect(nextRatingsPollDelay(terminal, 0)).toBe(false);
  });
});

describe('useSeriesRatings — F-08 per-series backoff reset', () => {
  beforeEach(() => {
    mockApi.mockReset();
    vi.useFakeTimers();
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it('resets the backoff ladder when seriesId changes so a new series re-polls', async () => {
    // Always "revalidating" ⇒ the hook wants to re-poll until its per-instance
    // cap. If the counter were NOT reset on id-change, series 2 would inherit
    // series 1's exhausted counter and never re-poll.
    mockApi.mockResolvedValue({ sources: { tmdb: 'revalidating' } });
    const qc = new QueryClient({
      defaultOptions: { queries: { retry: false, gcTime: 0 } },
    });
    const w = ({ children }: { children: React.ReactNode }) => (
      <QueryClientProvider client={qc}>{children}</QueryClientProvider>
    );

    const { rerender } = renderHook(
      ({ id }: { id: number }) => useSeriesRatings({ seriesId: id }),
      { wrapper: w, initialProps: { id: 1 } },
    );

    const callsFor = (p: string) =>
      mockApi.mock.calls.filter(([arg]) => arg === p).length;

    // Initial fetch + walk the full 3s → 6s → 12s ladder for series 1.
    await vi.advanceTimersByTimeAsync(0);
    await vi.advanceTimersByTimeAsync(3_000);
    await vi.advanceTimersByTimeAsync(6_000);
    await vi.advanceTimersByTimeAsync(12_000);
    const series1After = callsFor('/series/1/ratings');
    expect(series1After).toBeGreaterThan(1); // it DID re-poll

    // Cap reached ⇒ bounded: further time advances must NOT poll series 1 again.
    await vi.advanceTimersByTimeAsync(60_000);
    expect(callsFor('/series/1/ratings')).toBe(series1After);

    // Navigate to series 2 — the effect keyed on seriesId resets the ladder.
    rerender({ id: 2 });
    await vi.advanceTimersByTimeAsync(0);
    await vi.advanceTimersByTimeAsync(3_000);
    await vi.advanceTimersByTimeAsync(6_000);
    // Ladder restarted from 0 ⇒ series 2 re-polls (initial fetch + ≥1 re-poll).
    // Without the reset this would be exactly 1 (never re-polls).
    expect(callsFor('/series/2/ratings')).toBeGreaterThan(1);
  });
});
