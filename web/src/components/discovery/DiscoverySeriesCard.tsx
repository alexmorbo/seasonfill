import { useState } from 'react';
import { Link } from 'react-router-dom';
import type { DiscoverySeriesItem } from '@/api/discovery';
import { cn } from '@/lib/utils';
import { InLibraryBadge } from './InLibraryBadge';

const TMDB_POSTER_BASE = 'https://image.tmdb.org/t/p/w342';

export interface DiscoverySeriesCardProps {
  readonly item: DiscoverySeriesItem;
  readonly className?: string;
}

// Story 514 / N-3b: poster card consumed by every discovery grid in
// 515-517. Renders TMDB posters directly (poster_path is TMDB-shaped,
// not a content-addressed sha256 — MediaImage doesn't apply here).
export function DiscoverySeriesCard({ item, className }: DiscoverySeriesCardProps) {
  const [errored, setErrored] = useState(false);
  const src = item.poster_path ? `${TMDB_POSTER_BASE}${item.poster_path}` : null;
  const showImg = Boolean(src) && !errored;
  const inLibrary = item.in_library_instances ?? [];

  return (
    <Link
      to={`/series/${item.series_id}`}
      data-testid="discovery-series-card"
      data-series-id={item.series_id}
      className={cn(
        'group relative block overflow-hidden rounded-lg bg-bg-surface-1',
        'transition-transform duration-150 ease-out hover:-translate-y-0.5',
        'focus-visible:outline-hidden focus-visible:ring-2 focus-visible:ring-accent',
        className,
      )}
    >
      <div className="relative aspect-[2/3] overflow-hidden">
        {showImg ? (
          <img
            src={src ?? ''} alt="" aria-hidden="true"
            loading="lazy" decoding="async"
            onError={() => setErrored(true)}
            data-testid="discovery-poster-img"
            className="absolute inset-0 h-full w-full object-cover transition-transform duration-200 group-hover:scale-[1.03]"
          />
        ) : (
          <div
            data-testid="discovery-poster-fallback"
            className="absolute inset-0 flex items-center justify-center bg-bg-surface-2 text-tx-faint"
          >
            <span className="text-2xl font-semibold">
              {(item.title[0] ?? '?').toUpperCase()}
            </span>
          </div>
        )}
        {inLibrary.length > 0 && (
          <div className="absolute right-2 top-2">
            <InLibraryBadge instances={inLibrary} />
          </div>
        )}
      </div>
      <div className="p-2.5">
        <div className="truncate text-[13px] font-medium text-tx-primary">
          {item.title}
        </div>
        {item.year ? (
          <div data-testid="discovery-card-year" className="text-[11.5px] text-tx-muted">
            {item.year}
          </div>
        ) : null}
      </div>
    </Link>
  );
}
