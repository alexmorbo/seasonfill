import { useRef, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { cn } from '@/lib/utils';
import { toBcp47 } from '@/lib/locale';
import { Skeleton } from '@/components/ui/skeleton';
import { SeriesCard } from '@/components/series/SeriesCard';
import {
  useSeriesRecommendations,
  useIsSectionVisible,
} from '@/api/seriesRecommendations';

export interface RecommendationsCarouselProps {
  readonly seriesId: number;
  readonly limit?: number;
  readonly className?: string | undefined;
  readonly staleBadge?: ReactNode;
  // When true AND the fetched list is empty, render 6 skeleton tiles
  // + loading label instead of returning null. Used by SeriesDetail
  // when tmdb_series is in the parent /series response's degraded[].
  readonly tmdbSeriesLoading?: boolean | undefined;
}

function SkeletonGrid({
  label,
  staleBadge,
  t,
  headingId,
}: {
  label: string;
  staleBadge?: ReactNode;
  t: ReturnType<typeof useTranslation>['t'];
  headingId: string;
}) {
  return (
    <>
      <h2
        id={headingId}
        className="flex items-center gap-2 text-[10.5px] font-bold uppercase tracking-wide text-tx-faint"
      >
        {t('seriesDetail.recommendations.label')}
        {staleBadge}
        <span
          data-testid="recommendations-loading-label"
          className="ml-2 text-[10px] font-normal normal-case tracking-normal text-tx-muted"
        >
          {label}
        </span>
      </h2>
      <div
        className={cn(
          'flex flex-row gap-3 overflow-x-auto snap-x snap-mandatory pb-2',
          'md:grid md:grid-cols-6 md:gap-4 md:overflow-visible md:snap-none md:pb-0',
        )}
      >
        {Array.from({ length: 6 }).map((_, i) => (
          <div
            key={i}
            data-testid="recommendations-skeleton-tile"
            className="flex flex-col gap-1.5 min-w-[124px] md:min-w-0"
          >
            <Skeleton className="aspect-[2/3] w-full rounded-md" />
            <Skeleton className="h-3 w-[80%]" />
            <Skeleton className="h-2.5 w-[50%]" />
          </div>
        ))}
      </div>
    </>
  );
}

export function RecommendationsCarousel({
  seriesId,
  limit = 20,
  className,
  staleBadge,
  tmdbSeriesLoading,
}: RecommendationsCarouselProps) {
  const { t, i18n } = useTranslation();
  const ref = useRef<HTMLElement | null>(null);
  const visible = useIsSectionVisible(ref);
  const lang = toBcp47(i18n.resolvedLanguage);

  const query = useSeriesRecommendations({
    seriesId,
    limit,
    offset: 0,
    ...(lang ? { lang } : {}),
    enabled: visible,
    pollWhileDegraded: true,
  });

  const items = query.data?.items ?? [];
  const heading = 'recommendations-heading';
  // Story 531 — derive loading from this hook's own degraded[] so the
  // carousel surfaces skeletons even when the parent /series response
  // doesn't carry tmdb_series in its degraded list (per-section split).
  // The `tmdbSeriesLoading` prop is kept as a backward-compat fallback.
  const tmdbDegradedLocal = (query.data?.degraded ?? []).includes('tmdb_series');
  const isLoading =
    query.isLoading ||
    (items.length === 0 && (tmdbDegradedLocal || Boolean(tmdbSeriesLoading)));

  // Empty + not loading: return null (matches pre-530 behaviour).
  // Note: still attach the ref to a sentinel so we observe scroll-in.
  if (items.length === 0 && !isLoading && !visible) {
    return (
      <section
        ref={ref}
        data-testid="recommendations-carousel-sentinel"
        aria-hidden="true"
        className={cn('min-h-[1px]', className)}
      />
    );
  }
  if (items.length === 0 && !isLoading) {
    return null;
  }

  return (
    <section
      ref={ref}
      data-testid={items.length === 0 ? 'recommendations-carousel-loading' : 'recommendations-carousel'}
      data-visible={visible ? 'true' : 'false'}
      aria-labelledby={heading}
      className={cn('flex flex-col gap-3', className)}
    >
      {items.length === 0 ? (
        <SkeletonGrid
          label={t('seriesDetail.degraded.recommendations.loading')}
          staleBadge={staleBadge}
          t={t}
          headingId={heading}
        />
      ) : (
        <>
          <h2
            id={heading}
            className="flex items-center gap-2 text-[10.5px] font-bold uppercase tracking-wide text-tx-faint"
          >
            {t('seriesDetail.recommendations.label')}
            {staleBadge}
          </h2>
          <div
            className={cn(
              'flex flex-row gap-3 overflow-x-auto snap-x snap-mandatory pb-2',
              'md:grid md:grid-cols-6 md:gap-4 md:overflow-visible md:snap-none md:pb-0',
            )}
          >
            {items.slice(0, limit).map((rec, idx) => (
              <SeriesCard
                key={rec.series_id || rec.tmdb_series_id || rec.title || `idx-${idx}`}
                seriesId={rec.series_id}
                tmdbId={rec.tmdb_series_id}
                title={rec.title ?? ''}
                year={rec.year}
                posterAsset={rec.poster_asset}
                rating={rec.tmdb_rating}
                libraryBadge={rec.in_library ? 'inLibrary' : undefined}
                className="snap-start min-w-[124px] md:min-w-0"
              />
            ))}
          </div>
        </>
      )}
    </section>
  );
}
