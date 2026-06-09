import { useCallback, useMemo, useState } from 'react';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { useWatchdogRollups } from '@/lib/api/watchdogRollups';
import { useWatchdogSeasonsTotals } from '@/lib/api/watchdogSeasons';
import type { WatchdogSeasonsFilters } from '@/lib/api/watchdogSeasons';
import { WatchdogAggregateStrip } from '@/components/watchdog/WatchdogAggregateStrip';
import { WatchdogActivityFeed } from '@/components/watchdog/WatchdogActivityFeed';
import { WatchdogInstancePanel } from '@/components/watchdog/WatchdogInstancePanel';
import { WatchdogBlacklistTable } from '@/components/watchdog/WatchdogBlacklistTable';
import { WatchdogNotConfiguredEmpty } from '@/components/watchdog/WatchdogNotConfiguredEmpty';
import { WatchdogSeasonsFilters as SeasonsFilters } from '@/components/watchdog/WatchdogSeasonsFilters';
import { WatchdogSeasonsTable } from '@/components/watchdog/WatchdogSeasonsTable';
import { WatchdogSeriesDrawer } from '@/components/watchdog/WatchdogSeriesDrawer';
import { Skeleton } from '@/components/ui/skeleton';
import { useSetPageTitle } from '@/components/shell/page-title-context';

export function Watchdog() {
  const { t } = useTranslation();
  useSetPageTitle(t('watchdog.title'));
  const rollups = useWatchdogRollups();
  const totals = useWatchdogSeasonsTotals();

  // Primary "current" instance for the activity feed = first active
  // instance, else first by name. The future F2 instance switcher in
  // the topbar will override this once it ships; for tonight we pick
  // the natural default.
  const navigate = useNavigate();
  const items = useMemo(() => rollups.data?.items ?? [], [rollups.data?.items]);
  const primary = items.find((r) => r.active) ?? items[0] ?? null;
  const instanceNames = useMemo(
    () => items.map((r) => r.instance_name).filter(Boolean),
    [items],
  );

  // Local filter state for the new seasons table. Persisting these in
  // the URL is a follow-up — for now we keep the page reload-safe by
  // defaulting to "all" / "no filters".
  const [filters, setFilters] = useState<WatchdogSeasonsFilters>({
    instance: null,
    q: '',
    cooldownOnly: false,
    blacklistedOnly: false,
  });

  // Drill-down drawer (Story 098c) is driven by URL params written by
  // WatchdogSeasonsTable row clicks. Keep state in the URL so deep
  // links + back/forward remain stable.
  const [searchParams, setSearchParams] = useSearchParams();
  const seriesIDRaw = searchParams.get('series_id');
  const drawerInstance = searchParams.get('instance');
  const drawerSeriesID = seriesIDRaw ? Number(seriesIDRaw) : null;
  const drawerSeriesIDValid =
    drawerSeriesID !== null && Number.isFinite(drawerSeriesID)
      ? drawerSeriesID
      : null;

  const onDrawerOpenChange = useCallback(
    (open: boolean) => {
      if (open) return;
      const next = new URLSearchParams(searchParams);
      next.delete('series_id');
      next.delete('instance');
      setSearchParams(next, { replace: true });
    },
    [searchParams, setSearchParams],
  );

  const openInstanceForm = useCallback(
    (name: string) => {
      navigate(`/instances?edit=${encodeURIComponent(name)}`);
    },
    [navigate],
  );

  // "Configured" = at least one Sonarr instance is present in the
  // rollups list. The runtime state (enabled / qbit_reachable /
  // last_poll_at) is surfaced by the panel + strip; we do NOT gate
  // the page on it. A configured instance with watchdog off, an
  // unreachable qBit, or a not-yet-stamped first poll all render
  // the panel — operators need to see the toggle and the status
  // chip to fix or flip those states.
  const showEmpty = !rollups.isLoading && Boolean(rollups.data) && items.length === 0;

  return (
    <div data-testid="watchdog-page">
      {showEmpty ? (
        <WatchdogNotConfiguredEmpty />
      ) : (
        <>
          <WatchdogAggregateStrip
            rollups={rollups.data}
            isLoading={rollups.isLoading}
            totals={totals.data}
          />

          <div
            className="mb-5 grid gap-5 items-start [grid-template-columns:1fr_320px] max-[1080px]:[grid-template-columns:1fr]"
            data-testid="watchdog-grid"
          >
            <div className="flex flex-col gap-3.5">
              {primary ? (
                <WatchdogActivityFeed
                  instance={primary.instance_name}
                  maxNoBetter={primary.no_better_max}
                />
              ) : (
                <Skeleton className="h-[400px] w-full" />
              )}

              <section data-testid="watchdog-seasons-section">
                <SeasonsFilters
                  filters={filters}
                  instances={instanceNames}
                  onChange={setFilters}
                />
                <WatchdogSeasonsTable filters={filters} />
              </section>
            </div>
            <div className="flex flex-col gap-3.5">
              {rollups.isLoading
                ? Array.from({ length: 2 }).map((_, i) => (
                    <Skeleton key={i} className="h-[220px] w-full" />
                  ))
                : items.map((r) => (
                    <WatchdogInstancePanel
                      key={r.instance_name}
                      rollup={r}
                      onOpenInstanceForm={openInstanceForm}
                    />
                  ))}
            </div>
          </div>

          <div className="flex flex-col gap-3.5" data-testid="watchdog-blacklist-slot">
            {items
              .filter((r) => r.enabled)
              .map((r) => (
                <WatchdogBlacklistTable
                  key={r.instance_name}
                  instance={r.instance_name}
                  maxNoBetter={r.no_better_max}
                />
              ))}
          </div>

          <WatchdogSeriesDrawer
            seriesID={drawerSeriesIDValid}
            instance={drawerInstance}
            onOpenChange={onDrawerOpenChange}
          />
        </>
      )}
    </div>
  );
}
