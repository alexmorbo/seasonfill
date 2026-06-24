import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { SlidersHorizontal } from 'lucide-react';
import { useDiscover, type DiscoveryFilter } from '@/api/discovery';
import { EmptyState } from '@/components/EmptyState';
import { Skeleton } from '@/components/ui/skeleton';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { DiscoverySeriesCard } from './DiscoverySeriesCard';

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
export function FilteredResults({ filter, hasActiveFilter }: FilteredResultsProps) {
  const { t } = useTranslation();
  const [page, setPage] = useState(1);
  // Local page wins over any URL `page`; resets when filter changes.
  const merged: DiscoveryFilter = { ...filter, page };
  const q = useDiscover(merged, undefined, hasActiveFilter);

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
      <div className={GRID_CLASS} data-testid="discovery-filtered-grid">
        {items.map((it) => <DiscoverySeriesCard key={it.series_id} item={it} />)}
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
