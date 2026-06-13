import { useQuery, type UseQueryResult } from '@tanstack/react-query';
import { api } from '@/lib/api';
import type { components } from '@/api/schema';

export type PersonDetailResponse = components['schemas']['dto.PersonDetailResponse'];
export type PersonInfo = components['schemas']['dto.PersonInfo'];
export type LibraryCreditEntry = components['schemas']['dto.LibraryCreditEntry'];
export type OtherCreditEntry = components['schemas']['dto.OtherCreditEntry'];
export type PersonSyncInfo = components['schemas']['dto.SyncInfo'];

export type LibrarySort = 'recent' | 'episodes' | 'title';
export const LIBRARY_SORT_VALUES: readonly LibrarySort[] = ['recent', 'episodes', 'title'] as const;
export const PERSON_STUB_POLL_MS = 5_000;
export const PERSON_STUB_SOURCE = 'tmdb_person';

export interface UsePersonParams {
  readonly tmdbId: number | undefined;
  readonly lang?: string | undefined;
  readonly sort?: LibrarySort | undefined;
}

export function personQueryKey(
  tmdbId: number,
  lang: string,
  sort: LibrarySort,
): readonly [string, number, string, LibrarySort] {
  return ['person', tmdbId, lang, sort] as const;
}

export function isPersonStub(resp: PersonDetailResponse | undefined): boolean {
  return (resp?.degraded ?? []).includes(PERSON_STUB_SOURCE);
}

export function usePerson({
  tmdbId,
  lang,
  sort,
}: UsePersonParams): UseQueryResult<PersonDetailResponse> {
  const ready = typeof tmdbId === 'number' && tmdbId > 0 && Number.isFinite(tmdbId);
  const effectiveLang = lang ?? '';
  const effectiveSort: LibrarySort = sort ?? 'recent';
  return useQuery<PersonDetailResponse>({
    queryKey: ready
      ? personQueryKey(tmdbId as number, effectiveLang, effectiveSort)
      : (['person', 0, '', 'recent'] as const),
    queryFn: () => {
      const parts: string[] = [];
      if (effectiveLang) parts.push(`lang=${encodeURIComponent(effectiveLang)}`);
      // Always send sort so the server-side ordering matches the
      // queryKey. The server defaults to "recent" too — making it
      // explicit kills any "cached the wrong order" ambiguity.
      parts.push(`sort=${encodeURIComponent(effectiveSort)}`);
      const qs = `?${parts.join('&')}`;
      return api<PersonDetailResponse>(`/people/${tmdbId}${qs}`);
    },
    enabled: ready,
    staleTime: 30_000,
    refetchOnWindowFocus: false,
    // Function form: TanStack passes the live query state so we can
    // read the latest payload and decide whether to keep polling.
    // Returning `false` halts polling cleanly; returning a number
    // schedules the next tick. No setInterval, no manual unmount
    // cleanup — React unmount tears the query down.
    refetchInterval: (q) => {
      const data = q.state.data as PersonDetailResponse | undefined;
      return isPersonStub(data) ? PERSON_STUB_POLL_MS : false;
    },
  });
}
