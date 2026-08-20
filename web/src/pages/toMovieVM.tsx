import type { ReactNode } from 'react';
import type { TFunction } from 'i18next';
import { useTranslation } from 'react-i18next';
import { Badge } from '@/components/ui/badge';
import { StatusBadge } from '@/components/StatusBadge';
import { CountryName } from '@/components/series-detail/CountryName';
import { LanguageName } from '@/components/series-detail/LanguageName';
import { PremiereDate } from '@/components/series-detail/PremiereDate';
import { MovieCollectionBlock } from '@/components/movies/MovieCollectionBlock';
import { formatMoney, isMoneyPresent } from '@/lib/money';
import type { CastMember, DegradedSource } from '@/api/series';
import type { MovieDetail, MovieDetailLibrary } from '@/api/movies';
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
// (series-only mid-page sections), and `collection.node` BUNDLES the
// MovieCollectionBlock + "Library membership" section together (as a single
// fragment) so their visual order — collection block, then library section,
// immediately after the overview grid — is preserved without needing a
// second scaffold slot.
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
      tmdb: p.showTmdb ? { score: movie.tmdb_rating as number } : undefined,
      imdb: p.showImdb ? { score: movie.imdb_rating as number } : undefined,
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
      href: undefined,
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

    collection: { node: buildCollectionSection(movie, t, lang) },
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
function buildMovieSidebarFacts(movie: MovieDetail, t: TFunction): readonly MediaFact[] {
  const facts: MediaFact[] = [];

  if (movie.status) {
    facts.push({
      id: 'status',
      label: t('seriesDetail.rail.status'),
      testId: 'rail-row-status',
      value: <StatusBadge value={movie.status} />,
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

// LibraryRow — verbatim from the pre-wave-2 `MovieDetail.tsx` local
// component, relocated here because it's now composed INTO
// `vm.collection.node` by `buildCollectionSection` below. This file's
// primary export (`toMovieVM`) is not a component, hence the disable below
// (same pattern as `RatingDuo.tsx`'s `humanizeVotes`).
// eslint-disable-next-line react-refresh/only-export-components
function LibraryRow({ row }: { row: MovieDetailLibrary }) {
  const { t } = useTranslation();
  return (
    <div
      data-testid={`movie-library-row-${row.instance_name ?? 'unknown'}`}
      className="flex flex-wrap items-center gap-2 rounded-md border border-border-subtle bg-bg-surface px-3 py-2"
    >
      <span className="text-[13px] font-medium text-tx-primary">
        {row.instance_name}
      </span>
      {row.monitored && (
        <Badge variant="accent" data-testid="movie-library-monitored">
          {t('movieDetail.library.monitored')}
        </Badge>
      )}
      {row.has_file && (
        <Badge variant="ok" data-testid="movie-library-hasfile">
          {t('movieDetail.library.hasFile')}
        </Badge>
      )}
      {row.availability && (
        <span className="text-[12px] text-tx-muted">{row.availability}</span>
      )}
    </div>
  );
}

// Decision B (bundled) — `MovieCollectionBlock` + the "Library membership"
// section, rendered as ONE fragment so `vm.collection.node` preserves the
// exact old visual order (collection block, then library section,
// immediately after the overview grid) without needing a second
// `MediaDetail` slot.
function buildCollectionSection(
  movie: MovieDetail,
  t: TFunction,
  lang: string | undefined,
): ReactNode {
  const library = movie.library ?? [];
  const collectionId = movie.collection?.tmdb_collection_id;
  const hasCollection = typeof collectionId === 'number' && collectionId > 0;

  return (
    <>
      {hasCollection && (
        <MovieCollectionBlock
          tmdbCollectionId={collectionId}
          {...(library[0]?.instance_name ? { instance: library[0].instance_name } : {})}
          {...(lang ? { lang } : {})}
        />
      )}

      <section data-testid="movie-detail-library">
        <h2 className="mb-1.5 text-[13px] font-semibold uppercase tracking-wide text-tx-faint">
          {t('movieDetail.library.title')}
        </h2>
        {library.length === 0 ? (
          <p className="text-[13px] text-tx-muted" data-testid="movie-detail-library-empty">
            {t('movieDetail.library.empty')}
          </p>
        ) : (
          <div className="flex flex-col gap-2">
            {library.map((row) => (
              <LibraryRow key={row.instance_name ?? row.radarr_movie_id} row={row} />
            ))}
          </div>
        )}
      </section>
    </>
  );
}
