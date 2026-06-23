import { useCallback, useMemo, useRef } from 'react';
import { useTranslation } from 'react-i18next';
import { useParams } from 'react-router-dom';
import { TriangleAlert } from 'lucide-react';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Skeleton } from '@/components/ui/skeleton';
import { useSetPageTitle } from '@/components/shell/page-title-context';
import { useSeries, parseStatus, isSonarrOnly, isDegraded } from '@/api/series';
import { SeriesHero } from '@/components/series-detail/SeriesHero';
import { OverviewGrid } from '@/components/series-detail/OverviewGrid';
import { RailCard } from '@/components/series-detail/RailCard';
import { CastStrip } from '@/components/series-detail/CastStrip';
import { RecentStrip } from '@/components/series-detail/RecentStrip';
import { ExternalLinksFooter } from '@/components/series-detail/ExternalLinksFooter';
import { SeriesDetailSkeleton } from '@/components/series-detail/SeriesDetailSkeleton';
import { StaleBadge } from '@/components/series-detail/StaleBadge';
import { SeasonsAccordion } from '@/components/series-detail/SeasonsAccordion';
import { RecommendationsCarousel } from '@/components/series-detail/RecommendationsCarousel';
import { LanguageFallbackTag } from '@/components/series-detail/LanguageFallbackTag';
import { TorrentsSection } from '@/components/torrents/TorrentsSection';
import { useFormatDate } from '@/lib/timezone';

export function SeriesDetail() {
  const { t, i18n } = useTranslation();
  // Story 495 / N-1e §A1: URL is global — `:instance` segment is gone.
  // The primary instance for downstream sections (`<SeriesHero>` Sonarr
  // link, `<CastStrip>` back-link, `<TorrentsSection>` qBit fetch) is
  // derived from `data.in_library_instances[0]` after fetch.
  const { id } = useParams<{ id: string }>();
  const seriesId = id ? Number(id) : undefined;
  const lang = i18n.resolvedLanguage;
  const fmt = useFormatDate();
  const torrentsRef = useRef<HTMLDivElement | null>(null);

  // Story 495 / N-1e (B-20): poll while a hot degraded source is
  // active. Tick budget lives inside `useSeries` (~30 s cap).
  const detail = useSeries({
    seriesId,
    ...(lang ? { lang } : {}),
    pollWhileDegraded: true,
  });

  const data = detail.data;
  const hero = data?.hero;
  const status = parseStatus(hero?.status);
  const sonarrOnly = useMemo(() => isSonarrOnly(hero), [hero]);
  // Story 495 / N-1e §C1: composer emits `tmdb_series`, not `'tmdb'`.
  // The legacy call site was always returning `false` against live data.
  const tmdbSeriesDegraded = isDegraded(data, 'tmdb_series');
  const tmdbSeasonDegraded = isDegraded(data, 'tmdb_season');
  const tmdbPersonDegraded = isDegraded(data, 'tmdb_person');
  const omdbDegraded = isDegraded(data, 'omdb');
  const tmdbStaleAt = tmdbSeriesDegraded ? data?.synced_at : undefined;
  const imdbStaleAt = omdbDegraded ? data?.synced_at : undefined;
  const syncedAt = data?.synced_at;

  // Story 495 / N-1e §A1: pick the first in-library instance as the
  // anchor for downstream sections. Undefined ⇒ TMDB-only series.
  const primaryInstance = data?.in_library_instances?.[0];

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
  // Overview text:
  //   - empty + tmdb_series in degraded ⇒ "загружается" copy + skeleton.
  //   - empty + tmdb_series NOT in degraded ⇒ existing "недоступно" fallback.
  //   - non-empty ⇒ normal text.
  const overviewEmpty = !data?.overview?.overview;
  const overviewLoading = overviewEmpty && tmdbSeriesDegraded;
  // Cast strip — degraded UI is internal to CastStrip; pass the bool.
  const castEmpty = (data?.cast?.length ?? 0) === 0;
  const castLoading = castEmpty && tmdbPersonDegraded;
  const showCastSection = !sonarrOnly && (!castEmpty || castLoading);
  // Seasons / Recommendations — same shape.
  const seasonsEmpty = (data?.seasons?.length ?? 0) === 0;
  const seasonsLoading = seasonsEmpty && (tmdbSeasonDegraded || tmdbSeriesDegraded);
  const recsEmpty = (data?.recommendations?.length ?? 0) === 0;
  const recsLoading = recsEmpty && tmdbSeriesDegraded;
  // IMDb rating loading slot in hero (RatingDuo handles the render).
  const imdbLoading = omdbDegraded && !hero?.imdb_rating;

  // Build the cast href once so CastStrip stays URL-agnostic
  // (Story 495 §A3).
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

      {detail.isSuccess && data && (
        <>
          <SeriesHero
            instance={primaryInstance}
            seriesId={seriesId}
            hero={hero}
            {...(data.library ? { library: data.library } : {})}
            {...(data.download ? { download: data.download } : {})}
            {...(tmdbStaleAt ? { tmdbStaleAt } : {})}
            {...(imdbStaleAt ? { imdbStaleAt } : {})}
            {...(tmdbSeriesDegraded ? { tmdbSeriesDegraded: true } : {})}
            {...(imdbLoading ? { imdbLoading: true } : {})}
            onScrollToTorrents={scrollToTorrents}
          />

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
                        contentLang={data.overview?.language}
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
                        : (data.overview?.overview || t('seriesDetail.overview.empty'))}
                    </p>
                  </div>
                  {showCastSection && (
                    <CastStrip
                      castHref={castHref}
                      seriesId={seriesId}
                      {...(data.cast ? { cast: data.cast } : {})}
                      {...(tmdbPersonDegraded ? { tmdbPersonDegraded: true } : {})}
                    />
                  )}
                </>
              }
              right={
                <RailCard
                  status={status}
                  hero={hero}
                  {...(data.overview?.awards ? { awards: data.overview.awards } : {})}
                  omdbDegraded={omdbDegraded}
                  {...(data.overview?.keywords ? { keywords: data.overview.keywords } : {})}
                />
              }
            />
          </section>

          <RecentStrip {...(data.recent ? { recent: data.recent } : {})} />

          <div ref={torrentsRef}>
            <TorrentsSection instance={primaryInstance ?? ''} seriesId={seriesId} />
          </div>

          <SeasonsAccordion
            seriesId={seriesId}
            seasons={data.seasons}
            {...(lang ? { lang } : {})}
            {...(tmdbStaleSlot ? { staleBadge: tmdbStaleSlot } : {})}
            {...(seasonsLoading ? { tmdbSeasonLoading: true } : {})}
          />

          <RecommendationsCarousel
            recommendations={data.recommendations}
            {...(tmdbStaleSlot ? { staleBadge: tmdbStaleSlot } : {})}
            {...(recsLoading ? { tmdbSeriesLoading: true } : {})}
          />

          <ExternalLinksFooter {...(data.external_links ? { links: data.external_links } : {})} />

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
