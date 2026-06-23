import { useTranslation } from 'react-i18next';
import { cn } from '@/lib/utils';
import { relativeTime } from '@/lib/format';
import type { RecentEvent } from '@/api/series';

export interface RecentStripProps {
  readonly recent?: readonly RecentEvent[] | undefined;
  readonly className?: string | undefined;
}

function eventDotColor(type: string): string {
  switch (type) {
    case 'imported': return 'bg-ok';
    case 'grabbed': return 'bg-info';
    case 'failed': return 'bg-danger';
    default: return 'bg-tx-faint';
  }
}

export function RecentStrip({ recent, className }: RecentStripProps) {
  const { t } = useTranslation();
  const events = (recent ?? []).slice(0, 3);
  if (events.length === 0) return null;

  return (
    <section
      data-testid="recent-strip"
      className={cn(
        'flex flex-wrap items-center gap-x-3 gap-y-1.5 text-[12px]',
        className,
      )}
    >
      <span className="text-[10px] font-semibold uppercase tracking-[0.1em] text-tx-faint">
        {t('seriesDetail.library.recent')}
      </span>
      {events.map((ev, i) => (
        <span
          key={`${ev.event_type ?? ''}-${ev.at ?? ''}-${i}`}
          data-testid="recent-strip-event"
          data-event-type={ev.event_type ?? 'unknown'}
          className="inline-flex items-center gap-1.5 text-tx-muted"
        >
          <span
            aria-hidden="true"
            className={cn('w-1.5 h-1.5 rounded-full', eventDotColor(ev.event_type ?? ''))}
          />
          <span className="text-tx-secondary">
            {t(`seriesDetail.library.event.${ev.event_type ?? 'unknown'}` as 'seriesDetail.library.event.imported',
               { defaultValue: ev.event_type ?? '' })}
          </span>
          {ev.subject && <span className="font-mono text-tx-muted">{ev.subject}</span>}
          {ev.at && <span className="text-tx-faint">· {relativeTime(ev.at)}</span>}
        </span>
      ))}
    </section>
  );
}
