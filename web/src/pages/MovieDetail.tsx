import { useCallback, useMemo, useRef } from 'react';
import { useParams } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import {
  AlertTriangle, ExternalLink, Plus, ChevronDown,
} from 'lucide-react';
import { useSetPageTitle } from '@/components/shell/page-title-context';
import { useLanguage } from '@/hooks/useLanguage';
import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import {
  DropdownMenu, DropdownMenuTrigger, DropdownMenuContent, DropdownMenuItem,
} from '@/components/ui/dropdown-menu';
import { aggregateDegraded } from '@/api/series';
import { useMovie, type MovieDetail as MovieDetailDTO } from '@/api/movies';
import { useMovieOverview } from '@/api/movieOverview';
import { useMovieCast } from '@/api/movieCast';
import { useMovieRatings } from '@/api/movieRatings';
import { MovieRecommendationsRail } from '@/components/movies/MovieRecommendationsRail';
import { MovieExternalLinksFooter } from '@/components/movies/MovieExternalLinksFooter';
import { MovieSyncFooter } from '@/components/movies/MovieSyncFooter';
import { CollectionHeroCard } from '@/components/movies/CollectionHeroCard';
import { MovieHeroLibraryStrip } from '@/components/movies/MovieHeroLibraryStrip';
import { useAddToRadarrLauncher } from '@/components/movies/add-to-radarr-context';
import { useInstances } from '@/lib/instances';
import { buildRadarrMovieHref } from '@/lib/radarrUrl';
import { MediaDetail } from '@/components/media-detail';
import type { MediaAction } from '@/components/media-detail/view-model';
import { FollowButton } from '@/components/follow/FollowButton';
import { MovieTorrentsSection } from '@/components/torrents/MovieTorrentsSection';
import { toMovieVM } from './toMovieVM';

function parseTmdbId(raw: string | undefined): number | null {
  if (!raw) return null;
  const n = Number.parseInt(raw, 10);
  return Number.isFinite(n) && n > 0 && String(n) === raw ? n : null;
}

// AddToRadarrSplitButton — a primary "Add to Radarr" action plus a dropdown
// of radarr instances. The primary click opens the modal with no instance
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

  if (href) {
    return (
      <Button asChild variant="outline" size="sm" data-testid="movie-detail-open-in-radarr">
        <a href={href} target="_blank" rel="noopener noreferrer">
          <ExternalLink className="h-4 w-4" aria-hidden="true" />
          {t('common.openInRadarr')}
        </a>
      </Button>
    );
  }

  // Disabled state: the movie is already held by a radarr instance, but the
  // instance has no operator-configured public_url so we can't build a deep
  // link. Kept visible+disabled (parity with the series "Open in Sonarr"
  // hero slot) rather than hidden, but a `title` explains why it doesn't do
  // anything instead of silently doing nothing. The `title` lives on the
  // outer wrapper, not the disabled <button> itself: the button's own
  // `disabled:pointer-events-none` styling would make the browser treat it
  // as un-hoverable and never show a native tooltip.
  const disabledReason = t('movies.open.noPublicUrl');
  return (
    <span title={disabledReason} className="inline-flex">
      <Button
        variant="outline"
        size="sm"
        disabled
        className="disabled:cursor-not-allowed"
        data-testid="movie-detail-open-in-radarr"
      >
        <ExternalLink className="h-4 w-4" aria-hidden="true" />
        {t('common.openInRadarr')}
      </Button>
    </span>
  );
}

export function MovieDetail() {
  const { t } = useTranslation();
  const { tmdbId: tmdbParam } = useParams();
  const lang = useLanguage().current;
  const torrentsRef = useRef<HTMLDivElement | null>(null);

  const tmdbId = useMemo(() => parseTmdbId(tmdbParam), [tmdbParam]);
  const query = useMovie(tmdbId ?? undefined, lang);
  const movie = query.data;

  // Prop-driven sections: the page owns the fetch, the component owns the view.
  const overviewQ = useMovieOverview({ tmdbId: tmdbId ?? undefined, lang });
  const castQ = useMovieCast({ tmdbId: tmdbId ?? undefined, lang });
  // ADR-0022 Wave-2 Story C — ratings-SECTION ownership moves UP from inside
  // `MovieRatingsSection` (which self-fetched) so `toMovieVM` can feed the
  // shared `<MediaRatingsSection>` scaffold slot.
  const ratingsQ = useMovieRatings({ tmdbId: tmdbId ?? undefined });

  useSetPageTitle(movie?.title ?? t('movies.title'));

  const scrollToTorrents = useCallback(() => {
    torrentsRef.current?.scrollIntoView({ behavior: 'smooth', block: 'start' });
  }, []);

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
      <div className="flex flex-col gap-4 px-[36px] lg:px-[36px]" data-testid="movie-detail-loading">
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
  // B1.5/ADR-0023 — the instance the movie torrents panel is scoped to.
  // Mirrors `OpenInRadarrButton`'s / `CollectionHeroCard`'s existing
  // `library[0]?.instance_name` resolution (first library row = the
  // Radarr instance holding this movie). '' when the movie is in no
  // library — MovieTorrentsSection then never leaves the "settings
  // pending" gate (same behavior SeriesDetail's `primaryInstance ?? ''`
  // fallback already relies on for TorrentsSection).
  const primaryInstanceName = library[0]?.instance_name ?? '';

  // Synced/stale footer — mirror of the SeriesDetail synced footer, kept as
  // a sibling OUTSIDE the shared scaffold (its staleness check is
  // movie-specific — the shared footer's is series-specific). The movie DTO
  // does not yet expose synced_at everywhere; read it forward-compatibly so
  // the footer lights up the moment BE adds the column. degraded[] IS
  // present on the DTO today.
  const degraded = movie.degraded ?? [];
  const tmdbStale = degraded.some((d) => d.startsWith('tmdb'));
  const omdbStale = degraded.includes('omdb');
  const syncedAt = (movie as MovieDetailDTO & { synced_at?: string }).synced_at;
  // F-04 — reuse the series DegradedChip via `MediaDetail`'s internal
  // degraded-chip render. aggregateDegraded narrows the movie
  // `degraded: string[]` to the known `DegradedSource[]` union.
  const degradedSources = aggregateDegraded(degraded);

  // Overview block — base movie.overview paints on first frame (gating query),
  // the localized /overview endpoint refines it once it lands. loading only
  // while BOTH are absent + pending, so a populated block never regresses.
  const ov = overviewQ.data;
  const overviewText = ov?.overview ?? movie.overview;
  const overviewTagline = ov?.tagline ?? movie.tagline;
  const overviewLoading = overviewQ.isLoading && !overviewText && !overviewTagline;

  // Hero action node(s) — Add-to-Radarr split button / Open-in-Radarr deep
  // link, resolved here (needs `useInstances`/`useAddToRadarrLauncher`,
  // hooks the `toMovieVM` adapter does not call — same "resolved by the
  // page" pattern `toSeriesVM`'s `heroActions` param uses).
  const radarrAction: readonly MediaAction[] =
    typeof movie.tmdb_id === 'number' && movie.tmdb_id > 0
      ? [{
          id: 'radarr-action',
          kind: 'node',
          node: library.length > 0 && library[0]?.instance_name ? (
            <OpenInRadarrButton tmdbId={movie.tmdb_id} instanceName={library[0].instance_name} />
          ) : (
            <AddToRadarrSplitButton title={movie.title ?? ''} tmdbId={movie.tmdb_id} />
          ),
        }]
      : [];
  // B1.5/ADR-0023 — quick-jump hero action to the torrents panel below the
  // overview grid. Mirrors SeriesDetail's `HeroLibraryStrip`
  // `onDownloadClick` chip in SPIRIT (both call `scrollToTorrents`), but
  // movies have no "download in progress" DTO field yet (that's the
  // series-only `download`/`DownloadChip` prop on `HeroLibraryStrip.tsx` —
  // adding it for movies is out of scope, non-goal "No re-grab UI (B2)"),
  // so this is an always-visible link gated on library presence (same gate
  // the torrents panel itself needs a resolvable instance for) rather than
  // a state-conditional badge.
  const torrentsAction: readonly MediaAction[] =
    library.length > 0
      ? [{
          id: 'torrents-action',
          kind: 'node',
          node: (
            <Button
              type="button"
              variant="ghost"
              size="sm"
              onClick={scrollToTorrents}
              data-testid="movie-detail-view-torrents"
            >
              {t('movieDetail.torrents.viewLink')}
            </Button>
          ),
        }]
      : [];
  const actions: readonly MediaAction[] = [...radarrAction, ...torrentsAction];

  // Follow/watchlist hero button — mirrors `SeriesHero`'s
  // `<FollowButton seriesId={seriesId}/>` (resolved here, same "resolved by
  // the page" pattern the radarr `actions` above use), keyed by TMDB id
  // since the movie API surface is TMDB-keyed throughout.
  const followButtonId = movie.tmdb_id ?? tmdbId;
  const followButton = <FollowButton mediaType="movie" tmdbId={followButtonId} />;

  // Hero-right compact collection card — the same `.sd-next-wrap` slot the
  // series hero fills with `NextEpisodeCard` (see `SeriesDetail.tsx`'s
  // `heroExtras.nextCard`). Movies have no next-episode concept, so the slot
  // is empty unless the movie belongs to a TMDB collection.
  const collectionId = movie.collection?.tmdb_collection_id;
  const hasCollection = typeof collectionId === 'number' && collectionId > 0;
  // Bottom-of-hero on-disk strip — always rendered (even for the
  // not-in-any-library case) so movies show a consistent "НА ДИСКЕ" area at
  // the bottom of the hero scrim, mirroring `SeriesDetail.tsx`'s
  // `heroExtras.bottomStrip` (`HeroLibraryStrip`). `heroExtras` therefore
  // never falls back to bare `undefined` anymore.
  const heroExtras = {
    ...(hasCollection
      ? {
          nextCard: (
            <CollectionHeroCard
              tmdbCollectionId={collectionId}
              {...(library[0]?.instance_name ? { instance: library[0].instance_name } : {})}
              {...(lang ? { lang } : {})}
            />
          ),
        }
      : {}),
    bottomStrip: <MovieHeroLibraryStrip library={library} />,
  };

  const vm = toMovieVM({
    t,
    lang,
    tmdbId,
    movie,
    ov,
    showTmdb,
    showImdb,
    sectionTmdbRating: ratingsQ.data?.tmdb_rating,
    sectionTmdbVotes: ratingsQ.data?.tmdb_votes,
    sectionImdbRating: ratingsQ.data?.imdb_rating,
    sectionImdbVotes: ratingsQ.data?.imdb_votes,
    rated: ratingsQ.data?.rated,
    awards: ratingsQ.data?.awards,
    actions,
    followButton,
    cast: castQ.data?.cast,
    castServedLang: castQ.data?.served_language,
    overviewText,
    overviewLoading,
    degraded: degradedSources,
  });

  return (
    <div
      className="sd-real -mt-5 flex flex-col gap-5 px-[36px] lg:px-[36px]"
      data-testid="movie-detail-page"
    >
      <MediaDetail
        vm={vm}
        heroExtras={heroExtras}
        belowGrid={
          <div ref={torrentsRef}>
            <MovieTorrentsSection instance={primaryInstanceName} tmdbId={tmdbId} />
          </div>
        }
        recommendationsSlot={<MovieRecommendationsRail tmdbId={movie.tmdb_id ?? tmdbId} />}
      />

      {/* External-links footer (movie /movie/ TMDB path + IMDb + homepage) —
          kept outside the scaffold: the shared built-in footer hardcodes
          /tv/{tmdb_id} + TVDB, which is wrong for movies. */}
      <MovieExternalLinksFooter
        {...(typeof movie.tmdb_id === 'number' ? { tmdbId: movie.tmdb_id } : {})}
        {...(movie.imdb_id ? { imdbId: movie.imdb_id } : {})}
        {...(movie.homepage ? { homepage: movie.homepage } : {})}
      />

      {/* Synced/stale footer — kept outside the scaffold: the shared
          built-in footer's staleness check is series-specific. */}
      <MovieSyncFooter
        {...(syncedAt ? { syncedAt } : {})}
        {...(tmdbStale ? { tmdbStale: true } : {})}
        {...(omdbStale ? { omdbStale: true } : {})}
      />
    </div>
  );
}
