import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import { ArrowLeft, RefreshCw } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';
import { relativeTime } from '@/lib/format';

export interface QueueHeaderProps {
  readonly name: string;
  readonly mode: 'auto' | 'manual';
  readonly updatedAtMs: number;
  readonly isFetching: boolean;
  readonly onRefresh: () => void;
}

export function QueueHeader({
  name, mode, updatedAtMs, isFetching, onRefresh,
}: QueueHeaderProps) {
  const { t } = useTranslation();
  // reason: relative-time label is intentionally render-time. Staleness
  // self-corrects on the next react-query refetch tick (parent invalidates
  // `missing` query, re-renders, label re-computes). Migrating to a
  // `useNow()` subscription is out of scope for this story.
  // eslint-disable-next-line react-hooks/purity
  const updatedAge = Date.now() - updatedAtMs;
  const updatedLabel = updatedAge < 30_000
    ? t('instanceQueue.updatedJust')
    : t('instanceQueue.updatedAgo', { time: relativeTime(updatedAtMs) });
  return (
    <header className="flex items-center gap-3 flex-wrap mb-[18px]" data-testid="queue-header">
      <Link
        to="/instances"
        className="inline-flex items-center gap-1.5 text-[13px] text-muted hover:text-foreground"
      >
        <ArrowLeft className="w-[15px] h-[15px]" aria-hidden="true" />
        {t('instanceQueue.back')}
      </Link>
      <h2 className="text-[16px] font-[650] m-0 font-mono">{name}</h2>
      <span
        className={cn(
          'inline-flex items-center gap-1.5 rounded-full border px-2 py-0.5 text-[11px] font-semibold',
          mode === 'auto'
            ? 'bg-ok-dim border-ok/40 text-ok'
            : 'bg-warn-dim border-warn/40 text-warn',
        )}
        data-testid="queue-mode-chip"
      >
        <span
          className={cn(
            'w-1.5 h-1.5 rounded-full',
            mode === 'auto' ? 'bg-ok' : 'bg-warn',
          )}
          aria-hidden="true"
        />
        {t('instanceQueue.modeChip', { mode })}
      </span>
      <span className="flex-1" />
      <span className="text-[12px] text-faint">{updatedLabel}</span>
      <Button
        variant="outline"
        size="sm"
        className="h-7"
        onClick={onRefresh}
        disabled={isFetching}
        aria-label={t('instanceQueue.refreshAria')}
      >
        <RefreshCw
          className={cn('w-3.5 h-3.5 mr-1', isFetching && 'animate-spin')}
          aria-hidden="true"
        />
        {t('instanceQueue.refresh')}
      </Button>
    </header>
  );
}
