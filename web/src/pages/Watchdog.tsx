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

export function Watchdog() {
  const { t } = useTranslation();
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

  const total = items.length;
  const active = items.reduce(
    (n, r) => n + (r.enabled && r.qbit_reachable ? 1 : 0),
    0,
  );
  const showEmpty =
    !rollups.isLoading &&
    Boolean(rollups.data) &&
    (total === 0 || (active === 0 && total > 0));

  return (
    <div className="px-6 pb-10 pt-5" data-testid="watchdog-page">
      <h1 className="mb-4 text-[20px] font-semibold">
        {t('watchdog.title')}
      </h1>

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
