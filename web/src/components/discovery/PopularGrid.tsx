import { useEffect, useRef } from 'react';
import { useTranslation } from 'react-i18next';
import { TrendingUp } from 'lucide-react';
import { toast } from 'sonner';
import { useDiscoveryPopular } from '@/api/discovery';
import { ApiError } from '@/lib/api';
import { toBcp47 } from '@/lib/locale';
import { EmptyState } from '@/components/EmptyState';
import { Skeleton } from '@/components/ui/skeleton';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { SeriesCard } from '@/components/series/SeriesCard';
import { DiscoverSkeleton } from './DiscoverSkeleton';
import { WarmingBanner } from './WarmingBanner';
import { useDegradedPolling, degradedRefetchInterval } from './useDegradedPolling';

const GRID_CLASS =
  'grid gap-4 grid-cols-2 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-5';

function GridSkeleton() {
  return (
    <div className={GRID_CLASS} data-testid="discovery-popular-skeleton">
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

// Story 515 / N-3c: Popular tab. Story 517 / N-3e adds the warming
// banner + skeleton + 502 toast in the same shape as TrendingGrid.
export function PopularGrid() {
  const { t, i18n } = useTranslation();
  const q = useDiscoveryPopular(toBcp47(i18n.resolvedLanguage), degradedRefetchInterval);
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

  if (q.isPending) return <GridSkeleton />;

  if (q.isError) {
    return (
      <Alert variant="destructive" data-testid="discovery-popular-error">
        <AlertDescription>{t('discovery.error.fetch_failed')}</AlertDescription>
      </Alert>
    );
  }

  const items = q.data?.items ?? [];
  if (polling.isDegraded && items.length === 0) {
    return (
      <div className="space-y-3" data-testid="discovery-popular-warming">
        <WarmingBanner
          kind={polling.degradedKind ?? 'cold_start'}
          estimateSeconds={polling.estimateSeconds}
          retryAfterSeconds={polling.retryAfterSeconds}
        />
        <DiscoverSkeleton testId="discovery-popular-warming-skeleton" />
      </div>
    );
  }
  if (items.length === 0) {
    return (
      <EmptyState
        icon={<TrendingUp className="h-7 w-7" />}
        title={t('discovery.tabs.popular')}
      />
    );
  }

  return (
    <div className="space-y-3">
      {polling.isDegraded && (
        <WarmingBanner
          kind={polling.degradedKind ?? 'cold_start'}
          estimateSeconds={polling.estimateSeconds}
          retryAfterSeconds={polling.retryAfterSeconds}
        />
      )}
      <div className={GRID_CLASS} data-testid="discovery-popular-grid">
        {items.map((item) => {
          const inLib = (item.in_library_instances ?? []).length > 0;
          return (
            <SeriesCard
              key={`${item.series_id}-${item.tmdb_id}`}
              seriesId={item.series_id}
              tmdbId={item.tmdb_id}
              title={item.title}
              year={item.year}
              posterAsset={item.poster_hash ?? item.poster_path}
              rating={item.tmdb_rating}
              libraryBadge={inLib ? 'inLibrary' : undefined}
              addToSonarr={inLib ? undefined : item}
            />
          );
        })}
      </div>
    </div>
  );
}
