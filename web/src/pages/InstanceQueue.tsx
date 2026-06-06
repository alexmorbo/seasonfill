import { useCallback } from 'react';
import { useTranslation } from 'react-i18next';
import { useParams, useNavigate, Link } from 'react-router-dom';
import { useQueryClient } from '@tanstack/react-query';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Button } from '@/components/ui/button';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import {
  Table, TableBody, TableCell, TableHead, TableHeader, TableRow,
} from '@/components/ui/table';
import {
  ArrowLeft, AlertTriangle, RefreshCw, PlayCircle, Loader2,
} from 'lucide-react';
import { toast } from 'sonner';
import { EmptyState } from '@/components/EmptyState';
import { SeriesTitleLink } from '@/components/SeriesTitleLink';
import { SkeletonRows } from '@/components/SkeletonRows';
import { StatusBadge } from '@/components/StatusBadge';
import { StatCard } from '@/components/StatCard';
import { useMissing, type MissingSeries } from '@/lib/missing';
import { useTriggerScan, firstScanRunId, NoScanStartedError } from '@/lib/scan-mutations';
import { useInstances } from '@/lib/instances';
import { useInstanceDetail } from '@/lib/instances-mutations';
import { ApiError } from '@/lib/api';
import { relativeTime } from '@/lib/format';
import { cn } from '@/lib/utils';

export function InstanceQueue() {
  const { t } = useTranslation();
  const { name } = useParams<{ name: string }>();
  const navigate = useNavigate();
  const qc = useQueryClient();

  const missing = useMissing(name);
  const instances = useInstances();
  const instanceDetail = useInstanceDetail(name ?? null);
  const uiUrl = instanceDetail.data?.detail.ui_url;
  const trigger = useTriggerScan();

  const inst = instances.data?.instances?.find((i) => i.name === name);
  const mode = inst?.mode ?? 'auto';

  const onRefresh = useCallback(() => {
    if (!name) return;
    qc.invalidateQueries({ queryKey: ['missing', name] });
  }, [qc, name]);

  const onScan = useCallback(
    (s: MissingSeries) => {
      if (!name || s.series_id === undefined) return;
      const seriesId = s.series_id;
      trigger.mutate(
        { instance: name, series_ids: [seriesId] },
        {
          onSuccess: (items) => {
            try {
              const runId = firstScanRunId(items);
              toast.success(t('instanceQueue.scanning', { title: s.title ?? `#${seriesId}` }));
              navigate(`/scans/${runId}`);
            } catch (err) {
              if (err instanceof NoScanStartedError) {
                toast.error(t('instanceQueue.noScanId'));
              } else {
                throw err;
              }
            }
          },
          onError: (err) => {
            if (err.status === 409) {
              toast.error(t('instanceQueue.alreadyRunning', { name }));
            } else if (err.status === 404) {
              toast.error(t('instanceQueue.unknownInstance', { name }));
            } else {
              toast.error(t('instanceQueue.scanFailed', { error: err.message }));
            }
          },
        },
      );
    },
    [name, trigger, navigate, t],
  );

  if (!name) {
    return (
      <div className="max-w-[1440px] mx-auto p-6">
        <Alert variant="destructive">
          <AlertTriangle className="w-4 h-4" />
          <AlertTitle>{t('instanceQueue.missingName')}</AlertTitle>
          <AlertDescription>{t('instanceQueue.malformedUrl')}</AlertDescription>
        </Alert>
      </div>
    );
  }

  const items: readonly MissingSeries[] = missing.data?.items ?? [];
  const total = missing.data?.total ?? items.length;
  const totalEpisodes = items.reduce(
    (acc, s) => acc + (s.total_missing_aired ?? 0),
    0,
  );

  // Track in-flight series-id for inline spinner / disabled state. The
  // mutation is single-flight (no concurrent triggers), so the latest
  // variables identify the active row.
  const inFlightId =
    trigger.isPending && trigger.variables?.series_ids?.[0];

  return (
    <div className="max-w-[1440px] mx-auto p-6 flex flex-col gap-5">
      <Button
        variant="ghost"
        size="sm"
        className="self-start -ml-2 h-8"
        onClick={() => navigate('/instances')}
      >
        <ArrowLeft className="w-3.5 h-3.5 mr-1" /> {t('instanceQueue.back')}
      </Button>

      <header className="flex flex-col gap-1.5">
        <div className="flex items-center gap-3 flex-wrap">
          <h1 className="text-[22px] font-semibold tracking-tight">
            {t('instanceQueue.title')} · <span className="font-mono font-medium">{name}</span>
          </h1>
          <StatusBadge value={mode === 'manual' ? 'pending' : 'completed'} />
          <span className="font-mono text-[11px] text-faint">
            {t('instanceQueue.mode', { mode })}
          </span>
          <span className="font-mono text-[11px] text-faint">
            {t('instanceQueue.updated', { time: relativeTime(missing.dataUpdatedAt || Date.now()) })}
          </span>
          <Button
            variant="outline"
            size="sm"
            className="h-7 ml-auto"
            onClick={onRefresh}
            disabled={missing.isFetching}
            aria-label={t('instanceQueue.refresh')}
          >
            <RefreshCw
              className={cn(
                'w-3.5 h-3.5 mr-1',
                missing.isFetching && 'animate-spin',
              )}
            />
            {t('instanceQueue.refresh')}
          </Button>
        </div>
        <p className="text-[12.5px] text-muted max-w-prose">
          {t('instanceQueue.description', { name })}
        </p>
      </header>

      {missing.isError && (
        <Alert variant="destructive">
          <AlertTriangle className="w-4 h-4" />
          <AlertTitle>
            {missing.error instanceof ApiError && missing.error.status === 404
              ? t('instanceQueue.unknownInstance', { name })
              : t('instanceQueue.loadFailed')}
          </AlertTitle>
          <AlertDescription className="font-mono text-[12px]">
            {missing.error?.message ?? t('common.unknown').toLowerCase()}
          </AlertDescription>
        </Alert>
      )}

      {!missing.isError && (
        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3">
          <StatCard label={t('instanceQueue.cards.backlog')} value={total} />
          <StatCard label={t('instanceQueue.cards.missing')} value={totalEpisodes} />
          <StatCard
            label={t('instanceQueue.cards.mode')}
            value={
              <span className="text-[18px] lowercase">{mode}</span>
            }
            variant={mode === 'manual' ? 'warning' : 'default'}
          />
        </div>
      )}

      <Card>
        <CardHeader className="py-3">
          <CardTitle className="text-[14px] font-semibold">
            {t('instanceQueue.tableTitle')}{' '}
            <span className="text-faint font-mono text-[11px] ml-2">
              {missing.isPending ? t('instanceQueue.loadingRows') : t('instanceQueue.rows', { count: items.length })}
            </span>
          </CardTitle>
        </CardHeader>
        <CardContent className="p-0">
          {missing.isPending && (
            <Table>
              <TableBody>
                <SkeletonRows rows={5} cols={['lg', 'sm', 'xl', 'md']} />
              </TableBody>
            </Table>
          )}
          {!missing.isPending && !missing.isError && items.length === 0 && (
            <EmptyState
              title={t('instanceQueue.emptyTitle')}
              body={t('instanceQueue.emptyBody')}
            />
          )}
          {!missing.isPending && !missing.isError && items.length > 0 && (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead className="w-[40%]">{t('instanceQueue.col.series')}</TableHead>
                  <TableHead className="w-[15%] text-right">{t('instanceQueue.col.missing')}</TableHead>
                  <TableHead>{t('instanceQueue.col.seasons')}</TableHead>
                  <TableHead className="w-[140px] text-right">{t('instanceQueue.col.action')}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {items.map((s) => {
                  const seriesId = s.series_id;
                  const isInFlight =
                    inFlightId !== undefined &&
                    inFlightId !== false &&
                    inFlightId === seriesId;
                  return (
                    <TableRow key={seriesId ?? s.title}>
                      <TableCell>
                        <div className="flex flex-col gap-0.5">
                          <SeriesTitleLink
                            title={s.title ?? '—'}
                            titleSlug={s.title_slug}
                            year={s.year}
                            instanceUiUrl={uiUrl}
                          />
                          <span className="font-mono text-[11px] text-faint">
                            id: {seriesId ?? '—'}
                          </span>
                        </div>
                      </TableCell>
                      <TableCell className="text-right font-mono">
                        {s.total_missing_aired ?? 0}
                      </TableCell>
                      <TableCell>
                        <div className="flex flex-wrap gap-1">
                          {(s.seasons ?? []).map((sea) => (
                            <span
                              key={sea.season_number}
                              className="inline-flex items-center gap-1 px-1.5 h-5 rounded border border-border-faint bg-surface-2 font-mono text-[11px]"
                              aria-label={t('instanceQueue.seasonAria', { num: sea.season_number, count: sea.missing_aired_count ?? 0 })}
                            >
                              S{String(sea.season_number ?? 0).padStart(2, '0')}
                              <span className="text-faint">·</span>
                              <span>{sea.missing_aired_count ?? 0}</span>
                            </span>
                          ))}
                        </div>
                      </TableCell>
                      <TableCell className="text-right">
                        <Button
                          variant="outline"
                          size="sm"
                          className="h-7"
                          onClick={() => onScan(s)}
                          disabled={
                            isInFlight ||
                            trigger.isPending ||
                            seriesId === undefined
                          }
                          aria-label={t('instanceQueue.scanRowAria', { title: s.title ?? `series ${seriesId}` })}
                        >
                          {isInFlight ? (
                            <Loader2 className="w-3.5 h-3.5 mr-1 animate-spin" />
                          ) : (
                            <PlayCircle className="w-3.5 h-3.5 mr-1" />
                          )}
                          {isInFlight ? t('instanceQueue.scanRunning') : t('instanceQueue.scanNow')}
                        </Button>
                      </TableCell>
                    </TableRow>
                  );
                })}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>

      <div className="text-[12px] text-muted">
        {t('instanceQueue.sweepHint')}{' '}
        <Link to="/scans?new=1" className="underline hover:text-foreground">
          {t('instanceQueue.fullScanLink')}
        </Link>
      </div>
    </div>
  );
}
