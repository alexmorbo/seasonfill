import { useEffect, useRef } from 'react';
import { useQuery, type UseQueryResult } from '@tanstack/react-query';
import { api } from '@/lib/api';
import type { components } from '@/api/schema';

export type SeriesRatingsResponse = components['schemas']['dto.SeriesRatingsResponse'];
export type SeriesRatingsSources = components['schemas']['dto.SeriesRatingsSources'];

export interface UseSeriesRatingsParams {
  readonly seriesId: number | undefined;
}

// The /ratings endpoint has NO lang param — the query key carries only the
// canonical series id (same id the sibling detail hooks pass).
export function seriesRatingsQueryKey(
  seriesId: number,
): readonly ['series-ratings', number] {
  return ['series-ratings', seriesId] as const;
}

// F-05 (net-new): bounded backoff ladder for the stale-while-revalidate
// re-poll. This is DISTINCT from series.ts's fixed-interval tick-cap poll —
// there the delay is a constant (POLL_MS); here it grows 3s → 6s → 12s and
// stops after RATINGS_MAX_ATTEMPTS re-polls.
export const RATINGS_BACKOFF_MS: readonly number[] = [3_000, 6_000, 12_000];
export const RATINGS_MAX_ATTEMPTS = 3;

// A source is still "in flight" (BE actualizing in the background) while it is
// revalidating a stale value or awaiting a first fetch. `fresh` and
// `unavailable` are terminal — nothing more will land, so polling stops.
const IN_FLIGHT = new Set<string>(['revalidating', 'pending']);

export function isRatingsRevalidating(
  resp: SeriesRatingsResponse | undefined,
): boolean {
  const sources = resp?.sources;
  if (!sources) return false;
  return IN_FLIGHT.has(sources.tmdb ?? '') || IN_FLIGHT.has(sources.omdb ?? '');
}

// Pure poll-delay decision — mirrors series.ts's `isMissingLang` (a pure,
// directly unit-tested helper) rather than an impure refetchInterval closure.
// Returns the ms until the next re-poll, or false to stop. BOUNDED: once
// `attempt` reaches RATINGS_MAX_ATTEMPTS we stop even if a source is still
// revalidating — an unbounded poll is unacceptable.
export function nextRatingsPollDelay(
  resp: SeriesRatingsResponse | undefined,
  attempt: number,
): number | false {
  if (!isRatingsRevalidating(resp)) return false;
  if (attempt >= RATINGS_MAX_ATTEMPTS) return false;
  return RATINGS_BACKOFF_MS[attempt] ?? RATINGS_BACKOFF_MS[RATINGS_BACKOFF_MS.length - 1] ?? 12_000;
}

export function useSeriesRatings({
  seriesId,
}: UseSeriesRatingsParams): UseQueryResult<SeriesRatingsResponse> {
  const ready = typeof seriesId === 'number' && seriesId > 0;
  // Attempt counter lives OUTSIDE the query callback: TanStack's
  // refetchInterval(query) must stay pure on the data slice (same discipline
  // as series.ts's tickRef).
  const attemptRef = useRef<number>(0);
  // F-08: the counter is per-hook-instance, and this hook stays mounted across
  // series navigation (RatingsSection / SeriesHero keep one instance). Reset
  // the ladder whenever the id changes so a new series starts fresh — otherwise
  // it inherits the previous series' exhausted counter and never re-polls.
  useEffect(() => {
    attemptRef.current = 0;
  }, [seriesId]);
  return useQuery<SeriesRatingsResponse>({
    queryKey: ready
      ? seriesRatingsQueryKey(seriesId as number)
      : (['series-ratings', 0] as const),
    queryFn: () => api<SeriesRatingsResponse>(`/series/${seriesId}/ratings`),
    enabled: ready,
    staleTime: 30_000,
    refetchOnWindowFocus: false,
    refetchInterval: (query) => {
      const data = query.state.data;
      if (!isRatingsRevalidating(data)) {
        attemptRef.current = 0; // terminal transition → reset the ladder
        return false;
      }
      const delay = nextRatingsPollDelay(data, attemptRef.current);
      if (delay === false) return false; // cap reached — stop, keep what we have
      attemptRef.current += 1;
      return delay;
    },
  });
}
