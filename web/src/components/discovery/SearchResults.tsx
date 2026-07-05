import { useTranslation } from 'react-i18next';
import { SearchX } from 'lucide-react';
import { useDiscoverySearch } from '@/api/discovery';
import { toBcp47 } from '@/lib/locale';
import { EmptyState } from '@/components/EmptyState';
import { Skeleton } from '@/components/ui/skeleton';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { SeriesCard } from '@/components/series/SeriesCard';

const GRID_CLASS =
  'grid gap-4 grid-cols-2 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-5';

export interface SearchResultsProps {
  readonly q: string;
}

// Story 515 / N-3c: search results grid. The discovery search hook
// already disables the query when q.trim().length < 2, but we mirror
// the guard here so the visible state (skeleton vs empty) matches.
export function SearchResults({ q }: SearchResultsProps) {
  const { t, i18n } = useTranslation();
  const trimmed = q.trim();
  const eff = trimmed.length >= 2;
  const query = useDiscoverySearch(trimmed, eff, toBcp47(i18n.resolvedLanguage));

  if (!eff) return null;

  if (query.isPending) {
    return (
      <div className={GRID_CLASS} data-testid="discovery-search-skeleton">
        {Array.from({ length: 10 }).map((_, i) => (
          <Skeleton key={i} className="aspect-[2/3] w-full rounded-lg" />
        ))}
      </div>
    );
  }
  if (query.isError) {
    return (
      <Alert variant="destructive" data-testid="discovery-search-error">
        <AlertDescription>{t('discovery.error.fetch_failed')}</AlertDescription>
      </Alert>
    );
  }
  const items = query.data?.items ?? [];
  if (items.length === 0) {
    return (
      <EmptyState
        icon={<SearchX className="h-7 w-7" />}
        title={t('discovery.search.no_results', { query: trimmed })}
      />
    );
  }
  return (
    <div className={GRID_CLASS} data-testid="discovery-search-grid">
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
  );
}
