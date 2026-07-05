import { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { SlidersHorizontal } from 'lucide-react';
import { toast } from 'sonner';
import { useDiscover, type DiscoveryFilter } from '@/api/discovery';
import { ApiError } from '@/lib/api';
import { toBcp47 } from '@/lib/locale';
import { EmptyState } from '@/components/EmptyState';
import { Skeleton } from '@/components/ui/skeleton';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { SeriesCard } from '@/components/series/SeriesCard';
import { DiscoverSkeleton } from './DiscoverSkeleton';
import { WarmingBanner } from './WarmingBanner';
import { useDegradedPolling, degradedRefetchInterval } from './useDegradedPolling';

const GRID_CLASS =
  'grid gap-4 grid-cols-2 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-5';

export interface FilteredResultsProps {
  readonly filter: DiscoveryFilter;
  readonly hasActiveFilter: boolean;
}

// Story 516 / N-3d: results grid for the Filter tab. Renders the
// "pick filters" prompt when nothing is selected. Pagination is a
// local-state "page" overlay — not URL-synced — to avoid clobbering
// deep links with stale page numbers.
// Story 517 / N-3e adds warming banner + skeleton + 502 toast.
export function FilteredResults({ filter, hasActiveFilter }: FilteredResultsProps) {
  const { t, i18n } = useTranslation();
  const [page, setPage] = useState(1);
  // Local page wins over any URL `page`; resets when filter changes.
  const merged: DiscoveryFilter = { ...filter, page };
  const q = useDiscover(
    merged, toBcp47(i18n.resolvedLanguage), hasActiveFilter, degradedRefetchInterval,
  );
  const polling = useDegradedPolling(q.data);
  const toastedRef = useRef(false);
  useEffect(() => {
    if (q.isError && q.error instanceof ApiError && q.error.status === 502
      && !toastedRef.current) {
      toastedRef.current = true;
      toast.error(t('discovery.error.fetch_failed'));
    }
    if (!q.isError) toastedRef.current = false;
  }, [q.isError, q.error, t]);

  if (!hasActiveFilter) {
    return (
      <EmptyState
        icon={<SlidersHorizontal className="h-7 w-7" />}
        title={t('discovery.filter.prompt')}
      />
    );
  }

  if (q.isPending) {
    return (
      <div className={GRID_CLASS} data-testid="discovery-filtered-skeleton">
        {Array.from({ length: 10 }).map((_, i) => (
          <Skeleton key={i} className="aspect-[2/3] w-full rounded-lg" />
        ))}
      </div>
    );
  }
  if (q.isError) {
    return (
      <Alert variant="destructive" data-testid="discovery-filtered-error">
        <AlertDescription>{t('discovery.error.fetch_failed')}</AlertDescription>
      </Alert>
    );
  }
  const items = q.data?.items ?? [];
  if (polling.isDegraded && items.length === 0) {
    return (
      <div className="space-y-3" data-testid="discovery-filtered-warming">
        <WarmingBanner
          kind={polling.degradedKind ?? 'cold_start'}
          estimateSeconds={polling.estimateSeconds}
          retryAfterSeconds={polling.retryAfterSeconds}
        />
        <DiscoverSkeleton testId="discovery-filtered-warming-skeleton" />
      </div>
    );
  }
  if (items.length === 0) {
    return (
      <EmptyState
        icon={<SlidersHorizontal className="h-7 w-7" />}
        title={t('discovery.tabs.filtered')}
      />
    );
  }
  return (
    <div className="space-y-4">
      {polling.isDegraded && (
        <WarmingBanner
          kind={polling.degradedKind ?? 'cold_start'}
          estimateSeconds={polling.estimateSeconds}
          retryAfterSeconds={polling.retryAfterSeconds}
        />
      )}
      <div className={GRID_CLASS} data-testid="discovery-filtered-grid">
        {items.map((it) => {
          const inLib = (it.in_library_instances ?? []).length > 0;
          return (
            <SeriesCard
              key={`${it.series_id}-${it.tmdb_id}`}
              seriesId={it.series_id}
              tmdbId={it.tmdb_id}
              title={it.title}
              year={it.year}
              posterAsset={it.poster_hash ?? it.poster_path}
              rating={it.tmdb_rating}
              libraryBadge={inLib ? 'inLibrary' : undefined}
              addToSonarr={inLib ? undefined : it}
            />
          );
        })}
      </div>
      <div className="flex justify-center">
        <Button
          type="button" variant="outline" size="sm"
          data-testid="discovery-filtered-load-more"
          onClick={() => setPage((p) => p + 1)}
        >{t('discovery.filter.load_more')}</Button>
      </div>
    </div>
  );
}
