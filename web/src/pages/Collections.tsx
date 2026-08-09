import { Link } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { RefreshCw, TriangleAlert, Layers, Sparkles } from 'lucide-react';

import {
  useCollections,
  type CollectionsInstance,
  type Collection,
  type CollectionSeries,
} from '@/api/collections';
import { useInstanceFilter } from '@/lib/instance-filter-context-internal';
import { useSetPageTitle } from '@/components/shell/page-title-context';
import { Card } from '@/components/ui/card';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import { Alert, AlertTitle, AlertDescription } from '@/components/ui/alert';
import { relativeTime } from '@/lib/format';
import { cn } from '@/lib/utils';

// ── series row ──────────────────────────────────────────────────────────

function SeriesRow({ series }: { series: CollectionSeries }) {
  const { t } = useTranslation();
  const seriesId = series.series_id;
  const title =
    series.title ||
    (typeof seriesId === 'number' ? t('collections.series.fallback', { id: seriesId }) : '—');

  return (
    <div
      className="flex flex-wrap items-center gap-x-2 gap-y-1 rounded-md border border-border-faint bg-bg-base/40 px-3 py-2"
      data-testid={typeof seriesId === 'number' ? `collections-series-${seriesId}` : undefined}
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
    </div>
  );
}

// ── collection card ───────────────────────────────────────────────────────

function CollectionCard({ instance, col }: { instance: string; col: Collection }) {
  const { t } = useTranslation();
  const slug = col.slug;
  const title = t(`collections.bucket.${slug}`, { defaultValue: col.title });
  const owned = col.owned_count ?? 0;
  const series = col.series ?? [];

  return (
    <Card className="flex flex-col gap-2.5 p-3" data-testid={`collections-${instance}-bucket-${slug}`}>
      <div className="flex items-center gap-2">
        <Layers className="h-4 w-4 shrink-0 text-tx-muted" />
        <h3 className="text-[12.5px] font-medium text-tx-secondary">{title}</h3>
        {col.is_franchise ? (
          <Badge variant="ok" className="text-[10.5px]" data-testid={`collections-${instance}-bucket-${slug}-franchise`}>
            {t('collections.franchise')}
          </Badge>
        ) : null}
        <Badge
          variant={owned > 0 ? 'neutral' : 'ok'}
          mono
          className="ml-auto"
          data-testid={`collections-${instance}-bucket-${slug}-count`}
        >
          {owned}
        </Badge>
      </div>

      {series.length === 0 ? (
        <p className="text-[12px] text-tx-faint">{t('collections.bucketEmpty')}</p>
      ) : (
        <div className="flex flex-col gap-1.5">
          {series.map((s, i) => (
            <SeriesRow key={s.series_id ?? i} series={s} />
          ))}
        </div>
      )}
    </Card>
  );
}

// ── instance section ──────────────────────────────────────────────────────

function InstanceSection({ inst }: { inst: CollectionsInstance }) {
  const { t } = useTranslation();
  const name = inst.instance_name ?? '—';
  const collections = inst.collections ?? [];

  return (
    <section className="flex flex-col gap-3" data-testid={`collections-instance-${name}`}>
      <Badge variant="neutral" mono className="w-fit text-[12px]">
        {name}
      </Badge>
      {collections.length === 0 ? (
        <p className="text-[12px] text-tx-faint" data-testid={`collections-instance-${name}-empty`}>
          {t('collections.instanceEmpty')}
        </p>
      ) : (
        <div className="grid grid-cols-1 gap-3 md:grid-cols-2 xl:grid-cols-3">
          {collections.map((col, i) => (
            <CollectionCard key={col.slug ?? i} instance={name} col={col} />
          ))}
        </div>
      )}
    </section>
  );
}

// ── page ───────────────────────────────────────────────────────────────────

export function Collections() {
  const { t } = useTranslation();
  useSetPageTitle(t('collections.title'));
  // Instance scope reuses the GLOBAL instance filter (same as Gaps/Stats/Lists).
  const { filter } = useInstanceFilter();
  const query = useCollections(filter ?? undefined);
  const data = query.data;
  const instances = data?.instances ?? [];

  return (
    <div className="flex flex-col gap-4" data-testid="collections-page">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="min-w-0">
          <p className="text-[12.5px] text-tx-muted">{t('collections.subtitle')}</p>
          {data?.generated_at ? (
            <p className="text-[11.5px] text-tx-faint" data-testid="collections-generated-at">
              {t('collections.generatedAt', { time: relativeTime(data.generated_at) })}
            </p>
          ) : null}
        </div>
        <Button
          variant="outline"
          size="sm"
          onClick={() => query.refetch()}
          disabled={query.isFetching}
          data-testid="collections-refresh"
        >
          <RefreshCw className={cn(query.isFetching && 'animate-spin')} />
          {query.isFetching ? t('collections.refreshing') : t('collections.refresh')}
        </Button>
      </div>

      {query.isError ? (
        <Alert variant="destructive" data-testid="collections-error">
          <TriangleAlert className="size-4" />
          <AlertTitle>{t('collections.loadFailed')}</AlertTitle>
          <AlertDescription>
            {query.error.message}{' '}
            <Button variant="link" size="sm" onClick={() => query.refetch()}>
              {t('common.retry')}
            </Button>
          </AlertDescription>
        </Alert>
      ) : query.isPending ? (
        <div className="flex flex-col gap-3" data-testid="collections-loading">
          {Array.from({ length: 3 }).map((_, i) => (
            <Skeleton key={i} className="h-[120px] w-full" />
          ))}
        </div>
      ) : instances.length === 0 ? (
        <Alert data-testid="collections-empty">
          <Sparkles className="size-4" />
          <AlertTitle>{t('collections.empty.title')}</AlertTitle>
          <AlertDescription className="text-tx-secondary">
            {t('collections.empty.description')}
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
