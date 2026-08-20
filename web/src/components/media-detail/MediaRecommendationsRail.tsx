import type { ReactNode, RefObject } from 'react';
import { cn } from '@/lib/utils';
import { Skeleton } from '@/components/ui/skeleton';
import type { RecommendationItem } from './view-model';

export interface MediaRecommendationsRailProps {
  readonly items: readonly RecommendationItem[];
  readonly isLoading: boolean;
  readonly visible: boolean;
  readonly sentinelRef: RefObject<HTMLElement | null>;
  readonly renderCard: (item: RecommendationItem, idx: number) => ReactNode;
  readonly limit?: number;
  readonly className?: string | undefined;
  readonly staleBadge?: ReactNode;
  readonly label: string;          // i18n-resolved "Recommendations"
  readonly loadingLabel: string;   // i18n-resolved loading string
}

function SkeletonGrid({
  label,
  loadingLabel,
  staleBadge,
  headingId,
}: {
  label: string;
  loadingLabel: string;
  staleBadge?: ReactNode;
  headingId: string;
}) {
  return (
    <>
      <h2
        id={headingId}
        className="flex items-center gap-2 text-[10.5px] font-bold uppercase tracking-wide text-tx-faint"
      >
        {label}
        {staleBadge}
        <span
          data-testid="recommendations-loading-label"
          className="ml-2 text-[10px] font-normal normal-case tracking-normal text-tx-muted"
        >
          {loadingLabel}
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

// Presentational-only rail — the query/visibility hook is owned by the
// adapter (series: sub-step B's useSeriesRecommendationsRail). This
// component just renders items/isLoading/visible/sentinelRef via renderCard.
export function MediaRecommendationsRail({
  items,
  isLoading,
  visible,
  sentinelRef,
  renderCard,
  limit = 20,
  className,
  staleBadge,
  label,
  loadingLabel,
}: MediaRecommendationsRailProps) {
  const heading = 'recommendations-heading';

  // Empty + not loading + not visible: return null (matches pre-530
  // behaviour). Note: still attach the ref to a sentinel so we observe
  // scroll-in.
  if (items.length === 0 && !isLoading && !visible) {
    return (
      <section
        ref={sentinelRef}
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
      ref={sentinelRef}
      data-testid={items.length === 0 ? 'recommendations-carousel-loading' : 'recommendations-carousel'}
      data-visible={visible ? 'true' : 'false'}
      aria-labelledby={heading}
      className={cn('flex flex-col gap-3', className)}
    >
      {items.length === 0 ? (
        <SkeletonGrid
          label={label}
          loadingLabel={loadingLabel}
          staleBadge={staleBadge}
          headingId={heading}
        />
      ) : (
        <>
          <h2
            id={heading}
            className="flex items-center gap-2 text-[10.5px] font-bold uppercase tracking-wide text-tx-faint"
          >
            {label}
            {staleBadge}
          </h2>
          <div
            className={cn(
              'flex flex-row gap-3 overflow-x-auto snap-x snap-mandatory pb-2',
              'md:grid md:grid-cols-6 md:gap-4 md:overflow-visible md:snap-none md:pb-0',
            )}
          >
            {items.slice(0, limit).map((rec, idx) => renderCard(rec, idx))}
          </div>
        </>
      )}
    </section>
  );
}
