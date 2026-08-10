import { useCallback, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { cn } from '@/lib/utils';
import { toBcp47 } from '@/lib/locale';
import { Skeleton } from '@/components/ui/skeleton';
import { DiscoveryCard } from './DiscoveryCard';
import { useHideFromDiscovery } from './useHideFromDiscovery';
import {
  useDiscoveryTrending, useDiscoveryPopular,
  type DiscoverySeriesItem,
} from '@/api/discovery';
import { useRowDiscover, todayISO, daysAgoISO, type DiscoveryRow } from '@/api/discoveryRows';
import { useLibraryRecentlyAdded } from './useLibraryRecentlyAdded';

const TRACK = cn(
  'flex flex-row gap-3 overflow-x-auto snap-x snap-mandatory pb-2',
  'md:grid md:grid-flow-col md:auto-cols-[minmax(140px,1fr)] md:overflow-x-auto',
);
const CARD = 'snap-start min-w-[124px] md:min-w-0';

// DiscoveryRail — one rail: a Russian heading (row.title, server-authored) +
// a horizontal snap slider of SeriesCards. The item fetch is dispatched by
// row_type. All hooks run unconditionally (Rules of Hooks); each is gated via
// its `enabled` flag so only the active branch actually fetches.
export function DiscoveryRail({ row }: { row: DiscoveryRow }) {
  const { i18n } = useTranslation();
  const lang = toBcp47(i18n.resolvedLanguage);
  const rt = row.row_type;

  const trending = useDiscoveryTrending(lang, false);
  const popular = useDiscoveryPopular(lang, false);

  const isDiscover =
    rt === 'upcoming' || rt === 'genre' || rt === 'network' ||
    rt === 'keyword' || rt === 'watch_provider' || rt === 'upcoming_releases';

  // Pass row.params VERBATIM (dotted keys). upcoming_releases injects a live
  // today date (a static one baked in the BE default would rot).
  const discoverParams = useMemo<Record<string, string>>(() => {
    const p: Record<string, string> = { ...row.params };
    if (rt === 'upcoming_releases') {
      p['first_air_date.gte'] = todayISO();
    } else if (rt === 'upcoming') {
      p['first_air_date.gte'] = daysAgoISO(45);
      p['first_air_date.lte'] = todayISO();
    }
    return p;
  }, [row.params, rt]);

  const discover = useRowDiscover(discoverParams, lang, isDiscover);
  const library = useLibraryRecentlyAdded(rt === 'recently_added');

  // Pick the active query's items by row_type.
  const { items, isPending, isError } = ((): {
    items: readonly DiscoverySeriesItem[]; isPending: boolean; isError: boolean;
  } => {
    switch (rt) {
      case 'trending': return { items: trending.data?.items ?? [], isPending: trending.isPending, isError: trending.isError };
      case 'popular':  return { items: popular.data?.items ?? [], isPending: popular.isPending, isError: popular.isError };
      case 'recently_added': return { items: library.items, isPending: library.isPending, isError: library.isError };
      default: return { items: discover.data?.items ?? [], isPending: discover.isPending, isError: discover.isError };
    }
  })();

  // Session-local optimistic hide set (keyed by tmdb_id). The POST persists
  // server-side; the visual removal is instant and reversible via the Undo
  // toast. A future refetch drops the item anyway once the BE filters it.
  const [hiddenIds, setHiddenIds] = useState<ReadonlySet<number>>(
    () => new Set<number>(),
  );
  const { hide } = useHideFromDiscovery();
  const onHide = useCallback((it: DiscoverySeriesItem) => {
    setHiddenIds((prev) => {
      const next = new Set(prev);
      next.add(it.tmdb_id);
      return next;
    });
    hide({
      tmdbId: it.tmdb_id,
      title: it.title,
      onRestore: () => setHiddenIds((prev) => {
        const next = new Set(prev);
        next.delete(it.tmdb_id);
        return next;
      }),
    });
  }, [hide]);

  const visibleItems = useMemo(
    () => items.filter((it) => !hiddenIds.has(it.tmdb_id)),
    [items, hiddenIds],
  );

  // Empty/error rails render nothing (a broken rail must not break the page).
  // When every card is hidden the whole rail collapses too.
  if (isError) return null;
  if (!isPending && visibleItems.length === 0) return null;

  return (
    <section
      className="flex flex-col gap-2"
      data-testid={`discovery-rail-${rt}`}
      data-position={row.position}
    >
      <h2 className="text-[13px] font-semibold text-tx-primary">{row.title}</h2>
      <div className={TRACK} data-testid={`discovery-rail-track-${rt}`}>
        {isPending
          ? Array.from({ length: 8 }).map((_, i) => (
              <div key={i} className={cn(CARD, 'flex flex-col gap-1.5')}>
                <Skeleton className="aspect-[2/3] w-full rounded-md" />
                <Skeleton className="h-3 w-3/4" />
              </div>
            ))
          : visibleItems.map((item, idx) => (
              <DiscoveryCard
                key={`${item.series_id}-${item.tmdb_id}-${idx}`}
                item={item}
                onHide={onHide}
                className={CARD}
              />
            ))}
      </div>
    </section>
  );
}
