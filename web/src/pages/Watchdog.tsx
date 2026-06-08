import { useCallback } from 'react';
import { useNavigate } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { useWatchdogRollups } from '@/lib/api/watchdogRollups';
import { WatchdogAggregateStrip } from '@/components/watchdog/WatchdogAggregateStrip';
import { WatchdogActivityFeed } from '@/components/watchdog/WatchdogActivityFeed';
import { WatchdogInstancePanel } from '@/components/watchdog/WatchdogInstancePanel';
import { WatchdogBlacklistTable } from '@/components/watchdog/WatchdogBlacklistTable';
import { WatchdogNotConfiguredEmpty } from '@/components/watchdog/WatchdogNotConfiguredEmpty';
import { Skeleton } from '@/components/ui/skeleton';
import { useSetPageTitle } from '@/components/shell/page-title-context';

export function Watchdog() {
  const { t } = useTranslation();
  useSetPageTitle(t('watchdog.title'));
  const rollups = useWatchdogRollups();

  // Primary "current" instance for the activity feed = first active
  // instance, else first by name. The future F2 instance switcher in
  // the topbar will override this once it ships; for tonight we pick
  // the natural default.
  const navigate = useNavigate();
  const items = rollups.data?.items ?? [];
  const primary = items.find((r) => r.active) ?? items[0] ?? null;

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
          />

          <div
            className="mb-5 grid gap-5 items-start [grid-template-columns:1fr_320px] max-[1080px]:[grid-template-columns:1fr]"
            data-testid="watchdog-grid"
          >
            <div>
              {primary ? (
                <WatchdogActivityFeed
                  instance={primary.instance}
                  maxNoBetter={primary.max_no_better}
                />
              ) : (
                <Skeleton className="h-[400px] w-full" />
              )}
            </div>
            <div className="flex flex-col gap-3.5">
              {rollups.isLoading
                ? Array.from({ length: 2 }).map((_, i) => (
                    <Skeleton key={i} className="h-[220px] w-full" />
                  ))
                : items.map((r) => (
                    <WatchdogInstancePanel
                      key={r.instance}
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
                  key={r.instance}
                  instance={r.instance}
                  maxNoBetter={r.max_no_better}
                />
              ))}
          </div>
        </>
      )}
    </div>
  );
}
