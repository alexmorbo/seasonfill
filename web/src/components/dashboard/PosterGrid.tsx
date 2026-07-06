import { Skeleton } from '@/components/ui/skeleton';
import { SeriesCardTile } from '@/components/series/SeriesCardTile';
import type { SeriesCacheItem } from '@/lib/api/seriesCache';

export interface PosterGridProps {
  readonly items: readonly SeriesCacheItem[];
  readonly isLoading: boolean;
}

export function PosterGrid({ items, isLoading }: PosterGridProps) {
  if (isLoading) {
    return (
      <div data-testid="poster-grid-skeleton" className="grid gap-3.5 grid-cols-[repeat(auto-fill,minmax(150px,1fr))]">
        {Array.from({ length: 12 }).map((_, i) => (
          <Skeleton key={i} className="w-full aspect-[2/3] rounded-lg" />
        ))}
      </div>
    );
  }
  return (
    <div data-testid="poster-grid" className="grid gap-3.5 grid-cols-[repeat(auto-fill,minmax(150px,1fr))]">
      {items.map((item) => (
        <SeriesCardTile
          key={`${item.instance_name}-${item.sonarr_series_id}`}
          item={item}
        />
      ))}
    </div>
  );
}
