import { useQuery, type UseQueryResult, keepPreviousData } from '@tanstack/react-query';
import { ApiError, api } from '@/lib/api';

// TODO(orchestrator): swap to codegen types from @/api/schema once
// the post-054a swag regeneration lands. Local mirror today.
// `title` is optional on the wire (omitempty + legacy backends pre-054c)
// — the chip grid treats an empty string as "fall back to the plain
// 'Episode N' tooltip" rather than failing to render.
export interface SeasonEpisodeItem {
  readonly number: number;
  readonly title?: string;
  readonly monitored: boolean;
  readonly has_file: boolean;
  readonly aired: boolean;
  readonly air_date_utc: string;
}

export interface SeasonEpisodeList {
  readonly items: readonly SeasonEpisodeItem[];
  readonly total: number;
  readonly have: number;
  readonly miss: number;
}

export type EpisodeState = 'have' | 'miss' | 'upcoming';

// Map one episode to its UI state. Mirrors the backend's `miss`
// classification: monitored + aired + !has_file. Unmonitored aired
// episodes with no file render as `upcoming` to avoid alarming the
// operator with "missing" copy for episodes they explicitly chose
// not to fetch.
export function episodeState(e: SeasonEpisodeItem): EpisodeState {
  if (e.has_file) return 'have';
  if (e.monitored && e.aired) return 'miss';
  return 'upcoming';
}

function buildPath(seriesId: number, seasonNumber: number): string {
  return `/series/${seriesId}/seasons/${seasonNumber}/episodes`;
}

export function useSeasonEpisodes(
  seriesId: number | undefined,
  seasonNumber: number | null,
): UseQueryResult<SeasonEpisodeList, ApiError> {
  const enabled = Boolean(seriesId) && seasonNumber !== null;
  return useQuery<SeasonEpisodeList, ApiError>({
    queryKey: ['queue-season-episodes', seriesId ?? 0, seasonNumber ?? -1] as const,
    queryFn: () => api<SeasonEpisodeList>(buildPath(seriesId!, seasonNumber!)),
    enabled,
    staleTime: 30_000,
    refetchInterval: enabled ? 60_000 : false,
    refetchOnWindowFocus: false,
    placeholderData: keepPreviousData,
  });
}
