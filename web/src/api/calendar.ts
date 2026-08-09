import { useQuery, type UseQueryResult } from '@tanstack/react-query';
import { ApiError, api } from '@/lib/api';
import type { components } from '@/api/schema';

// S2 wire types — GET /api/v1/calendar → dto.CalendarDTO. All fields optional
// in the generated schema; the FE treats a missing array as [] and a missing
// milestone/state as null.
export type CalendarReport = components['schemas']['dto.CalendarDTO'];
export type CalendarDay = components['schemas']['dto.CalendarDayDTO'];
export type CalendarEvent = components['schemas']['dto.CalendarEventDTO'];

export interface CalendarParams {
  from?: string; // YYYY-MM-DD
  to?: string; // YYYY-MM-DD
  scope?: 'library' | 'followed' | 'all';
  instance?: string;
  onlyLibrary?: boolean;
  onlyPremieres?: boolean;
  lang?: string;
}

function toQuery(p: CalendarParams): string {
  const qs = new URLSearchParams();
  if (p.from) qs.set('from', p.from);
  if (p.to) qs.set('to', p.to);
  if (p.scope) qs.set('scope', p.scope);
  if (p.instance) qs.set('instance', p.instance);
  if (p.onlyLibrary) qs.set('only-library', 'true');
  if (p.onlyPremieres) qs.set('only-premieres', 'true');
  if (p.lang) qs.set('lang', p.lang);
  const s = qs.toString();
  return s ? `?${s}` : '';
}

// useCalendar fetches the release calendar. staleTime keeps page bounces from
// re-running the windowed DB read; no refetchInterval (not a live monitor).
export function useCalendar(p: CalendarParams): UseQueryResult<CalendarReport, ApiError> {
  return useQuery<CalendarReport, ApiError>({
    queryKey: ['calendar', p] as const,
    queryFn: () => api<CalendarReport>(`/calendar${toQuery(p)}`),
    staleTime: 30_000,
    refetchOnWindowFocus: false,
  });
}
