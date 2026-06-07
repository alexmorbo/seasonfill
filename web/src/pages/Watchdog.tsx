import { useTranslation } from 'react-i18next';
import { useWatchdogRollups } from '@/lib/api/watchdogRollups';
import { WatchdogAggregateStrip } from '@/components/watchdog/WatchdogAggregateStrip';
import { WatchdogActivityFeed } from '@/components/watchdog/WatchdogActivityFeed';
import { WatchdogInstancePanel } from '@/components/watchdog/WatchdogInstancePanel';
import { Skeleton } from '@/components/ui/skeleton';

export function Watchdog() {
  const { t } = useTranslation();
  const rollups = useWatchdogRollups();

  // Primary "current" instance for the activity feed = first active
  // instance, else first by name. The future F2 instance switcher in
  // the topbar will override this once it ships; for tonight we pick
  // the natural default.
  const items = rollups.data?.items ?? [];
  const primary =
    items.find((r) => r.active) ?? items[0] ?? null;

  return (
    <div className="px-6 pb-10 pt-5" data-testid="watchdog-page">
      <h1 className="mb-4 text-[20px] font-semibold">
        {t('watchdog.title')}
      </h1>

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
                <WatchdogInstancePanel key={r.instance} rollup={r} />
              ))}
        </div>
      </div>

      {/* 052b replaces this slot with <WatchdogBlacklistTable /> */}
      <section data-testid="watchdog-blacklist-slot">
        <Skeleton className="h-[180px] w-full" />
      </section>
    </div>
  );
}
