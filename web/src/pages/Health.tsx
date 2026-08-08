import { useMemo, useState, type ComponentType, type ReactNode } from 'react';
import { Link } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import {
  RefreshCw,
  ChevronDown,
  TriangleAlert,
  CircleCheck,
  Hash,
  ImageOff,
  Clock,
  Download,
  Inbox,
  Gauge,
} from 'lucide-react';

import {
  useHealthDashboard,
  type HealthDashboard,
  type HealthSeriesItem,
  type HealthStaleItem,
  type HealthGrabItem,
  type HealthInboxItem,
  type HealthDeferredSignal,
} from '@/api/health';
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

// Badge tones reused from ui/badge.tsx cva variants. `ok` is applied
// whenever a signal's count is 0 so a healthy card reads green regardless
// of its "problem" tone.
type Tone = 'warn' | 'danger' | 'info';

// ── drill-down bodies ──────────────────────────────────────────────────
// Each renderer receives the bounded item slice (top ~50 from BE) plus the
// signal count so it can surface a "showing first N of M" note when the DB
// count exceeds the returned window.

function Truncated({ shown, total }: { shown: number; total: number }) {
  const { t } = useTranslation();
  if (total <= shown) return null;
  return (
    <p className="mt-2 text-[11.5px] text-tx-faint">
      {t('health.truncated', { shown, total })}
    </p>
  );
}

function EmptyDrill() {
  const { t } = useTranslation();
  return <p className="text-[12px] text-tx-faint">{t('health.drilldownEmpty')}</p>;
}

function seriesLabel(
  it: { series_id?: number; title?: string },
  fallback: (id: number) => string,
): string {
  if (it.title) return it.title;
  if (typeof it.series_id === 'number') return fallback(it.series_id);
  return '—';
}

function SeriesDrill({
  items,
  count,
  testid,
}: {
  items: readonly HealthSeriesItem[];
  count: number;
  testid: string;
}) {
  const { t } = useTranslation();
  const fallback = (id: number) => t('health.item.seriesFallback', { id });
  if (items.length === 0) return <EmptyDrill />;
  return (
    <>
      <ul className="flex flex-col gap-1.5" data-testid={`${testid}-items`}>
        {items.map((it, i) => (
          <li key={it.series_id ?? i} className="text-[12.5px]">
            {typeof it.series_id === 'number' ? (
              <Link
                to={`/series/${it.series_id}`}
                className="text-tx-secondary hover:text-tx-primary hover:underline"
              >
                {seriesLabel(it, fallback)}
              </Link>
            ) : (
              <span className="text-tx-secondary">{seriesLabel(it, fallback)}</span>
            )}
          </li>
        ))}
      </ul>
      <Truncated shown={items.length} total={count} />
    </>
  );
}

function StaleDrill({
  items,
  count,
  testid,
}: {
  items: readonly HealthStaleItem[];
  count: number;
  testid: string;
}) {
  const { t } = useTranslation();
  const fallback = (id: number) => t('health.item.seriesFallback', { id });
  if (items.length === 0) return <EmptyDrill />;
  return (
    <>
      <ul className="flex flex-col gap-1.5" data-testid={`${testid}-items`}>
        {items.map((it, i) => (
          <li
            key={it.series_id ?? i}
            className="flex flex-wrap items-center gap-x-2 gap-y-1 text-[12.5px]"
          >
            {typeof it.series_id === 'number' ? (
              <Link
                to={`/series/${it.series_id}`}
                className="text-tx-secondary hover:text-tx-primary hover:underline"
              >
                {seriesLabel(it, fallback)}
              </Link>
            ) : (
              <span className="text-tx-secondary">{seriesLabel(it, fallback)}</span>
            )}
            {it.tier ? (
              <Badge variant="neutral" className="px-1.5 py-0 text-[10.5px]">
                {t(`health.item.tier.${it.tier}`)}
              </Badge>
            ) : null}
            <span className="text-tx-faint">{relativeTime(it.synced_at)}</span>
          </li>
        ))}
      </ul>
      <Truncated shown={items.length} total={count} />
    </>
  );
}

function GrabDrill({
  items,
  count,
  testid,
}: {
  items: readonly HealthGrabItem[];
  count: number;
  testid: string;
}) {
  const { t } = useTranslation();
  if (items.length === 0) return <EmptyDrill />;
  return (
    <>
      <ul className="flex flex-col gap-1.5" data-testid={`${testid}-items`}>
        {items.map((it, i) => (
          <li
            key={it.id ?? i}
            className="flex flex-wrap items-center gap-x-2 gap-y-1 text-[12.5px]"
          >
            <span className="text-tx-secondary">{it.series_title || '—'}</span>
            {typeof it.season_number === 'number' ? (
              <span className="text-tx-muted">
                {t('health.item.season', { n: it.season_number })}
              </span>
            ) : null}
            {it.instance_name ? (
              <Badge variant="neutral" mono className="px-1.5 py-0 text-[10.5px]">
                {it.instance_name}
              </Badge>
            ) : null}
            <span className="text-tx-faint">{relativeTime(it.created_at)}</span>
          </li>
        ))}
      </ul>
      <Truncated shown={items.length} total={count} />
    </>
  );
}

function InboxDrill({
  items,
  count,
  testid,
}: {
  items: readonly HealthInboxItem[];
  count: number;
  testid: string;
}) {
  const { t } = useTranslation();
  if (items.length === 0) return <EmptyDrill />;
  return (
    <>
      <ul className="flex flex-col gap-2" data-testid={`${testid}-items`}>
        {items.map((it, i) => (
          <li key={it.id ?? i} className="text-[12.5px]">
            <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
              <span className="text-tx-secondary">{it.event_type || '—'}</span>
              {it.instance_name ? (
                <Badge variant="neutral" mono className="px-1.5 py-0 text-[10.5px]">
                  {it.instance_name}
                </Badge>
              ) : null}
              {typeof it.attempts === 'number' ? (
                <span className="text-tx-muted">
                  {t('health.item.attempts', { count: it.attempts })}
                </span>
              ) : null}
              <span className="text-tx-faint">{relativeTime(it.created_at)}</span>
            </div>
            {it.last_error ? (
              <p
                className="mt-0.5 truncate text-[11.5px] text-danger"
                title={it.last_error}
              >
                {it.last_error}
              </p>
            ) : null}
          </li>
        ))}
      </ul>
      <Truncated shown={items.length} total={count} />
    </>
  );
}

// ── card shell ─────────────────────────────────────────────────────────

function SignalCard({
  icon: Icon,
  label,
  description,
  count,
  tone,
  testid,
  children,
}: {
  icon: ComponentType<{ className?: string }>;
  label: string;
  description: string;
  count: number;
  tone: Tone;
  testid: string;
  children: ReactNode;
}) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const healthy = count === 0;
  const expandable = count > 0;

  return (
    <Card data-testid={testid} className="overflow-hidden">
      <Collapsible open={expandable && open} onOpenChange={setOpen}>
        <div className="flex items-start gap-3 p-4">
          <span
            className={cn(
              'grid h-8 w-8 shrink-0 place-items-center rounded-lg border',
              healthy
                ? 'border-ok/30 bg-ok-dim text-ok'
                : tone === 'danger'
                  ? 'border-danger/30 bg-danger-dim text-danger'
                  : tone === 'warn'
                    ? 'border-warn/30 bg-warn-dim text-warn'
                    : 'border-info/30 bg-info-dim text-info',
            )}
          >
            <Icon className="h-4 w-4" />
          </span>
          <div className="min-w-0 flex-1">
            <div className="flex items-center justify-between gap-2">
              <span className="text-[13px] font-medium text-tx-primary">{label}</span>
              <Badge
                variant={healthy ? 'ok' : tone}
                mono
                data-testid={`${testid}-count`}
              >
                {count}
              </Badge>
            </div>
            <p className="mt-0.5 text-[12px] leading-snug text-tx-muted">
              {description}
            </p>
            {expandable ? (
              <CollapsibleTrigger asChild>
                <Button
                  variant="ghost"
                  size="sm"
                  className="mt-2 -ml-1 h-6 gap-1 px-1.5 text-[11.5px] text-tx-muted"
                  data-testid={`${testid}-toggle`}
                >
                  <ChevronDown
                    className={cn('transition-transform', open && 'rotate-180')}
                  />
                  {open ? t('health.collapse') : t('health.expand')}
                </Button>
              </CollapsibleTrigger>
            ) : null}
          </div>
        </div>
        <CollapsibleContent className="border-t border-border-faint bg-bg-base/40 px-4 py-3">
          {children}
        </CollapsibleContent>
      </Collapsible>
    </Card>
  );
}

// Deferred signal (rate-limit pressure): no count, points at a Grafana
// metric. Rendered as an explicit "deferred" card, never a 0-counter.
function DeferredCard({ signal }: { signal: HealthDeferredSignal | undefined }) {
  const { t } = useTranslation();
  return (
    <Card data-testid="health-rate-limit" className="overflow-hidden">
      <div className="flex items-start gap-3 p-4">
        <span className="grid h-8 w-8 shrink-0 place-items-center rounded-lg border border-border-subtle bg-bg-surface-2 text-tx-muted">
          <Gauge className="h-4 w-4" />
        </span>
        <div className="min-w-0 flex-1">
          <div className="flex items-center justify-between gap-2">
            <span className="text-[13px] font-medium text-tx-primary">
              {t('health.signals.rateLimitPressure.label')}
            </span>
            <Badge variant="neutral" data-testid="health-rate-limit-deferred">
              {t('health.signals.rateLimitPressure.deferred')}
            </Badge>
          </div>
          <p className="mt-0.5 text-[12px] leading-snug text-tx-muted">
            {signal?.reason || t('health.signals.rateLimitPressure.description')}
          </p>
          {signal?.metric ? (
            <p className="mt-2 text-[11.5px] text-tx-faint">
              {t('health.signals.rateLimitPressure.metricLabel')}:{' '}
              <code className="rounded bg-bg-surface-2 px-1 py-0.5 font-mono text-[11px] text-tx-secondary">
                {signal.metric}
              </code>
            </p>
          ) : null}
        </div>
      </div>
    </Card>
  );
}

// ── helpers ────────────────────────────────────────────────────────────

function cnt(sig: { count?: number } | undefined): number {
  return sig?.count ?? 0;
}

function isAllHealthy(d: HealthDashboard | undefined): boolean {
  if (!d) return false;
  return (
    cnt(d.missing_tvdb_id) === 0 &&
    cnt(d.missing_poster) === 0 &&
    cnt(d.stale_enrichment) === 0 &&
    cnt(d.stuck_grabs) === 0 &&
    cnt(d.dead_letters) === 0
  );
}

// ── page ───────────────────────────────────────────────────────────────

export function Health() {
  const { t } = useTranslation();
  useSetPageTitle(t('health.title'));
  const query = useHealthDashboard();
  const data = query.data;

  const allHealthy = useMemo(() => isAllHealthy(data), [data]);

  return (
    <div className="flex flex-col gap-4" data-testid="health-page">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="min-w-0">
          <p className="text-[12.5px] text-tx-muted">{t('health.subtitle')}</p>
          {data?.generated_at ? (
            <p className="text-[11.5px] text-tx-faint" data-testid="health-generated-at">
              {t('health.generatedAt', { time: relativeTime(data.generated_at) })}
            </p>
          ) : null}
        </div>
        <Button
          variant="outline"
          size="sm"
          onClick={() => query.refetch()}
          disabled={query.isFetching}
          data-testid="health-refresh"
        >
          <RefreshCw className={cn(query.isFetching && 'animate-spin')} />
          {query.isFetching ? t('health.refreshing') : t('health.refresh')}
        </Button>
      </div>

      {query.isError ? (
        <Alert variant="destructive" data-testid="health-error">
          <TriangleAlert className="size-4" />
          <AlertTitle>{t('health.loadFailed')}</AlertTitle>
          <AlertDescription>
            {query.error.message}{' '}
            <Button variant="link" size="sm" onClick={() => query.refetch()}>
              {t('common.retry')}
            </Button>
          </AlertDescription>
        </Alert>
      ) : query.isPending ? (
        <div
          className="grid gap-3 [grid-template-columns:repeat(auto-fill,minmax(280px,1fr))]"
          data-testid="health-loading"
        >
          {Array.from({ length: 6 }).map((_, i) => (
            <Skeleton key={i} className="h-[120px] w-full" />
          ))}
        </div>
      ) : (
        <>
          {allHealthy ? (
            <Alert data-testid="health-all-healthy" className="border-ok/35 bg-ok-dim">
              <CircleCheck className="size-4 text-ok" />
              <AlertTitle className="text-ok">{t('health.allHealthy.title')}</AlertTitle>
              <AlertDescription className="text-tx-secondary">
                {t('health.allHealthy.description')}
              </AlertDescription>
            </Alert>
          ) : null}

          <div className="grid gap-3 [grid-template-columns:repeat(auto-fill,minmax(280px,1fr))]">
            <SignalCard
              icon={Hash}
              label={t('health.signals.missingTvdbId.label')}
              description={t('health.signals.missingTvdbId.description')}
              count={cnt(data?.missing_tvdb_id)}
              tone="warn"
              testid="health-missing-tvdb"
            >
              <SeriesDrill
                items={data?.missing_tvdb_id?.items ?? []}
                count={cnt(data?.missing_tvdb_id)}
                testid="health-missing-tvdb"
              />
            </SignalCard>

            <SignalCard
              icon={ImageOff}
              label={t('health.signals.missingPoster.label')}
              description={t('health.signals.missingPoster.description')}
              count={cnt(data?.missing_poster)}
              tone="warn"
              testid="health-missing-poster"
            >
              <SeriesDrill
                items={data?.missing_poster?.items ?? []}
                count={cnt(data?.missing_poster)}
                testid="health-missing-poster"
              />
            </SignalCard>

            <SignalCard
              icon={Clock}
              label={t('health.signals.staleEnrichment.label')}
              description={t('health.signals.staleEnrichment.description')}
              count={cnt(data?.stale_enrichment)}
              tone="info"
              testid="health-stale"
            >
              <StaleDrill
                items={data?.stale_enrichment?.items ?? []}
                count={cnt(data?.stale_enrichment)}
                testid="health-stale"
              />
            </SignalCard>

            <SignalCard
              icon={Download}
              label={t('health.signals.stuckGrabs.label')}
              description={t('health.signals.stuckGrabs.description')}
              count={cnt(data?.stuck_grabs)}
              tone="warn"
              testid="health-stuck-grabs"
            >
              <GrabDrill
                items={data?.stuck_grabs?.items ?? []}
                count={cnt(data?.stuck_grabs)}
                testid="health-stuck-grabs"
              />
            </SignalCard>

            <SignalCard
              icon={Inbox}
              label={t('health.signals.deadLetters.label')}
              description={t('health.signals.deadLetters.description')}
              count={cnt(data?.dead_letters)}
              tone="danger"
              testid="health-dead-letters"
            >
              <InboxDrill
                items={data?.dead_letters?.items ?? []}
                count={cnt(data?.dead_letters)}
                testid="health-dead-letters"
              />
            </SignalCard>

            <DeferredCard signal={data?.rate_limit_pressure} />
          </div>
        </>
      )}
    </div>
  );
}
