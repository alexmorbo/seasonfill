import { useQuery } from '@tanstack/react-query';
import { api } from '@/lib/api';
import type { components } from '@/api/schema';

export type SeriesDetailResponse = components['schemas']['dto.SeriesDetailResponse'];
export type SeriesHero = components['schemas']['dto.SeriesHero'];
export type LibraryStrip = components['schemas']['dto.LibraryStrip'];
export type OverviewAside = components['schemas']['dto.OverviewAside'];
export type NextEpisode = components['schemas']['dto.NextEpisode'];
export type RatingScore = components['schemas']['dto.RatingScore'];
export type RecentEvent = components['schemas']['dto.RecentEvent'];
export type DownloadChip = components['schemas']['dto.DownloadChip'];
export type ExternalLinks = components['schemas']['dto.ExternalLinks'];
export type Trailer = components['schemas']['dto.Trailer'];
export type TaxonomyChip = components['schemas']['dto.TaxonomyChip'];
export type NetworkChip = components['schemas']['dto.NetworkChip'];
export type ContentRatingBadge = components['schemas']['dto.ContentRatingBadge'];

export type StatusToken =
  | 'continuing'
  | 'ended'
  | 'canceled'
  | 'in_production'
  | 'upcoming'
  | 'unknown';

// Tokens the composer emits. Anything else falls through to "unknown"
// so the pill renders a neutral chip instead of crashing.
const VALID_STATUSES: ReadonlySet<StatusToken> = new Set([
  'continuing', 'ended', 'canceled', 'in_production', 'upcoming', 'unknown',
]);

export function parseStatus(raw: string | undefined): StatusToken {
  const t = (raw ?? '').toLowerCase() as StatusToken;
  return VALID_STATUSES.has(t) ? t : 'unknown';
}

// Build a same-origin URL for the content-addressed media handler.
// Returns undefined when hash is empty so callers can render a
// monogram placeholder via SeriesPoster's gradient fallback.
export function mediaUrl(hash: string | undefined | null): string | undefined {
  if (!hash || hash.length === 0) return undefined;
  return `/api/v1/media/${encodeURIComponent(hash)}`;
}

export interface UseSeriesParams {
  readonly seriesId: number | undefined;
  readonly lang?: string | undefined;
}

export function seriesQueryKey(
  seriesId: number,
  lang: string,
): readonly [string, number, string] {
  return ['series-detail', seriesId, lang] as const;
}

export function useSeries({
  seriesId,
  lang,
}: UseSeriesParams) {
  const enabled = typeof seriesId === 'number' && seriesId > 0;
  const effectiveLang = lang ?? '';
  return useQuery<SeriesDetailResponse>({
    queryKey: enabled
      ? seriesQueryKey(seriesId as number, effectiveLang)
      : (['series-detail', 0, ''] as const),
    queryFn: () => {
      const qs = effectiveLang ? `?lang=${encodeURIComponent(effectiveLang)}` : '';
      return api<SeriesDetailResponse>(
        `/series/${seriesId}${qs}`,
      );
    },
    enabled,
    staleTime: 30_000,
    refetchOnWindowFocus: false,
  });
}

// Helper consumed by hero + skeleton: did the response degrade the
// given source? `degraded[]` carries source tokens like "tmdb" /
// "omdb" / "sonarr_queue" / "torrents".
export function isDegraded(
  resp: SeriesDetailResponse | undefined,
  source: 'tmdb' | 'omdb' | 'sonarr_queue' | 'torrents',
): boolean {
  return (resp?.degraded ?? []).includes(source);
}

// Hero renders in "Sonarr-only" mode when the TMDB-derived fields
// the composer would normally fill are absent. Used by SeriesHero
// to decide whether to render the backdrop/tagline/genre row at all.
export function isSonarrOnly(hero: SeriesHero | undefined): boolean {
  if (!hero) return true;
  const noBackdrop = !hero.backdrop_asset;
  const noTagline = !hero.tagline;
  const noGenres = !hero.genres || hero.genres.length === 0;
  const noTmdbRating = !hero.tmdb_rating;
  return noBackdrop && noTagline && noGenres && noTmdbRating;
}
