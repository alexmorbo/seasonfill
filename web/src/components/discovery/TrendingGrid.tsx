import { useTranslation } from 'react-i18next';
import { Flame } from 'lucide-react';
import { useDiscoveryTrending } from '@/api/discovery';
import { EmptyState } from '@/components/EmptyState';
import { Skeleton } from '@/components/ui/skeleton';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { DiscoverySeriesCard } from './DiscoverySeriesCard';

const GRID_CLASS =
  'grid gap-4 grid-cols-2 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-5';

function GridSkeleton() {
  return (
    <div className={GRID_CLASS} data-testid="discovery-trending-skeleton">
      {Array.from({ length: 10 }).map((_, i) => (
        <div key={i} className="overflow-hidden rounded-lg bg-bg-surface-1">
          <Skeleton className="aspect-[2/3] w-full rounded-none" />
          <div className="space-y-1.5 p-2.5">
            <Skeleton className="h-3 w-3/4" />
            <Skeleton className="h-2.5 w-1/3" />
          </div>
        </div>
      ))}
    </div>
  );
}

// Story 514 / N-3b: first wired tab. Cold-start banner + skeleton
// upgrades land in 517; here we keep the loading affordance minimal.
export function TrendingGrid() {
  const { t } = useTranslation();
  // lang resolved server-side via Accept-Language; passing undefined
  // keeps the URL clean. 515+ will reuse this pattern.
  const q = useDiscoveryTrending(undefined);

  if (q.isPending) return <GridSkeleton />;

  if (q.isError) {
    return (
      <Alert variant="destructive" data-testid="discovery-trending-error">
        <AlertDescription>{t('discovery.error.fetch_failed')}</AlertDescription>
      </Alert>
    );
  }

  const items = q.data?.items ?? [];
  if (items.length === 0) {
    return (
      <EmptyState
        icon={<Flame className="h-7 w-7" />}
        title={t('discovery.tabs.trending')}
      />
    );
  }

  return (
    <div className={GRID_CLASS} data-testid="discovery-trending-grid">
      {items.map((item) => (
        <DiscoverySeriesCard key={item.series_id} item={item} />
      ))}
    </div>
  );
}
