import { useTranslation } from 'react-i18next';
import {
  ShieldCheck,
  Eye,
  Unplug,
  RotateCw,
  Ban,
  Clock,
} from 'lucide-react';
import type { LucideIcon } from 'lucide-react';
import { Skeleton } from '@/components/ui/skeleton';
import { cn } from '@/lib/utils';
import type { WatchdogRollupAggregate } from '@/lib/api/watchdogRollups';

interface TileProps {
  icon: LucideIcon;
  value: string;
  label: string;
  tone?: 'default' | 'run' | 'warn';
  testid?: string;
}

function Tile({ icon: Icon, value, label, tone = 'default', testid }: TileProps) {
  const iconBg =
    tone === 'run'
      ? 'bg-ok-dim text-ok'
      : tone === 'warn'
        ? 'bg-warn-dim text-warn'
        : 'bg-bg-surface-2 text-tx-muted';
  return (
    <div
      data-testid={testid}
      className={cn(
        'flex items-center gap-2.5 rounded-md border border-border-faint bg-bg-surface px-3 py-2.5',
        'min-w-[180px] flex-1',
      )}
    >
      <span
        className={cn(
          'flex h-[30px] w-[30px] items-center justify-center rounded-lg',
          iconBg,
        )}
      >
        <Icon className="h-[15px] w-[15px]" />
      </span>
      <div className="flex flex-col">
        <span className="mono text-[17px] font-bold leading-none">{value}</span>
        <span className="text-[10.5px] uppercase tracking-wide text-tx-faint">
          {label}
        </span>
      </div>
    </div>
  );
}

function formatRelative(iso: string | undefined, neverLabel: string): string {
  if (!iso) return neverLabel;
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return neverLabel;
  const diff = Math.max(0, Date.now() - t);
  const min = Math.floor(diff / 60_000);
  if (min < 1) return '<1м';
  if (min < 60) return `${min}м`;
  const h = Math.floor(min / 60);
  if (h < 24) return `${h}ч`;
  return `${Math.floor(h / 24)}д`;
}

export interface WatchdogAggregateStripProps {
  rollups?: WatchdogRollupAggregate | undefined;
  isLoading?: boolean | undefined;
}

export function WatchdogAggregateStrip({
  rollups,
  isLoading = false,
}: WatchdogAggregateStripProps) {
  const { t } = useTranslation();

  if (isLoading || !rollups) {
    return (
      <div className="mb-5 flex flex-wrap gap-2.5" data-testid="watchdog-strip-loading">
        {Array.from({ length: 6 }).map((_, i) => (
          <Skeleton key={i} className="h-[58px] min-w-[180px] flex-1 rounded-md" />
        ))}
      </div>
    );
  }

  const items = rollups.items;
  const watched = items.reduce((s, r) => s + r.watched, 0);
  const unregistered = items.reduce((s, r) => s + r.unregistered, 0);
  const regrabs7d = items.reduce((s, r) => s + r.regrabs_7d, 0);
  const blacklistSize = items.reduce((s, r) => s + r.blacklist_size, 0);

  // Last-poll = most recent across all instances; pick the latest ISO.
  const lastPollIso = items
    .map((r) => r.last_poll_at)
    .filter((v): v is string => Boolean(v))
    .sort()
    .pop();

  return (
    <div
      className="mb-5 flex flex-wrap gap-2.5"
      data-testid="watchdog-aggregate-strip"
    >
      <Tile
        icon={ShieldCheck}
        value={`${rollups.active_count} / ${rollups.total_count}`}
        label={t('watchdog.aggregate.activeInstances')}
        tone={rollups.active_count > 0 ? 'run' : 'default'}
        testid="watchdog-strip-active"
      />
      <Tile
        icon={Eye}
        value={String(watched)}
        label={t('watchdog.aggregate.watched')}
      />
      <Tile
        icon={Unplug}
        value={String(unregistered)}
        label={t('watchdog.aggregate.unregistered')}
        tone={unregistered > 0 ? 'warn' : 'default'}
      />
      <Tile
        icon={RotateCw}
        value={String(regrabs7d)}
        label={t('watchdog.aggregate.regrab7d')}
      />
      <Tile
        icon={Ban}
        value={String(blacklistSize)}
        label={t('watchdog.aggregate.blacklist')}
        tone={blacklistSize > 0 ? 'warn' : 'default'}
      />
      <Tile
        icon={Clock}
        value={formatRelative(lastPollIso, t('watchdog.aggregate.never'))}
        label={t('watchdog.aggregate.lastPoll')}
      />
    </div>
  );
}
