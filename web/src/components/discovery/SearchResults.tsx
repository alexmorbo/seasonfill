import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { SearchX } from 'lucide-react';
import { useDiscoverySearch, type DiscoverySeriesItem } from '@/api/discovery';
import { useMovieSearch, type DiscoveryMovieItem } from '@/api/discoveryMovies';
import { toBcp47 } from '@/lib/locale';
import { cn } from '@/lib/utils';
import { EmptyState } from '@/components/EmptyState';
import { Skeleton } from '@/components/ui/skeleton';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { SeriesCard } from '@/components/series/SeriesCard';
import { MovieCard } from '@/components/movies/MovieCard';

const GRID_CLASS =
  'grid gap-4 grid-cols-2 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-5';

type SearchTab = 'tv' | 'movie';

export interface SearchResultsProps {
  readonly q: string;
}

// Story 515 / N-3c + M-FIX-3: search results grid with a TV/Movies tab. The
// discovery search hooks already disable the query when q.trim().length < 2;
// we mirror the guard here so the visible state (skeleton vs empty) matches.
// Both hooks run unconditionally (Rules of Hooks) and are gated by the active
// tab so only the visible tab fetches.
export function SearchResults({ q }: SearchResultsProps) {
  const { t, i18n } = useTranslation();
  const [tab, setTab] = useState<SearchTab>('tv');
  const trimmed = q.trim();
  const eff = trimmed.length >= 2;
  const lang = toBcp47(i18n.resolvedLanguage);

  const tvQuery = useDiscoverySearch(trimmed, eff && tab === 'tv', lang);
  const movieQuery = useMovieSearch(trimmed, eff && tab === 'movie', lang);

  if (!eff) return null;

  const query = tab === 'tv' ? tvQuery : movieQuery;

  const tabs: { readonly id: SearchTab; readonly label: string }[] = [
    { id: 'tv', label: t('discovery.search.tab_tv') },
    { id: 'movie', label: t('discovery.search.tab_movie') },
  ];

  return (
    <div className="space-y-4" data-testid="discovery-search-results">
      <div
        role="tablist"
        aria-label={t('discovery.search.tabs_label')}
        className="inline-flex items-center gap-1 rounded-md border border-border-subtle bg-bg-surface p-1"
      >
        {tabs.map((tb) => (
          <button
            key={tb.id}
            type="button"
            role="tab"
            aria-selected={tab === tb.id}
            data-testid={`discovery-search-tab-${tb.id}`}
            onClick={() => setTab(tb.id)}
            className={cn(
              'rounded px-3 py-1 text-sm transition-colors',
              tab === tb.id
                ? 'bg-accent/15 font-semibold text-accent'
                : 'text-tx-muted hover:text-tx-primary',
            )}
          >
            {tb.label}
          </button>
        ))}
      </div>

      {query.isPending ? (
        <div className={GRID_CLASS} data-testid="discovery-search-skeleton">
          {Array.from({ length: 10 }).map((_, i) => (
            <Skeleton key={i} className="aspect-[2/3] w-full rounded-lg" />
          ))}
        </div>
      ) : query.isError ? (
        <Alert variant="destructive" data-testid="discovery-search-error">
          <AlertDescription>{t('discovery.error.fetch_failed')}</AlertDescription>
        </Alert>
      ) : tab === 'tv' ? (
        <TvGrid items={tvQuery.data?.items ?? []} trimmed={trimmed} />
      ) : (
        <MovieResultsGrid items={movieQuery.data?.items ?? []} trimmed={trimmed} />
      )}
    </div>
  );
}

function TvGrid({
  items, trimmed,
}: {
  readonly items: readonly DiscoverySeriesItem[];
  readonly trimmed: string;
}) {
  const { t } = useTranslation();
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
            posterAsset={it.poster_hash || it.poster_path}
            rating={it.tmdb_rating}
            libraryBadge={inLib ? 'inLibrary' : undefined}
          />
        );
      })}
    </div>
  );
}

function MovieResultsGrid({
  items, trimmed,
}: {
  readonly items: readonly DiscoveryMovieItem[];
  readonly trimmed: string;
}) {
  const { t } = useTranslation();
  // Only movies with a tmdb_id can link / be shown (mirrors MovieDiscoveryRail).
  const visible = items.filter((it) => typeof it.tmdb_id === 'number' && it.tmdb_id > 0);
  if (visible.length === 0) {
    return (
      <EmptyState
        icon={<SearchX className="h-7 w-7" />}
        title={t('discovery.search.no_results', { query: trimmed })}
      />
    );
  }
  return (
    <div className={GRID_CLASS} data-testid="discovery-search-movie-grid">
      {visible.map((it) => {
        const tmdbId = it.tmdb_id as number;
        return (
          <MovieCard
            key={`${it.movie_id}-${tmdbId}`}
            tmdbId={tmdbId}
            title={it.title}
            {...(it.year !== undefined ? { year: it.year } : {})}
            {...(it.tmdb_rating !== undefined ? { rating: it.tmdb_rating } : {})}
            {...(it.poster_hash !== undefined ? { poster: it.poster_hash } : {})}
          />
        );
      })}
    </div>
  );
}
