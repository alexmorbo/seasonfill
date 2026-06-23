import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useParams, Link } from 'react-router-dom';
import { ChevronLeft, TriangleAlert, X } from 'lucide-react';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Tabs, TabsList, TabsTrigger, TabsContent } from '@/components/ui/tabs';
import { Input } from '@/components/ui/input';
import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import { useSetPageTitle } from '@/components/shell/page-title-context';
import { useSeriesCast, type CastPageMember, type CrewPageMember } from '@/api/seriesCast';
import { degradedIncludes } from '@/api/series';
import { CompactHero } from '@/components/cast-page/CompactHero';
import { CastGrid } from '@/components/cast-page/CastGrid';
import { CrewGrid } from '@/components/cast-page/CrewGrid';

function normalize(s: string | undefined): string {
  return (s ?? '').toLowerCase();
}

function filterCast(cast: readonly CastPageMember[], q: string): readonly CastPageMember[] {
  if (!q) return cast;
  const needle = q.toLowerCase();
  return cast.filter(
    (m) => normalize(m.name).includes(needle) || normalize(m.character_name).includes(needle),
  );
}

function filterCrew(crew: readonly CrewPageMember[], q: string): readonly CrewPageMember[] {
  if (!q) return crew;
  const needle = q.toLowerCase();
  return crew.filter(
    (m) => normalize(m.name).includes(needle) || normalize(m.job).includes(needle),
  );
}

export function SeriesCast() {
  const { t, i18n } = useTranslation();
  // Story 495 / N-1e §A1: URL is global — `:instance` segment is gone.
  // Back-link uses `/series/${seriesId}` (instance-less).
  const { id } = useParams<{ id: string }>();
  const seriesId = id ? Number(id) : undefined;
  const lang = i18n.resolvedLanguage;
  const [query, setQuery] = useState('');

  const result = useSeriesCast({
    seriesId,
    ...(lang ? { lang } : {}),
  });
  const data = result.data;
  const cast = useMemo<readonly CastPageMember[]>(() => data?.cast ?? [], [data?.cast]);
  const crew = useMemo<readonly CrewPageMember[]>(() => data?.crew ?? [], [data?.crew]);

  const filteredCast = useMemo(() => filterCast(cast, query), [cast, query]);
  const filteredCrew = useMemo(() => filterCrew(crew, query), [crew, query]);

  useSetPageTitle(t('seriesDetail.castPage.pageTitle'));

  if (!seriesId || Number.isNaN(seriesId)) {
    return (
      <div className="p-4">
        <Alert variant="destructive">
          <TriangleAlert className="h-4 w-4" />
          <AlertTitle>{t('seriesDetail.errors.invalidParams')}</AlertTitle>
          <AlertDescription>{t('seriesDetail.errors.invalidParamsBody')}</AlertDescription>
        </Alert>
      </div>
    );
  }

  // Story 495 / N-1e (B-20): when TMDB person enrichment is in flight
  // AND both lists are empty, render a skeleton instead of the "empty"
  // fallback so operator can tell "loading" from "no data". The
  // dto.SeriesCastResponse schema doesn't expose `degraded[]` yet —
  // we read it dynamically (`as unknown`) so the BE can add it
  // without a schema regen and we get the UX immediately.
  const dataDegraded = (data as unknown as { degraded?: readonly string[] } | undefined)?.degraded;
  const tmdbPersonDegraded = degradedIncludes(dataDegraded, 'tmdb_person');
  const castLoading = result.isSuccess
    && cast.length === 0 && crew.length === 0 && tmdbPersonDegraded;

  return (
    <div className="flex flex-col gap-4">
      <nav className="flex items-center gap-2 text-[12.5px] text-tx-muted">
        <Link
          to={`/series/${seriesId}`}
          className="inline-flex items-center gap-1 hover:text-tx-primary transition-colors"
          data-testid="cast-page-back"
        >
          <ChevronLeft className="w-3.5 h-3.5" aria-hidden="true" />
          {t('seriesDetail.back')}
        </Link>
      </nav>

      {result.isPending && (
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

      {result.isError && (
        <Alert variant="destructive" data-testid="cast-page-error">
          <TriangleAlert className="h-4 w-4" />
          <AlertTitle>{t('seriesDetail.castPage.loadFailedTitle')}</AlertTitle>
          <AlertDescription>{t('seriesDetail.castPage.loadFailedBody')}</AlertDescription>
        </Alert>
      )}

      {result.isSuccess && (
        <>
          <CompactHero
            title={data?.series_summary?.title}
            posterAsset={data?.series_summary?.poster_url ?? undefined}
            status={data?.series_summary?.status}
            yearStart={data?.series_summary?.first_aired_year ?? undefined}
            yearEnd={data?.series_summary?.last_aired_year ?? undefined}
            castCount={cast.length}
            crewCount={crew.length}
          />

          <div className="flex items-center justify-end">
            <div className="relative w-full max-w-[320px]">
              <Input
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                placeholder={t('seriesDetail.castPage.searchPlaceholder')}
                aria-label={t('seriesDetail.castPage.searchPlaceholder')}
                data-testid="cast-search"
                className="pr-8"
              />
              {query && (
                <button
                  type="button"
                  onClick={() => setQuery('')}
                  className="absolute right-2 top-1/2 -translate-y-1/2 text-tx-muted hover:text-tx-primary"
                  aria-label={t('seriesDetail.castPage.searchClear')}
                  data-testid="cast-search-clear"
                >
                  <X className="w-3.5 h-3.5" />
                </button>
              )}
            </div>
          </div>

          {cast.length === 0 && crew.length === 0 ? (
            castLoading ? (
              <div
                data-testid="cast-page-loading"
                className="flex flex-col gap-4"
              >
                <p className="text-[12.5px] text-tx-muted text-center">
                  {t('seriesDetail.degraded.cast.loading')}
                </p>
                <div className="grid gap-3 grid-cols-1 sm:grid-cols-2 md:grid-cols-3 lg:grid-cols-5">
                  {Array.from({ length: 10 }).map((_, i) => (
                    <Skeleton
                      key={i}
                      data-testid="cast-page-skeleton-tile"
                      className="h-[180px] w-full rounded-lg"
                    />
                  ))}
                </div>
              </div>
            ) : (
              <p
                data-testid="cast-page-empty"
                className="text-[13px] text-tx-muted py-12 text-center"
              >
                {t('seriesDetail.castPage.empty.both')}
              </p>
            )
          ) : (
            <Tabs defaultValue="cast">
              <TabsList data-testid="cast-tabs-list">
                <TabsTrigger value="cast" data-testid="cast-tab-cast">
                  {query
                    ? t('seriesDetail.castPage.tabs.castFiltered', {
                        matched: filteredCast.length,
                        total: cast.length,
                      })
                    : `${t('seriesDetail.castPage.tabs.cast')} (${cast.length})`}
                </TabsTrigger>
                <TabsTrigger value="crew" data-testid="cast-tab-crew">
                  {query
                    ? t('seriesDetail.castPage.tabs.crewFiltered', {
                        matched: filteredCrew.length,
                        total: crew.length,
                      })
                    : `${t('seriesDetail.castPage.tabs.crew')} (${crew.length})`}
                </TabsTrigger>
              </TabsList>

              <TabsContent value="cast">
                {query && filteredCast.length === 0 ? (
                  <div className="flex flex-col items-center gap-2 py-8">
                    <p className="text-[12.5px] text-tx-muted" data-testid="cast-search-empty">
                      {t('seriesDetail.castPage.empty.search', { query })}
                    </p>
                    <Button variant="outline" size="sm" onClick={() => setQuery('')}>
                      {t('seriesDetail.castPage.searchClear')}
                    </Button>
                  </div>
                ) : (
                  <CastGrid
                    cast={filteredCast}
                    totalEpisodeCount={data?.total_episode_count ?? 0}
                  />
                )}
              </TabsContent>

              <TabsContent value="crew">
                {query && filteredCrew.length === 0 ? (
                  <div className="flex flex-col items-center gap-2 py-8">
                    <p className="text-[12.5px] text-tx-muted" data-testid="crew-search-empty">
                      {t('seriesDetail.castPage.empty.search', { query })}
                    </p>
                    <Button variant="outline" size="sm" onClick={() => setQuery('')}>
                      {t('seriesDetail.castPage.searchClear')}
                    </Button>
                  </div>
                ) : (
                  <CrewGrid crew={filteredCrew} />
                )}
              </TabsContent>
            </Tabs>
          )}
        </>
      )}
    </div>
  );
}
