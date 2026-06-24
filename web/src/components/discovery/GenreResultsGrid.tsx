import { useTranslation } from 'react-i18next';
import { Tag } from 'lucide-react';
import { useDiscoveryByGenre } from '@/api/discovery';
import { EmptyState } from '@/components/EmptyState';
import { Skeleton } from '@/components/ui/skeleton';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { DiscoverySeriesCard } from './DiscoverySeriesCard';

const GRID_CLASS =
  'grid gap-4 grid-cols-2 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-5';

export interface GenreResultsGridProps {
  readonly genreId: number;
}

// Story 515 / N-3c: results grid for a selected genre chip.
export function GenreResultsGrid({ genreId }: GenreResultsGridProps) {
  const { t } = useTranslation();
  const q = useDiscoveryByGenre(genreId, undefined);

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
  if (items.length === 0) {
    return (
      <EmptyState
        icon={<Tag className="h-7 w-7" />}
        title={t('discovery.tabs.genres')}
      />
    );
  }
  return (
    <div className={GRID_CLASS} data-testid="discovery-genre-grid">
      {items.map((it) => <DiscoverySeriesCard key={it.series_id} item={it} />)}
    </div>
  );
}
