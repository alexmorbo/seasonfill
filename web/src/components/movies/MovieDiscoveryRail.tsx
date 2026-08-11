import { useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { PlusCircle } from 'lucide-react';
import { cn } from '@/lib/utils';
import { toBcp47 } from '@/lib/locale';
import { Skeleton } from '@/components/ui/skeleton';
import { MovieCard } from './MovieCard';
import { useAddToRadarrLauncher } from './add-to-radarr-context';
import {
  useMovieTrending, useMoviePopular, useMovieRowDiscover,
  type DiscoveryMovieItem,
} from '@/api/discoveryMovies';
import { todayISO, daysAgoISO, type DiscoveryRow } from '@/api/discoveryRows';

const TRACK = cn(
  'flex flex-row gap-3 overflow-x-auto snap-x snap-mandatory pb-2',
  'md:grid md:grid-flow-col md:auto-cols-[minmax(140px,1fr)] md:overflow-x-auto',
);
const CARD = 'snap-start min-w-[124px] md:min-w-0';

// MovieDiscoveryRail — the movie analogue of DiscoveryRail. One rail: a
// server-authored heading (row.title) + a horizontal snap slider of MovieCards,
// each with an "Add to Radarr" overlay. The item fetch is dispatched by
// row_type; all hooks run unconditionally (Rules of Hooks), gated via `enabled`.
export function MovieDiscoveryRail({ row }: { row: DiscoveryRow }) {
  const { i18n, t } = useTranslation();
  const lang = toBcp47(i18n.resolvedLanguage);
  const rt = row.row_type;
  const { openAddToRadarr } = useAddToRadarrLauncher();

  const trending = useMovieTrending(lang);
  const popular = useMoviePopular(lang);

  // library-sourced rows (recently_added) have no movie endpoint this wave.
  const isDiscover = row.source === 'tmdb_discover'
    && rt !== 'trending' && rt !== 'popular';

  // Pass row.params VERBATIM (dotted keys). upcoming(_releases) inject a live
  // date window using the MOVIE key primary_release_date.* (not the TV
  // first_air_date.*), analogous to DiscoveryRail.
  const discoverParams = useMemo<Record<string, string>>(() => {
    const p: Record<string, string> = { ...row.params };
    if (rt === 'upcoming_releases') {
      p['primary_release_date.gte'] = todayISO();
    } else if (rt === 'upcoming') {
      p['primary_release_date.gte'] = daysAgoISO(45);
      p['primary_release_date.lte'] = todayISO();
    }
    return p;
  }, [row.params, rt]);

  const discover = useMovieRowDiscover(discoverParams, lang, isDiscover);

  const { items, isPending, isError } = ((): {
    items: readonly DiscoveryMovieItem[]; isPending: boolean; isError: boolean;
  } => {
    switch (rt) {
      case 'trending': return { items: trending.data?.items ?? [], isPending: trending.isPending, isError: trending.isError };
      case 'popular': return { items: popular.data?.items ?? [], isPending: popular.isPending, isError: popular.isError };
      default:
        if (!isDiscover) return { items: [], isPending: false, isError: false };
        return { items: discover.data?.items ?? [], isPending: discover.isPending, isError: discover.isError };
    }
  })();

  // Only movies with a tmdb_id can link / be added.
  const visibleItems = useMemo(
    () => items.filter((it) => typeof it.tmdb_id === 'number' && it.tmdb_id > 0),
    [items],
  );

  // Empty/error rails render nothing (a broken rail must not break the page).
  if (isError) return null;
  if (!isPending && visibleItems.length === 0) return null;

  return (
    <section
      className="flex flex-col gap-2"
      data-testid={`movie-discovery-rail-${rt}`}
      data-position={row.position}
    >
      <h2 className="text-[13px] font-semibold text-tx-primary">{row.title}</h2>
      <div className={TRACK} data-testid={`movie-discovery-rail-track-${rt}`}>
        {isPending
          ? Array.from({ length: 8 }).map((_, i) => (
              <div key={i} className={cn(CARD, 'flex flex-col gap-1.5')}>
                <Skeleton className="aspect-[2/3] w-full rounded-md" />
                <Skeleton className="h-3 w-3/4" />
              </div>
            ))
          : visibleItems.map((item, idx) => {
              const tmdbId = item.tmdb_id as number;
              return (
                <div
                  key={`${item.movie_id}-${tmdbId}-${idx}`}
                  className={cn('group relative', CARD)}
                  data-testid="movie-discovery-card"
                >
                  <MovieCard
                    tmdbId={tmdbId}
                    title={item.title}
                    {...(item.year !== undefined ? { year: item.year } : {})}
                    {...(item.tmdb_rating !== undefined ? { rating: item.tmdb_rating } : {})}
                    {...(item.poster_hash !== undefined ? { poster: item.poster_hash } : {})}
                  />
                  <button
                    type="button"
                    data-testid="movie-discovery-add"
                    aria-label={t('movies.add.button')}
                    onClick={() => openAddToRadarr({ title: item.title, tmdbId })}
                    className={cn(
                      'absolute right-1.5 top-1.5 z-30 inline-flex h-7 w-7 items-center justify-center rounded-md',
                      'bg-black/50 text-white backdrop-blur-sm hover:bg-black/70',
                      'opacity-100 md:opacity-0 group-hover:opacity-100 focus-visible:opacity-100',
                      'transition-opacity focus-visible:outline-hidden focus-visible:ring-2 focus-visible:ring-accent',
                    )}
                  >
                    <PlusCircle className="h-4 w-4" />
                  </button>
                </div>
              );
            })}
      </div>
    </section>
  );
}
