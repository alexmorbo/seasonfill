import type { ComponentType } from 'react';
import { Link } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import {
  RefreshCw,
  TriangleAlert,
  Sparkles,
  PackageX,
  CalendarClock,
  PauseCircle,
} from 'lucide-react';

import {
  useLists,
  type SmartListInstance,
  type SmartListShelf,
  type SmartListSeries,
  type SmartListShelfKey,
} from '@/api/lists';
import { useInstanceFilter } from '@/lib/instance-filter-context-internal';
import { useSetPageTitle } from '@/components/shell/page-title-context';
import { Card } from '@/components/ui/card';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import { Alert, AlertTitle, AlertDescription } from '@/components/ui/alert';
import { relativeTime } from '@/lib/format';
import { cn } from '@/lib/utils';

// ── shelf presentation ──────────────────────────────────────────────────

const SHELF_ICON: Record<SmartListShelfKey, ComponentType<{ className?: string }>> = {
  ended_incomplete: PackageX,
  returning_soon: CalendarClock,
  hiatus: PauseCircle,
};

// shelfMetric renders the shelf-specific trailing metric for one series row.
// The BE sets exactly one of missing_count / next_air_date / last_aired_at per
// the owning shelf; a missing value degrades to a neutral placeholder.
function shelfMetric(
  key: SmartListShelfKey,
  s: SmartListSeries,
  t: ReturnType<typeof useTranslation>['t'],
): string {
  switch (key) {
    case 'ended_incomplete':
      return t('lists.series.missing', { count: s.missing_count ?? 0 });
    case 'returning_soon':
      return t('lists.series.nextAir', { time: relativeTime(s.next_air_date) });
    case 'hiatus':
      return t('lists.series.lastAired', { time: relativeTime(s.last_aired_at) });
    default:
      return '';
  }
}

// ── series row ──────────────────────────────────────────────────────────

function SeriesRow({ shelfKey, series }: { shelfKey: SmartListShelfKey; series: SmartListSeries }) {
  const { t } = useTranslation();
  const seriesId = series.series_id;
  const title =
    series.title ||
    (typeof seriesId === 'number' ? t('lists.series.fallback', { id: seriesId }) : '—');
  const metric = shelfMetric(shelfKey, series, t);

  return (
    <div
      className="flex flex-wrap items-center gap-x-2 gap-y-1 rounded-md border border-border-faint bg-bg-base/40 px-3 py-2"
      data-testid={typeof seriesId === 'number' ? `lists-series-${seriesId}` : undefined}
    >
      {typeof seriesId === 'number' ? (
        <Link
          to={`/series/${seriesId}`}
          className="text-[13px] font-medium text-tx-secondary hover:text-tx-primary hover:underline"
        >
          {title}
        </Link>
      ) : (
        <span className="text-[13px] font-medium text-tx-secondary">{title}</span>
      )}
      <span className="ml-auto font-mono tabular-nums text-[11.5px] text-tx-muted">{metric}</span>
    </div>
  );
}

// ── shelf ───────────────────────────────────────────────────────────────

function ShelfSection({ instance, shelf }: { instance: string; shelf: SmartListShelf }) {
  const { t } = useTranslation();
  const key = shelf.key;
  const Icon = SHELF_ICON[key] ?? Sparkles;
  const title = t(`lists.shelf.${key}`, { defaultValue: shelf.title });
  const count = shelf.count ?? 0;
  const series = shelf.series ?? [];

  return (
    <Card className="flex flex-col gap-2.5 p-3" data-testid={`lists-${instance}-shelf-${key}`}>
      <div className="flex items-center gap-2">
        <Icon className="h-4 w-4 shrink-0 text-tx-muted" />
        <h3 className="text-[12.5px] font-medium text-tx-secondary">{title}</h3>
        <Badge
          variant={count > 0 ? 'neutral' : 'ok'}
          mono
          className="ml-auto"
          data-testid={`lists-${instance}-shelf-${key}-count`}
        >
          {count}
        </Badge>
      </div>

      {count === 0 || series.length === 0 ? (
        <p className="text-[12px] text-tx-faint" data-testid={`lists-${instance}-shelf-${key}-empty`}>
          {t('lists.shelfEmpty')}
        </p>
      ) : (
        <div className="flex flex-col gap-1.5">
          {series.map((s, i) => (
            <SeriesRow key={s.series_id ?? i} shelfKey={key} series={s} />
          ))}
        </div>
      )}
    </Card>
  );
}

// ── instance section ──────────────────────────────────────────────────────

function InstanceSection({ inst }: { inst: SmartListInstance }) {
  const name = inst.instance_name ?? '—';
  const shelves = inst.shelves ?? [];

  return (
    <section className="flex flex-col gap-3" data-testid={`lists-instance-${name}`}>
      <Badge variant="neutral" mono className="w-fit text-[12px]">
        {name}
      </Badge>
      <div className="grid grid-cols-1 gap-3 lg:grid-cols-3">
        {shelves.map((shelf, i) => (
          <ShelfSection key={shelf.key ?? i} instance={name} shelf={shelf} />
        ))}
      </div>
    </section>
  );
}

// ── page ───────────────────────────────────────────────────────────────────

export function Lists() {
  const { t } = useTranslation();
  useSetPageTitle(t('lists.title'));
  // Instance scope reuses the GLOBAL instance filter (same as Gaps/Stats) so the
  // sidebar switcher drives this page too. A specific selection scopes the BE
  // report to that instance; "all" (null) asks for every instance.
  const { filter } = useInstanceFilter();
  const query = useLists(filter ?? undefined);
  const data = query.data;
  const instances = data?.instances ?? [];

  return (
    <div className="flex flex-col gap-4" data-testid="lists-page">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="min-w-0">
          <p className="text-[12.5px] text-tx-muted">{t('lists.subtitle')}</p>
          {data?.generated_at ? (
            <p className="text-[11.5px] text-tx-faint" data-testid="lists-generated-at">
              {t('lists.generatedAt', { time: relativeTime(data.generated_at) })}
            </p>
          ) : null}
        </div>
        <Button
          variant="outline"
          size="sm"
          onClick={() => query.refetch()}
          disabled={query.isFetching}
          data-testid="lists-refresh"
        >
          <RefreshCw className={cn(query.isFetching && 'animate-spin')} />
          {query.isFetching ? t('lists.refreshing') : t('lists.refresh')}
        </Button>
      </div>

      {query.isError ? (
        <Alert variant="destructive" data-testid="lists-error">
          <TriangleAlert className="size-4" />
          <AlertTitle>{t('lists.loadFailed')}</AlertTitle>
          <AlertDescription>
            {query.error.message}{' '}
            <Button variant="link" size="sm" onClick={() => query.refetch()}>
              {t('common.retry')}
            </Button>
          </AlertDescription>
        </Alert>
      ) : query.isPending ? (
        <div className="flex flex-col gap-3" data-testid="lists-loading">
          {Array.from({ length: 3 }).map((_, i) => (
            <Skeleton key={i} className="h-[120px] w-full" />
          ))}
        </div>
      ) : instances.length === 0 ? (
        <Alert data-testid="lists-empty">
          <Sparkles className="size-4" />
          <AlertTitle>{t('lists.empty.title')}</AlertTitle>
          <AlertDescription className="text-tx-secondary">
            {t('lists.empty.description')}
          </AlertDescription>
        </Alert>
      ) : (
        <div className="flex flex-col gap-6">
          {instances.map((inst, i) => (
            <InstanceSection key={inst.instance_name ?? i} inst={inst} />
          ))}
        </div>
      )}
    </div>
  );
}
