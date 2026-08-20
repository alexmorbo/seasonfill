import type { TFunction } from 'i18next';
import {
  mediaUrl,
  type SeriesHero as SeriesHeroDTO,
  type StatusToken,
  type RatingScore,
  type CastMember,
  type ExternalLinks,
  type DegradedSource,
} from '@/api/series';
import { CountryName } from '@/components/series-detail/CountryName';
import { LanguageName } from '@/components/series-detail/LanguageName';
import { PremiereDate } from '@/components/series-detail/PremiereDate';
import { buildSeriesHeroCore } from '@/components/media-detail/adapters/seriesHeroVM';
import type {
  MediaDetailVM,
  MediaFact,
  MediaKeyword,
  MediaHeroActions,
} from '@/components/media-detail/view-model';

export interface ToSeriesVMParams {
  readonly seriesId: number;
  readonly t: TFunction;
  readonly lang: string | undefined;

  readonly hero: SeriesHeroDTO | undefined;
  readonly status: StatusToken;
  readonly tmdbSeriesDegraded: boolean;

  // Ratings — #1059 effective hero score + #1064 stale/pending signals +
  // the ratings-SECTION's own (undeduped) raw values.
  readonly tmdbScore: RatingScore | undefined;
  readonly imdbScore: RatingScore | undefined;
  readonly tmdbStaleAt: string | undefined;
  readonly imdbStaleAt: string | undefined;
  readonly imdbLoading: boolean;
  readonly sectionTmdbRating: number | undefined;
  readonly sectionTmdbVotes: number | undefined;
  readonly sectionImdbRating: number | undefined;
  readonly sectionImdbVotes: number | undefined;
  readonly rated: string | undefined;
  readonly awards: string | undefined;

  // Hero split-button / follow / back-link data — resolved by the page
  // (same shape SeriesHero.tsx's adapter builds for its own direct render).
  readonly heroActions: MediaHeroActions;

  // Overview.
  readonly overviewText: string;
  readonly overviewContentLang: string | undefined;
  readonly overviewLoading: boolean;

  // Cast.
  readonly castHref: string;
  readonly cast: readonly CastMember[];
  readonly tmdbPersonDegraded: boolean;

  // Sidebar.
  readonly keywords: readonly MediaKeyword[];

  // Recommendations — U-4 §9 R1 fix: no longer built here. The page now
  // renders `<SeriesRecommendationsRail>` via `MediaDetail`'s
  // `recommendationsSlot` prop instead (mount-timing fix — see that
  // component's doc comment), so `vm.recommendations` below is an inert
  // placeholder `MediaDetail` never renders.

  // Footer / misc.
  readonly externalLinks: ExternalLinks | undefined;
  readonly syncedAt: string | undefined;
  readonly degraded: readonly DegradedSource[];
}

// toSeriesVM — U-4 sub-step B. Builds the FULL `MediaDetailVM` the real
// `<SeriesDetail>` page renders via `<MediaDetail>`. Mirrors the pre-U-4
// inline JSX field-for-field (§3.7); every value here is either passed
// straight through from a hook `SeriesDetail.tsx` already owned, or built
// with the SAME logic the old `RailCard`/`RecommendationsCarousel` call
// sites used (§3.2 / §3.5 mapping tables).
export function toSeriesVM(p: ToSeriesVMParams): MediaDetailVM {
  const heroCore = buildSeriesHeroCore({
    hero: p.hero,
    tmdbScore: p.tmdbScore,
    imdbScore: p.imdbScore,
    ...(p.tmdbStaleAt ? { tmdbStaleAt: p.tmdbStaleAt } : {}),
    ...(p.imdbStaleAt ? { imdbStaleAt: p.imdbStaleAt } : {}),
    ...(p.imdbLoading ? { imdbLoading: p.imdbLoading } : {}),
    ...(p.tmdbSeriesDegraded ? { tmdbSeriesDegraded: p.tmdbSeriesDegraded } : {}),
    backdropLoadingLabel: p.t('seriesDetail.degraded.backdrop.loading'),
  });

  const sidebarFacts = buildSidebarFacts(p.hero, p.status, p.t);

  return {
    ...heroCore,
    statusToken: p.status,
    actions: [],
    heroActions: p.heroActions,
    ratings: {
      ...heroCore.ratings,
      ...(p.sectionTmdbRating !== undefined ? { sectionTmdbRating: p.sectionTmdbRating } : {}),
      ...(p.sectionTmdbVotes !== undefined ? { sectionTmdbVotes: p.sectionTmdbVotes } : {}),
      ...(p.sectionImdbRating !== undefined ? { sectionImdbRating: p.sectionImdbRating } : {}),
      ...(p.sectionImdbVotes !== undefined ? { sectionImdbVotes: p.sectionImdbVotes } : {}),
      ...(p.rated ? { rated: p.rated } : {}),
      ...(p.awards ? { awards: p.awards } : {}),
    },
    sidebarFacts,
    keywords: p.keywords,
    cast: {
      members: p.cast,
      href: p.castHref,
      mediaId: p.seriesId,
      ...(p.tmdbPersonDegraded ? { loading: true } : {}),
    },
    // U-4 §9 R1 fix — inert placeholder. `SeriesDetail.tsx` passes
    // `recommendationsSlot={<SeriesRecommendationsRail .../>}` to
    // `MediaDetail`, which renders that slot INSTEAD of building the rail
    // from this field, so `MediaDetail` never reads these values.
    recommendations: {
      items: [],
      isLoading: false,
      visible: false,
      sentinelRef: { current: null },
      renderCard: () => null,
    },
    overview: {
      label: p.t('seriesDetail.overview.label'),
      text: p.overviewText,
      ...(p.overviewContentLang ? { contentLang: p.overviewContentLang } : {}),
      ...(p.lang ? { requestedLang: p.lang } : {}),
      loading: p.overviewLoading,
    },
    externalLinks: p.externalLinks,
    syncedAt: p.syncedAt,
    degraded: p.degraded,
  };
}

// §3.2 mapping table — order + testids REPLICATE `RailCard.tsx` exactly:
// status → network → studio → premiere-date → countries → original-language.
function buildSidebarFacts(
  hero: SeriesHeroDTO | undefined,
  status: StatusToken,
  t: TFunction,
): readonly MediaFact[] {
  const facts: MediaFact[] = [];

  facts.push({
    id: 'status',
    label: t('seriesDetail.rail.status'),
    value: t(`seriesDetail.status.${status}`),
    testId: 'rail-row-status',
    ...(status === 'continuing' ? { accent: true } : {}),
  });

  const network = hero?.networks?.[0];
  if (network?.name) {
    const networkLogo = mediaUrl(network.logo_asset);
    facts.push({
      id: 'network',
      label: t('seriesDetail.rail.network'),
      testId: 'rail-row-network',
      value: networkLogo ? (
        <img
          src={networkLogo}
          alt={network.name ?? ''}
          title={network.name ?? ''}
          className="h-4 w-auto object-contain opacity-90"
          loading="lazy"
        />
      ) : (
        <span className="font-mono text-[10.5px] tracking-[0.08em] uppercase">
          {network.name}
        </span>
      ),
    });
  }

  if (hero?.studio) {
    facts.push({
      id: 'studio',
      label: t('seriesDetail.rail.studio'),
      testId: 'rail-row-studio',
      value: <span data-testid="rail-row-studio-value">{hero.studio}</span>,
    });
  }

  if (hero?.premiere_date) {
    facts.push({
      id: 'premiere-date',
      label: t('seriesDetail.rail.premiereDate'),
      testId: 'rail-row-premiere-date',
      value: <PremiereDate iso={hero.premiere_date} />,
    });
  }

  const countries = hero?.countries ?? [];
  const countriesList: readonly string[] = countries.length > 0
    ? countries
    : (hero?.country ? [hero.country] : []);
  if (countriesList.length > 0) {
    facts.push({
      id: 'countries',
      label: t('seriesDetail.rail.country', { count: countriesList.length }),
      testId: 'rail-row-countries',
      value: (
        <span data-testid="rail-row-countries-value">
          {countriesList.map((c, i) => (
            <span key={`${c}-${i}`}>
              {i > 0 && ', '}
              <CountryName code={c} />
            </span>
          ))}
        </span>
      ),
    });
  }

  if (hero?.original_language) {
    facts.push({
      id: 'original-language',
      label: t('seriesDetail.rail.originalLanguage'),
      testId: 'rail-row-original-language',
      value: <LanguageName code={hero.original_language} />,
    });
  }

  return facts;
}
