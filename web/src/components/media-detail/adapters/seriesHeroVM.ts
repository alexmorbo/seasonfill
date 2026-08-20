// U-4 sub-step B — pure, hook-free helpers shared by the TWO places that
// build a series hero view-model:
//   - `series-detail/SeriesHero.tsx` (the adapter kept for its own
//     byte-identical direct-render test)
//   - `pages/toSeriesVM.tsx` (the real page's full `MediaDetailVM`)
//
// Factoring the derivation logic here (rather than literally duplicating it
// in both files, as the story blueprint's §7 R3 tolerates) reduces the risk
// of the two VM-builders drifting apart — while each CALLER still owns its
// own hook calls (`useSeriesRatings`, `useInstances`, …), satisfying the
// hard constraint that `SeriesHero.tsx` keeps calling `useSeriesRatings`
// directly (its test mocks that hook and renders `<SeriesHero>` alone).
import {
  mediaUrl,
  isSonarrOnly,
  parseStatus,
  type SeriesHero as SeriesHeroDTO,
  type RatingScore,
  type StatusToken,
} from '@/api/series';
import { slugifyTitle, buildSonarrSeriesHref } from '@/lib/sonarrUrl';
import type { MediaHeroVM } from '../MediaHero';

// yearRange — verbatim from the pre-U-4 `SeriesHero.tsx`.
export function yearRange(start: number | undefined, end: number | undefined, status: string): string {
  if (!start) return '';
  if (status === 'continuing' || status === 'in_production') return `${start}–`;
  if (!end || end === start) return String(start);
  return `${start}–${end}`;
}

// #1059 / F-11-FE — single-source the effective ★ off the live /ratings
// value, falling back to the skeleton hero rating for instant first paint.
export function effectiveRatingScore(
  liveScore: number | undefined,
  liveVotes: number | undefined,
  fallback: RatingScore | undefined,
): RatingScore | undefined {
  if (typeof liveScore === 'number' && liveScore > 0) {
    return {
      score: liveScore,
      ...(liveVotes && liveVotes > 0 ? { votes: liveVotes } : {}),
    };
  }
  return fallback;
}

export interface BuildSeriesHeroCoreParams {
  readonly hero: SeriesHeroDTO | undefined;
  readonly tmdbScore: RatingScore | undefined;
  readonly imdbScore: RatingScore | undefined;
  readonly tmdbStaleAt?: string | undefined;
  readonly imdbStaleAt?: string | undefined;
  readonly imdbLoading?: boolean | undefined;
  readonly tmdbSeriesDegraded?: boolean | undefined;
  readonly backdropLoadingLabel: string; // already t()-resolved by the caller
}

// The hero-relevant subset of `MediaDetailVM` (everything `MediaHero`
// reads except `heroActions`, which the caller attaches separately since
// it needs JSX / callbacks the caller owns).
export type SeriesHeroCoreVM = Omit<MediaHeroVM, 'heroActions'>;

export function buildSeriesHeroCore({
  hero, tmdbScore, imdbScore, tmdbStaleAt, imdbStaleAt, imdbLoading, tmdbSeriesDegraded,
  backdropLoadingLabel,
}: BuildSeriesHeroCoreParams): SeriesHeroCoreVM {
  const status = parseStatus(hero?.status);
  const sonarrOnly = isSonarrOnly(hero);
  const title = hero?.title ?? '';
  const originalTitle = hero?.original_title && hero.original_title !== title
    ? hero.original_title
    : undefined;
  const tagline = sonarrOnly ? undefined : hero?.tagline;
  const genres = sonarrOnly ? [] : (hero?.genres ?? []).slice(0, 5);
  const backdropSrc = mediaUrl(hero?.backdrop_asset);
  const showBackdropLoadingLabel = !sonarrOnly && !backdropSrc && Boolean(tmdbSeriesDegraded);

  return {
    type: 'series',
    sonarrOnly,
    // U-4 wave-2 C: `actions` (movie-only hero action nodes) is always
    // empty for series — `MediaHero` only reads it under `vm.type ===
    // 'movie'`, so this is a structural no-op, present just to satisfy
    // `MediaHeroVM`.
    actions: [],
    localizedTitle: title,
    ...(originalTitle ? { originalTitle } : {}),
    ...(tagline ? { tagline } : {}),
    yearLabel: yearRange(hero?.year_start, hero?.year_end, status),
    ...(hero?.runtime_minutes ? { runtimeMinutes: hero.runtime_minutes } : {}),
    ...(hero?.content_rating ? { contentRating: hero.content_rating } : {}),
    genres,
    posterAsset: hero?.poster_asset,
    backdropAsset: hero?.backdrop_asset,
    ...(showBackdropLoadingLabel ? { backdropLoadingLabel } : {}),
    ratings: {
      ...(tmdbScore ? { tmdb: tmdbScore } : {}),
      ...(imdbScore ? { imdb: imdbScore } : {}),
      ...(tmdbStaleAt ? { tmdbStaleAt } : {}),
      ...(imdbStaleAt ? { imdbStaleAt } : {}),
      ...(imdbLoading ? { imdbLoading: true } : {}),
    },
    ...(hero?.trailer ? { trailer: hero.trailer } : {}),
  };
}

export interface SeriesHeroMenuItem {
  readonly name: string;
  readonly href?: string | undefined;
}

export interface SeriesHeroActionData {
  readonly sonarrHref?: string | undefined;
  readonly showCaret: boolean;
  readonly showAddToSonarr: boolean;
  readonly openItems: readonly SeriesHeroMenuItem[];
  readonly addItems: readonly string[];
}

export interface ResolveSeriesHeroActionDataParams {
  readonly instance: string | undefined;
  readonly title: string;
  readonly titleSlug?: string | undefined;
  readonly inLibraryInstances: readonly string[];
  readonly allInstances: readonly { readonly name?: string | undefined; readonly public_url?: string | undefined }[];
  readonly hasAddToSonarrTarget: boolean;
}

// Instance/href resolution — verbatim from the pre-U-4 `SeriesHero.tsx`
// (publicUrlByName map, sonarrHref, openItems/addItems, showCaret).
export function resolveSeriesHeroActionData({
  instance, title, titleSlug, inLibraryInstances, allInstances, hasAddToSonarrTarget,
}: ResolveSeriesHeroActionDataParams): SeriesHeroActionData {
  const publicUrlByName = new Map<string, string>();
  for (const i of allInstances) {
    if (i.name && i.public_url) publicUrlByName.set(i.name, i.public_url);
  }
  const sonarrPublic = instance ? publicUrlByName.get(instance) : undefined;
  const slug = titleSlug && titleSlug.length > 0 ? titleSlug : slugifyTitle(title);
  const sonarrHref = sonarrPublic ? buildSonarrSeriesHref(sonarrPublic, slug) : undefined;

  const inLibrarySet = new Set(inLibraryInstances);
  const openItems = inLibraryInstances
    .filter((name) => Boolean(name))
    .map((name) => {
      const url = publicUrlByName.get(name);
      return { name, ...(url ? { href: buildSonarrSeriesHref(url, slug) } : {}) };
    });
  const addItems = allInstances
    .map((i) => i.name)
    .filter((name): name is string => typeof name === 'string' && name.length > 0 && !inLibrarySet.has(name));
  const showCaret = allInstances.length > 1;

  return {
    ...(sonarrHref ? { sonarrHref } : {}),
    showCaret,
    showAddToSonarr: hasAddToSonarrTarget,
    openItems,
    addItems,
  };
}

export type { StatusToken };
