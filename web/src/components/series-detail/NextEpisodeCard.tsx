import { Flag, Hammer } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { cn } from '@/lib/utils';
import { useFormatDate } from '@/lib/timezone';
import { type NextEpisode, type StatusToken } from '@/api/seriesDetail';
import { IpCdBadge } from './IpCdBadge';

export interface NextEpisodeCardProps {
  readonly nextEpisode?: NextEpisode | undefined;
  readonly status: StatusToken;
  readonly yearEnd?: number | undefined;
  /** "glass" — frosted-glass over hero scrim (default v2).
   *  "panel" — bordered surface (legacy / standalone). */
  readonly variant?: 'glass' | 'panel';
  readonly className?: string | undefined;
}

function pad(n: number): string {
  return n.toString().padStart(2, '0');
}

function daysUntil(iso: string): number {
  const ms = new Date(iso).getTime() - Date.now();
  return Math.max(0, Math.ceil(ms / 86_400_000));
}

function shellClass(variant: 'glass' | 'panel'): string {
  if (variant === 'glass') {
    // Frosted glass over the hero scrim. Inline style because Tailwind
    // doesn't ship arbitrary backdrop-filter blurs at this density without
    // a custom plugin — keep the magic numbers in one place.
    return cn(
      'inline-flex items-center gap-3 rounded-lg px-3.5 py-2.5',
      'w-[330px] max-w-[34vw] text-white',
      'border border-white/[0.14] shadow-[0_12px_34px_oklch(0_0_0/.42)]',
      '[background:oklch(0.15_0.006_270/.60)]',
      '[backdrop-filter:blur(12px)] [-webkit-backdrop-filter:blur(12px)]',
    );
  }
  return cn(
    'inline-flex items-center gap-3 rounded-lg px-4 py-3',
    'border border-border-faint bg-bg-surface/60',
  );
}

export function NextEpisodeCard({
  nextEpisode, status, yearEnd, variant = 'glass', className,
}: NextEpisodeCardProps) {
  const { t } = useTranslation();
  const fmt = useFormatDate();

  // Continuing with a scheduled next episode → counter-badge.
  if (nextEpisode?.air_date && nextEpisode.season_number !== undefined) {
    const days = daysUntil(nextEpisode.air_date);
    const code = `S${pad(nextEpisode.season_number)}E${pad(nextEpisode.episode_number ?? 0)}`;
    return (
      <div
        data-testid="next-episode-card"
        data-variant="default"
        className={cn(shellClass(variant), className)}
      >
        <IpCdBadge
          digit={days}
          unit={t('seriesDetail.next.unitDays', { count: days })}
        />
        <div className="flex flex-col gap-[3px] min-w-0">
          <span className={cn(
            'text-[10px] font-semibold uppercase tracking-[0.09em]',
            variant === 'glass' ? 'text-white/55' : 'text-tx-faint',
          )}>
            {t('seriesDetail.next.label')}
          </span>
          <span className={cn(
            'text-[13.5px] truncate',
            variant === 'glass' ? 'text-white' : 'text-tx-primary',
          )}>
            <span className="font-mono font-semibold text-accent">{code}</span>
            {nextEpisode.title && (
              <span className={variant === 'glass' ? 'text-white/85' : 'text-tx-secondary'}>
                {' '}«{nextEpisode.title}»
              </span>
            )}
          </span>
          <span className={cn(
            'text-[11.5px] tabular-nums',
            variant === 'glass' ? 'text-white/70' : 'text-tx-muted',
          )}>
            {fmt(nextEpisode.air_date, 'monthDay')}
          </span>
        </div>
      </div>
    );
  }

  // Ended series → flag badge.
  if (status === 'ended' || status === 'canceled') {
    return (
      <div
        data-testid="next-episode-card"
        data-variant="ended"
        className={cn(shellClass(variant), className)}
      >
        <IpCdBadge icon={<Flag className="w-[18px] h-[18px]" aria-hidden="true" />} />
        <div className="flex flex-col gap-[3px] min-w-0">
          <span className={cn(
            'text-[10px] font-semibold uppercase tracking-[0.09em]',
            variant === 'glass' ? 'text-white/55' : 'text-tx-faint',
          )}>
            {t('seriesDetail.next.ended')}
          </span>
          <span className={cn(
            'text-[13.5px]',
            variant === 'glass' ? 'text-white' : 'text-tx-primary',
          )}>
            {t('seriesDetail.next.endedLastAir')}
          </span>
          <span className={cn(
            'text-[11.5px] tabular-nums',
            variant === 'glass' ? 'text-white/70' : 'text-tx-muted',
          )}>
            {yearEnd ? t('seriesDetail.next.endedYear', { year: yearEnd }) : t('seriesDetail.next.endedUnknown')}
          </span>
        </div>
      </div>
    );
  }

  // In production / upcoming → hammer badge.
  if (status === 'in_production' || status === 'upcoming') {
    return (
      <div
        data-testid="next-episode-card"
        data-variant="production"
        className={cn(shellClass(variant), className)}
      >
        <IpCdBadge icon={<Hammer className="w-[17px] h-[17px]" aria-hidden="true" />} />
        <div className="flex flex-col gap-[3px] min-w-0">
          <span className={cn(
            'text-[10px] font-semibold uppercase tracking-[0.09em]',
            variant === 'glass' ? 'text-white/55' : 'text-tx-faint',
          )}>
            {t('seriesDetail.next.production')}
          </span>
          <span className={cn(
            'text-[13.5px]',
            variant === 'glass' ? 'text-white' : 'text-tx-primary',
          )}>
            {t('seriesDetail.next.productionBody')}
          </span>
          <span className={cn(
            'text-[11.5px]',
            variant === 'glass' ? 'text-white/70' : 'text-tx-muted',
          )}>
            {t('seriesDetail.next.tba')}
          </span>
        </div>
      </div>
    );
  }

  // Continuing without a scheduled next → don't render in hero (per
  // v2 handoff: no card when there's nothing to say). Story 358 may
  // still render the row in the legacy panel variant for the right rail.
  return null;
}
