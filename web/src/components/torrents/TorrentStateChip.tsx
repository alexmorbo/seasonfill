import { ArrowDown, ArrowUp, Ban, CircleAlert, CircleDashed, Loader2, Pause, Trash2 } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { cn } from '@/lib/utils';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import type { StateGroup } from '@/api/seriesTorrents';

export interface TorrentStateChipProps {
  readonly group: StateGroup | string | undefined;
  readonly rawState?: string | undefined;
  // deleted swaps the chip for a `Trash2 · Deleted` tag. The
  // caller passes `deleted=true` when row.present===false.
  readonly deleted?: boolean | undefined;
  readonly deletedAt?: string | undefined;
  readonly className?: string | undefined;
}

interface Style { readonly icon: typeof ArrowDown; readonly cls: string; }

// Token palette per CD handoff Q10. Each group maps to one
// (background, text, border) triplet. Stick to the app's existing
// chip tokens — do not invent new colours here.
const STYLES: Record<StateGroup, Style> = {
  downloading: { icon: ArrowDown, cls: 'bg-info-dim text-info' },
  seeding:     { icon: ArrowUp,   cls: 'bg-ok-dim text-ok' },
  stalled:     { icon: CircleAlert,  cls: 'bg-transparent text-warn border border-warn/45' },
  queued:      { icon: CircleDashed, cls: 'bg-transparent text-tx-muted border border-tx-muted/45' },
  paused:      { icon: Pause,     cls: 'bg-bg-surface-2 text-tx-secondary' },
  checking:    { icon: Loader2,   cls: 'bg-[oklch(0.20_0.06_300)] text-[oklch(0.85_0.13_300)]' },
  error:       { icon: Ban,       cls: 'bg-danger-dim text-danger' },
  unknown:     { icon: CircleDashed, cls: 'bg-transparent text-tx-faint border border-dashed border-tx-faint/45' },
};

function normalize(g: string | undefined): StateGroup {
  if (!g) return 'unknown';
  if (g in STYLES) return g as StateGroup;
  return 'unknown';
}

export function TorrentStateChip({ group, rawState, deleted, deletedAt, className }: TorrentStateChipProps) {
  const { t, i18n } = useTranslation();
  if (deleted) {
    const when = deletedAt
      ? new Date(deletedAt).toLocaleDateString(i18n.resolvedLanguage, { month: 'short', day: 'numeric' })
      : '';
    return (
      <span
        data-testid="torrent-state-chip"
        data-state="deleted"
        className={cn(
          'inline-flex items-center gap-1 rounded-full bg-bg-surface-2 text-tx-faint',
          'px-2 py-0.5 text-[11px] font-medium',
          className,
        )}
      >
        <Trash2 className="w-3 h-3" aria-hidden="true" />
        <span>{when ? t('seriesDetail.torrents.state.deletedOn', { date: when }) : t('seriesDetail.torrents.state.deleted')}</span>
      </span>
    );
  }
  const key = normalize(group as string | undefined);
  const { icon: Icon, cls } = STYLES[key];
  const label = t(`seriesDetail.torrents.state.${key}`);
  const tooltip = rawState && rawState !== key ? rawState : label;
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span
          data-testid="torrent-state-chip"
          data-state={key}
          className={cn(
            'inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-[11px] font-medium',
            'tabular-nums',
            cls,
            className,
          )}
        >
          <Icon className={cn('w-3 h-3', key === 'checking' && 'animate-spin')} aria-hidden="true" />
          <span>{label}</span>
        </span>
      </TooltipTrigger>
      <TooltipContent>{tooltip}</TooltipContent>
    </Tooltip>
  );
}
