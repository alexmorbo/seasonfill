import { useCallback, useMemo, useRef } from 'react';
import { useTranslation } from 'react-i18next';
import { useParams } from 'react-router-dom';
import { TriangleAlert } from 'lucide-react';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Skeleton } from '@/components/ui/skeleton';
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
import { useSeriesLibrary } from '@/api/seriesLibrary';
import { SeriesHero } from '@/components/series-detail/SeriesHero';
import { DegradedChip } from '@/components/series-detail/DegradedChip';
import { OverviewGrid } from '@/components/series-detail/OverviewGrid';
import { RailCard } from '@/components/series-detail/RailCard';
import { CastStrip } from '@/components/series-detail/CastStrip';
import { AwardsBlock } from '@/components/series-detail/AwardsBlock';
import { RecentStrip } from '@/components/series-detail/RecentStrip';
import { SeriesDetailSkeleton } from '@/components/series-detail/SeriesDetailSkeleton';
import { StaleBadge } from '@/components/series-detail/StaleBadge';
import { SeasonsAccordion } from '@/components/series-detail/SeasonsAccordion';
import { RecommendationsCarousel } from '@/components/series-detail/RecommendationsCarousel';
import { ExternalLinksFooter } from '@/components/series-detail/ExternalLinksFooter';
import { LanguageFallbackTag } from '@/components/series-detail/LanguageFallbackTag';
import { TorrentsSection } from '@/components/torrents/TorrentsSection';
import { useFormatDate } from '@/lib/timezone';

export function SeriesDetail() {
  const { t, i18n } = useTranslation();
  // Story 495 / N-1e §A1: URL is global — `:instance` segment is gone.
  // The primary instance for downstream sections is derived from
  // `skeleton.in_library_instances[0]` after fetch.
  const { id } = useParams<{ id: string }>();
  const seriesId = id ? Number(id) : undefined;
  const lang = i18n.resolvedLanguage;
  const fmt = useFormatDate();
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

  // Hero view-model composed from skeleton hero + sidebar.
  const hero = useMemo(
    () => adaptHero(skeleton?.hero, skeleton?.sidebar),
    [skeleton?.hero, skeleton?.sidebar],
  );
  const status = parseStatus(hero?.status);
  const sonarrOnly = useMemo(() => isSonarrOnly(hero), [hero]);

  // Story 529 — overview block loads from its own endpoint.
  const overviewQ = useSeriesOverview({
    seriesId,
    ...(lang ? { lang } : {}),
    pollWhileDegraded: true,
  });
  const overviewData = overviewQ.data?.overview;

  // C3b — cast strip loads from /series/:id/cast; adaptCast renames
  // tmdb_id → tmdb_person_id for the /person link guard.
  const castQ = useSeriesCast({ seriesId, ...(lang ? { lang } : {}) });
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

  // Story 970 / C3c-2 — per-season on-disk / downloading counts, keyed by
  // season_number, from the /library endpoint (per-instance). Undefined ⇒
  // TMDB-only (no /library call) ⇒ accordion shows totals only.
  const librarySeasons = useMemo(() => {
    const rows = libraryQ.data?.seasons;
    if (!rows) return undefined;
    const m = new Map<number, { onDisk: number; downloading: number }>();
    for (const s of rows) {
      if (typeof s.season_number !== 'number') continue;
      m.set(s.season_number, {
        onDisk: s.episodes_on_disk ?? 0,
        downloading: s.downloading ?? 0,
      });
    }
    return m;
  }, [libraryQ.data?.seasons]);

  // Story 531 — shadow the recommendations query at the page level so the
  // global degraded chip aggregates it even when the carousel is below the
  // fold. Same cache key ⇒ TanStack dedupes, no extra traffic.
  const recsQ = useSeriesRecommendations({
    seriesId,
    ...(lang ? { lang } : {}),
    enabled: typeof seriesId === 'number' && seriesId > 0,
    pollWhileDegraded: true,
  });

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
  const tmdbStaleAt = tmdbSeriesDegraded ? syncedAt : undefined;
  const imdbStaleAt = omdbDegraded ? syncedAt : undefined;

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
  const castEmpty = cast.length === 0;
  const castLoading = castEmpty && tmdbPersonDegraded;
  const showCastSection = !sonarrOnly && (!castEmpty || castLoading);
  const seasonsEmpty = seasons.length === 0;
  const seasonsLoading = seasonsEmpty && (tmdbSeasonDegraded || tmdbSeriesDegraded);
  const imdbLoading = omdbDegraded && !hero?.imdb_rating;

  // Build the cast href once so CastStrip stays URL-agnostic (Story 495 §A3).
  const castHref = `/series/${seriesId}/cast`;

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
        <>
          <SeriesHero
            instance={primaryInstance}
            seriesId={seriesId}
            hero={hero}
            {...(library ? { library } : {})}
            {...(tmdbStaleAt ? { tmdbStaleAt } : {})}
            {...(imdbStaleAt ? { imdbStaleAt } : {})}
            {...(tmdbSeriesDegraded ? { tmdbSeriesDegraded: true } : {})}
            {...(imdbLoading ? { imdbLoading: true } : {})}
            onScrollToTorrents={scrollToTorrents}
          />

          {aggregatedDegraded.length > 0 && (
            <div className="-mt-2 flex justify-end">
              <DegradedChip sources={aggregatedDegraded} />
            </div>
          )}

          <section
            data-testid="overview-section"
            className="relative z-[2] rounded-md"
          >
            <OverviewGrid
              left={
                <>
                  <div className="flex flex-col gap-3 min-w-0">
                    <div className="flex items-center gap-2 text-[10.5px] font-bold uppercase tracking-wide text-tx-faint [text-shadow:0_1px_2px_oklch(0_0_0/.55)]">
                      {t('seriesDetail.overview.label')}
                      <LanguageFallbackTag
                        contentLang={overviewData?.language}
                        {...(lang ? { requestedLang: lang } : {})}
                        testid="overview-lang-fallback"
                      />
                    </div>
                    {overviewLoading && (
                      <div
                        data-testid="overview-skeleton"
                        className="flex flex-col gap-1.5 max-w-[64ch]"
                      >
                        <Skeleton className="h-3 w-full" />
                        <Skeleton className="h-3 w-[92%]" />
                        <Skeleton className="h-3 w-[78%]" />
                      </div>
                    )}
                    <p
                      data-testid="overview-text"
                      className="text-[13.5px] leading-relaxed text-tx-primary whitespace-pre-line max-w-[64ch] [text-shadow:0_1px_2px_oklch(0_0_0/.55)]"
                    >
                      {overviewLoading
                        ? t('seriesDetail.degraded.overview.loading')
                        : (overviewData?.overview || t('seriesDetail.overview.empty'))}
                    </p>
                  </div>
                  {showCastSection && (
                    <CastStrip
                      castHref={castHref}
                      seriesId={seriesId}
                      cast={cast}
                      {...(tmdbPersonDegraded ? { tmdbPersonDegraded: true } : {})}
                    />
                  )}
                  {/* B-36: awards relocated from right MetaSidebar to under
                      cast. AwardsBlock self-hides when awards is empty / N/A
                      or omdb is degraded — no outer guard needed here. */}
                  <AwardsBlock
                    awards={overviewData?.awards ?? undefined}
                    omdbDegraded={omdbDegraded}
                    {...(syncedAt ? { syncedAt } : {})}
                  />
                </>
              }
              right={
                <RailCard
                  status={status}
                  hero={hero}
                  omdbDegraded={omdbDegraded}
                  {...(overviewData?.keywords ? { keywords: overviewData.keywords } : {})}
                />
              }
            />
          </section>

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
          />

          <RecommendationsCarousel
            seriesId={seriesId}
            {...(tmdbStaleSlot ? { staleBadge: tmdbStaleSlot } : {})}
          />

          <ExternalLinksFooter links={skeleton.external_links} />

          {syncedAt && (
            <div className="flex items-center justify-end gap-2 text-[11px] text-tx-faint pt-1">
              <span>{t('seriesDetail.synced', { time: fmt(syncedAt, 'datetime') })}</span>
              {tmdbSeriesDegraded && <StaleBadge asOf={syncedAt} source="tmdb" />}
              {omdbDegraded && <StaleBadge asOf={syncedAt} source="omdb" />}
            </div>
          )}
        </>
      )}
    </div>
  );
}
