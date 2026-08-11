import { useQuery, type UseQueryResult } from '@tanstack/react-query';
import { ApiError, api } from '@/lib/api';
import type { components } from '@/api/schema';

// Ф6-R-6b wire types — GET /api/v1/movies/calendar → dto.MovieCalendarDTO.
// Mirrors @/api/calendar. Movie events carry NO series_id/season/episode; a
// movie event is keyed by tmdb_id with a `milestone` of
// theatrical|digital|physical and a RAW canon poster_asset path (rendered via
// the same /api/v1/media/{path} handler as series posters).
export type MovieCalendarReport = components['schemas']['dto.MovieCalendarDTO'];
export type MovieCalendarDay = components['schemas']['dto.MovieCalendarDayDTO'];
export type MovieCalendarEvent = components['schemas']['dto.MovieCalendarEventDTO'];

export interface MovieCalendarParams {
  from?: string; // YYYY-MM-DD
  to?: string; // YYYY-MM-DD
  // Gate the fetch so the TV-only calendar view never hits /movies/calendar.
  // Defaults to true when omitted (mirrors a plain useCalendar caller).
  enabled?: boolean;
}

function toQuery(p: MovieCalendarParams): string {
  const qs = new URLSearchParams();
  if (p.from) qs.set('from', p.from);
  if (p.to) qs.set('to', p.to);
  const s = qs.toString();
  return s ? `?${s}` : '';
}

// useMovieCalendar fetches the movie release calendar (3 milestones). staleTime
// keeps page bounces from re-running the windowed DB read; no refetchInterval.
export function useMovieCalendar(
  p: MovieCalendarParams,
): UseQueryResult<MovieCalendarReport, ApiError> {
  return useQuery<MovieCalendarReport, ApiError>({
    queryKey: ['movie-calendar', p] as const,
    queryFn: () => api<MovieCalendarReport>(`/movies/calendar${toQuery(p)}`),
    enabled: p.enabled ?? true,
    staleTime: 30_000,
    refetchOnWindowFocus: false,
  });
}
