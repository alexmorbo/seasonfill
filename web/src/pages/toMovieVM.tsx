import type { TFunction } from 'i18next';
import { CountryName } from '@/components/series-detail/CountryName';
import { LanguageName } from '@/components/series-detail/LanguageName';
import { PremiereDate } from '@/components/series-detail/PremiereDate';
import { formatMoney, isMoneyPresent } from '@/lib/money';
import type { CastMember, DegradedSource } from '@/api/series';
import type { MovieDetail } from '@/api/movies';
import type { MovieCastMember } from '@/api/movieCast';
import type { MovieOverviewResponse } from '@/api/movieOverview';
import type {
  MediaDetailVM,
  MediaFact,
  MediaAction,
} from '@/components/media-detail/view-model';

export interface ToMovieVMParams {
  readonly t: TFunction;
  readonly lang: string | undefined;
  // movie.tmdb_id ?? url tmdbId — identity fallback for cast.mediaId /
  // MovieRecommendationsRail's tmdbId prop (built by the caller).
  readonly tmdbId: number;
  readonly movie: MovieDetail;
  readonly ov: MovieOverviewResponse | undefined;

  // Ratings — hero ★ duo (no votes; movie has no votes field on the base
  // DTO) + the ratings-SECTION's own numbers, sourced from the page's
  // `useMovieRatings({ tmdbId })` call (moved up from inside
  // `MovieRatingsSection` — U-4 wave-2 C ownership move).
  readonly showTmdb: boolean;
  readonly showImdb: boolean;
  readonly sectionTmdbRating: number | undefined;
  readonly sectionTmdbVotes: number | undefined;
  readonly sectionImdbRating: number | undefined;
  readonly sectionImdbVotes: number | undefined;
  readonly rated: string | undefined;
  readonly awards: string | undefined;

  // Hero action node(s) — the AddToRadarrSplitButton/OpenInRadarrButton JSX,
  // already resolved by the page (same "resolved by the page" pattern
  // `toSeriesVM`'s `heroActions` param doc describes — the instance/href
  // resolution needs `useInstances`/`useAddToRadarrLauncher`, hooks this
  // adapter does not call).
  readonly actions: readonly MediaAction[];

  // Cast.
  readonly cast: readonly MovieCastMember[] | undefined;
  readonly castServedLang: string | undefined;

  // Overview — text/loading gating already resolved by the page (mirrors
  // `overviewLoading` in `MovieDetail.tsx` verbatim). `overviewText` falls
  // back to `movie.overview`, itself optional on the DTO.
  readonly overviewText: string | undefined;
  readonly overviewLoading: boolean;

  readonly degraded: readonly DegradedSource[];
}

// toMovieVM — ADR-0022 Wave-2 Story C. Builds the FULL `MediaDetailVM` the
// real `<MovieDetail>` page renders via `<MediaDetail>`. Mirrors the pre-
// wave-2 inline JSX field-for-field; `belowGrid` has no movie equivalent
// (series-only mid-page sections). `collection` is left unset — the
// collection block lives in the hero-right slot as a compact
// `CollectionHeroCard` (see `MovieDetail.tsx`'s `heroExtras.nextCard`,
// mirroring how series places `NextEpisodeCard` there), and the on-disk
// "library membership" info that used to render below the hero as its own
// section now lives in the hero-bottom `MovieHeroLibraryStrip` (see
// `MovieDetail.tsx`'s `heroExtras.bottomStrip`, mirroring series'
// `HeroLibraryStrip`) — so there's nothing left for `collection.node` to
// render.
export function toMovieVM(p: ToMovieVMParams): MediaDetailVM {
  const { t, lang, tmdbId, movie, ov } = p;

  const originalTitle =
    movie.original_title && movie.original_title !== movie.title
      ? movie.original_title
      : undefined;

  return {
    type: 'movie',
    localizedTitle: ov?.title ?? movie.title ?? '',
    originalTitle,
    tagline: ov?.tagline ?? movie.tagline,
    // TODO wave-3: widen MediaStatusToken for movie TMDB status vocabulary
    // (released/post_production/in_production/upcoming/canceled/planned) —
    // field currently unread by any renderer.
    statusToken: 'unknown',
    yearLabel: typeof movie.year === 'number' && movie.year > 0 ? String(movie.year) : '',
    runtimeMinutes: movie.runtime_minutes,
    contentRating: undefined,
    genres: (movie.genres ?? []).slice(0, 5),

    posterAsset: movie.poster,
    backdropAsset: movie.backdrop,
    backdropLoadingLabel: undefined,

    ratings: {
      tmdb: p.showTmdb
        ? {
            score: movie.tmdb_rating as number,
            ...(typeof p.sectionTmdbVotes === 'number' && p.sectionTmdbVotes > 0
              ? { votes: p.sectionTmdbVotes }
              : {}),
          }
        : undefined,
      imdb: p.showImdb
        ? {
            score: movie.imdb_rating as number,
            ...(typeof p.sectionImdbVotes === 'number' && p.sectionImdbVotes > 0
              ? { votes: p.sectionImdbVotes }
              : {}),
          }
        : undefined,
      imdbHref: movie.imdb_id ? `https://www.imdb.com/title/${movie.imdb_id}/` : undefined,
      // TODO wave-3: movie ratings has no SWR backoff-poll ladder yet
      // (mirrors series #1064's stale/pending signals) — always absent.
      tmdbStaleAt: undefined,
      imdbStaleAt: undefined,
      imdbLoading: undefined,
      sectionTmdbRating: p.sectionTmdbRating,
      sectionTmdbVotes: p.sectionTmdbVotes,
      sectionImdbRating: p.sectionImdbRating,
      sectionImdbVotes: p.sectionImdbVotes,
      rated: p.rated,
      awards: p.awards,
    },

    actions: p.actions,
    trailer: {
      key: movie.trailer?.key,
      site: movie.trailer?.site,
      name: movie.trailer?.name,
    },
    heroActions: {
      backHref: '/movies',
      backLabel: t('movieDetail.back'),
      sonarrHref: undefined,
      showAddToSonarr: false,
      showCaret: false,
      openItems: [],
      addItems: [],
      onAddToSonarr: () => {},
      onAddToInstance: () => {},
      followButton: undefined,
    },

    sidebarFacts: buildMovieSidebarFacts(movie, t),
    keywords: movie.keywords ?? [],

    cast: {
      members: toMovieCastMembers(p.cast),
      href: `/movies/${movie.tmdb_id ?? tmdbId}/cast`,
      mediaId: movie.tmdb_id ?? tmdbId,
      limit: 8,
      loading: undefined,
      // TODO wave-3: cast.servedLang is reserved for a future EN badge and
      // currently unread by MediaCastStrip, so the movie cast strip's old
      // LanguageFallbackTag ("served in EN") signal is silently dropped.
      servedLang: p.castServedLang,
    },

    // Inert placeholder — `MovieDetail.tsx` passes
    // `recommendationsSlot={<MovieRecommendationsRail .../>}` to
    // `MediaDetail`, which renders that slot INSTEAD of building the rail
    // from this field, so `MediaDetail` never reads these values.
    recommendations: {
      items: [],
      isLoading: false,
      visible: false,
      sentinelRef: { current: null },
      renderCard: () => null,
    },

    externalLinks: undefined,
    overview: {
      label: t('movieDetail.overview.label'),
      text: p.overviewLoading ? '' : (p.overviewText || t('movieDetail.overview.empty')),
      contentLang: ov?.served_language,
      requestedLang: lang,
      loading: p.overviewLoading,
    },

    // Kept outside the scaffold — `MovieSyncFooter` is rendered as a
    // sibling of `<MediaDetail>` in `MovieDetail.tsx` (its staleness check
    // is movie-specific, unlike the shared footer's series assumptions).
    syncedAt: undefined,
    degraded: p.degraded,

    sonarrOnly: false,
  };
}

// dto.MovieCastMember[] → CastMember[]. NOTE the field rename: the DTO calls
// the TMDB person id `tmdb_id`; MediaCastStrip reads `tmdb_person_id`. No
// `episode_count` is mapped — MediaCastStrip's stable episode-count sort is
// then a no-op, preserving the BE's `credit_order` ASC order verbatim.
function toMovieCastMembers(
  members: readonly MovieCastMember[] | undefined,
): readonly CastMember[] {
  return (members ?? []).map((m) => ({
    ...(m.person_id !== undefined ? { person_id: m.person_id } : {}),
    ...(m.tmdb_id !== undefined ? { tmdb_person_id: m.tmdb_id } : {}),
    ...(m.name ? { name: m.name } : {}),
    ...(m.character_name ? { character_name: m.character_name } : {}),
    ...(m.profile_asset ? { profile_asset: m.profile_asset } : {}),
  }));
}

// §mapping table — order + testids: status → original-title → studio →
// premiere-date → countries → original-language → budget → revenue. Mirrors
// `MovieSidebar.tsx`'s pre-wave-2 row order, with a NEW premiere-date row
// (Decision B — replaces the old hero release-date display) inserted between
// studio and countries, and `countries` widened to render EVERY origin
// country (toSeriesVM's `buildSidebarFacts` pattern) rather than only the
// first one `MovieSidebar.tsx` used to show.
// Raw TMDB movie `status` values are mixed-case English strings (`Released`,
// `Planned`, `In Production`, `Post Production`, `Rumored`, `Canceled`).
// Normalize case-insensitively to the `movieDetail.status.*` i18n key
// vocabulary; unrecognized/future TMDB values fall through to the caller's
// `defaultValue` (the raw string) rather than a raw i18n key.
const MOVIE_STATUS_KEYS: Record<string, string> = {
  released: 'released',
  planned: 'planned',
  'in production': 'inProduction',
  'post production': 'postProduction',
  rumored: 'rumored',
  canceled: 'canceled',
  cancelled: 'canceled',
};

function normalizeMovieStatus(status: string): string {
  const normalized = status.trim().toLowerCase();
  return MOVIE_STATUS_KEYS[normalized] ?? normalized;
}

function buildMovieSidebarFacts(movie: MovieDetail, t: TFunction): readonly MediaFact[] {
  const facts: MediaFact[] = [];

  if (movie.status) {
    const normalizedStatus = normalizeMovieStatus(movie.status);
    facts.push({
      id: 'status',
      label: t('seriesDetail.rail.status'),
      value: t(`movieDetail.status.${normalizedStatus}`, { defaultValue: movie.status }),
      testId: 'rail-row-status',
      ...(normalizedStatus === 'released' ? { accent: true } : {}),
    });
  }

  const originalTitle = movie.original_title;
  const showOriginalTitle = Boolean(originalTitle) && originalTitle !== movie.title;
  if (showOriginalTitle) {
    facts.push({
      id: 'original-title',
      label: t('movieDetail.meta.originalTitle'),
      testId: 'rail-row-original-title',
      value: <span data-testid="rail-row-original-title-value">{originalTitle}</span>,
    });
  }

  if (movie.studio) {
    facts.push({
      id: 'studio',
      label: t('seriesDetail.rail.studio'),
      testId: 'rail-row-studio',
      value: <span data-testid="rail-row-studio-value">{movie.studio}</span>,
    });
  }

  if (movie.release_date) {
    // BE marshals `Released` as a full RFC3339 timestamp (e.g.
    // "2021-10-22T00:00:00Z"), not a plain YYYY-MM-DD string.
    // `PremiereDate`'s regex guard only accepts YYYY-MM-DD, so truncate
    // to the calendar-date prefix here (mirrors the old `formatDate(...,
    // 'date')` call this replaces).
    facts.push({
      id: 'premiere-date',
      label: t('seriesDetail.rail.premiereDate'),
      testId: 'rail-row-premiere-date',
      value: <PremiereDate iso={movie.release_date.slice(0, 10)} />,
    });
  }

  if (movie.digital_release_date) {
    facts.push({
      id: 'digital-release',
      label: t('movieDetail.rail.digitalRelease'),
      testId: 'rail-row-digital-release',
      value: <PremiereDate iso={movie.digital_release_date.slice(0, 10)} />,
    });
  }

  if (movie.physical_release_date) {
    facts.push({
      id: 'physical-release',
      label: t('movieDetail.rail.physicalRelease'),
      testId: 'rail-row-physical-release',
      value: <PremiereDate iso={movie.physical_release_date.slice(0, 10)} />,
    });
  }

  const countries = (movie.countries?.length ?? 0) > 0
    ? (movie.countries as readonly string[])
    : (movie.country ? [movie.country] : []);
  if (countries.length > 0) {
    facts.push({
      id: 'countries',
      label: t('seriesDetail.rail.country', { count: countries.length }),
      testId: 'rail-row-countries',
      value: (
        <span data-testid="rail-row-countries-value">
          {countries.map((c, i) => (
            <span key={`${c}-${i}`}>
              {i > 0 && ', '}
              <CountryName code={c} />
            </span>
          ))}
        </span>
      ),
    });
  }

  if (movie.original_language) {
    facts.push({
      id: 'original-language',
      label: t('seriesDetail.rail.originalLanguage'),
      testId: 'rail-row-original-language',
      value: <LanguageName code={movie.original_language} />,
    });
  }

  if (isMoneyPresent(movie.budget)) {
    facts.push({
      id: 'budget',
      label: t('movieDetail.meta.budget'),
      testId: 'rail-row-budget',
      value: (
        <span className="tabular-nums" data-testid="rail-row-budget-value">
          {formatMoney(movie.budget as number)}
        </span>
      ),
    });
  }

  if (isMoneyPresent(movie.revenue)) {
    facts.push({
      id: 'revenue',
      label: t('movieDetail.meta.revenue'),
      testId: 'rail-row-revenue',
      value: (
        <span className="tabular-nums" data-testid="rail-row-revenue-value">
          {formatMoney(movie.revenue as number)}
        </span>
      ),
    });
  }

  return facts;
}
