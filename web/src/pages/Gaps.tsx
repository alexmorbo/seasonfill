import { useMemo, useState } from 'react';
import { Link } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import {
  RefreshCw,
  ChevronDown,
  TriangleAlert,
  CircleCheck,
  PackageX,
  Layers,
} from 'lucide-react';

import {
  useGaps,
  type GapReport,
  type GapInstance,
  type GapSeries,
  type GapSeason,
} from '@/api/gaps';
import { useInstanceFilter } from '@/lib/instance-filter-context-internal';
import { useSetPageTitle } from '@/components/shell/page-title-context';
import { Card } from '@/components/ui/card';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import { Alert, AlertTitle, AlertDescription } from '@/components/ui/alert';
import {
  Collapsible,
  CollapsibleTrigger,
  CollapsibleContent,
} from '@/components/ui/collapsible';
import { relativeTime } from '@/lib/format';
import { cn } from '@/lib/utils';

// ── helpers ────────────────────────────────────────────────────────────

function missCount(i: GapInstance | undefined): number {
  return i?.missing_episode_count ?? 0;
}

function wholeCount(i: GapInstance | undefined): number {
  return i?.whole_season_missing_count ?? 0;
}

function pad2(n: number): string {
  return String(n).padStart(2, '0');
}

function isAllHealthy(d: GapReport | undefined): boolean {
  const list = d?.instances ?? [];
  if (list.length === 0) return true;
  return list.every((i) => missCount(i) === 0 && wholeCount(i) === 0);
}

// ── episode row ────────────────────────────────────────────────────────

function EpisodeList({
  season,
  seriesId,
}: {
  season: GapSeason;
  seriesId: number;
}) {
  const eps = season.episodes ?? [];
  const seasonNumber = season.season_number ?? 0;
  if (eps.length === 0) return null;
  return (
    <ul
      className="mt-2 flex flex-wrap gap-x-3 gap-y-1"
      data-testid={`gaps-season-${seriesId}-${seasonNumber}-episodes`}
    >
      {eps.map((ep, i) => {
        const s = ep.season_number ?? seasonNumber;
        const e = ep.episode_number ?? 0;
        return (
          <li
            key={ep.episode_id ?? `${s}-${e}-${i}`}
            className="flex items-center gap-1.5 text-[12px] text-tx-secondary"
          >
            <span className="font-mono tabular-nums text-tx-primary">
              S{pad2(s)}E{pad2(e)}
            </span>
            <span className="text-tx-faint">{relativeTime(ep.air_date)}</span>
          </li>
        );
      })}
    </ul>
  );
}

// ── season row ─────────────────────────────────────────────────────────

function SeasonRow({
  season,
  seriesId,
}: {
  season: GapSeason;
  seriesId: number;
}) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const seasonNumber = season.season_number ?? 0;
  const missing = season.missing_count ?? 0;
  const aired = season.aired_monitored_count ?? 0;
  const whole = season.whole_season_missing === true;
  const hasEpisodes = (season.episodes ?? []).length > 0;

  return (
    <div
      className={cn(
        'rounded-md border px-3 py-2',
        whole ? 'border-danger/35 bg-danger-dim' : 'border-border-faint bg-bg-base/40',
      )}
      data-testid={`gaps-season-${seriesId}-${seasonNumber}`}
    >
      <Collapsible open={hasEpisodes && open} onOpenChange={setOpen}>
        <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
          <span className="text-[12.5px] font-medium text-tx-primary">
            {t('gaps.season.label', { n: seasonNumber })}
          </span>
          {whole ? (
            <Badge
              variant="danger"
              className="px-1.5 py-0 text-[10.5px]"
              data-testid={`gaps-season-${seriesId}-${seasonNumber}-whole`}
            >
              {t('gaps.season.fullyMissing', { n: seasonNumber })}
            </Badge>
          ) : null}
          <span className="text-[12px] text-tx-muted">
            {t('gaps.season.ratio', { missing, aired })}
          </span>
          {hasEpisodes ? (
            <CollapsibleTrigger asChild>
              <Button
                variant="ghost"
                size="sm"
                className="ml-auto -mr-1 h-6 gap-1 px-1.5 text-[11.5px] text-tx-muted"
                data-testid={`gaps-season-${seriesId}-${seasonNumber}-toggle`}
              >
                <ChevronDown
                  className={cn('transition-transform', open && 'rotate-180')}
                />
                {open ? t('gaps.collapse') : t('gaps.expand')}
              </Button>
            </CollapsibleTrigger>
          ) : null}
        </div>
        <CollapsibleContent>
          <EpisodeList season={season} seriesId={seriesId} />
        </CollapsibleContent>
      </Collapsible>
    </div>
  );
}

// ── series row ─────────────────────────────────────────────────────────

function SeriesRow({ series }: { series: GapSeries }) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const seriesId = series.series_id;
  const missing = series.missing_count ?? 0;
  const seasons = series.seasons ?? [];
  const expandable = seasons.length > 0;
  const title =
    series.title ||
    (typeof seriesId === 'number'
      ? t('gaps.series.fallback', { id: seriesId })
      : '—');

  return (
    <Card
      className="overflow-hidden"
      data-testid={typeof seriesId === 'number' ? `gaps-series-${seriesId}` : undefined}
    >
      <Collapsible open={expandable && open} onOpenChange={setOpen}>
        <div className="flex flex-wrap items-center gap-x-2 gap-y-1 p-3">
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
          <Badge
            variant="warn"
            mono
            className="px-1.5 py-0 text-[10.5px]"
            data-testid={
              typeof seriesId === 'number' ? `gaps-series-${seriesId}-missing` : undefined
            }
          >
            {t('gaps.series.missing', { count: missing })}
          </Badge>
          {expandable ? (
            <CollapsibleTrigger asChild>
              <Button
                variant="ghost"
                size="sm"
                className="ml-auto -mr-1 h-6 gap-1 px-1.5 text-[11.5px] text-tx-muted"
                data-testid={
                  typeof seriesId === 'number' ? `gaps-series-${seriesId}-toggle` : undefined
                }
              >
                <ChevronDown
                  className={cn('transition-transform', open && 'rotate-180')}
                />
                {open ? t('gaps.collapse') : t('gaps.expand')}
              </Button>
            </CollapsibleTrigger>
          ) : null}
        </div>
        <CollapsibleContent className="flex flex-col gap-1.5 border-t border-border-faint bg-bg-base/40 px-3 py-2.5">
          {seasons.map((s, i) => (
            <SeasonRow
              key={s.season_number ?? i}
              season={s}
              seriesId={typeof seriesId === 'number' ? seriesId : -1}
            />
          ))}
        </CollapsibleContent>
      </Collapsible>
    </Card>
  );
}

// ── instance section ───────────────────────────────────────────────────

function InstanceSection({ inst }: { inst: GapInstance }) {
  const { t } = useTranslation();
  const name = inst.instance_name ?? '—';
  const missing = missCount(inst);
  const whole = wholeCount(inst);
  const series = inst.series ?? [];
  const healthy = missing === 0 && whole === 0;

  return (
    <section className="flex flex-col gap-3" data-testid={`gaps-instance-${name}`}>
      <div className="flex flex-wrap items-center gap-2">
        <Badge variant="neutral" mono className="text-[12px]">
          {name}
        </Badge>
        <span className="flex items-center gap-1.5">
          <PackageX className="h-3.5 w-3.5 text-tx-muted" />
          <Badge
            variant={missing > 0 ? 'warn' : 'ok'}
            mono
            data-testid={`gaps-instance-${name}-missing`}
          >
            {t('gaps.counters.missing', { count: missing })}
          </Badge>
        </span>
        <span className="flex items-center gap-1.5">
          <Layers className="h-3.5 w-3.5 text-tx-muted" />
          <Badge
            variant={whole > 0 ? 'danger' : 'ok'}
            mono
            data-testid={`gaps-instance-${name}-whole-season`}
          >
            {t('gaps.counters.wholeSeason', { count: whole })}
          </Badge>
        </span>
      </div>

      {healthy ? (
        <Alert
          className="border-ok/35 bg-ok-dim"
          data-testid={`gaps-instance-${name}-healthy`}
        >
          <CircleCheck className="size-4 text-ok" />
          <AlertTitle className="text-ok">{t('gaps.instanceHealthy.title')}</AlertTitle>
          <AlertDescription className="text-tx-secondary">
            {t('gaps.instanceHealthy.description')}
          </AlertDescription>
        </Alert>
      ) : (
        <div className="flex flex-col gap-2">
          {series.map((s, i) => (
            <SeriesRow key={s.series_id ?? i} series={s} />
          ))}
        </div>
      )}
    </section>
  );
}

// ── page ───────────────────────────────────────────────────────────────

export function Gaps() {
  const { t } = useTranslation();
  useSetPageTitle(t('gaps.title'));
  // Instance scope reuses the GLOBAL instance filter (same as Grabs/Scans/
  // Decisions) so the sidebar switcher drives this page too. A specific
  // selection scopes the BE report to that instance; "all" (null) asks for
  // every instance and renders one section each.
  const { filter } = useInstanceFilter();
  const query = useGaps(filter ?? undefined);
  const data = query.data;

  const allHealthy = useMemo(() => isAllHealthy(data), [data]);
  const instances = data?.instances ?? [];

  return (
    <div className="flex flex-col gap-4" data-testid="gaps-page">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="min-w-0">
          <p className="text-[12.5px] text-tx-muted">{t('gaps.subtitle')}</p>
          {data?.generated_at ? (
            <p className="text-[11.5px] text-tx-faint" data-testid="gaps-generated-at">
              {t('gaps.generatedAt', { time: relativeTime(data.generated_at) })}
            </p>
          ) : null}
        </div>
        <Button
          variant="outline"
          size="sm"
          onClick={() => query.refetch()}
          disabled={query.isFetching}
          data-testid="gaps-refresh"
        >
          <RefreshCw className={cn(query.isFetching && 'animate-spin')} />
          {query.isFetching ? t('gaps.refreshing') : t('gaps.refresh')}
        </Button>
      </div>

      {query.isError ? (
        <Alert variant="destructive" data-testid="gaps-error">
          <TriangleAlert className="size-4" />
          <AlertTitle>{t('gaps.loadFailed')}</AlertTitle>
          <AlertDescription>
            {query.error.message}{' '}
            <Button variant="link" size="sm" onClick={() => query.refetch()}>
              {t('common.retry')}
            </Button>
          </AlertDescription>
        </Alert>
      ) : query.isPending ? (
        <div className="flex flex-col gap-3" data-testid="gaps-loading">
          {Array.from({ length: 4 }).map((_, i) => (
            <Skeleton key={i} className="h-[72px] w-full" />
          ))}
        </div>
      ) : allHealthy ? (
        <Alert data-testid="gaps-all-healthy" className="border-ok/35 bg-ok-dim">
          <CircleCheck className="size-4 text-ok" />
          <AlertTitle className="text-ok">{t('gaps.allHealthy.title')}</AlertTitle>
          <AlertDescription className="text-tx-secondary">
            {t('gaps.allHealthy.description')}
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
