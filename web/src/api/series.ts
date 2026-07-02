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

// ── C3b (story 968): GET /series/:id serves `seriesdetail.SkeletonDTO`
// (hero + sidebar + season_count + in_library_instances + degraded + lang +
// synced_at). `useSeries` is typed against this GENERATED shape; the heavy
// sections load from their own lazy endpoints and the hero/rail view-models
// below are produced by the pure `adaptHero`/`adaptCast`/`adaptSeasons`
// adapters (schema → presentation types), keeping every section component
// untouched.
export type SeriesSkeleton = components['schemas']['seriesdetail.SkeletonDTO'];

// C3c-1: external-links footer row now sourced from the SkeletonDTO. Points
// the footer's prop type at the generated schema (imdb_id / tmdb_id / tvdb_id
// / homepage) so it stays in lock-step with the BE contract.
export type ExternalLinks = components['schemas']['seriesdetail.ExternalLinks'];

// ── C3b (story 968): these are the COMPONENT VIEW-MODEL contracts the hero /
// rail tree consumes (presentation types, NOT API types). `adaptHero` /
// `adaptCast` / `adaptSeasons` map the generated `SkeletonDTO` + lazy DTOs onto
// them, so `SeriesHero`, `RailCard`, `RatingDuo`, `CastStrip` etc. stay byte-
// identical. Keeping them decoupled from the schema does not violate "use the
// generated schema" — the API surface `useSeries` returns is the generated
// `SkeletonDTO`; these are strictly the render-side shapes.
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
// C3c-3 (story 971): the hero download chip is restored on the /library response
// (`dto.SeriesLibraryResponse.download`), so DownloadChip points at the generated
// schema type again — single source of truth. All fields optional/readonly; the
// hero renders status/title only (HeroLibraryStrip).
export type DownloadChip = components['schemas']['dto.DownloadChip'];
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

  return useQuery<SeriesSkeleton>({
    queryKey: enabled
      ? seriesQueryKey(seriesId as number, effectiveLang)
      : (['series-detail', 0, ''] as const),
    queryFn: () => {
      const qs = effectiveLang ? `?lang=${encodeURIComponent(effectiveLang)}` : '';
      return api<SeriesSkeleton>(
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
  resp: SeriesSkeleton | undefined,
  source: DegradedSource,
): boolean {
  return (resp?.degraded ?? []).includes(source);
}

// Story 495 / N-1e: generic predicate so any per-section response
// (SkeletonDTO, SeriesCastResponse, …) can share the degraded[] check
// without a per-DTO helper.
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

export function isHotDegraded(resp: SeriesSkeleton | undefined): boolean {
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

// ── C3b (story 968) — pure adapters: generated SkeletonDTO + lazy DTOs →
// the kept component view-models above. Project uses
// `exactOptionalPropertyTypes`, so every optional key is emitted via the
// `{...(x ? { k: x } : {})}` spread pattern — never assign `undefined`.

type SkeletonHero = NonNullable<SeriesSkeleton['hero']>;
type SkeletonSidebar = NonNullable<SeriesSkeleton['sidebar']>;
type CastPageMember = components['schemas']['dto.CastPageMember'];
type Season = components['schemas']['dto.Season'];
type SeasonSummary = components['schemas']['dto.SeasonSummaryDTO'];

// values.TitleWire / values.TaglineWire → plain string (empty → undefined).
function wireText(
  w: { readonly value?: string } | undefined,
): string | undefined {
  const v = w?.value;
  return v && v.length > 0 ? v : undefined;
}

// SkeletonDTO.hero + SkeletonDTO.sidebar → the fat SeriesHero view-model the
// hero/rail components already consume. Status, networks, studio, countries,
// premiere_date, original_language all live in `sidebar` in the skeleton.
export function adaptHero(
  hero: SkeletonHero | undefined,
  sidebar: SkeletonSidebar | undefined,
): SeriesHero | undefined {
  if (!hero && !sidebar) return undefined;
  const h = hero ?? {};
  const s = sidebar ?? {};
  const title = wireText(h.title);
  const originalTitle = wireText(h.original_title);
  const tagline = wireText(h.tagline);
  const studio = s.production_companies?.find((c) => c.name)?.name;
  const ne = h.next_episode;
  const neTitle = wireText(ne?.title);
  return {
    ...(title ? { title } : {}),
    ...(originalTitle ? { original_title: originalTitle } : {}),
    ...(tagline ? { tagline } : {}),
    ...(s.status ? { status: s.status } : {}),
    ...(h.year_start ? { year_start: h.year_start } : {}),
    ...(h.year_end ? { year_end: h.year_end } : {}),
    ...(h.runtime_minutes ? { runtime_minutes: h.runtime_minutes } : {}),
    ...(h.poster_asset ? { poster_asset: h.poster_asset } : {}),
    ...(h.backdrop_asset ? { backdrop_asset: h.backdrop_asset } : {}),
    ...(h.genres ? { genres: h.genres.map((g) => ({
      ...(g.tmdb_id !== undefined ? { id: g.tmdb_id } : {}),
      ...(g.name ? { name: g.name } : {}),
    })) } : {}),
    ...(s.networks ? { networks: s.networks.map((n) => ({
      ...(n.tmdb_id !== undefined ? { id: n.tmdb_id } : {}),
      ...(n.name ? { name: n.name } : {}),
      ...(n.logo_asset ? { logo_asset: n.logo_asset } : {}),
    })) } : {}),
    ...(h.tmdb_rating ? { tmdb_rating: h.tmdb_rating } : {}),
    ...(h.imdb_rating ? { imdb_rating: h.imdb_rating } : {}),
    ...(h.content_rating ? { content_rating: { rating: h.content_rating } } : {}),
    ...(ne ? { next_episode: {
      ...(ne.air_date ? { air_date: ne.air_date } : {}),
      ...(ne.episode_number !== undefined ? { episode_number: ne.episode_number } : {}),
      ...(ne.season_number !== undefined ? { season_number: ne.season_number } : {}),
      ...(neTitle ? { title: neTitle } : {}),
    } } : {}),
    ...(h.trailer_key ? { trailer: { key: h.trailer_key, site: 'youtube' } } : {}),
    ...(studio ? { studio } : {}),
    ...(s.origin_countries ? { countries: s.origin_countries } : {}),
    ...(s.first_air_date ? { premiere_date: s.first_air_date } : {}),
    ...(s.original_language ? { original_language: s.original_language } : {}),
  };
}

// dto.CastPageMember[] → CastMember[]. NOTE the field rename: the DTO calls
// the TMDB person id `tmdb_id`; CastStrip reads `tmdb_person_id`.
export function adaptCast(
  members: readonly CastPageMember[] | undefined,
): readonly CastMember[] {
  return (members ?? []).map((m) => ({
    ...(m.person_id !== undefined ? { person_id: m.person_id } : {}),
    ...(m.tmdb_id !== undefined ? { tmdb_person_id: m.tmdb_id } : {}),
    ...(m.name ? { name: m.name } : {}),
    ...(m.character_name ? { character_name: m.character_name } : {}),
    ...(m.profile_asset ? { profile_asset: m.profile_asset } : {}),
    ...(m.episode_count !== undefined ? { episode_count: m.episode_count } : {}),
  }));
}

// dto.SeasonSummaryDTO[] → dto.Season[] (summary rows only). Episodes,
// on_disk_count and downloading_count are NOT in the summary endpoint — the
// accordion fetches full episode state per-season on expand via useSeriesSeason.
// air_date maps from air_date_start.
export function adaptSeasons(
  summaries: readonly SeasonSummary[] | undefined,
): readonly Season[] {
  return (summaries ?? []).map((s) => ({
    ...(s.season_number !== undefined ? { season_number: s.season_number } : {}),
    ...(s.name ? { name: s.name } : {}),
    ...(s.episode_count !== undefined ? { episode_count: s.episode_count } : {}),
    ...(s.poster_asset ? { poster_asset: s.poster_asset } : {}),
    ...(s.air_date_start ? { air_date: s.air_date_start } : {}),
    ...(s.overview ? { overview: s.overview } : {}),
  }));
}
