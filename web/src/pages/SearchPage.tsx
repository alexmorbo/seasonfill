import { useEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useSearchParams } from 'react-router-dom';
import { Search, SearchX, Loader2 } from 'lucide-react';
import { useSetPageTitle } from '@/components/shell/page-title-context';
import { cn } from '@/lib/utils';
import { EmptyState } from '@/components/EmptyState';
import { Skeleton } from '@/components/ui/skeleton';
import { SeriesCard } from '@/components/series/SeriesCard';
import { MovieCard } from '@/components/movies/MovieCard';
import { CollectionCard } from '@/components/movies/CollectionCard';
import { PersonCard } from '@/components/search/PersonCard';
import { useUnifiedSearch, type SearchGroup, type SearchScope } from '@/api/search';

const GRID_CLASS =
  'grid gap-4 grid-cols-2 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-5';

const MIN_CHARS = 2;
const DEBOUNCE_MS = 250;

type TypeTab = 'all' | 'tv' | 'movie' | 'collection' | 'people';
type ScopeSeg = 'all' | 'library' | 'catalog';

// Year sentinel: native <select> values are strings; 'all' = no filter.
const YEAR_ALL = 'all';

function groupVisibleCount(g: SearchGroup, tab: TypeTab): number {
  const s = tab === 'all' || tab === 'tv' ? g.series.length : 0;
  const m = tab === 'all' || tab === 'movie' ? g.movies.length : 0;
  const c = tab === 'all' || tab === 'collection' ? g.collections.length : 0;
  const p = tab === 'all' || tab === 'people' ? g.people.length : 0;
  return s + m + c + p;
}

// SearchPage — S3.1. The full-page counterpart of the ⌘K palette: it reuses
// the exact same useUnifiedSearch hook (library instant + catalog deferred,
// cross-scope dedup) and renders ALL returned hits (no top-3 slice). Tabs and
// the Library/Catalog segment are PURE client filters over the already-fetched
// groups — no refetch. The input seeds from ?q= and keeps the URL in sync
// (replace) so results are shareable / back-friendly.
//
// S3.3 adds a CLIENT-SIDE year filter. The BE /search endpoint has NO `year`
// param — filtering is 100% over the already-fetched arrays. Design choices:
//   • Year options are derived from the CURRENTLY-RETURNED series+movies within
//     the visible scope (respects the Library/Catalog segment), numbers only,
//     sorted DESC. Deriving from live data (not a static list) honours the
//     top-N caveat of the search API.
//   • The year filter narrows the series and movies groups ONLY; collections
//     and people are shown UNFILTERED regardless of the selected year.
//   • The control is HIDDEN on the Collections and People tabs (no year there).
//   • Year is LOCAL STATE only — NOT synced to the URL. onQueryChange rebuilds
//     URLSearchParams from scratch on every keystroke and would clobber a year
//     param, and F-01 does not require a shareable year.
//   • Stale-year guard: `activeYear` falls through to null (no filter) whenever
//     the selected year is absent from the freshly derived options, so results
//     never get stuck on an empty grid.
export function SearchPage() {
  const { t } = useTranslation();
  useSetPageTitle(t('search.title'));

  const [params, setParams] = useSearchParams();
  const initialQ = params.get('q') ?? '';

  const [query, setQuery] = useState(initialQ);
  const [debouncedQuery, setDebouncedQuery] = useState(initialQ);
  const [typeTab, setTypeTab] = useState<TypeTab>('all');
  const [scope, setScope] = useState<ScopeSeg>('all');
  const [year, setYear] = useState<string>(YEAR_ALL);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(
    () => () => {
      if (timer.current) clearTimeout(timer.current);
    },
    [],
  );

  const onQueryChange = (next: string) => {
    setQuery(next);
    if (timer.current) clearTimeout(timer.current);
    timer.current = setTimeout(() => {
      setDebouncedQuery(next);
      const p = new URLSearchParams();
      const trimmedNext = next.trim();
      if (trimmedNext) p.set('q', trimmedNext);
      setParams(p, { replace: true });
    }, DEBOUNCE_MS);
  };

  const search = useUnifiedSearch(debouncedQuery);
  const trimmed = debouncedQuery.trim();

  const scopesToRender: readonly { key: SearchScope; group: SearchGroup }[] =
    scope === 'all'
      ? [
          { key: 'library', group: search.library },
          { key: 'catalog', group: search.catalog },
        ]
      : scope === 'library'
        ? [{ key: 'library', group: search.library }]
        : [{ key: 'catalog', group: search.catalog }];

  // Distinct years across the series+movies of the CURRENTLY-VISIBLE scope
  // groups, numbers only (drop undefined), sorted DESCENDING. Derived from the
  // UNFILTERED groups so the option list stays stable while a year is active.
  const yearOptions = useMemo<number[]>(() => {
    const groups =
      scope === 'library'
        ? [search.library]
        : scope === 'catalog'
          ? [search.catalog]
          : [search.library, search.catalog];
    const set = new Set<number>();
    for (const g of groups) {
      for (const s of g.series) if (typeof s.year === 'number') set.add(s.year);
      for (const m of g.movies) if (typeof m.year === 'number') set.add(m.year);
    }
    return Array.from(set).sort((a, b) => b - a);
  }, [scope, search.library, search.catalog]);

  // Stale-year guard: a selected year absent from the derived options behaves
  // as "All years" (null = no filter). Binding the <select> value to this keeps
  // the control visually consistent when results change out from under it.
  const activeYear =
    year !== YEAR_ALL && yearOptions.includes(Number(year)) ? Number(year) : null;

  // Narrows series + movies to activeYear; collections/people pass through
  // untouched. Items with an undefined year are dropped when a year is active.
  const filterByYear = (g: SearchGroup): SearchGroup =>
    activeYear === null
      ? g
      : {
          ...g,
          series: g.series.filter((h) => h.year === activeYear),
          movies: g.movies.filter((h) => h.year === activeYear),
        };

  const filteredScopes = scopesToRender.map(({ key, group }) => ({
    key,
    group: filterByYear(group),
  }));

  const totalVisible = filteredScopes.reduce(
    (n, s) => n + groupVisibleCount(s.group, typeTab),
    0,
  );

  const showPrompt = trimmed.length < MIN_CHARS;
  const showSkeleton = !showPrompt && search.libraryLoading && totalVisible === 0;
  const showEmpty =
    !showPrompt &&
    !search.libraryLoading &&
    !search.catalogSearching &&
    totalVisible === 0;

  const typeTabs: readonly { id: TypeTab; label: string }[] = [
    { id: 'all', label: t('search.tab.all') },
    { id: 'tv', label: t('search.tab.tv') },
    { id: 'movie', label: t('search.tab.movie') },
    { id: 'collection', label: t('search.tab.collections') },
    { id: 'people', label: t('search.tab.people') },
  ];
  const scopeSegs: readonly { id: ScopeSeg; label: string }[] = [
    { id: 'all', label: t('search.scope.all') },
    { id: 'library', label: t('search.scope.library') },
    { id: 'catalog', label: t('search.scope.catalog') },
  ];

  const showSeries = typeTab === 'all' || typeTab === 'tv';
  const showMovies = typeTab === 'all' || typeTab === 'movie';
  const showCollections = typeTab === 'all' || typeTab === 'collection';
  const showPeople = typeTab === 'all' || typeTab === 'people';

  // Year control only makes sense for tabs that carry a year.
  const showYearFilter =
    typeTab === 'all' || typeTab === 'tv' || typeTab === 'movie';

  return (
    <div className="flex flex-col gap-4" data-testid="search-page">
      <div>
        <h1 className="text-lg font-semibold text-tx-primary">{t('search.title')}</h1>
      </div>

      <div className="relative w-full max-w-md">
        <Search
          aria-hidden="true"
          className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-tx-muted"
        />
        <input
          type="search"
          autoFocus
          aria-label={t('search.title')}
          data-testid="search-page-input"
          placeholder={t('search.input.placeholder')}
          value={query}
          onChange={(e) => onQueryChange(e.target.value)}
          className={cn(
            'flex h-9 w-full rounded-md border border-strong bg-input pl-9 pr-3 py-1',
            'text-base shadow-xs transition-colors placeholder:text-muted',
            'focus-visible:outline-hidden focus-visible:ring-1 focus-visible:ring-ring md:text-sm',
          )}
        />
      </div>

      {!showPrompt && (
        <div className="flex flex-wrap items-center gap-3">
          <div
            role="tablist"
            aria-label={t('search.title')}
            className="inline-flex items-center gap-1 rounded-md border border-border-subtle bg-bg-surface p-1"
          >
            {typeTabs.map((tb) => (
              <button
                key={tb.id}
                type="button"
                role="tab"
                aria-selected={typeTab === tb.id}
                data-testid={`search-tab-${tb.id}`}
                onClick={() => setTypeTab(tb.id)}
                className={cn(
                  'rounded px-3 py-1 text-sm transition-colors',
                  typeTab === tb.id
                    ? 'bg-accent/15 font-semibold text-accent'
                    : 'text-tx-muted hover:text-tx-primary',
                )}
              >
                {tb.label}
              </button>
            ))}
          </div>

          <div
            role="tablist"
            aria-label={t('search.scope.all')}
            className="inline-flex items-center gap-1 rounded-md border border-border-subtle bg-bg-surface p-1"
          >
            {scopeSegs.map((sg) => (
              <button
                key={sg.id}
                type="button"
                role="tab"
                aria-selected={scope === sg.id}
                data-testid={`search-scope-${sg.id}`}
                onClick={() => setScope(sg.id)}
                className={cn(
                  'rounded px-3 py-1 text-sm transition-colors',
                  scope === sg.id
                    ? 'bg-accent/15 font-semibold text-accent'
                    : 'text-tx-muted hover:text-tx-primary',
                )}
              >
                {sg.label}
              </button>
            ))}
          </div>

          {showYearFilter && (
            <select
              aria-label={t('search.year.label')}
              data-testid="search-year-select"
              value={activeYear !== null ? String(activeYear) : YEAR_ALL}
              onChange={(e) => setYear(e.target.value)}
              className={cn(
                'flex h-9 items-center whitespace-nowrap rounded-md border border-strong',
                'bg-input px-3 py-1 text-sm shadow-xs transition-colors',
                'focus-visible:outline-hidden focus-visible:ring-1 focus-visible:ring-ring',
              )}
            >
              <option value={YEAR_ALL}>{t('search.year.all')}</option>
              {yearOptions.map((y) => (
                <option key={y} value={String(y)}>
                  {y}
                </option>
              ))}
            </select>
          )}
        </div>
      )}

      {showPrompt ? (
        <div data-testid="search-prompt">
          <EmptyState icon={<Search className="h-7 w-7" />} title={t('search.empty.prompt')} />
        </div>
      ) : showSkeleton ? (
        <div className={GRID_CLASS} data-testid="search-skeleton">
          {Array.from({ length: 10 }).map((_, i) => (
            <Skeleton key={i} className="aspect-[2/3] w-full rounded-lg" />
          ))}
        </div>
      ) : showEmpty ? (
        <div data-testid="search-empty">
          <EmptyState
            icon={<SearchX className="h-7 w-7" />}
            title={t('search.empty.noResults', { query: trimmed })}
          />
        </div>
      ) : (
        <div className="flex flex-col gap-6" data-testid="search-results">
          {filteredScopes.map(({ key, group }) => {
            if (groupVisibleCount(group, typeTab) === 0) return null;
            return (
              <section key={key} className="flex flex-col gap-3">
                <h2 className="text-[11px] font-semibold uppercase tracking-[0.06em] text-tx-muted">
                  {key === 'library'
                    ? t('search.scope.library')
                    : t('search.scope.catalog')}
                </h2>
                <div className={GRID_CLASS} data-testid={`search-grid-${key}`}>
                  {showSeries &&
                    group.series.map((hit) => (
                      <SeriesCard
                        key={`series-${hit.id ?? hit.tmdbId}`}
                        seriesId={hit.id}
                        tmdbId={hit.tmdbId}
                        title={hit.title}
                        year={hit.year}
                        posterAsset={hit.posterPath}
                        libraryBadge={key === 'library' ? 'inLibrary' : undefined}
                      />
                    ))}
                  {showMovies &&
                    group.movies.map((hit) => (
                      <MovieCard
                        key={`movie-${hit.tmdbId}`}
                        tmdbId={hit.tmdbId}
                        title={hit.title}
                        libraryBadge={key === 'library'}
                        {...(hit.year !== undefined ? { year: hit.year } : {})}
                        {...(hit.posterPath !== undefined ? { poster: hit.posterPath } : {})}
                      />
                    ))}
                  {showCollections &&
                    group.collections.map((hit) => (
                      <CollectionCard
                        key={`collection-${hit.tmdbId}`}
                        tmdbId={hit.tmdbId}
                        name={hit.name}
                        {...(hit.posterPath !== undefined ? { poster: hit.posterPath } : {})}
                      />
                    ))}
                  {showPeople &&
                    group.people.map((hit) => (
                      <PersonCard
                        key={`person-${hit.tmdbId}`}
                        tmdbId={hit.tmdbId}
                        name={hit.name}
                        {...(hit.knownFor !== undefined ? { knownFor: hit.knownFor } : {})}
                        {...(hit.profilePath !== undefined
                          ? { profilePath: hit.profilePath }
                          : {})}
                      />
                    ))}
                </div>
              </section>
            );
          })}

          {search.catalogSearching ? (
            <div
              data-testid="search-catalog-searching"
              className="flex items-center gap-2 text-[12px] text-tx-muted"
            >
              <Loader2 className="h-3.5 w-3.5 animate-spin" />
              {t('search.catalogSearching')}
            </div>
          ) : null}
        </div>
      )}
    </div>
  );
}
