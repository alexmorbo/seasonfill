import { useTranslation } from 'react-i18next';
import { cn } from '@/lib/utils';

export interface QueueStatsStripProps {
  readonly backlogCount: number;
  readonly missingEpisodes: number;
  readonly monitoredTotal: number | undefined;
}

interface StatProps {
  readonly value: number;
  readonly label: string;
  readonly warn?: boolean;
}

function Stat({ value, label, warn = false }: StatProps) {
  return (
    <div className="flex flex-col gap-0.5 rounded-md border border-border-faint bg-surface px-4 py-2.5">
      <span
        className={cn(
          'font-mono tabular-nums text-[22px] font-bold leading-none',
          warn && 'text-warn',
        )}
      >
        {value.toLocaleString()}
      </span>
      <span className="text-[10.5px] uppercase tracking-wider text-faint">
        {label}
      </span>
    </div>
  );
}

export function QueueStatsStrip({
  backlogCount, missingEpisodes, monitoredTotal,
}: QueueStatsStripProps) {
  const { t } = useTranslation();
  return (
    <div className="flex gap-2.5 flex-wrap mb-[18px]" data-testid="queue-stats">
      <Stat value={backlogCount} label={t('instanceQueue.stats.backlog')} />
      <Stat
        value={missingEpisodes}
        label={t('instanceQueue.stats.missing')}
        warn={missingEpisodes > 0}
      />
      {monitoredTotal !== undefined && (
        <Stat value={monitoredTotal} label={t('instanceQueue.stats.monitored')} />
      )}
    </div>
  );
}
