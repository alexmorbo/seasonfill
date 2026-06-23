import type { ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import { Plus, Star } from 'lucide-react';
import { cn } from '@/lib/utils';
import { mediaUrl } from '@/api/series';
import { Skeleton } from '@/components/ui/skeleton';
import type { components } from '@/api/schema';

type Recommendation = components['schemas']['dto.Recommendation'];

export interface RecommendationsCarouselProps {
  readonly recommendations: readonly Recommendation[] | undefined;
  readonly limit?: number;
  readonly className?: string | undefined;
  // Optional badge rendered inline with the section heading
  // (used for per-section StaleBadge wire-up from SeriesDetail).
  readonly staleBadge?: ReactNode;
  // Story 495 / N-1e (B-20): when true AND list is empty, render 6
  // tile skeletons + loading label instead of returning null.
  readonly tmdbSeriesLoading?: boolean | undefined;
}

interface RecCardProps {
  readonly rec: Recommendation;
}

function RecCard({ rec }: RecCardProps) {
  const { t } = useTranslation();
  const src = mediaUrl(rec.poster_asset);
  const title = rec.title ?? '';
  const year = rec.year;
  const rating = rec.tmdb_rating;
  const inLibrary = Boolean(rec.in_library) && Boolean(rec.instance_name)
    && typeof rec.sonarr_series_id === 'number' && rec.sonarr_series_id > 0;

  const body = (
    <div
      data-testid="recommendation-card"
      data-in-library={inLibrary ? 'true' : 'false'}
      className={cn(
        'group relative flex flex-col gap-1.5 snap-start min-w-[124px] md:min-w-0',
        'rounded-md overflow-hidden',
      )}
    >
      <div className="relative aspect-[2/3] rounded-md overflow-hidden border border-border-subtle bg-bg-surface-2">
        {src ? (
          <img
            src={src}
            alt=""
            aria-hidden="true"
            loading="lazy"
            decoding="async"
            className="w-full h-full object-cover"
          />
        ) : (
          <span className="flex items-center justify-center w-full h-full text-[22px] font-bold text-tx-faint">
            {(title.charAt(0) || '?').toUpperCase()}
          </span>
        )}
        {inLibrary && (
          <span
            data-testid="recommendation-in-library"
            className="absolute top-1.5 left-1.5 inline-flex items-center gap-1 rounded-full bg-ok-dim text-ok px-1.5 py-0.5 text-[9.5px] font-bold uppercase tracking-wide"
          >
            {t('seriesDetail.recommendations.inLibrary')}
          </span>
        )}
        {!inLibrary && (
          <div
            data-testid="recommendation-add-overlay"
            className={cn(
              'absolute inset-0 flex items-center justify-center bg-bg-base/70 opacity-0',
              'group-hover:opacity-100 transition-opacity',
            )}
          >
            <span className="inline-flex items-center gap-1 rounded-full bg-bg-surface text-tx-primary px-2 py-1 text-[11px] font-semibold border border-border-subtle">
              <Plus className="w-3 h-3" aria-hidden="true" />
              {t('seriesDetail.recommendations.addCta')}
            </span>
          </div>
        )}
      </div>
      <div className="text-[12px] font-semibold text-tx-primary truncate" title={title}>{title}</div>
      <div className="flex items-center gap-1.5 text-[10.5px] text-tx-muted tabular-nums">
        {year && <span>{year}</span>}
        {rating !== undefined && rating > 0 && (
          <span className="inline-flex items-center gap-0.5">
            <Star className="w-2.5 h-2.5 text-warn" aria-hidden="true" fill="currentColor" />
            {rating.toFixed(1)}
          </span>
        )}
      </div>
    </div>
  );

  if (inLibrary) {
    return (
      <Link
        to={`/series/${encodeURIComponent(rec.instance_name as string)}/${rec.sonarr_series_id}`}
        className="block"
        data-testid="recommendation-link"
      >
        {body}
      </Link>
    );
  }
  return body;
}

export function RecommendationsCarousel({
  recommendations, limit = 8, className, staleBadge, tmdbSeriesLoading,
}: RecommendationsCarouselProps) {
  const { t } = useTranslation();
  const items = (recommendations ?? []).slice(0, limit);
  if (items.length === 0) {
    if (!tmdbSeriesLoading) return null;
    return (
      <section
        data-testid="recommendations-carousel-loading"
        aria-labelledby="recommendations-heading"
        className={cn('flex flex-col gap-3', className)}
      >
        <h2
          id="recommendations-heading"
          className="flex items-center gap-2 text-[10.5px] font-bold uppercase tracking-wide text-tx-faint"
        >
          {t('seriesDetail.recommendations.label')}
          {staleBadge}
          <span
            data-testid="recommendations-loading-label"
            className="ml-2 text-[10px] font-normal normal-case tracking-normal text-tx-muted"
          >
            {t('seriesDetail.degraded.recommendations.loading')}
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
      </section>
    );
  }
  return (
    <section
      data-testid="recommendations-carousel"
      aria-labelledby="recommendations-heading"
      className={cn('flex flex-col gap-3', className)}
    >
      <h2
        id="recommendations-heading"
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
        {items.map((rec, idx) => (
          <RecCard
            key={rec.series_id ?? rec.tmdb_series_id ?? rec.title ?? `idx-${idx}`}
            rec={rec}
          />
        ))}
      </div>
    </section>
  );
}
