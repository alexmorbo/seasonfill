import { Skeleton } from '@/components/ui/skeleton';
import { MovieCard } from './MovieCard';
import type { MovieLibraryItem } from '@/api/movies';

export interface MovieGridProps {
  readonly items: readonly MovieLibraryItem[];
  readonly isLoading: boolean;
}

// MovieGrid — the movie library grid. Mirrors SeriesGrid's layout + skeleton.
// Each item maps to a MovieCard keyed by tmdb_id (movies are deduped across
// instances, so tmdb_id is unique per row).
export function MovieGrid({ items, isLoading }: MovieGridProps) {
  if (isLoading) {
    return (
      <div
        data-testid="movie-grid-skeleton"
        className="grid gap-3.5 grid-cols-[repeat(auto-fill,minmax(150px,1fr))]"
      >
        {Array.from({ length: 18 }).map((_, i) => (
          <Skeleton key={i} className="w-full aspect-[2/3] rounded-lg" />
        ))}
      </div>
    );
  }

  return (
    <div
      data-testid="movie-grid"
      className="grid gap-3.5 grid-cols-[repeat(auto-fill,minmax(150px,1fr))]"
    >
      {items.map((item) => (
        <MovieCard
          key={item.tmdb_id ?? item.title}
          tmdbId={item.tmdb_id ?? 0}
          title={item.title ?? ''}
          {...(item.year !== undefined ? { year: item.year } : {})}
          {...(item.tmdb_rating !== undefined ? { rating: item.tmdb_rating } : {})}
          {...(item.poster !== undefined ? { poster: item.poster } : {})}
          libraryBadge={(item.instances?.length ?? 0) > 0}
        />
      ))}
    </div>
  );
}
