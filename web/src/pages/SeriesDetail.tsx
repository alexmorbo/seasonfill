import { useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { useParams, Link } from 'react-router-dom';
import { ChevronLeft, TriangleAlert } from 'lucide-react';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { useSetPageTitle } from '@/components/shell/page-title-context';
import { useSeriesDetail, parseStatus, isSonarrOnly, isDegraded } from '@/api/seriesDetail';
import { SeriesHero } from '@/components/series-detail/SeriesHero';
import { NextEpisodeCard } from '@/components/series-detail/NextEpisodeCard';
import { LibraryStatusCard } from '@/components/series-detail/LibraryStatusCard';
import { ExternalLinksFooter } from '@/components/series-detail/ExternalLinksFooter';
import { SeriesDetailSkeleton } from '@/components/series-detail/SeriesDetailSkeleton';
import { StaleBadge } from '@/components/series-detail/StaleBadge';

export function SeriesDetail() {
  const { t, i18n } = useTranslation();
  const { instance, id } = useParams<{ instance: string; id: string }>();
  const seriesId = id ? Number(id) : undefined;
  const lang = i18n.resolvedLanguage;

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

  useSetPageTitle(hero?.title ?? t('seriesDetail.title'));

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

  return (
    <div className="flex flex-col gap-5">
      {/* Sub-nav: back to /series. Sub-section anchors come in I-2/A-*. */}
      <nav className="flex items-center gap-2 text-[12.5px] text-tx-muted">
        <Link
          to="/series"
          className="inline-flex items-center gap-1 hover:text-tx-primary transition-colors"
          data-testid="series-detail-back"
        >
          <ChevronLeft className="w-3.5 h-3.5" aria-hidden="true" />
          {t('seriesDetail.back')}
        </Link>
      </nav>

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
            {...(tmdbStaleAt ? { tmdbStaleAt } : {})}
            {...(imdbStaleAt ? { imdbStaleAt } : {})}
          />

          <div className="grid grid-cols-1 min-[1440px]:grid-cols-[1fr_380px] gap-4">
            <div className="flex flex-col gap-3 min-w-0">
              <div className="flex items-center gap-2 text-[10.5px] font-bold uppercase tracking-wide text-tx-faint">
                {t('seriesDetail.overview.label')}
                {data.overview?.language && data.overview.language !== (lang ?? 'en')
                  && data.overview.language.toLowerCase().startsWith('en')
                  && (lang ?? 'en').toLowerCase().startsWith('ru') && (
                  <span
                    className="rounded border border-border-subtle text-tx-muted px-1.5 py-0 text-[9.5px] font-semibold"
                    data-testid="overview-lang-fallback"
                  >
                    EN
                  </span>
                )}
              </div>
              <p data-testid="overview-text" className="text-[13.5px] leading-relaxed text-tx-secondary whitespace-pre-line">
                {data.overview?.overview || t('seriesDetail.overview.empty')}
              </p>
            </div>
            <aside className="flex flex-col gap-3 min-w-0">
              <NextEpisodeCard
                {...(hero?.next_episode ? { nextEpisode: hero.next_episode } : {})}
                status={status}
                {...(hero?.year_end ? { yearEnd: hero.year_end } : {})}
              />
              {!sonarrOnly && data.overview?.keywords && data.overview.keywords.length > 0 && (
                <div
                  data-testid="overview-keywords"
                  className="flex flex-col gap-2 rounded-lg border border-border-faint bg-bg-surface/60 px-4 py-3"
                >
                  <div className="text-[10.5px] font-bold uppercase tracking-wide text-tx-faint">
                    {t('seriesDetail.overview.keywords')}
                  </div>
                  <div className="flex flex-wrap gap-1.5">
                    {data.overview.keywords.slice(0, 12).map((k) => (
                      <span
                        key={k.id ?? k.name}
                        className="rounded-md bg-bg-surface-2/70 border border-border-subtle px-1.5 py-0.5 text-[11px] text-tx-secondary"
                      >
                        {k.name}
                      </span>
                    ))}
                  </div>
                </div>
              )}
              {!omdbDegraded && data.overview?.awards && (
                <div
                  data-testid="overview-awards"
                  className="flex items-center gap-2 rounded-lg border border-border-faint bg-bg-surface/60 px-4 py-3 text-[12.5px] text-tx-secondary"
                >
                  {data.overview.awards}
                </div>
              )}
            </aside>
          </div>

          <LibraryStatusCard
            {...(data.library ? { library: data.library } : {})}
            {...(data.download ? { download: data.download } : {})}
            {...(data.recent ? { recent: data.recent } : {})}
          />

          {/* Deferred sections — placeholders so the page composition is
              locked. Each gets replaced by I-2 / A-* / K-1. */}
          <div className="flex flex-col gap-2 rounded-lg border border-dashed border-border-faint bg-bg-surface/30 px-4 py-3 text-[12px] text-tx-faint">
            <span data-testid="placeholder-seasons">{t('seriesDetail.placeholders.seasons')}</span>
            <span data-testid="placeholder-cast">{t('seriesDetail.placeholders.cast')}</span>
            <span data-testid="placeholder-torrents">{t('seriesDetail.placeholders.torrents')}</span>
            <span data-testid="placeholder-recommendations">{t('seriesDetail.placeholders.recommendations')}</span>
          </div>

          <ExternalLinksFooter {...(data.external_links ? { links: data.external_links } : {})} />

          {data.synced_at && (
            <div className="flex items-center justify-end gap-2 text-[11px] text-tx-faint pt-1">
              <span>{t('seriesDetail.synced', { time: new Date(data.synced_at).toLocaleString(lang) })}</span>
              {tmdbDegraded && <StaleBadge asOf={data.synced_at} source="tmdb" />}
              {omdbDegraded && <StaleBadge asOf={data.synced_at} source="omdb" />}
            </div>
          )}
        </>
      )}
    </div>
  );
}
