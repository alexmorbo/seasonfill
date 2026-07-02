import { useEffect, useRef } from 'react';
import { Skeleton } from '@/components/ui/skeleton';
import { SeriesCardTile } from './SeriesCardTile';
import type { SeriesCacheItem } from '@/lib/api/seriesCache';

export interface SeriesGridProps {
  readonly items: readonly SeriesCacheItem[];
  readonly isLoading: boolean;
  readonly isFetchingNextPage: boolean;
  readonly hasNextPage: boolean;
  readonly onLoadMore: () => void;
}

export function SeriesGrid({
  items, isLoading, isFetchingNextPage, hasNextPage, onLoadMore,
}: SeriesGridProps) {
  const sentinelRef = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    const el = sentinelRef.current;
    if (!el) return;
    if (!hasNextPage) return;
    if (typeof IntersectionObserver === 'undefined') return;
    const obs = new IntersectionObserver((entries) => {
      for (const entry of entries) {
        if (entry.isIntersecting) {
          onLoadMore();
        }
      }
    }, { rootMargin: '320px 0px 320px 0px' });
    obs.observe(el);
    return () => obs.disconnect();
  }, [hasNextPage, onLoadMore]);

  if (isLoading) {
    return (
      <div
        data-testid="series-grid-skeleton"
        className="grid gap-3.5 grid-cols-[repeat(auto-fill,minmax(150px,1fr))]"
      >
        {Array.from({ length: 18 }).map((_, i) => (
          <Skeleton key={i} className="w-full aspect-[2/3] rounded-lg" />
        ))}
      </div>
    );
  }

  return (
    <>
      <div
        data-testid="series-grid"
        className="grid gap-3.5 grid-cols-[repeat(auto-fill,minmax(150px,1fr))]"
      >
        {items.map((item) => (
          <SeriesCardTile
            key={`${item.instance_name}-${item.sonarr_series_id}`}
            item={item}
            variant="library"
          />
        ))}
        {isFetchingNextPage && (
          <Skeleton
            data-testid="series-grid-next-skeleton"
            className="w-full aspect-[2/3] rounded-lg"
          />
        )}
      </div>
      <div
        ref={sentinelRef}
        data-testid="series-grid-sentinel"
        aria-hidden="true"
        className="h-px w-full"
      />
    </>
  );
}
