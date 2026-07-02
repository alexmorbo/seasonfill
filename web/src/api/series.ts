import { useRef } from 'react';
import { useQuery } from '@tanstack/react-query';
import { api } from '@/lib/api';
import type { components } from '@/api/schema';

// ── Section DTOs that SURVIVED the B1b SkeletonDTO cutover. These keys are
// still present in the generated schema, so they keep pointing at it.
export type LibraryStrip = components['schemas']['dto.LibraryStrip'];
export type OverviewAside = components['schemas']['dto.OverviewAside'];
export type NextEpisode = components['schemas']['dto.NextEpisode'];
export type RecentEvent = components['schemas']['dto.RecentEvent'];
export type TaxonomyChip = components['schemas']['dto.TaxonomyChip'];

// ── C3 (story 966): B1b-2 deleted the fat `dto.SeriesDetailResponse` and its
// hero / rating / download / links / cast sub-types from swagger (GET
// /series/:id now serves `seriesdetail.SkeletonDTO`). To GREEN `tsc` and
// unblock the Build-web CI job WITHOUT the full SeriesDetail rewrite (deferred
// to C3b), the shape the existing component tree + its unit tests consume is
// re-materialised here as local interfaces, decoupled from the generated
// schema. BUILD-UNBLOCK ONLY — the live page is degraded (cast/seasons/library
// /recent/external empty; hero enrichment opaque) until C3b wires the lazy
// /overview /cast /seasons /library endpoints. See documentation/refactor-first
// /stories/966-c3-seriesdetail-rewrite.md.
export interface RatingScore {
  readonly score?: number;
  readonly votes?: number;
}
export interface ContentRatingBadge {
  readonly rating?: string;
}
export interface Trailer {
  readonly key?: string;
  readonly site?: string;
  readonly name?: string;
}
export interface NetworkChip {
  readonly id?: number;
  readonly name?: string;
  readonly logo_asset?: string;
}
export interface DownloadChip {
  readonly status?: string;
  readonly title?: string;
  // fat-compat: HeroLibraryStrip fixtures pass the Sonarr queue id even
  // though only status/title are rendered. Kept optional so the existing
  // test object literals stay valid (excess-property check) until C3b.
  readonly queue_id?: number;
}
export interface ExternalLinks {
  readonly imdb_id?: string;
  readonly tmdb_id?: number;
  readonly tvdb_id?: number;
  readonly homepage?: string;
}
export interface CastMember {
  readonly person_id?: number;
  readonly tmdb_person_id?: number;
  readonly name?: string;
  readonly character_name?: string;
  readonly profile_asset?: string;
  readonly episode_count?: number;
}
export interface SeriesHero {
  readonly title?: string;
  readonly original_title?: string;
  readonly tagline?: string;
  readonly status?: string;
  readonly year_start?: number;
  readonly year_end?: number;
  readonly runtime_minutes?: number;
  readonly poster_asset?: string;
  readonly backdrop_asset?: string;
  readonly genres?: readonly { readonly id?: number; readonly name?: string }[];
  readonly networks?: readonly NetworkChip[];
  readonly tmdb_rating?: RatingScore;
  readonly imdb_rating?: RatingScore;
  readonly content_rating?: ContentRatingBadge;
  readonly next_episode?: NextEpisode;
  readonly trailer?: Trailer;
  readonly studio?: string;
  readonly countries?: readonly string[];
  readonly country?: string;
  readonly premiere_date?: string;
  readonly original_language?: string;
}
export interface SeriesDetailResponse {
  readonly series_id?: number;
  readonly sonarr_series_id?: number;
  readonly instance?: string;
  readonly in_library_instances?: readonly string[];
  readonly synced_at?: string;
  readonly degraded?: readonly string[];
  readonly hero?: SeriesHero;
  readonly library?: LibraryStrip;
  readonly download?: DownloadChip;
  readonly recent?: readonly RecentEvent[];
  readonly external_links?: ExternalLinks;
  readonly cast?: readonly CastMember[];
  readonly seasons?: readonly components['schemas']['dto.Season'][];
}

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

// Story 495 / N-1e: source tokens emitted by the composer's degraded[]
// field. Widened from the prior stale set ('tmdb'/'omdb'/...) which
// silently never matched live data — composer has emitted *_series /
// *_season / *_person variants since story 215.
export type DegradedSource =
  | 'tmdb_series'
  | 'tmdb_season'
  | 'tmdb_person'
  | 'omdb'
  | 'sonarr_queue';

export interface UseSeriesParams {
  readonly seriesId: number | undefined;
  readonly lang?: string | undefined;
  // Story 495 / N-1e (B-20): when true, refetches every 5 s while the
  // response carries a "hot" degraded source (tmdb_*, omdb). Auto-disables
  // after 6 consecutive ticks at the same `degraded.length` so cold
  // series don't poll forever.
  readonly pollWhileDegraded?: boolean | undefined;
}

export function seriesQueryKey(
  seriesId: number,
  lang: string,
): readonly [string, number, string] {
  return ['series-detail', seriesId, lang] as const;
}

const POLL_MS = 5_000;
const POLL_MAX_TICKS = 6; // ~30 s ceiling

export function useSeries({
  seriesId,
  lang,
  pollWhileDegraded,
}: UseSeriesParams) {
  const enabled = typeof seriesId === 'number' && seriesId > 0;
  const effectiveLang = lang ?? '';
  // Tick-cap lives outside the query callback because TanStack's
  // refetchInterval(query) callback must be pure on the data slice.
  const tickRef = useRef<{ lastLen: number; ticks: number }>({ lastLen: -1, ticks: 0 });

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
    refetchInterval: (query) => {
      if (!pollWhileDegraded) return false;
      const data = query.state.data;
      if (!data || !isHotDegraded(data)) {
        tickRef.current = { lastLen: -1, ticks: 0 };
        return false;
      }
      const len = data.degraded?.length ?? 0;
      const t = tickRef.current;
      if (len === t.lastLen) {
        t.ticks += 1;
      } else {
        t.lastLen = len;
        t.ticks = 1;
      }
      if (t.ticks > POLL_MAX_TICKS) return false;
      return POLL_MS;
    },
  });
}

// Story 495 / N-1e: typed against the composer's actual token union.
// The hero stale-badge call site previously asked for `'tmdb'` which
// never matched live data — fixed in SeriesDetail.tsx as part of this
// story.
export function isDegraded(
  resp: SeriesDetailResponse | undefined,
  source: DegradedSource,
): boolean {
  return (resp?.degraded ?? []).includes(source);
}

// Story 495 / N-1e: generic predicate so both SeriesDetailResponse
// and SeriesCastResponse can share the degraded[] check without a
// per-DTO helper.
export function degradedIncludes(
  degraded: readonly string[] | undefined,
  source: DegradedSource,
): boolean {
  return (degraded ?? []).includes(source);
}

// Tokens that should trigger auto-poll while the response is degraded.
// `sonarr_queue` is excluded because it's a live source — failure means
// "Sonarr is down right now", not "enrichment in progress".
const HOT_SOURCES: ReadonlySet<DegradedSource> = new Set([
  'tmdb_series', 'tmdb_season', 'tmdb_person', 'omdb',
]);

export function isHotDegraded(resp: SeriesDetailResponse | undefined): boolean {
  const degraded = resp?.degraded ?? [];
  return degraded.some((s): boolean => HOT_SOURCES.has(s as DegradedSource));
}

// Story 531 — set of degraded tokens the FE knows how to surface.
// Tokens outside this set are dropped from the aggregated list so a
// stray BE label can't break the chip rendering.
export const KNOWN_DEGRADED: ReadonlySet<DegradedSource> = new Set([
  'tmdb_series',
  'tmdb_season',
  'tmdb_person',
  'omdb',
  'sonarr_queue',
]);

// Story 531 — aggregateDegraded merges N degraded[] inputs from
// different per-section hooks (parent /series, /series/:id/overview,
// /series/:id/recommendations…) into a single deduped + filtered
// list. Used in <SeriesDetail> via useMemo to drive `<DegradedChip>`.
//
// Pure helper — no React hooks — so the call site can stay a single
// useMemo and the helper is unit-testable in isolation.
export function aggregateDegraded(
  ...lists: readonly (readonly string[] | undefined)[]
): readonly DegradedSource[] {
  const acc = new Set<DegradedSource>();
  for (const list of lists) {
    if (!list) continue;
    for (const src of list) {
      const s = src as DegradedSource;
      if (KNOWN_DEGRADED.has(s)) acc.add(s);
    }
  }
  return Array.from(acc);
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
