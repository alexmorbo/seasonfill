import { useTranslation } from 'react-i18next';
import { Ban, Bot, Hand } from 'lucide-react';
import { Badge } from '@/components/ui/badge';
import { Skeleton } from '@/components/ui/skeleton';
import { cn } from '@/lib/utils';
import { relativeTime } from '@/lib/format';
import {
  useWatchdogBlacklist, type WatchdogBlacklistItem,
} from '@/lib/api/watchdogBlacklist';
import { UnBlacklistButton } from './UnBlacklistButton';

export interface WatchdogBlacklistTableProps {
  instance: string;
  // For "auto · no better {n}/{max}"; falls back to `?` so the table
  // does not need a rollup fetch.
  maxNoBetter?: number;
}

const GRID_COLS = '[grid-template-columns:1fr_70px_1fr_130px_110px]';

function ReasonCell({
  item, maxNoBetter,
}: { item: WatchdogBlacklistItem; maxNoBetter: number | undefined }) {
  const { t } = useTranslation();
  if (item.source === 'manual') {
    return (
      <Badge variant="neutral" mono className="px-2 py-0.5 text-[10.5px]">
        <Hand className="h-3 w-3" />
        {t('watchdog.blacklist.reason.manual')}
      </Badge>
    );
  }
  return (
    <Badge variant="warn" mono className="px-2 py-0.5 text-[10.5px]">
      <Bot className="h-3 w-3" />
      {t('watchdog.blacklist.reason.auto', {
        n: item.consecutive, max: maxNoBetter ?? '?',
      })}
    </Badge>
  );
}

function HeaderRow() {
  const { t } = useTranslation();
  return (
    <div
      role="row"
      data-testid="watchdog-blacklist-header"
      className={cn(
        'grid gap-3 border-b border-border-subtle px-4 py-2.5',
        GRID_COLS,
        'text-[10px] font-semibold uppercase tracking-[0.09em] text-tx-faint',
      )}
    >
      <span>{t('watchdog.blacklist.col.series')}</span>
      <span>{t('watchdog.blacklist.col.season')}</span>
      <span>{t('watchdog.blacklist.col.reason')}</span>
      <span>{t('watchdog.blacklist.col.when')}</span>
      <span>{t('watchdog.blacklist.col.action')}</span>
    </div>
  );
}

function DataRow({
  item, instance, maxNoBetter,
}: { item: WatchdogBlacklistItem; instance: string; maxNoBetter: number | undefined }) {
  return (
    <div
      role="row"
      data-testid={`watchdog-blacklist-row-${item.id}`}
      className={cn(
        'grid items-center gap-3 border-b border-border-faint px-4 py-2.5',
        'text-[13px] last:border-b-0',
        GRID_COLS,
      )}
    >
      <span className="truncate font-semibold text-tx-primary">
        {item.series_title || `#${item.series_id}`}
      </span>
      <span className="font-mono text-tx-muted">
        S{String(item.season_number).padStart(2, '0')}
      </span>
      <span><ReasonCell item={item} maxNoBetter={maxNoBetter} /></span>
      <span className="font-mono text-[11.5px] text-tx-faint">
        {relativeTime(item.created_at)}
      </span>
      <span>
        <UnBlacklistButton
          instance={instance}
          id={item.id}
          seriesTitle={item.series_title || `#${item.series_id}`}
          seasonNumber={item.season_number}
        />
      </span>
    </div>
  );
}

export function WatchdogBlacklistTable({
  instance, maxNoBetter,
}: WatchdogBlacklistTableProps) {
  const { t } = useTranslation();
  const q = useWatchdogBlacklist(instance);
  const items = q.data?.items ?? [];

  return (
    <section
      data-testid={`watchdog-blacklist-${instance}`}
      className="rounded-md border border-border-faint bg-bg-surface"
    >
      <header className="flex items-center gap-2.5 border-b border-border-faint px-4 py-3">
        <Ban className="h-4 w-4 text-warn" aria-hidden />
        <h3 className="text-[15px] font-semibold leading-none">
          {t('watchdog.blacklist.title')}
        </h3>
        <span className="text-[12px] text-tx-faint">
          {t('watchdog.blacklist.subtitle')}
        </span>
        <span className="grow" />
        <Badge variant="warn" mono className="px-2 py-0.5">{items.length}</Badge>
      </header>

      {q.isLoading ? (
        <div className="p-4" data-testid="watchdog-blacklist-loading">
          <Skeleton className="h-[100px] w-full" />
        </div>
      ) : items.length === 0 ? (
        <div
          data-testid="watchdog-blacklist-empty"
          className="px-4 py-6 text-center text-[13px] text-tx-muted"
        >
          {t('watchdog.blacklist.empty')}
        </div>
      ) : (
        <div role="table">
          <HeaderRow />
          {items.map((it) => (
            <DataRow key={it.id} item={it}
              instance={instance} maxNoBetter={maxNoBetter} />
          ))}
        </div>
      )}
    </section>
  );
}
