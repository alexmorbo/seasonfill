import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import {
  Ban,
  Check,
  CheckCircle2,
  DownloadCloud,
  RotateCw,
  TriangleAlert,
  Unplug,
  GitBranch,
  Radar,
  XCircle,
} from 'lucide-react';
import type { LucideIcon } from 'lucide-react';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Badge } from '@/components/ui/badge';
import { Skeleton } from '@/components/ui/skeleton';
import { cn } from '@/lib/utils';
import { useFormatDate } from '@/lib/timezone';
import {
  useWatchdogActivity,
  type WatchdogActivityEvent,
  type WatchdogActivityType,
} from '@/lib/api/watchdogActivity';

type FmtFn = ReturnType<typeof useFormatDate>;

const TYPE_ICON: Record<WatchdogActivityType, LucideIcon> = {
  unregistered: Unplug,
  regrab: RotateCw,
  better: Check,
  no_better: TriangleAlert,
  blacklist: Ban,
  grab: DownloadCloud,
  decision: CheckCircle2,
};

const TYPE_VARIANT: Record<
  WatchdogActivityType,
  'danger' | 'accent' | 'ok' | 'warn' | 'neutral'
> = {
  unregistered: 'danger',
  regrab: 'accent',
  better: 'ok',
  no_better: 'warn',
  blacklist: 'danger',
  grab: 'accent',
  decision: 'neutral',
};

function isSameTzDay(a: Date, b: Date, fmt: FmtFn): boolean {
  // Compare yyyy-mm-dd in the configured zone, NOT the browser zone.
  return fmt(a, 'date') === fmt(b, 'date');
}

function formatRowTime(iso: string, fmt: FmtFn, yesterdayLabel: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  const now = new Date();
  if (isSameTzDay(d, now, fmt)) return fmt(d, 'time');
  const y = new Date(now.getTime() - 24 * 60 * 60 * 1000);
  if (isSameTzDay(d, y, fmt)) return `${yesterdayLabel} ${fmt(d, 'time')}`;
  return fmt(d, 'date');
}

function EventRow({ ev }: { ev: WatchdogActivityEvent }) {
  const { t } = useTranslation();
  const fmt = useFormatDate();
  const Icon = TYPE_ICON[ev.type];
  const isErrorDecision =
    ev.type === 'decision' &&
    (ev.decision_outcome === 'error' ||
      ev.decision_outcome === 'blocked_cooldown' ||
      ev.decision_outcome === 'expired');
  const variant = isErrorDecision ? 'warn' : TYPE_VARIANT[ev.type];
  const DisplayIcon = isErrorDecision ? XCircle : Icon;
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
      case 'grab':
        return ev.release_title ?? '';
      case 'decision':
        return t('watchdog.activity.detail.decision', {
          outcome: ev.decision_outcome ?? '?',
          reason: ev.decision_reason ?? '—',
        });
    }
  })();

  const actionTo =
    ev.type === 'regrab' || ev.type === 'unregistered'
      ? `/scans?instance=${encodeURIComponent(ev.instance)}`
      : ev.type === 'decision'
        ? `/decisions?instance=${encodeURIComponent(ev.instance)}&series=${ev.series_id}`
        : `/grabs?instance=${encodeURIComponent(ev.instance)}&series=${ev.series_id}`;

  return (
    <div
      data-testid={`feed-row-${ev.type}`}
      className="flex items-start gap-3 border-b border-border-faint px-4 py-3 last:border-b-0"
    >
      <span className="mono w-[80px] flex-none pt-0.5 text-[11px] text-tx-faint">
        {formatRowTime(ev.at, fmt, t('watchdog.activity.yesterday'))}
      </span>
      <Badge variant={variant} className="flex-none gap-1 px-2 py-0.5 text-[11px]">
        <DisplayIcon className="h-3 w-3" />
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
