import { useTranslation } from 'react-i18next';
import { Pencil, RefreshCw, MoreHorizontal, WifiOff } from 'lucide-react';
import type { Instance } from '@/lib/instances';
import { useInstanceCounters } from '@/lib/counters';
import { Card } from '@/components/ui/card';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import {
  DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import { cn } from '@/lib/utils';
import { relativeTime } from '@/lib/format';
import { KIND_CLASS, KIND_DOT, healthKind, healthLabelKey } from '@/lib/badge-variants';

export interface InstanceCompactRowProps {
  readonly instance: Instance;
  readonly onEdit: (name: string) => void;
  readonly onRecheck: (name: string) => void;
  readonly onDelete: (name: string) => void;
}

/**
 * Compact instance row. One-line; degraded variant adds a 3px danger
 * border-left + dim red gradient overlay + last-error line.
 */
export function InstanceCompactRow({
  instance, onEdit, onRecheck, onDelete,
}: InstanceCompactRowProps) {
  const { t } = useTranslation();
  const name = instance.name ?? '';
  const c7 = useInstanceCounters(name, '7d');
  const degraded = healthKind(instance.health) !== 'success';
  const flips = instance.transitions_count ?? 0;
  const totals = c7.data?.totals;

  return (
    <Card
      data-testid={`instance-row-${name}`}
      className={cn(
        'flex items-center gap-4 px-[18px] py-[13px]',
        degraded && [
          'border-l-[3px] border-l-status-danger',
          'bg-[linear-gradient(90deg,var(--color-status-danger-dim),transparent_40%)]',
        ],
      )}
    >
      <div className="flex-1 min-w-0 flex flex-col gap-1.5">
        <div className="flex items-center gap-2.5">
          <h3 className="text-[16px] font-[650] tracking-tight m-0">{name}</h3>
          <span
            className={cn(
              'inline-flex items-center gap-1 px-1.5 h-[18px] rounded border font-mono text-[10.5px]',
              KIND_CLASS[healthKind(instance.health)],
            )}
            data-testid={`row-health-${name}`}
          >
            <span className={cn('inline-block w-1.5 h-1.5 rounded-full', KIND_DOT[healthKind(instance.health)])} />
            {t(healthLabelKey(instance.health))} · {relativeTime(instance.last_check_at)}
          </span>
          <Badge variant="solid" mono>{instance.mode ?? 'auto'}</Badge>
        </div>
        <div className="font-mono text-[12.5px] text-tx-muted">
          {instance.mode ?? 'auto'} · {instance.url ?? ''}
        </div>
        {degraded && instance.last_error && (
          <div data-testid={`row-error-${name}`} className="font-mono text-[12px] text-status-danger break-all">
            <WifiOff className="w-3 h-3 inline -mt-0.5 mr-1" />
            {instance.last_error}
          </div>
        )}
      </div>
      <div className="flex items-center gap-[18px] flex-none">
        <span data-testid={`row-counts-${name}`} className="font-mono tabular-nums slashed-zero text-[13px] text-tx-muted">
          {(totals?.grabs ?? 0)} / {(totals?.imports ?? 0)} / {(totals?.fails ?? 0)} · {t('instances.compact.windowSuffix')}
        </span>
        {flips > 0 && (
          <Badge variant="warn" mono data-testid={`row-flips-${name}`}>
            {t('instances.compact.degradation.flips', { count: flips })}
          </Badge>
        )}
      </div>
      <div className="flex gap-2 flex-none">
        <Button size="sm" variant="outline" onClick={() => onEdit(name)}>
          <Pencil className="w-3.5 h-3.5 mr-1.5" />
          {t('instances.hero.actions.edit')}
        </Button>
        <Button size="sm" variant="outline" onClick={() => onRecheck(name)}>
          <RefreshCw className="w-3.5 h-3.5 mr-1.5" />
          {t('instances.compact.actions.recheck')}
        </Button>
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button size="icon-btn" variant="ghost" aria-label={t('instances.compact.actions.more')}>
              <MoreHorizontal className="w-4 h-4" />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            <DropdownMenuItem onClick={() => onDelete(name)} className="text-status-danger">
              {t('common.delete')}
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
    </Card>
  );
}
