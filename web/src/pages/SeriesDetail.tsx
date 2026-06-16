import { useCallback, useMemo, useRef } from 'react';
import { useTranslation } from 'react-i18next';
import { useParams } from 'react-router-dom';
import { TriangleAlert } from 'lucide-react';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { useSetPageTitle } from '@/components/shell/page-title-context';
import { useSeriesDetail, parseStatus, isSonarrOnly, isDegraded } from '@/api/seriesDetail';
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
  const { instance, id } = useParams<{ instance: string; id: string }>();
  const seriesId = id ? Number(id) : undefined;
  const lang = i18n.resolvedLanguage;
  const fmt = useFormatDate();
  const torrentsRef = useRef<HTMLDivElement | null>(null);

  const detail = useSeriesDetail({
    instance,
    seriesId,
    ...(lang ? { lang } : {}),
  });

  const data = detail.data;
  const hero = data?.hero;
  const status = parseStatus(hero?.status);
  const sonarrOnly = useMemo(() => isSonarrOnly(hero), [hero]);
  const tmdbDegraded = isDegraded(data, 'tmdb');
  const omdbDegraded = isDegraded(data, 'omdb');
  const tmdbStaleAt = tmdbDegraded ? data?.synced_at : undefined;
  const imdbStaleAt = omdbDegraded ? data?.synced_at : undefined;
  const syncedAt = data?.synced_at;

  useSetPageTitle(hero?.title ?? t('seriesDetail.title'));

  const scrollToTorrents = useCallback(() => {
    torrentsRef.current?.scrollIntoView({ behavior: 'smooth', block: 'start' });
  }, []);

  if (!instance || !seriesId || Number.isNaN(seriesId)) {
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

  const tmdbStaleSlot = tmdbDegraded && syncedAt
    ? <StaleBadge asOf={syncedAt} source="tmdb" />
    : undefined;

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
            instance={instance}
            seriesId={seriesId}
            hero={hero}
            {...(data.library ? { library: data.library } : {})}
            {...(data.download ? { download: data.download } : {})}
            {...(tmdbStaleAt ? { tmdbStaleAt } : {})}
            {...(imdbStaleAt ? { imdbStaleAt } : {})}
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
                    <p
                      data-testid="overview-text"
                      className="text-[13.5px] leading-relaxed text-tx-primary whitespace-pre-line max-w-[64ch] [text-shadow:0_1px_2px_oklch(0_0_0/.55)]"
                    >
                      {data.overview?.overview || t('seriesDetail.overview.empty')}
                    </p>
                  </div>
                  {!sonarrOnly && (
                    <CastStrip
                      instance={instance}
                      seriesId={seriesId}
                      {...(data.cast ? { cast: data.cast } : {})}
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
            <TorrentsSection instance={instance} seriesId={seriesId} />
          </div>

          <SeasonsAccordion
            instance={instance}
            seriesId={seriesId}
            seasons={data.seasons}
            {...(lang ? { lang } : {})}
            {...(tmdbStaleSlot ? { staleBadge: tmdbStaleSlot } : {})}
          />

          <RecommendationsCarousel
            recommendations={data.recommendations}
            {...(tmdbStaleSlot ? { staleBadge: tmdbStaleSlot } : {})}
          />

          <ExternalLinksFooter {...(data.external_links ? { links: data.external_links } : {})} />

          {syncedAt && (
            <div className="flex items-center justify-end gap-2 text-[11px] text-tx-faint pt-1">
              <span>{t('seriesDetail.synced', { time: fmt(syncedAt, 'datetime') })}</span>
              {tmdbDegraded && <StaleBadge asOf={syncedAt} source="tmdb" />}
              {omdbDegraded && <StaleBadge asOf={syncedAt} source="omdb" />}
            </div>
          )}
        </>
      )}
    </div>
  );
}
