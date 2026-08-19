import { useMemo, useState } from 'react';
import { useParams } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import {
  AlertTriangle, Star, ExternalLink, Clock, Plus, ChevronDown, PlayCircle,
} from 'lucide-react';
import { useSetPageTitle } from '@/components/shell/page-title-context';
import { useLanguage } from '@/hooks/useLanguage';
import { MediaImage } from '@/components/MediaImage';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import {
  DropdownMenu, DropdownMenuTrigger, DropdownMenuContent, DropdownMenuItem,
} from '@/components/ui/dropdown-menu';
import { OverviewGrid } from '@/components/series-detail/OverviewGrid';
import { KeywordChips } from '@/components/series-detail/KeywordChips';
import { TrailerModal } from '@/components/series-detail/TrailerModal';
import { useMovie, type MovieDetail, type MovieDetailLibrary } from '@/api/movies';
import { useMovieOverview } from '@/api/movieOverview';
import { useMovieCast } from '@/api/movieCast';
import { MovieOverviewBlock } from '@/components/movies/MovieOverviewBlock';
import { MovieCastStrip } from '@/components/movies/MovieCastStrip';
import { MovieRatingsSection } from '@/components/movies/MovieRatingsSection';
import { MovieRecommendationsRail } from '@/components/movies/MovieRecommendationsRail';
import { MovieCollectionBlock } from '@/components/movies/MovieCollectionBlock';
import { MovieSidebar } from '@/components/movies/MovieSidebar';
import { MovieExternalLinksFooter } from '@/components/movies/MovieExternalLinksFooter';
import { MovieSyncFooter } from '@/components/movies/MovieSyncFooter';
import { useAddToRadarrLauncher } from '@/components/movies/add-to-radarr-context';
import { useInstances } from '@/lib/instances';
import { buildRadarrMovieHref } from '@/lib/radarrUrl';
import { useFormatDate } from '@/lib/timezone';

function parseTmdbId(raw: string | undefined): number | null {
  if (!raw) return null;
  const n = Number.parseInt(raw, 10);
  return Number.isFinite(n) && n > 0 && String(n) === raw ? n : null;
}

function LibraryRow({ row }: { row: MovieDetailLibrary }) {
  const { t } = useTranslation();
  return (
    <div
      data-testid={`movie-library-row-${row.instance_name ?? 'unknown'}`}
      className="flex flex-wrap items-center gap-2 rounded-md border border-border-subtle bg-bg-surface px-3 py-2"
    >
      <span className="text-[13px] font-medium text-tx-primary">
        {row.instance_name}
      </span>
      {row.monitored && (
        <Badge variant="accent" data-testid="movie-library-monitored">
          {t('movieDetail.library.monitored')}
        </Badge>
      )}
      {row.has_file && (
        <Badge variant="ok" data-testid="movie-library-hasfile">
          {t('movieDetail.library.hasFile')}
        </Badge>
      )}
      {row.availability && (
        <span className="text-[12px] text-tx-muted">{row.availability}</span>
      )}
    </div>
  );
}

// AddToRadarrSplitButton — a primary "Add to Radarr" action plus a dropdown of
// radarr instances. The primary click opens the modal with no instance
// preselected (auto-picks the first radarr); each dropdown item preselects a
// specific radarr instance.
function AddToRadarrSplitButton({ title, tmdbId }: { title: string; tmdbId: number }) {
  const { t } = useTranslation();
  const { openAddToRadarr } = useAddToRadarrLauncher();
  const instancesQ = useInstances();
  const radarrInstances = useMemo(
    () => (instancesQ.data?.instances ?? []).filter(
      (i) => Boolean(i.name) && (i.type ?? 'sonarr') === 'radarr',
    ),
    [instancesQ.data],
  );

  return (
    <div className="inline-flex items-stretch" data-testid="movie-detail-add-to-radarr">
      <Button
        type="button"
        size="sm"
        className="rounded-r-none"
        data-testid="movie-detail-add-to-radarr-primary"
        onClick={() => openAddToRadarr({ title, tmdbId })}
      >
        <Plus className="h-4 w-4" />
        {t('movies.add.button')}
      </Button>
      {radarrInstances.length > 0 && (
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button
              type="button"
              size="sm"
              className="rounded-l-none border-l border-black/20 px-2"
              aria-label={t('movies.add.instance')}
              data-testid="movie-detail-add-to-radarr-menu"
            >
              <ChevronDown className="h-4 w-4" />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            {radarrInstances.map((i) => (
              <DropdownMenuItem
                key={i.name}
                data-testid={`movie-detail-add-to-radarr-instance-${i.name}`}
                onSelect={() => openAddToRadarr({
                  title, tmdbId, ...(i.name ? { instanceName: i.name } : {}),
                })}
              >
                {i.name}
              </DropdownMenuItem>
            ))}
          </DropdownMenuContent>
        </DropdownMenu>
      )}
    </div>
  );
}

// OpenInRadarrButton — deep-links the movie in the radarr instance that already
// holds it. Mirrors the SERIES hero "Open in Sonarr" CTA: resolves the
// instance's operator-configured public_url from the roster and renders an
// external link (disabled when the instance has no public_url configured).
function OpenInRadarrButton({ tmdbId, instanceName }: { tmdbId: number; instanceName: string }) {
  const { t } = useTranslation();
  const instancesQ = useInstances();
  const publicUrl = useMemo(() => {
    for (const i of instancesQ.data?.instances ?? []) {
      if (i.name === instanceName && i.public_url) return i.public_url;
    }
    return undefined;
  }, [instancesQ.data, instanceName]);
  const href = publicUrl ? buildRadarrMovieHref(publicUrl, tmdbId) : undefined;

  return (
    <Button
      asChild={Boolean(href)}
      variant="outline"
      size="sm"
      disabled={!href}
      data-testid="movie-detail-open-in-radarr"
    >
      {href ? (
        <a href={href} target="_blank" rel="noopener noreferrer">
          <ExternalLink className="h-4 w-4" aria-hidden="true" />
          {t('common.openInRadarr')}
        </a>
      ) : (
        <span>
          <ExternalLink className="h-4 w-4" aria-hidden="true" />
          {t('common.openInRadarr')}
        </span>
      )}
    </Button>
  );
}

export function MovieDetail() {
  const { t } = useTranslation();
  const { tmdbId: tmdbParam } = useParams();
  const lang = useLanguage().current;
  const formatDate = useFormatDate();
  const [trailerOpen, setTrailerOpen] = useState(false);

  const tmdbId = useMemo(() => parseTmdbId(tmdbParam), [tmdbParam]);
  const query = useMovie(tmdbId ?? undefined, lang);
  const movie = query.data;

  // Prop-driven sections: the page owns the fetch, the component owns the view.
  const overviewQ = useMovieOverview({ tmdbId: tmdbId ?? undefined, lang });
  const castQ = useMovieCast({ tmdbId: tmdbId ?? undefined, lang });

  useSetPageTitle(movie?.title ?? t('movies.title'));

  if (tmdbId === null) {
    return (
      <Alert variant="destructive" data-testid="movie-detail-invalid">
        <AlertTriangle className="h-4 w-4" />
        <AlertTitle>{t('movieDetail.errors.invalidParams')}</AlertTitle>
      </Alert>
    );
  }

  if (query.isPending) {
    return (
      <div className="flex flex-col gap-4" data-testid="movie-detail-loading">
        <div className="flex gap-5">
          <Skeleton className="h-[300px] w-[200px] shrink-0 rounded-lg" />
          <div className="flex flex-1 flex-col gap-3">
            <Skeleton className="h-7 w-2/3" />
            <Skeleton className="h-4 w-1/3" />
            <Skeleton className="h-20 w-full" />
          </div>
        </div>
      </div>
    );
  }

  if (query.isError || !movie) {
    return (
      <Alert variant="destructive" data-testid="movie-detail-error">
        <AlertTriangle className="h-4 w-4" />
        <AlertTitle>{t('movieDetail.errors.loadFailedTitle')}</AlertTitle>
        <AlertDescription>
          {query.error instanceof Error ? query.error.message : t('common.error')}
        </AlertDescription>
      </Alert>
    );
  }

  const showTmdb = typeof movie.tmdb_rating === 'number' && movie.tmdb_rating > 0;
  const showImdb = typeof movie.imdb_rating === 'number' && movie.imdb_rating > 0;
  const library = movie.library ?? [];
  const genres = movie.genres ?? [];

  // Synced/stale footer — mirror of the SeriesDetail synced footer. The movie
  // DTO does not yet expose synced_at (S4 shipped the 4 money/identity fields
  // only); read it forward-compatibly so the footer lights up the moment BE
  // adds the column. degraded[] IS present on the DTO today.
  const degraded = movie.degraded ?? [];
  const tmdbStale = degraded.some((d) => d.startsWith('tmdb'));
  const omdbStale = degraded.includes('omdb');
  const syncedAt = (movie as MovieDetail & { synced_at?: string }).synced_at;

  // Overview block — base movie.overview paints on first frame (gating query),
  // the localized /overview endpoint refines it once it lands. loading only
  // while BOTH are absent + pending, so a populated block never regresses.
  const ov = overviewQ.data;
  const overviewText = ov?.overview ?? movie.overview;
  const overviewTitle = ov?.title ?? movie.title;
  const overviewTagline = ov?.tagline ?? movie.tagline;
  const overviewLoading =
    overviewQ.isLoading && !overviewText && !overviewTagline;

  const cast = castQ.data?.cast;
  const castServed = castQ.data?.served_language;

  const trailerKey =
    movie.trailer?.key
    && (movie.trailer.site === undefined || movie.trailer.site === 'YouTube')
      ? movie.trailer.key
      : undefined;

  return (
    <div className="flex flex-col gap-6" data-testid="movie-detail-page">
      {/* Hero */}
      <div className="relative overflow-hidden rounded-xl border border-border-subtle">
        {movie.backdrop && (
          <div className="absolute inset-0" aria-hidden="true">
            <MediaImage
              hash={movie.backdrop}
              kind="backdrop"
              title={movie.title ?? ''}
              fallback="svg"
              aspectRatio="aspect-auto"
              className="absolute inset-0 opacity-30"
            />
            <div className="absolute inset-0 bg-gradient-to-t from-bg-base via-bg-base/80 to-bg-base/40" />
          </div>
        )}

        <div className="relative flex flex-col gap-5 p-5 sm:flex-row">
          <div className="w-[180px] shrink-0">
            <MediaImage
              hash={movie.poster ?? null}
              kind="poster"
              title={movie.title ?? ''}
              fallback="monogram"
              className="rounded-lg border border-border-subtle"
              data-testid="movie-detail-poster"
            />
          </div>

          <div className="flex flex-1 flex-col gap-3">
            <div>
              <h1
                data-testid="movie-detail-title"
                className="text-2xl font-semibold text-tx-primary"
              >
                {movie.title}
                {typeof movie.year === 'number' && movie.year > 0 && (
                  <span className="ml-2 text-tx-muted tabular-nums">({movie.year})</span>
                )}
              </h1>
              {movie.tagline && (
                <p
                  data-testid="movie-detail-tagline"
                  className="mt-1 text-[14px] italic text-tx-muted"
                >
                  {movie.tagline}
                </p>
              )}
            </div>

            {genres.length > 0 && (
              <KeywordChips chips={genres} className="mt-0.5" />
            )}

            <div className="flex flex-wrap items-center gap-2 text-[13px] text-tx-secondary">
              {typeof movie.runtime_minutes === 'number' && movie.runtime_minutes > 0 && (
                <span
                  data-testid="movie-detail-runtime"
                  className="inline-flex items-center gap-1"
                >
                  <Clock className="h-3.5 w-3.5" aria-hidden="true" />
                  {t('movieDetail.meta.runtime', { count: movie.runtime_minutes })}
                </span>
              )}
              {movie.release_date && (
                <span data-testid="movie-detail-released">
                  {t('movieDetail.meta.released', { date: formatDate(movie.release_date, 'date') })}
                </span>
              )}
            </div>

            {/* Ratings row (compact, mirrors SeriesHero ★). The richer
                MovieRatingsSection renders below in the main column. */}
            {(showTmdb || showImdb) && (
              <div
                className="flex flex-wrap items-center gap-4"
                data-testid="movie-detail-ratings"
              >
                {showTmdb && (
                  <span
                    className="inline-flex items-center gap-1.5 text-[13px]"
                    data-testid="movie-detail-rating-tmdb"
                  >
                    <Star className="h-4 w-4 text-warn" fill="currentColor" aria-hidden="true" />
                    <span className="font-semibold tabular-nums text-tx-primary">
                      {(movie.tmdb_rating as number).toFixed(1)}
                    </span>
                    <span className="text-tx-faint">{t('movieDetail.ratings.tmdb')}</span>
                  </span>
                )}
                {showImdb && (
                  <span
                    className="inline-flex items-center gap-1.5 text-[13px]"
                    data-testid="movie-detail-rating-imdb"
                  >
                    <span className="font-semibold tabular-nums text-tx-primary">
                      {(movie.imdb_rating as number).toFixed(1)}
                    </span>
                    {movie.imdb_id ? (
                      <a
                        href={`https://www.imdb.com/title/${movie.imdb_id}/`}
                        target="_blank"
                        rel="noreferrer"
                        className="inline-flex items-center gap-0.5 text-tx-faint hover:text-accent"
                        data-testid="movie-detail-imdb-link"
                      >
                        {t('movieDetail.ratings.imdb')}
                        <ExternalLink className="h-3 w-3" aria-hidden="true" />
                      </a>
                    ) : (
                      <span className="text-tx-faint">{t('movieDetail.ratings.imdb')}</span>
                    )}
                  </span>
                )}
              </div>
            )}

            <div className="flex flex-wrap items-center gap-2 pt-1">
              {typeof movie.tmdb_id === 'number' && movie.tmdb_id > 0 && (
                library.length > 0 && library[0]?.instance_name ? (
                  <OpenInRadarrButton
                    tmdbId={movie.tmdb_id}
                    instanceName={library[0].instance_name}
                  />
                ) : (
                  <AddToRadarrSplitButton title={movie.title ?? ''} tmdbId={movie.tmdb_id} />
                )
              )}
              {trailerKey && (
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  data-testid="movie-detail-trailer-button"
                  onClick={() => setTrailerOpen(true)}
                >
                  <PlayCircle className="h-4 w-4" aria-hidden="true" />
                  {t('seriesDetail.hero.trailer')}
                </Button>
              )}
            </div>
          </div>
        </div>
      </div>

      {/* Main content + right rail (series-parity OverviewGrid). */}
      <OverviewGrid
        left={
          <>
            <section data-testid="movie-detail-overview">
              <MovieOverviewBlock
                tmdbId={movie.tmdb_id ?? tmdbId}
                {...(overviewTitle ? { title: overviewTitle } : {})}
                {...(overviewText ? { overview: overviewText } : {})}
                {...(overviewTagline ? { tagline: overviewTagline } : {})}
                {...(ov?.served_language ? { servedLanguage: ov.served_language } : {})}
                {...(lang ? { requestedLang: lang } : {})}
                {...(overviewLoading ? { loading: true } : {})}
              />
            </section>

            <MovieCastStrip
              tmdbId={movie.tmdb_id ?? tmdbId}
              {...(cast ? { cast } : {})}
              {...(castServed ? { servedLanguage: castServed } : {})}
              {...(lang ? { requestedLang: lang } : {})}
            />

            <MovieRatingsSection tmdbId={movie.tmdb_id ?? tmdbId} />
          </>
        }
        right={<MovieSidebar movie={movie} />}
      />

      {/* Collection block (movie-only). */}
      {typeof movie.collection?.tmdb_collection_id === 'number'
        && movie.collection.tmdb_collection_id > 0 && (
        <MovieCollectionBlock
          tmdbCollectionId={movie.collection.tmdb_collection_id}
          {...(library[0]?.instance_name
            ? { instance: library[0].instance_name }
            : {})}
        />
      )}

      {/* Library membership. */}
      <section data-testid="movie-detail-library">
        <h2 className="mb-1.5 text-[13px] font-semibold uppercase tracking-wide text-tx-faint">
          {t('movieDetail.library.title')}
        </h2>
        {library.length === 0 ? (
          <p className="text-[13px] text-tx-muted" data-testid="movie-detail-library-empty">
            {t('movieDetail.library.empty')}
          </p>
        ) : (
          <div className="flex flex-col gap-2">
            {library.map((row) => (
              <LibraryRow key={row.instance_name ?? row.radarr_movie_id} row={row} />
            ))}
          </div>
        )}
      </section>

      {/* Recommendations rail (self-fetches). */}
      <MovieRecommendationsRail tmdbId={movie.tmdb_id ?? tmdbId} />

      {/* External-links footer (movie /movie/ TMDB path + IMDb + homepage). */}
      <MovieExternalLinksFooter
        {...(typeof movie.tmdb_id === 'number' ? { tmdbId: movie.tmdb_id } : {})}
        {...(movie.imdb_id ? { imdbId: movie.imdb_id } : {})}
        {...(movie.homepage ? { homepage: movie.homepage } : {})}
      />

      {/* Synced/stale footer (dormant until BE adds synced_at — see S5 note). */}
      <MovieSyncFooter
        {...(syncedAt ? { syncedAt } : {})}
        {...(tmdbStale ? { tmdbStale: true } : {})}
        {...(omdbStale ? { omdbStale: true } : {})}
      />

      {trailerKey && (
        <TrailerModal
          open={trailerOpen}
          onOpenChange={setTrailerOpen}
          youtubeKey={trailerKey}
          {...(movie.trailer?.name ? { name: movie.trailer.name } : {})}
        />
      )}
    </div>
  );
}
