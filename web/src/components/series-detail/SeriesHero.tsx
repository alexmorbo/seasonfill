import { useTranslation } from 'react-i18next';
import { useInstances } from '@/lib/instances';
import {
  parseStatus,
  type SeriesHero as HeroDTO,
  type LibraryStrip,
  type DownloadChip,
} from '@/api/series';
import { useSeriesRatings } from '@/api/seriesRatings';
import { NextEpisodeCard } from './NextEpisodeCard';
import { HeroLibraryStrip } from './HeroLibraryStrip';
import { FollowButton } from '@/components/follow/FollowButton';
import {
  useAddToSonarrLauncher,
  type AddToSonarrTarget,
} from '@/components/discovery/add-to-sonarr-context';
import { MediaHero } from '@/components/media-detail/MediaHero';
import type { MediaHeroActions } from '@/components/media-detail/view-model';
import {
  buildSeriesHeroCore,
  effectiveRatingScore,
  resolveSeriesHeroActionData,
} from '@/components/media-detail/adapters/seriesHeroVM';

export interface SeriesHeroProps {
  // Story 495 / N-1e: now optional — TMDB-only series carry no
  // primary instance; SeriesDetail picks `in_library_instances[0]`
  // and passes undefined when empty. An undefined `instance` yields no
  // `sonarrHref` because `publicUrlByName.get(instance)` is undefined
  // (the `instance ? publicUrlByName.get(instance) : undefined` ternary).
  readonly instance: string | undefined;
  readonly seriesId: number;
  readonly hero: HeroDTO | undefined;
  readonly library?: LibraryStrip | undefined;
  readonly download?: DownloadChip | undefined;
  readonly tmdbStaleAt?: string | undefined;
  readonly imdbStaleAt?: string | undefined;
  readonly titleSlug?: string | undefined;
  readonly onScrollToTorrents?: () => void;
  // Story 495 / N-1e (B-20): when true AND no backdrop_asset, render
  // the MonogramFallback with a "loading backdrop" plate overlay.
  readonly tmdbSeriesDegraded?: boolean | undefined;
  // Story 495 / N-1e (B-20): when true AND no imdb_rating, render a
  // skeleton chip on the IMDb side of `<RatingDuo>`.
  readonly imdbLoading?: boolean | undefined;
  // TMDB-only (not-in-library) series carry an add target; SeriesDetail
  // builds it only when `in_library_instances[0]` is undefined. Presence here
  // ⇒ render the hero "Add to Sonarr" button (mutually exclusive with the
  // "Open in Sonarr" button, which needs a resolved instance href).
  readonly addToSonarrTarget?: AddToSonarrTarget | undefined;
  readonly inLibraryInstances?: readonly string[];
}

// U-4 sub-step B — `SeriesHero` is now the SERIES HERO ADAPTER: it owns the
// hooks (`useInstances`, `useSeriesRatings` — the #1059 live-ratings dedup
// STAYS here, not in `MediaHero`, because `SeriesHero.test.tsx` mocks
// `useSeriesRatings` and renders `<SeriesHero>` directly) and the
// split-button resolution, builds a `MediaHeroVM` + `heroActions` +
// `heroExtras`, and delegates the actual DOM to the shared, pure
// `<MediaHero>`. Public props + rendered DOM are UNCHANGED (§3.7).
export function SeriesHero({
  instance, seriesId, hero, library, download, tmdbStaleAt, imdbStaleAt, titleSlug,
  onScrollToTorrents, tmdbSeriesDegraded, imdbLoading, addToSonarrTarget,
  inLibraryInstances = [],
}: SeriesHeroProps) {
  const { t } = useTranslation();
  const { openAddToSonarr } = useAddToSonarrLauncher();
  const instancesQ = useInstances();
  // #1059 / F-11-FE — shared live /ratings query (same key as RatingsSection ⇒
  // react-query dedups to one fetch). Drives the effective hero ★ values below.
  const { data: liveRatings } = useSeriesRatings({ seriesId });
  const status = parseStatus(hero?.status);
  const title = hero?.title ?? '';

  const tmdbScore = effectiveRatingScore(liveRatings?.tmdb_rating, liveRatings?.tmdb_votes, hero?.tmdb_rating);
  const imdbScore = effectiveRatingScore(liveRatings?.imdb_rating, liveRatings?.imdb_votes, hero?.imdb_rating);

  const heroCore = buildSeriesHeroCore({
    hero,
    tmdbScore,
    imdbScore,
    ...(tmdbStaleAt ? { tmdbStaleAt } : {}),
    ...(imdbStaleAt ? { imdbStaleAt } : {}),
    ...(imdbLoading ? { imdbLoading } : {}),
    ...(tmdbSeriesDegraded ? { tmdbSeriesDegraded } : {}),
    backdropLoadingLabel: t('seriesDetail.degraded.backdrop.loading'),
  });

  const actionData = resolveSeriesHeroActionData({
    instance,
    title,
    ...(titleSlug ? { titleSlug } : {}),
    inLibraryInstances,
    allInstances: instancesQ.data?.instances ?? [],
    hasAddToSonarrTarget: Boolean(addToSonarrTarget),
  });

  const heroActions: MediaHeroActions = {
    backHref: '/series',
    backLabel: t('seriesDetail.back'),
    ...(actionData.sonarrHref ? { sonarrHref: actionData.sonarrHref } : {}),
    showAddToSonarr: actionData.showAddToSonarr,
    showCaret: actionData.showCaret,
    openItems: actionData.openItems,
    addItems: actionData.addItems,
    onAddToSonarr: () => {
      if (addToSonarrTarget) openAddToSonarr(addToSonarrTarget);
    },
    onAddToInstance: (name: string) => {
      if (addToSonarrTarget) openAddToSonarr({ ...addToSonarrTarget, instanceName: name });
    },
    followButton: <FollowButton seriesId={seriesId} />,
  };

  const heroExtras = {
    nextCard: (
      <NextEpisodeCard
        variant="glass"
        status={status}
        {...(hero?.next_episode ? { nextEpisode: hero.next_episode } : {})}
        {...(hero?.year_end ? { yearEnd: hero.year_end } : {})}
      />
    ),
    bottomStrip: (
      <HeroLibraryStrip
        tone={heroCore.sonarrOnly ? 'light' : 'dark'}
        {...(library ? { library } : {})}
        {...(download ? { download } : {})}
        {...(onScrollToTorrents ? { onDownloadClick: onScrollToTorrents } : {})}
      />
    ),
  };

  return <MediaHero vm={{ ...heroCore, heroActions }} heroExtras={heroExtras} />;
}
