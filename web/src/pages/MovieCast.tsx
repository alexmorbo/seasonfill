import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useParams, Link } from 'react-router-dom';
import { ChevronLeft, TriangleAlert, X } from 'lucide-react';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Input } from '@/components/ui/input';
import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import { useSetPageTitle } from '@/components/shell/page-title-context';
import { useLanguage } from '@/hooks/useLanguage';
import { useMovie } from '@/api/movies';
import { useMovieCast, type MovieCastMember, type MovieCastSort } from '@/api/movieCast';
import { CompactHero } from '@/components/cast-page/CompactHero';
import { CastGrid } from '@/components/cast-page/CastGrid';

// Mirrors MovieDetail.tsx's local parseTmdbId verbatim — kept as a
// duplicate here rather than exported/shared, matching this codebase's
// existing per-page-parsing pattern (no shared helper module for it today).
function parseTmdbId(raw: string | undefined): number | null {
  if (!raw) return null;
  const n = Number.parseInt(raw, 10);
  return Number.isFinite(n) && n > 0 && String(n) === raw ? n : null;
}

function normalize(s: string | undefined): string {
  return (s ?? '').toLowerCase();
}

function filterCast(
  cast: readonly MovieCastMember[],
  q: string,
): readonly MovieCastMember[] {
  if (!q) return cast;
  const needle = q.toLowerCase();
  return cast.filter(
    (m) => normalize(m.name).includes(needle) || normalize(m.character_name).includes(needle),
  );
}

export function MovieCast() {
  const { t } = useTranslation();
  const { tmdbId: tmdbParam } = useParams<{ tmdbId: string }>();
  const lang = useLanguage().current;
  const [query, setQuery] = useState('');
  const [sortBy, setSortBy] = useState<MovieCastSort>('credit');

  const tmdbId = useMemo(() => parseTmdbId(tmdbParam), [tmdbParam]);

  const movieQ = useMovie(tmdbId ?? undefined, lang);
  const castQ = useMovieCast({
    tmdbId: tmdbId ?? undefined,
    lang,
    sort: sortBy,
  });

  const cast = useMemo<readonly MovieCastMember[]>(() => castQ.data?.cast ?? [], [castQ.data]);
  const filteredCast = useMemo(() => filterCast(cast, query), [cast, query]);

  useSetPageTitle(t('movieDetail.castPage.pageTitle'));

  if (tmdbId === null) {
    return (
      <div className="p-4">
        <Alert variant="destructive" data-testid="movie-cast-invalid">
          <TriangleAlert className="h-4 w-4" />
          <AlertTitle>{t('movieDetail.errors.invalidParams')}</AlertTitle>
        </Alert>
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-4">
      <nav className="flex items-center gap-2 text-[12.5px] text-tx-muted">
        <Link
          to={`/movies/${tmdbId}`}
          className="inline-flex items-center gap-1 hover:text-tx-primary transition-colors"
          data-testid="cast-page-back"
        >
          <ChevronLeft className="w-3.5 h-3.5" aria-hidden="true" />
          {t('movieDetail.back')}
        </Link>
      </nav>

      {(movieQ.isPending || castQ.isPending) && (
        <div data-testid="cast-page-skeleton" className="flex flex-col gap-4">
          <Skeleton className="h-[110px] w-full rounded-xl" />
          <div className="flex gap-2">
            <Skeleton className="h-9 w-24 rounded-md" />
            <Skeleton className="h-9 w-24 rounded-md" />
          </div>
          <div className="grid gap-3 grid-cols-1 sm:grid-cols-2 md:grid-cols-3 lg:grid-cols-5">
            {Array.from({ length: 10 }).map((_, i) => (
              <Skeleton key={i} className="h-[180px] w-full rounded-lg" />
            ))}
          </div>
        </div>
      )}

      {(movieQ.isError || castQ.isError) && (
        <Alert variant="destructive" data-testid="cast-page-error">
          <TriangleAlert className="h-4 w-4" />
          <AlertTitle>{t('movieDetail.errors.loadFailedTitle')}</AlertTitle>
          <AlertDescription>
            {castQ.error instanceof Error
              ? castQ.error.message
              : movieQ.error instanceof Error
                ? movieQ.error.message
                : t('common.error')}
          </AlertDescription>
        </Alert>
      )}

      {movieQ.isSuccess && castQ.isSuccess && (
        <>
          <CompactHero
            title={movieQ.data?.title}
            posterAsset={movieQ.data?.poster ?? undefined}
            yearStart={
              typeof movieQ.data?.year === 'number' && movieQ.data.year > 0
                ? movieQ.data.year
                : undefined
            }
            yearEnd={
              typeof movieQ.data?.year === 'number' && movieQ.data.year > 0
                ? movieQ.data.year
                : undefined
            }
            castCount={cast.length}
          />

          <div className="flex items-center justify-between gap-3 flex-wrap">
            <label className="flex items-center gap-2 text-[12.5px] text-tx-muted">
              <span className="shrink-0">{t('movieDetail.castPage.sort.label')}</span>
              <select
                data-testid="cast-sort"
                value={sortBy}
                onChange={(e) => setSortBy(e.target.value as MovieCastSort)}
                aria-label={t('movieDetail.castPage.sort.label')}
                className="h-9 rounded-md border border-border-subtle bg-bg-surface px-2.5 text-[12.5px] text-tx-primary focus-visible:outline-hidden focus-visible:ring-2 focus-visible:ring-accent"
              >
                <option value="credit" data-testid="cast-sort-option-credit">
                  {t('movieDetail.castPage.sort.credit')}
                </option>
                <option value="name" data-testid="cast-sort-option-name">
                  {t('movieDetail.castPage.sort.name')}
                </option>
              </select>
            </label>

            <div className="relative w-full max-w-[320px]">
              <Input
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                placeholder={t('movieDetail.castPage.searchPlaceholder')}
                aria-label={t('movieDetail.castPage.searchPlaceholder')}
                data-testid="cast-search"
                className="pr-8"
              />
              {query && (
                <button
                  type="button"
                  onClick={() => setQuery('')}
                  className="absolute right-2 top-1/2 -translate-y-1/2 text-tx-muted hover:text-tx-primary"
                  aria-label={t('movieDetail.castPage.searchClear')}
                  data-testid="cast-search-clear"
                >
                  <X className="w-3.5 h-3.5" />
                </button>
              )}
            </div>
          </div>

          {cast.length === 0 ? (
            <p
              data-testid="cast-page-empty"
              className="text-[13px] text-tx-muted py-12 text-center"
            >
              {t('movieDetail.castPage.empty.cast')}
            </p>
          ) : query && filteredCast.length === 0 ? (
            <div className="flex flex-col items-center gap-2 py-8">
              <p className="text-[12.5px] text-tx-muted" data-testid="cast-search-empty">
                {t('movieDetail.castPage.empty.search', { query })}
              </p>
              <Button variant="outline" size="sm" onClick={() => setQuery('')}>
                {t('movieDetail.castPage.searchClear')}
              </Button>
            </div>
          ) : (
            <CastGrid cast={filteredCast} totalEpisodeCount={0} />
          )}
        </>
      )}
    </div>
  );
}
