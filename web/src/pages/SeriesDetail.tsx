import { useCallback, useMemo, useRef } from 'react';
import { useTranslation } from 'react-i18next';
import { useParams } from 'react-router-dom';
import { TriangleAlert } from 'lucide-react';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { useSetPageTitle } from '@/components/shell/page-title-context';
import {
  useSeries,
  adaptHero,
  adaptCast,
  adaptSeasons,
  parseStatus,
  isSonarrOnly,
  aggregateDegraded,
  type DegradedSource,
} from '@/api/series';
import { useSeriesOverview } from '@/api/seriesOverview';
import { useSeriesRecommendations } from '@/api/seriesRecommendations';
import { useSeriesCast } from '@/api/seriesCast';
import { useSeriesSeasons } from '@/api/seriesSeasons';
import { useSeriesLibrary, useSeriesLibraryMonitoredByInstance } from '@/api/seriesLibrary';
import { useInstances } from '@/lib/instances';
import { useInstanceFilter } from '@/lib/instance-filter-context-internal';
import { useSeriesRatings } from '@/api/seriesRatings';
import {
  useAddToSonarrLauncher,
  type AddToSonarrTarget,
} from '@/components/discovery/add-to-sonarr-context';
import { RecentStrip } from '@/components/series-detail/RecentStrip';
import { SeriesDetailSkeleton } from '@/components/series-detail/SeriesDetailSkeleton';
import { StaleBadge } from '@/components/series-detail/StaleBadge';
import { SeasonsAccordion } from '@/components/series-detail/SeasonsAccordion';
import { NextEpisodeCard } from '@/components/series-detail/NextEpisodeCard';
import { HeroLibraryStrip } from '@/components/series-detail/HeroLibraryStrip';
import { FollowButton } from '@/components/follow/FollowButton';
import { TorrentsSection } from '@/components/torrents/TorrentsSection';
import { MediaDetail } from '@/components/media-detail';
import type { MediaHeroActions } from '@/components/media-detail/view-model';
import {
  effectiveRatingScore,
  resolveSeriesHeroActionData,
} from '@/components/media-detail/adapters/seriesHeroVM';
import { SeriesRecommendationsRail } from '@/components/media-detail/adapters/SeriesRecommendationsRail';
import { toSeriesVM } from './toSeriesVM';

export function SeriesDetail() {
  const { t, i18n } = useTranslation();
  // Story 495 / N-1e §A1: URL is global — `:instance` segment is gone.
  // The primary instance for downstream sections is derived from
  // `skeleton.in_library_instances[0]` after fetch.
  const { id } = useParams<{ id: string }>();
  const seriesId = id ? Number(id) : undefined;
  const lang = i18n.resolvedLanguage;
  const torrentsRef = useRef<HTMLDivElement | null>(null);

  // C3b (story 968): GET /series/:id now serves seriesdetail.SkeletonDTO
  // (hero + sidebar + degraded + synced_at). Hero + rail paint immediately;
  // heavy sections load from their own lazy hooks below.
  const detail = useSeries({
    seriesId,
    ...(lang ? { lang } : {}),
    pollWhileDegraded: true,
  });
  const skeleton = detail.data;

  // Primary instance drives all Sonarr-scoped sections. Undefined ⇒ TMDB-only.
  const primaryInstance = skeleton?.in_library_instances?.[0];

  // ADR-0012 S3 — the seasons accordion's per-season split-button targets the
  // SIDEBAR-selected instance by default, falling back to the series' primary,
  // then the first configured instance. The /library query below is scoped to it.
  const instances = useInstances().data?.instances ?? [];
  const sidebarFilter = useInstanceFilter().filter;
  const defaultSeasonInstance = sidebarFilter ?? primaryInstance ?? instances[0]?.name;
  // U-4 sub-step B — hero split-button "Add to Sonarr" launcher, resolved
  // into `heroActions` below (moved out of the old inline `<SeriesHero>`).
  const { openAddToSonarr } = useAddToSonarrLauncher();

  // Hero view-model composed from skeleton hero + sidebar.
  const hero = useMemo(
    () => adaptHero(skeleton?.hero, skeleton?.sidebar),
    [skeleton?.hero, skeleton?.sidebar],
  );
  const status = parseStatus(hero?.status);
  const sonarrOnly = useMemo(() => isSonarrOnly(hero), [hero]);

  // TMDB-only series (no primary instance ⇒ not in any Sonarr library) get an
  // "Add to Sonarr" hero button. Identity comes from external_links (tvdb/tmdb)
  // — NOT skeleton.series_id (that's the internal id). The modal tolerates a
  // missing tvdb, so the button is NOT gated on tvdb presence.
  const addToSonarrTarget = useMemo<AddToSonarrTarget | undefined>(() => {
    const links = skeleton?.external_links;
    return {
      title: hero?.title ?? '',
      ...(links?.tvdb_id !== undefined ? { tvdbId: links.tvdb_id } : {}),
      ...(links?.tmdb_id !== undefined ? { tmdbId: links.tmdb_id } : {}),
    };
  }, [skeleton?.external_links, hero?.title]);

  // Story 529 — overview block loads from its own endpoint.
  const overviewQ = useSeriesOverview({
    seriesId,
    ...(lang ? { lang } : {}),
    pollWhileDegraded: true,
  });
  const overviewData = overviewQ.data?.overview;

  // C3b — cast strip loads from /series/:id/cast; adaptCast renames
  // tmdb_id → tmdb_person_id for the /person link guard. Story 1087a —
  // request only the 8 the strip shows (top-N by episode_count on the BE);
  // the full /cast page uses a separate query key (no limit) so it still
  // loads the complete list.
  const castQ = useSeriesCast({ seriesId, ...(lang ? { lang } : {}), limit: 8 });
  const cast = useMemo(() => adaptCast(castQ.data?.cast), [castQ.data?.cast]);

  // C3b — seasons summary loads from /series/:id/seasons; per-season episode
  // state still lazy-loads on accordion expand via useSeriesSeason.
  const seasonsQ = useSeriesSeasons({
    seriesId,
    ...(lang ? { lang } : {}),
    pollWhileDegraded: true,
  });
  const seasons = useMemo(
    () => adaptSeasons(seasonsQ.data?.seasons),
    [seasonsQ.data?.seasons],
  );

  // C3b — Sonarr library strip + recent grabs from /series/:id/library.
  // Disabled when TMDB-only (no primary instance).
  const libraryQ = useSeriesLibrary({ seriesId, instance: primaryInstance });
  const library = libraryQ.data?.library;
  const recent = libraryQ.data?.recent;
  // C3c-3 (story 971) — hero download chip (first in-flight Sonarr queue record).
  // undefined when nothing downloading / Sonarr unreachable / TMDB-only (no /library
  // call) — SeriesHero then renders no chip.
  const download = libraryQ.data?.download;

  // ADR-0012 S3 — the accordion's per-season counts + monitored flags are
  // scoped to `defaultSeasonInstance` (the sidebar-selected instance the primary
  // split-button targets), NOT necessarily the primary. Separate query key ⇒
  // TanStack caches per instance; the hero keeps its primary-scoped libraryQ
  // above unchanged.
  const seasonsLibraryQ = useSeriesLibrary({
    seriesId,
    instance: defaultSeasonInstance,
  });

  // Story 970 / C3c-2 — per-season on-disk / downloading counts (+ ADR-0012 S2
  // monitored flag), keyed by season_number, from the /library endpoint
  // (per-instance). Undefined ⇒ TMDB-only (no /library call) ⇒ accordion shows
  // totals only.
  const librarySeasons = useMemo(() => {
    const rows = seasonsLibraryQ.data?.seasons;
    if (!rows) return undefined;
    const m = new Map<
      number,
      { onDisk: number; downloading: number; monitored: boolean }
    >();
    for (const s of rows) {
      if (typeof s.season_number !== 'number') continue;
      m.set(s.season_number, {
        onDisk: s.episodes_on_disk ?? 0,
        downloading: s.downloading ?? 0,
        monitored: s.monitored ?? false,
      });
    }
    return m;
  }, [seasonsLibraryQ.data?.seasons]);

  // ADR-0012 S5 — monitored season_numbers per in-library instance, so the
  // per-season caret can hide instances where the season is already monitored.
  // Reuses seriesLibraryQueryKey ⇒ the default instance's fetch dedups with
  // seasonsLibraryQ above. Scoped to in_library_instances only — instances that
  // lack the series need no /library call (the caret keeps them via the add path).
  const monitoredByInstance = useSeriesLibraryMonitoredByInstance(
    seriesId,
    skeleton?.in_library_instances ?? [],
  );

  // Story 531 — shadow the recommendations query at the page level so the
  // global degraded chip aggregates it even when the carousel is below the
  // fold. Same cache key ⇒ TanStack dedupes, no extra traffic.
  const recsQ = useSeriesRecommendations({
    seriesId,
    ...(lang ? { lang } : {}),
    enabled: typeof seriesId === 'number' && seriesId > 0,
    pollWhileDegraded: true,
  });
  // U-4 §9 R1 fix — the recommendations RAIL's own (visibility-gated) query
  // + sentinel ref hook is now owned by `<SeriesRecommendationsRail>` itself
  // (rendered below via `MediaDetail`'s `recommendationsSlot`), NOT called
  // here at the page's top level — calling it here ran the hook's one-shot
  // `useIsSectionVisible` effect BEFORE the success branch (and its
  // sentinel) ever mounted, so the IntersectionObserver was never attached
  // and the rail silently never fetched/rendered in a real browser. `recsQ`
  // above (the page-wide shadow query feeding the degraded chip) is
  // distinct and UNCHANGED by this fix.

  // #1064 — shadow the shared /ratings query at the page level so the hero's
  // ratings stale/loading SIGNALS read the SAME source of truth as its ★
  // NUMBERS (#1059 already repointed the numbers). Same query key as
  // SeriesHero / RatingsSection ⇒ react-query dedups to ONE network fetch, no
  // extra traffic. Per-source status vocabulary (dto.SeriesRatingsSources):
  // fresh → nothing; revalidating → StaleBadge (value present but stale);
  // pending → loading chip (value still absent); unavailable → nothing.
  const ratingsQ = useSeriesRatings({ seriesId });
  const ratingSources = ratingsQ.data?.sources;
  const tmdbRatingStale = ratingSources?.tmdb === 'revalidating';
  const omdbRatingStale = ratingSources?.omdb === 'revalidating';
  const omdbRatingPending = ratingSources?.omdb === 'pending';
  // U-4 sub-step B — #1059 dedup hoisted to the page level (this is now the
  // SOLE production render path for the hero; `SeriesHero.tsx` keeps its own
  // copy only for its direct-render test — §3.7 / §7 R3). Same shared
  // /ratings query as above ⇒ no extra fetch.
  const tmdbScore = effectiveRatingScore(ratingsQ.data?.tmdb_rating, ratingsQ.data?.tmdb_votes, hero?.tmdb_rating);
  const imdbScore = effectiveRatingScore(ratingsQ.data?.imdb_rating, ratingsQ.data?.imdb_votes, hero?.imdb_rating);

  // Story 531 / C3b — aggregate degraded[] across the parent /series skeleton
  // and the /overview, /recommendations, /cast, /seasons per-section hooks.
  // Dedup'd + filtered to KNOWN_DEGRADED. /library carries no degraded field.
  const aggregatedDegraded = useMemo<readonly DegradedSource[]>(
    () =>
      aggregateDegraded(
        skeleton?.degraded,
        overviewQ.data?.degraded,
        recsQ.data?.degraded,
        castQ.data?.degraded,
        seasonsQ.data?.degraded,
      ),
    [
      skeleton?.degraded,
      overviewQ.data?.degraded,
      recsQ.data?.degraded,
      castQ.data?.degraded,
      seasonsQ.data?.degraded,
    ],
  );
  const tmdbSeriesDegraded = aggregatedDegraded.includes('tmdb_series');
  const tmdbSeasonDegraded = aggregatedDegraded.includes('tmdb_season');
  const tmdbPersonDegraded = aggregatedDegraded.includes('tmdb_person');
  const omdbDegraded = aggregatedDegraded.includes('omdb');
  const syncedAt = skeleton?.synced_at;
  // #1064 — hero ratings StaleBadges now key off the /ratings per-source status
  // (revalidating = present-but-stale), NOT degraded[]. The /ratings DTO carries
  // no per-source timestamp, so syncedAt remains the best "as of" anchor.
  //
  // AUDIT-S6 (F-08) — /ratings.sources is only the source of truth when the
  // fetch actually resolved. When it ERRORS (5xx/network) or is still in flight,
  // `ratingSources` is undefined → every `=== 'revalidating' | 'pending'` check
  // silently goes false, hiding the hero StaleBadge + IMDb chip even though the
  // skeleton's degraded[] still legitimately reports the source stale. Fall back
  // to the pre-#1064 degraded[] signals ONLY when /ratings is unavailable; when
  // sources ARE present these resolve byte-for-byte to #1064's behaviour.
  const ratingsLoadingInitial = ratingSources === undefined && !ratingsQ.isError;
  const ratingsUnavailable = ratingsQ.isError || ratingSources === undefined;
  const imdbValuePresent = (hero?.imdb_rating?.score ?? 0) > 0;
  const tmdbRatingStaleEff = ratingsUnavailable ? tmdbSeriesDegraded : tmdbRatingStale;
  const omdbRatingStaleEff = ratingsUnavailable ? omdbDegraded : omdbRatingStale;
  const omdbRatingPendingEff = ratingsUnavailable
    ? (ratingsLoadingInitial || omdbDegraded) && !imdbValuePresent
    : omdbRatingPending;
  const tmdbStaleAt = tmdbRatingStaleEff ? syncedAt : undefined;
  const imdbStaleAt = omdbRatingStaleEff ? syncedAt : undefined;

  useSetPageTitle(hero?.title ?? t('seriesDetail.title'));

  const scrollToTorrents = useCallback(() => {
    torrentsRef.current?.scrollIntoView({ behavior: 'smooth', block: 'start' });
  }, []);

  if (!seriesId || Number.isNaN(seriesId)) {
    return (
      <div className="p-4">
        <Alert variant="destructive">
          <TriangleAlert className="h-4 w-4" />
          <AlertTitle>{t('seriesDetail.errors.invalidParams')}</AlertTitle>
          <AlertDescription>{t('seriesDetail.errors.invalidParamsBody')}</AlertDescription>
        </Alert>
      </div>
    );
  }

  const tmdbStaleSlot = tmdbSeriesDegraded && syncedAt
    ? <StaleBadge asOf={syncedAt} source="tmdb" />
    : undefined;

  // Story 495 / N-1e §C2: per-section degraded UX.
  const overviewEmpty = !overviewData?.overview;
  const overviewLoading = overviewQ.isLoading || (overviewEmpty && tmdbSeriesDegraded);
  const seasonsEmpty = seasons.length === 0;
  const seasonsLoading = seasonsEmpty && (tmdbSeasonDegraded || tmdbSeriesDegraded);
  // #1064 — hero IMDb loading chip keys off /ratings (omdb `pending` = value
  // still absent + fetch in flight). AUDIT-S6 (F-08) — `omdbRatingPendingEff`
  // additionally covers the initial /ratings round-trip and the /ratings-error
  // fallback (degraded[]'s `omdb`), so the chip doesn't gap on first fetch nor
  // vanish on a /ratings outage.
  const imdbLoading = omdbRatingPendingEff;

  // Build the cast href once so CastStrip stays URL-agnostic (Story 495 §A3).
  const castHref = `/series/${seriesId}/cast`;

  // U-4 sub-step B — hero split-button data (instance/href resolution moved
  // out of the old inline `<SeriesHero>`; same logic now lives in the
  // shared `resolveSeriesHeroActionData` helper — see `SeriesHero.tsx`'s
  // adapter for the byte-identical direct-render counterpart).
  const actionData = resolveSeriesHeroActionData({
    instance: primaryInstance,
    title: hero?.title ?? '',
    ...(libraryQ.data?.title_slug ? { titleSlug: libraryQ.data.title_slug } : {}),
    inLibraryInstances: skeleton?.in_library_instances ?? [],
    allInstances: instances,
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
        tone={sonarrOnly ? 'light' : 'dark'}
        {...(library ? { library } : {})}
        {...(download ? { download } : {})}
        onDownloadClick={scrollToTorrents}
      />
    ),
  };

  const belowGrid = (
    <>
      <RecentStrip {...(recent ? { recent } : {})} />

      <div ref={torrentsRef}>
        <TorrentsSection instance={primaryInstance ?? ''} seriesId={seriesId} />
      </div>

      <SeasonsAccordion
        seriesId={seriesId}
        seasons={seasons}
        {...(lang ? { lang } : {})}
        {...(tmdbStaleSlot ? { staleBadge: tmdbStaleSlot } : {})}
        {...(seasonsLoading ? { tmdbSeasonLoading: true } : {})}
        {...(librarySeasons ? { librarySeasons } : {})}
        {...(defaultSeasonInstance ? { defaultInstance: defaultSeasonInstance } : {})}
        inLibraryInstances={skeleton?.in_library_instances ?? []}
        monitoredByInstance={monitoredByInstance}
        title={hero?.title ?? ''}
        {...(skeleton?.external_links?.tvdb_id !== undefined ? { tvdbId: skeleton.external_links.tvdb_id } : {})}
        {...(skeleton?.external_links?.tmdb_id !== undefined ? { tmdbId: skeleton.external_links.tmdb_id } : {})}
      />
    </>
  );

  const vm = toSeriesVM({
    seriesId,
    t,
    lang,
    hero,
    status,
    tmdbSeriesDegraded,
    tmdbScore,
    imdbScore,
    tmdbStaleAt,
    imdbStaleAt,
    imdbLoading,
    sectionTmdbRating: ratingsQ.data?.tmdb_rating,
    sectionTmdbVotes: ratingsQ.data?.tmdb_votes,
    sectionImdbRating: ratingsQ.data?.imdb_rating,
    sectionImdbVotes: ratingsQ.data?.imdb_votes,
    rated: ratingsQ.data?.rated,
    awards: ratingsQ.data?.awards,
    heroActions,
    overviewText: overviewLoading
      ? t('seriesDetail.degraded.overview.loading')
      : (overviewData?.overview || t('seriesDetail.overview.empty')),
    overviewContentLang: overviewData?.language,
    overviewLoading,
    castHref,
    cast,
    tmdbPersonDegraded,
    keywords: overviewData?.keywords ?? [],
    externalLinks: skeleton?.external_links,
    syncedAt,
    degraded: aggregatedDegraded,
  });

  return (
    <div className="sd-real -mt-5 flex flex-col gap-5 px-[36px] lg:px-[36px]">
      {detail.isPending && <SeriesDetailSkeleton />}

      {detail.isError && (
        <Alert variant="destructive" data-testid="series-detail-error">
          <TriangleAlert className="h-4 w-4" />
          <AlertTitle>{t('seriesDetail.errors.loadFailedTitle')}</AlertTitle>
          <AlertDescription>
            {detail.error instanceof Error ? detail.error.message : t('seriesDetail.errors.loadFailedBody')}
          </AlertDescription>
        </Alert>
      )}

      {detail.isSuccess && skeleton && (
        <MediaDetail
          vm={vm}
          heroExtras={heroExtras}
          belowGrid={belowGrid}
          recommendationsSlot={
            <SeriesRecommendationsRail
              seriesId={seriesId}
              {...(tmdbStaleSlot ? { staleBadge: tmdbStaleSlot } : {})}
            />
          }
        />
      )}
    </div>
  );
}
