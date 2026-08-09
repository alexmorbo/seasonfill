import type { ComponentType } from 'react';
import { useTranslation } from 'react-i18next';
import { RefreshCw, TriangleAlert, BarChart3, Database, Download, Upload } from 'lucide-react';

import {
  useStats,
  type StatsInstance,
  type StatsKind,
} from '@/api/stats';
import { useInstanceFilter } from '@/lib/instance-filter-context-internal';
import { useSetPageTitle } from '@/components/shell/page-title-context';
import { Card } from '@/components/ui/card';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import { Alert, AlertTitle, AlertDescription } from '@/components/ui/alert';
import { relativeTime } from '@/lib/format';
import { cn } from '@/lib/utils';

// ── helpers ────────────────────────────────────────────────────────────

function fmtBytes(n: number | undefined): string {
  if (!n || n <= 0) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB'];
  let v = n;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) { v /= 1024; i += 1; }
  const prec = v >= 100 || i === 0 ? 0 : 1;
  return `${v.toFixed(prec)} ${units[i]}`;
}

function fmtInt(n: number | undefined): string {
  return new Intl.NumberFormat('ru-RU').format(n ?? 0);
}

function fmtPct(rate: number | undefined): string {
  return `${Math.round((rate ?? 0) * 1000) / 10}%`;
}

// ── bar list (CSS/flex horizontal bars — no chart lib) ───────────────────

function BarList({
  items,
  labelOf,
  emptyKey,
}: {
  items: readonly StatsKind[];
  labelOf: (k: StatsKind) => string;
  emptyKey: string;
}) {
  const { t } = useTranslation();
  if (items.length === 0) {
    return <p className="text-[12px] text-tx-faint">{t(emptyKey)}</p>;
  }
  const max = Math.max(...items.map((k) => k.size_bytes ?? 0), 1);
  return (
    <ul className="flex flex-col gap-1.5">
      {items.map((k, i) => {
        const size = k.size_bytes ?? 0;
        const pct = Math.max(2, Math.round((size / max) * 100));
        const label = labelOf(k) || t('stats.unknownLabel');
        return (
          <li key={`${label}-${i}`} className="flex flex-col gap-0.5" data-testid={`stats-bar-${i}`}>
            <div className="flex items-baseline justify-between gap-2">
              <span className="truncate text-[12.5px] text-tx-secondary">{label}</span>
              <span className="shrink-0 font-mono tabular-nums text-[11.5px] text-tx-muted">
                {fmtBytes(size)} · {t('stats.seriesCount', { count: k.series_count ?? 0 })}
              </span>
            </div>
            <div className="h-1.5 w-full overflow-hidden rounded-full bg-bg-base">
              <div
                className="h-full rounded-full bg-accent/70"
                style={{ width: `${pct}%` }}
              />
            </div>
          </li>
        );
      })}
    </ul>
  );
}

// ── totals stat tile ─────────────────────────────────────────────────────

function StatTile({
  icon: Icon,
  label,
  value,
  testid,
}: {
  icon: ComponentType<{ className?: string }>;
  label: string;
  value: string;
  testid: string;
}) {
  return (
    <div className="flex items-center gap-2.5 rounded-md border border-border-faint bg-bg-base/40 px-3 py-2" data-testid={testid}>
      <Icon className="h-4 w-4 shrink-0 text-tx-muted" />
      <div className="min-w-0">
        <div className="font-mono tabular-nums text-[15px] text-tx-primary">{value}</div>
        <div className="text-[11px] text-tx-faint">{label}</div>
      </div>
    </div>
  );
}

// ── instance section ─────────────────────────────────────────────────────

function InstanceSection({ inst }: { inst: StatsInstance }) {
  const { t } = useTranslation();
  const name = inst.instance_name ?? '—';
  const totals = inst.totals ?? {};
  const grab = inst.grab_success ?? {};
  const torrents = inst.torrent_totals ?? {};
  const genres = inst.by_genre ?? [];
  const networks = inst.by_network ?? [];

  return (
    <section className="flex flex-col gap-3" data-testid={`stats-instance-${name}`}>
      <Badge variant="neutral" mono className="w-fit text-[12px]">{name}</Badge>

      {/* Totals */}
      <div className="grid grid-cols-1 gap-2 sm:grid-cols-3">
        <StatTile icon={BarChart3} label={t('stats.totals.series')} value={fmtInt(totals.series_count)} testid={`stats-${name}-series`} />
        <StatTile icon={Database} label={t('stats.totals.episodes')} value={fmtInt(totals.episodes_on_disk)} testid={`stats-${name}-episodes`} />
        <StatTile icon={Database} label={t('stats.totals.size')} value={fmtBytes(totals.total_size_bytes)} testid={`stats-${name}-size`} />
      </div>

      {/* Genre / Network bars */}
      <div className="grid grid-cols-1 gap-3 lg:grid-cols-2">
        <Card className="flex flex-col gap-2.5 p-3">
          <h3 className="text-[12.5px] font-medium text-tx-secondary">{t('stats.byGenre')}</h3>
          <BarList items={genres} labelOf={(k: StatsKind) => k.genre ?? ''} emptyKey="stats.emptyGenre" />
        </Card>
        <Card className="flex flex-col gap-2.5 p-3">
          <h3 className="text-[12.5px] font-medium text-tx-secondary">{t('stats.byNetwork')}</h3>
          <BarList items={networks} labelOf={(k: StatsKind) => k.network ?? ''} emptyKey="stats.emptyNetwork" />
        </Card>
      </div>

      {/* Grab success + torrent totals */}
      <div className="grid grid-cols-1 gap-3 lg:grid-cols-2">
        <Card className="flex flex-col gap-2 p-3" data-testid={`stats-${name}-grab`}>
          <h3 className="text-[12.5px] font-medium text-tx-secondary">{t('stats.grab.title')}</h3>
          <div className="flex flex-wrap items-center gap-2">
            <Badge variant="ok" mono>{t('stats.grab.imported', { count: grab.imported ?? 0 })}</Badge>
            <Badge variant="warn" mono>{t('stats.grab.grabbed', { count: grab.grabbed ?? 0 })}</Badge>
            <Badge variant="danger" mono>{t('stats.grab.failed', { count: grab.failed ?? 0 })}</Badge>
            <span className="ml-auto font-mono tabular-nums text-[13px] text-tx-primary" data-testid={`stats-${name}-grab-rate`}>
              {t('stats.grab.rate', { pct: fmtPct(grab.success_rate) })}
            </span>
          </div>
        </Card>
        <Card className="flex flex-col gap-2 p-3" data-testid={`stats-${name}-torrents`}>
          <h3 className="text-[12.5px] font-medium text-tx-secondary">{t('stats.torrents.title')}</h3>
          <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-[12.5px]">
            <span className="flex items-center gap-1.5"><Upload className="h-3.5 w-3.5 text-tx-muted" /><span className="font-mono tabular-nums text-tx-secondary">{fmtBytes(torrents.total_uploaded_bytes)}</span></span>
            <span className="flex items-center gap-1.5"><Download className="h-3.5 w-3.5 text-tx-muted" /><span className="font-mono tabular-nums text-tx-secondary">{fmtBytes(torrents.total_downloaded_bytes)}</span></span>
            <span className="text-tx-muted">{t('stats.torrents.count', { count: torrents.torrent_count ?? 0 })}</span>
            <span className="ml-auto font-mono tabular-nums text-tx-primary">{t('stats.torrents.ratio', { ratio: (torrents.avg_ratio ?? 0).toFixed(2) })}</span>
          </div>
        </Card>
      </div>
    </section>
  );
}

// ── page ─────────────────────────────────────────────────────────────────

export function Stats() {
  const { t } = useTranslation();
  useSetPageTitle(t('stats.title'));
  const { filter } = useInstanceFilter();
  const query = useStats(filter ?? undefined);
  const data = query.data;
  const instances = data?.instances ?? [];

  return (
    <div className="flex flex-col gap-4" data-testid="stats-page">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="min-w-0">
          <p className="text-[12.5px] text-tx-muted">{t('stats.subtitle')}</p>
          {data?.generated_at ? (
            <p className="text-[11.5px] text-tx-faint" data-testid="stats-generated-at">
              {t('stats.generatedAt', { time: relativeTime(data.generated_at) })}
            </p>
          ) : null}
        </div>
        <Button variant="outline" size="sm" onClick={() => query.refetch()} disabled={query.isFetching} data-testid="stats-refresh">
          <RefreshCw className={cn(query.isFetching && 'animate-spin')} />
          {query.isFetching ? t('stats.refreshing') : t('stats.refresh')}
        </Button>
      </div>

      {query.isError ? (
        <Alert variant="destructive" data-testid="stats-error">
          <TriangleAlert className="size-4" />
          <AlertTitle>{t('stats.loadFailed')}</AlertTitle>
          <AlertDescription>
            {query.error.message}{' '}
            <Button variant="link" size="sm" onClick={() => query.refetch()}>{t('common.retry')}</Button>
          </AlertDescription>
        </Alert>
      ) : query.isPending ? (
        <div className="flex flex-col gap-3" data-testid="stats-loading">
          {Array.from({ length: 3 }).map((_, i) => (
            <Skeleton key={i} className="h-[120px] w-full" />
          ))}
        </div>
      ) : instances.length === 0 ? (
        <Alert data-testid="stats-empty">
          <BarChart3 className="size-4" />
          <AlertTitle>{t('stats.empty.title')}</AlertTitle>
          <AlertDescription className="text-tx-secondary">{t('stats.empty.description')}</AlertDescription>
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
