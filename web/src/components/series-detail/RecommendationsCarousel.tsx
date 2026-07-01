import { useRef, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import { Plus, Star } from 'lucide-react';
import { cn } from '@/lib/utils';
import { mediaUrl } from '@/api/series';
import { Skeleton } from '@/components/ui/skeleton';
import {
  useSeriesRecommendations,
  useIsSectionVisible,
  type Recommendation,
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

interface RecCardProps { readonly rec: Recommendation }

function RecCard({ rec }: RecCardProps) {
  const { t } = useTranslation();
  const src = mediaUrl(rec.poster_asset);
  const title = rec.title ?? '';
  const year = rec.year;
  const rating = rec.tmdb_rating;
  // B-42d / Story 542: navigate by canonical series_id regardless of
  // in_library — out-of-library recs also have canon rows in DB and
  // their detail page resolves via the TMDB-fallback path (Story 541).
  // `inLibrary` is kept ONLY as a visual hint (green badge + hover
  // CTA overlay); `hasValidId` gates the Link wrap.
  const inLibrary = Boolean(rec.in_library);
  const hasValidId = typeof rec.series_id === 'number' && rec.series_id > 0;

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

  if (hasValidId) {
    return (
      <Link
        to={`/series/${rec.series_id}`}
        className="block"
        data-testid="recommendation-link"
      >
        {body}
      </Link>
    );
  }
  return body;
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
  const lang = i18n.resolvedLanguage;

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
              <RecCard
                key={rec.series_id ?? rec.tmdb_series_id ?? rec.title ?? `idx-${idx}`}
                rec={rec}
              />
            ))}
          </div>
        </>
      )}
    </section>
  );
}
