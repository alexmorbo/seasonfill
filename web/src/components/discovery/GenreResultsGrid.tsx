import { useEffect, useRef } from 'react';
import { useTranslation } from 'react-i18next';
import { Tag } from 'lucide-react';
import { toast } from 'sonner';
import { useDiscoveryByGenre } from '@/api/discovery';
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

export interface GenreResultsGridProps {
  readonly genreId: number;
}

// Story 515 / N-3c: results grid for a selected genre chip.
// Story 517 / N-3e adds warming banner + skeleton + 502 toast.
export function GenreResultsGrid({ genreId }: GenreResultsGridProps) {
  const { t, i18n } = useTranslation();
  const q = useDiscoveryByGenre(genreId, toBcp47(i18n.resolvedLanguage), degradedRefetchInterval);
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

  if (q.isPending) {
    return (
      <div className={GRID_CLASS} data-testid="discovery-genre-skeleton">
        {Array.from({ length: 10 }).map((_, i) => (
          <Skeleton key={i} className="aspect-[2/3] w-full rounded-lg" />
        ))}
      </div>
    );
  }
  if (q.isError) {
    return (
      <Alert variant="destructive" data-testid="discovery-genre-error">
        <AlertDescription>{t('discovery.error.fetch_failed')}</AlertDescription>
      </Alert>
    );
  }
  const items = q.data?.items ?? [];
  if (polling.isDegraded && items.length === 0) {
    return (
      <div className="space-y-3" data-testid="discovery-genre-warming">
        <WarmingBanner
          kind={polling.degradedKind ?? 'cold_start'}
          estimateSeconds={polling.estimateSeconds}
          retryAfterSeconds={polling.retryAfterSeconds}
        />
        <DiscoverSkeleton testId="discovery-genre-warming-skeleton" />
      </div>
    );
  }
  if (items.length === 0) {
    return (
      <EmptyState
        icon={<Tag className="h-7 w-7" />}
        title={t('discovery.tabs.genres')}
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
      <div className={GRID_CLASS} data-testid="discovery-genre-grid">
        {items.map((it) => {
          const inLib = (it.in_library_instances ?? []).length > 0;
          return (
            <SeriesCard
              key={`${it.series_id}-${it.tmdb_id}`}
              seriesId={it.series_id}
              tmdbId={it.tmdb_id}
              title={it.title}
              year={it.year}
              posterAsset={it.poster_hash || it.poster_path}
              rating={it.tmdb_rating}
              libraryBadge={inLib ? 'inLibrary' : undefined}
              addToSonarr={inLib ? undefined : it}
            />
          );
        })}
      </div>
    </div>
  );
}
