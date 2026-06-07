import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import {
  Ban,
  Check,
  RotateCw,
  TriangleAlert,
  Unplug,
  GitBranch,
  Radar,
} from 'lucide-react';
import type { LucideIcon } from 'lucide-react';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Badge } from '@/components/ui/badge';
import { Skeleton } from '@/components/ui/skeleton';
import { cn } from '@/lib/utils';
import {
  useWatchdogActivity,
  type WatchdogActivityEvent,
  type WatchdogActivityType,
} from '@/lib/api/watchdogActivity';

const TYPE_ICON: Record<WatchdogActivityType, LucideIcon> = {
  unregistered: Unplug,
  regrab: RotateCw,
  better: Check,
  no_better: TriangleAlert,
  blacklist: Ban,
};

const TYPE_VARIANT: Record<
  WatchdogActivityType,
  'danger' | 'accent' | 'ok' | 'warn'
> = {
  unregistered: 'danger',
  regrab: 'accent',
  better: 'ok',
  no_better: 'warn',
  blacklist: 'danger',
};

function formatRowTime(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  const sameDay =
    d.toDateString() === new Date().toDateString()
      ? d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' })
      : null;
  if (sameDay) return sameDay;
  const y = new Date(Date.now() - 24 * 60 * 60 * 1000);
  if (d.toDateString() === y.toDateString()) {
    return `вчера ${d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' })}`;
  }
  return d.toLocaleDateString();
}

function EventRow({ ev }: { ev: WatchdogActivityEvent }) {
  const { t } = useTranslation();
  const Icon = TYPE_ICON[ev.type];
  const variant = TYPE_VARIANT[ev.type];
  const seriesLabel = `${ev.series_title} · S${String(ev.season_number).padStart(2, '0')}`;
  const detail = (() => {
    switch (ev.detail_key) {
      case 'regrabFound':
        return t('watchdog.activity.detail.regrabFound', {
          n: 1,
          episodes: ev.episodes ?? '?',
        });
      case 'regrabStarted':
        return t('watchdog.activity.detail.regrabStarted');
      case 'noBetter':
        return t('watchdog.activity.detail.noBetter', {
          x: ev.consecutive ?? '?',
          max: ev.max_no_better ?? 3,
        });
      case 'blacklistConsec':
        return t('watchdog.activity.detail.blacklistConsec', {
          x: ev.consecutive ?? '?',
          max: ev.max_no_better ?? 3,
        });
      case 'unregistered':
        return ''; // event chip alone is enough
    }
  })();

  const actionTo =
    ev.type === 'regrab' || ev.type === 'unregistered'
      ? `/scans?instance=${encodeURIComponent(ev.instance)}`
      : `/grabs?instance=${encodeURIComponent(ev.instance)}&series=${ev.series_id}`;

  return (
    <div
      data-testid={`feed-row-${ev.type}`}
      className="flex items-start gap-3 border-b border-border-faint px-4 py-3 last:border-b-0"
    >
      <span className="mono w-[80px] flex-none pt-0.5 text-[11px] text-tx-faint">
        {formatRowTime(ev.at)}
      </span>
      <Badge variant={variant} className="flex-none gap-1 px-2 py-0.5 text-[11px]">
        <Icon className="h-3 w-3" />
        {t(`watchdog.activity.event.${camelType(ev.type)}`)}
      </Badge>
      <span className="min-w-0 flex-1 text-[13px] text-tx-secondary">
        <b className="font-semibold text-tx-primary">{seriesLabel}</b>
        {detail ? ` — ${detail}` : null}
      </span>
      <Link
        to={actionTo}
        className={cn(
          'flex-none rounded-sm border border-border-subtle px-2 py-0.5 text-[11.5px] text-tx-muted',
          'inline-flex items-center gap-1 hover:border-accent hover:text-accent',
        )}
      >
        {ev.type === 'regrab' || ev.type === 'unregistered' ? (
          <Radar className="h-3 w-3" />
        ) : (
          <GitBranch className="h-3 w-3" />
        )}
        {ev.type === 'regrab' || ev.type === 'unregistered'
          ? t('watchdog.activity.action.scan')
          : t('watchdog.activity.action.chain')}
      </Link>
    </div>
  );
}

function camelType(t: WatchdogActivityType): string {
  switch (t) {
    case 'no_better':
      return 'noBetter';
    default:
      return t;
  }
}

export interface WatchdogActivityFeedProps {
  instance: string;
  maxNoBetter?: number;
}

export function WatchdogActivityFeed({
  instance,
  maxNoBetter,
}: WatchdogActivityFeedProps) {
  const { t } = useTranslation();
  const { events, isLoading } = useWatchdogActivity({
    instance,
    limit: 30,
    maxNoBetter: maxNoBetter ?? 3,
  });

  return (
    <Card data-testid="watchdog-activity-feed">
      <CardHeader className="flex flex-row items-center gap-3 pb-2">
        <CardTitle className="text-[15px] font-semibold">
          {t('watchdog.activity.title', { instance })}
        </CardTitle>
        <span className="ml-auto text-[10px] uppercase tracking-wide text-tx-faint">
          {t('watchdog.activity.label')}
        </span>
      </CardHeader>
      <CardContent className="p-0">
        {isLoading ? (
          <div className="space-y-2 p-4">
            {Array.from({ length: 4 }).map((_, i) => (
              <Skeleton key={i} className="h-10 w-full" />
            ))}
          </div>
        ) : events.length === 0 ? (
          <div className="px-4 py-6 text-center text-[13px] text-tx-muted">
            {t('watchdog.activity.placeholder')}
          </div>
        ) : (
          events.map((ev) => <EventRow key={ev.id} ev={ev} />)
        )}
      </CardContent>
    </Card>
  );
}
