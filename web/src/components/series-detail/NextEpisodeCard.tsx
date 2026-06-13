import { CalendarClock, CircleCheck, Wrench } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { cn } from '@/lib/utils';
import { type NextEpisode, type StatusToken } from '@/api/seriesDetail';

export interface NextEpisodeCardProps {
  readonly nextEpisode?: NextEpisode | undefined;
  readonly status: StatusToken;
  readonly yearEnd?: number | undefined;
  readonly className?: string | undefined;
}

function formatAirDate(iso: string, lng: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return '';
  return new Intl.DateTimeFormat(lng, { month: 'short', day: 'numeric' }).format(d);
}

function pad(n: number): string {
  return n.toString().padStart(2, '0');
}

export function NextEpisodeCard({ nextEpisode, status, yearEnd, className }: NextEpisodeCardProps) {
  const { t, i18n } = useTranslation();
  const lng = i18n.resolvedLanguage ?? 'en';

  if (nextEpisode && nextEpisode.air_date && nextEpisode.season_number !== undefined) {
    const code = `S${pad(nextEpisode.season_number)}E${pad(nextEpisode.episode_number ?? 0)}`;
    return (
      <div
        data-testid="next-episode-card"
        data-variant="next"
        className={cn(
          'flex flex-col gap-1 rounded-lg border border-border-faint bg-bg-surface/60 px-4 py-3',
          className,
        )}
      >
        <div className="flex items-center gap-1.5 text-[10.5px] font-bold uppercase tracking-wide text-info">
          <CalendarClock className="w-3 h-3" aria-hidden="true" />
          {t('seriesDetail.next.label')}
        </div>
        <div className="text-[13.5px] text-tx-primary">
          <span className="mono font-semibold">{code}</span>
          {nextEpisode.title && <span className="text-tx-secondary"> · {nextEpisode.title}</span>}
          <span className="text-tx-muted"> · {formatAirDate(nextEpisode.air_date, lng)}</span>
        </div>
      </div>
    );
  }

  if (status === 'ended' || status === 'canceled') {
    return (
      <div
        data-testid="next-episode-card"
        data-variant="ended"
        className={cn(
          'flex flex-col gap-1 rounded-lg border border-border-faint bg-bg-surface/60 px-4 py-3',
          className,
        )}
      >
        <div className="flex items-center gap-1.5 text-[10.5px] font-bold uppercase tracking-wide text-tx-muted">
          <CircleCheck className="w-3 h-3" aria-hidden="true" />
          {t('seriesDetail.next.ended')}
        </div>
        <div className="text-[13.5px] text-tx-secondary">
          {yearEnd ? t('seriesDetail.next.endedYear', { year: yearEnd }) : t('seriesDetail.next.endedUnknown')}
        </div>
      </div>
    );
  }

  if (status === 'in_production' || status === 'upcoming') {
    return (
      <div
        data-testid="next-episode-card"
        data-variant="production"
        className={cn(
          'flex flex-col gap-1 rounded-lg border border-border-faint bg-bg-surface/60 px-4 py-3',
          className,
        )}
      >
        <div className="flex items-center gap-1.5 text-[10.5px] font-bold uppercase tracking-wide text-info">
          <Wrench className="w-3 h-3" aria-hidden="true" />
          {t('seriesDetail.next.production')}
        </div>
        <div className="text-[13.5px] text-tx-muted">{t('seriesDetail.next.productionBody')}</div>
      </div>
    );
  }

  // Continuing / unknown without scheduled next: quiet line.
  return (
    <div
      data-testid="next-episode-card"
      data-variant="unscheduled"
      className={cn(
        'flex flex-col gap-1 rounded-lg border border-border-faint bg-bg-surface/60 px-4 py-3',
        className,
      )}
    >
      <div className="flex items-center gap-1.5 text-[10.5px] font-bold uppercase tracking-wide text-tx-muted">
        <CalendarClock className="w-3 h-3" aria-hidden="true" />
        {t('seriesDetail.next.label')}
      </div>
      <div className="text-[13.5px] text-tx-muted">{t('seriesDetail.next.unscheduled')}</div>
    </div>
  );
}
