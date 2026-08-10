import { cn } from '@/lib/utils';
import { SeriesCard } from '@/components/series/SeriesCard';
import type { DiscoverySeriesItem } from '@/api/discovery';
import { DiscoveryCardMenu } from './DiscoveryCardMenu';

// DiscoveryCard wraps the unified SeriesCard with the discovery-only kebab
// overlay. The wrapper carries the rail's snap/min-width class + `group` (so
// the kebab's group-hover reveal binds to it). SeriesCard stays untouched —
// the menu is a sibling, not a prop on the shared card.
export function DiscoveryCard({
  item, onHide, className,
}: {
  readonly item: DiscoverySeriesItem;
  readonly onHide: (item: DiscoverySeriesItem) => void;
  readonly className?: string;
}) {
  const inLib = (item.in_library_instances ?? []).length > 0;
  return (
    <div className={cn('group relative', className)} data-testid="discovery-card">
      <SeriesCard
        seriesId={item.series_id}
        tmdbId={item.tmdb_id}
        title={item.title}
        year={item.year}
        posterAsset={item.poster_hash || item.poster_path}
        rating={item.tmdb_rating}
        libraryBadge={inLib ? 'inLibrary' : undefined}
      />
      <DiscoveryCardMenu onHide={() => onHide(item)} />
    </div>
  );
}
