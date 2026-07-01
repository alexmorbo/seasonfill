import { useEffect, useMemo, useRef } from 'react';
import { useTranslation } from 'react-i18next';
import { useNavigate, useLocation } from 'react-router-dom';
import {
  Ban,
  ShieldCheck,
  ShieldOff,
  Timer,
  TriangleAlert,
} from 'lucide-react';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import { cn } from '@/lib/utils';
import { relativeTime } from '@/lib/format';
import { useFormatDate } from '@/lib/timezone';
import {
  flattenSeasons,
  useWatchdogSeasons,
  type WatchdogSeason,
  type WatchdogSeasonsFilters,
} from '@/lib/api/watchdogSeasons';

const GRID_COLS =
  '[grid-template-columns:minmax(220px,1.6fr)_minmax(170px,1fr)_120px_140px_90px_minmax(150px,1fr)]';

export interface WatchdogSeasonsTableProps {
  readonly filters: WatchdogSeasonsFilters;
}

function StatusBadges({ row }: { row: WatchdogSeason }) {
  const { t } = useTranslation();
  const badges: React.ReactNode[] = [];
  if (row.monitored) {
    badges.push(
      <Badge key="monitored" variant="ok" mono className="px-1.5 py-0.5 text-[10.5px]">
        <ShieldCheck className="h-3 w-3" />
        {t('watchdog.table.status.monitored')}
      </Badge>,
    );
  } else {
    badges.push(
      <Badge key="not-monitored" variant="neutral" mono className="px-1.5 py-0.5 text-[10.5px]">
        <ShieldOff className="h-3 w-3" />
        {t('watchdog.table.status.notMonitored')}
      </Badge>,
    );
  }
  if (row.blacklist) {
    badges.push(
      <Badge key="bl" variant="danger" mono className="px-1.5 py-0.5 text-[10.5px]">
        <Ban className="h-3 w-3" />
        {t('watchdog.table.status.blacklisted')}
      </Badge>,
    );
  }
  if (row.cooldown) {
    badges.push(
      <Badge key="cd" variant="warn" mono className="px-1.5 py-0.5 text-[10.5px]">
        <Timer className="h-3 w-3" />
        {t('watchdog.table.status.cooldownActive')}
      </Badge>,
    );
  }
  return <div className="flex flex-wrap gap-1">{badges}</div>;
}

function NoBetterCell({ row }: { row: WatchdogSeason }) {
  const nb = row.no_better_counter;
  if (!nb) return <span className="text-tx-faint">—</span>;
  const consec = nb.consecutive ?? 0;
  const max = nb.max ?? 3;
  const warn = max > 0 && consec >= Math.max(1, max - 1);
  return (
    <Badge
      variant={warn ? 'warn' : 'neutral'}
      mono
      className="px-1.5 py-0.5 text-[11px]"
    >
      {warn ? <TriangleAlert className="h-3 w-3" /> : null}
      {consec}/{max}
    </Badge>
  );
}

function CooldownCell({ row }: { row: WatchdogSeason }) {
  const { t } = useTranslation();
  const fmt = useFormatDate();
  if (!row.cooldown?.expires_at) {
    return <span className="text-tx-faint">—</span>;
  }
  const expiresMs = Date.parse(row.cooldown.expires_at);
  if (Number.isNaN(expiresMs)) {
    return <span className="text-tx-faint">—</span>;
  }
  return (
    <Badge variant="warn" mono className="px-1.5 py-0.5 text-[11px]">
      {t('watchdog.table.cooldownUntil', { ts: fmt(expiresMs, 'shortDateTime') })}
    </Badge>
  );
}

function OriginCell({ row }: { row: WatchdogSeason }) {
  if (!row.origin) return <span className="text-tx-faint">—</span>;
  const indexer = row.origin.indexer ?? '—';
  const first = row.origin.first_seen_at;
  return (
    <div className="flex flex-col">
      <span className="truncate text-[12.5px] font-medium text-tx-primary">
        {indexer}
      </span>
      <span className="text-[10.5px] text-tx-faint">{relativeTime(first)}</span>
    </div>
  );
}

function HeaderRow() {
  const { t } = useTranslation();
  return (
    <div
      role="row"
      data-testid="watchdog-seasons-header"
      className={cn(
        'grid gap-3 border-b border-border-subtle px-4 py-2.5',
        GRID_COLS,
        'text-[10px] font-semibold uppercase tracking-[0.09em] text-tx-faint',
      )}
    >
      <span>{t('watchdog.table.columns.series')}</span>
      <span>{t('watchdog.table.columns.origin')}</span>
      <span>{t('watchdog.table.columns.lastSeen')}</span>
      <span>{t('watchdog.table.columns.cooldown')}</span>
      <span>{t('watchdog.table.columns.noBetter')}</span>
      <span>{t('watchdog.table.columns.status')}</span>
    </div>
  );
}

function DataRow({
  row,
  onOpen,
}: {
  row: WatchdogSeason;
  onOpen: (instance: string, seriesId: number) => void;
}) {
  const seriesLabel = `${row.series_title ?? `#${row.series_id ?? '?'}`} · S${String(
    row.season_number ?? 0,
  ).padStart(2, '0')}`;
  const handleOpen = () => {
    if (row.instance && row.series_id !== undefined) {
      onOpen(row.instance, row.series_id);
    }
  };
  return (
    <div
      role="row"
      tabIndex={0}
      onClick={handleOpen}
      onKeyDown={(e) => {
        if (e.key === 'Enter' || e.key === ' ') {
          e.preventDefault();
          handleOpen();
        }
      }}
      data-testid={`watchdog-seasons-row-${row.instance ?? '?'}-${row.series_id ?? '?'}-${row.season_number ?? '?'}`}
      className={cn(
        'grid items-center gap-3 border-b border-border-faint px-4 py-2.5',
        'text-[13px] last:border-b-0',
        'cursor-pointer hover:bg-bg-surface-2',
        'focus:outline-hidden focus-visible:ring-1 focus-visible:ring-accent',
        GRID_COLS,
      )}
    >
      <div className="flex min-w-0 flex-col">
        <span className="truncate font-semibold text-tx-primary">
          {seriesLabel}
        </span>
        <span className="truncate text-[10.5px] text-tx-faint">
          {row.instance ?? '—'}
        </span>
      </div>
      <OriginCell row={row} />
      <span className="font-mono text-[11.5px] text-tx-faint">
        {relativeTime(row.origin?.last_seen_at ?? row.last_aired_at)}
      </span>
      <CooldownCell row={row} />
      <NoBetterCell row={row} />
      <StatusBadges row={row} />
    </div>
  );
}

// Auto-load next page when the sentinel scrolls into view.
function useInfiniteScroll(
  onIntersect: () => void,
  enabled: boolean,
): React.RefObject<HTMLDivElement | null> {
  const ref = useRef<HTMLDivElement | null>(null);
  useEffect(() => {
    const node = ref.current;
    if (!node || !enabled || typeof IntersectionObserver === 'undefined') return;
    const obs = new IntersectionObserver(
      (entries) => {
        for (const e of entries) {
          if (e.isIntersecting) {
            onIntersect();
            break;
          }
        }
      },
      { rootMargin: '200px' },
    );
    obs.observe(node);
    return () => obs.disconnect();
  }, [onIntersect, enabled]);
  return ref;
}

export function WatchdogSeasonsTable({ filters }: WatchdogSeasonsTableProps) {
  const { t, i18n } = useTranslation();
  const navigate = useNavigate();
  const location = useLocation();
  // Story E-1-B7 — forward the user's raw BCP-47 language so the
  // rollup grid renders localised series titles (queryKey-scoped).
  const query = useWatchdogSeasons(filters, undefined, i18n.resolvedLanguage ?? '');
  const items = useMemo(
    () => flattenSeasons(query.data?.pages),
    [query.data?.pages],
  );

  const openSeries = (instance: string, seriesId: number) => {
    // Story 098c will host the actual drawer at this path. Until then
    // we navigate using query params so deep-links remain stable.
    const sp = new URLSearchParams(location.search);
    sp.set('series_id', String(seriesId));
    sp.set('instance', instance);
    navigate({ pathname: location.pathname, search: `?${sp.toString()}` });
  };

  const sentinelRef = useInfiniteScroll(
    () => {
      if (query.hasNextPage && !query.isFetchingNextPage) {
        void query.fetchNextPage();
      }
    },
    Boolean(query.hasNextPage),
  );

  return (
    <Card data-testid="watchdog-seasons-table">
      <CardHeader className="flex flex-row items-center gap-3 pb-2">
        <CardTitle className="text-[15px] font-semibold">
          {t('watchdog.table.title')}
        </CardTitle>
        <Badge variant="neutral" mono className="px-2 py-0.5">
          {items.length}
        </Badge>
      </CardHeader>
      <CardContent className="p-0">
        {query.isLoading ? (
          <div className="space-y-2 p-4" data-testid="watchdog-seasons-loading">
            {Array.from({ length: 4 }).map((_, i) => (
              <Skeleton key={i} className="h-10 w-full" />
            ))}
          </div>
        ) : items.length === 0 ? (
          <div
            data-testid="watchdog-seasons-empty"
            className="px-4 py-8 text-center text-[13px] text-tx-muted"
          >
            {t('watchdog.table.emptyState')}
          </div>
        ) : (
          <div role="table" className="overflow-x-auto">
            <div className="min-w-[980px]">
              <HeaderRow />
              {items.map((r) => (
                <DataRow
                  key={`${r.instance ?? '?'}:${r.series_id ?? '?'}:${r.season_number ?? '?'}`}
                  row={r}
                  onOpen={openSeries}
                />
              ))}
              <div
                ref={sentinelRef}
                data-testid="watchdog-seasons-sentinel"
                className="h-2 w-full"
              />
              {query.isFetchingNextPage ? (
                <div className="px-4 py-3 text-center text-[12px] text-tx-faint">
                  {t('watchdog.table.loadingMore')}
                </div>
              ) : query.hasNextPage ? (
                <div className="px-4 py-3 text-center">
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() => void query.fetchNextPage()}
                    data-testid="watchdog-seasons-load-more"
                  >
                    {t('watchdog.table.loadMore')}
                  </Button>
                </div>
              ) : null}
            </div>
          </div>
        )}
      </CardContent>
    </Card>
  );
}
