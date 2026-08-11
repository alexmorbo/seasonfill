import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useSearchParams } from 'react-router-dom';
import { AlertTriangle } from 'lucide-react';
import { useSetPageTitle } from '@/components/shell/page-title-context';
import { Input } from '@/components/ui/input';
import { Button } from '@/components/ui/button';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { MovieGrid } from '@/components/movies/MovieGrid';
import {
  useMoviesLibrary,
  type MovieLibraryItem,
  type MoviesState,
  type MoviesSort,
} from '@/api/movies';

const PAGE_LIMIT = 24;

const STATE_VALUES: readonly MoviesState[] = ['all', 'downloaded', 'missing'];
const SORT_VALUES: readonly MoviesSort[] = ['updated_desc', 'title_asc', 'release_desc'];

const DEFAULT_STATE: MoviesState = 'all';
const DEFAULT_SORT: MoviesSort = 'updated_desc';

function isState(v: string | null): v is MoviesState {
  return v !== null && (STATE_VALUES as readonly string[]).includes(v);
}
function isSort(v: string | null): v is MoviesSort {
  return v !== null && (SORT_VALUES as readonly string[]).includes(v);
}

interface Filters {
  readonly state: MoviesState;
  readonly sort: MoviesSort;
  readonly q: string;
}

function readFilters(p: URLSearchParams): Filters {
  const stateRaw = p.get('state');
  const sortRaw = p.get('sort');
  return {
    state: isState(stateRaw) ? stateRaw : DEFAULT_STATE,
    sort: isSort(sortRaw) ? sortRaw : DEFAULT_SORT,
    q: p.get('q') ?? '',
  };
}

function writeFilters(f: Filters): URLSearchParams {
  const p = new URLSearchParams();
  if (f.state !== DEFAULT_STATE) p.set('state', f.state);
  if (f.sort !== DEFAULT_SORT) p.set('sort', f.sort);
  if (f.q) p.set('q', f.q);
  return p;
}

// SORT/STATE dropdown labels map to i18n keys.
const STATE_LABEL: Record<MoviesState, string> = {
  all: 'movies.filters.state.all',
  downloaded: 'movies.filters.state.downloaded',
  missing: 'movies.filters.state.missing',
};
const SORT_LABEL: Record<MoviesSort, string> = {
  updated_desc: 'movies.filters.sort.updated',
  title_asc: 'movies.filters.sort.title',
  release_desc: 'movies.filters.sort.release',
};

export function Movies() {
  const { t } = useTranslation();
  useSetPageTitle(t('movies.title'));

  const [params, setParams] = useSearchParams();
  const filters = useMemo(() => readFilters(params), [params]);
  const filterKey = `${filters.state}|${filters.sort}|${filters.q}`;

  // Cursor-based accumulation: `offset` is the next-page cursor (int); `items`
  // accumulates every page fetched for the current filter set. Both reset when
  // the filters change.
  const [offset, setOffset] = useState(0);
  const [items, setItems] = useState<readonly MovieLibraryItem[]>([]);
  const appliedRef = useRef<{ key: string; offset: number } | null>(null);

  // Reset the accumulator whenever the filter set changes. Done during render via
  // a previous-value comparison (React's sanctioned "adjust state when a prop/derived
  // value changes" pattern) instead of an effect, so the reset is applied before the
  // page below reads `offset`/`items` — no extra render, no set-state-in-effect.
  // `appliedRef` needs no reset here: the fold effect's dedup keys on `filterKey`, so
  // a changed key never matches the stale ref and the next page folds as offset 0.
  const [prevFilterKey, setPrevFilterKey] = useState(filterKey);
  if (filterKey !== prevFilterKey) {
    setPrevFilterKey(filterKey);
    setItems([]);
    setOffset(0);
  }

  const query = useMoviesLibrary({
    state: filters.state,
    sort: filters.sort,
    limit: PAGE_LIMIT,
    ...(filters.q ? { q: filters.q } : {}),
    ...(offset > 0 ? { cursor: offset } : {}),
  });

  // Fold each successfully-fetched page into the accumulator exactly once.
  useEffect(() => {
    if (!query.isSuccess || !query.data) return;
    const already =
      appliedRef.current?.key === filterKey && appliedRef.current.offset === offset;
    if (already) return;
    const pageItems = query.data.items ?? [];
    setItems((prev) => (offset === 0 ? [...pageItems] : [...prev, ...pageItems]));
    appliedRef.current = { key: filterKey, offset };
  }, [query.isSuccess, query.data, offset, filterKey]);

  const total = query.data?.total ?? 0;
  const hasMore = query.data?.has_more ?? false;
  const nextCursor = query.data?.next_cursor;

  const onLoadMore = useCallback(() => {
    if (!hasMore || query.isFetching) return;
    const next = nextCursor ? Number.parseInt(nextCursor, 10) : NaN;
    if (Number.isFinite(next)) setOffset(next);
  }, [hasMore, nextCursor, query.isFetching]);

  const update = useCallback(
    (patch: Partial<Filters>) => {
      setParams(writeFilters({ ...filters, ...patch }), { replace: true });
    },
    [filters, setParams],
  );

  const initialLoading = query.isPending && items.length === 0;
  const showEmpty = query.isSuccess && items.length === 0 && total === 0;

  return (
    <div className="flex flex-col gap-4" data-testid="movies-page">
      <div>
        <h1 className="text-lg font-semibold text-tx-primary">{t('movies.title')}</h1>
        <p className="text-[13px] text-tx-muted">{t('movies.subtitle')}</p>
      </div>

      <div className="flex flex-wrap items-center gap-2">
        <Input
          type="search"
          value={filters.q}
          onChange={(e) => update({ q: e.target.value })}
          placeholder={t('movies.filters.search.placeholder')}
          aria-label={t('movies.filters.search.placeholder')}
          data-testid="movies-search"
          className="w-full max-w-[240px]"
        />
        <select
          value={filters.state}
          onChange={(e) => update({ state: e.target.value as MoviesState })}
          aria-label={t('movies.filters.state.all')}
          data-testid="movies-filter-state"
          className="h-9 rounded-md border border-border-subtle bg-bg-surface px-2 text-[13px] text-tx-primary"
        >
          {STATE_VALUES.map((s) => (
            <option key={s} value={s}>
              {t(STATE_LABEL[s])}
            </option>
          ))}
        </select>
        <select
          value={filters.sort}
          onChange={(e) => update({ sort: e.target.value as MoviesSort })}
          aria-label={t('movies.filters.sort.updated')}
          data-testid="movies-filter-sort"
          className="h-9 rounded-md border border-border-subtle bg-bg-surface px-2 text-[13px] text-tx-primary"
        >
          {SORT_VALUES.map((s) => (
            <option key={s} value={s}>
              {t(SORT_LABEL[s])}
            </option>
          ))}
        </select>
      </div>

      {query.isError && (
        <Alert variant="destructive" data-testid="movies-list-error">
          <AlertTriangle className="h-4 w-4" />
          <AlertTitle>{t('movieDetail.errors.loadFailedTitle')}</AlertTitle>
          <AlertDescription>
            {query.error instanceof Error ? query.error.message : t('common.error')}
          </AlertDescription>
        </Alert>
      )}

      {showEmpty && (
        <div
          data-testid="movies-empty"
          className="flex flex-col items-center gap-1 rounded-lg border border-dashed border-border-subtle py-12 text-center"
        >
          <div className="text-[15px] font-semibold text-tx-primary">
            {t('movies.empty.title')}
          </div>
          <div className="text-[13px] text-tx-muted">{t('movies.empty.body')}</div>
        </div>
      )}

      {!showEmpty && !query.isError && (
        <>
          <MovieGrid items={items} isLoading={initialLoading} />
          {hasMore && (
            <div className="flex justify-center">
              <Button
                variant="outline"
                onClick={onLoadMore}
                disabled={query.isFetching}
                data-testid="movies-load-more"
              >
                {t('movies.loadMore')}
              </Button>
            </div>
          )}
        </>
      )}
    </div>
  );
}
