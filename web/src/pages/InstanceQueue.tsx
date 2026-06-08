import { useCallback, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useParams, useNavigate } from 'react-router-dom';
import { useQueryClient } from '@tanstack/react-query';
import { AlertTriangle } from 'lucide-react';
import { toast } from 'sonner';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Skeleton } from '@/components/ui/skeleton';
import { useSetPageTitle } from '@/components/shell/page-title-context';
import { QueueHeader } from '@/components/queue/QueueHeader';
import { QueueStatsStrip } from '@/components/queue/QueueStatsStrip';
import { QueueToolbar } from '@/components/queue/QueueToolbar';
import { QueueRow } from '@/components/queue/QueueRow';
import { QueueEmptyState } from '@/components/queue/QueueEmptyState';
import { QueueSweepCTA } from '@/components/queue/QueueSweepCTA';
import { QueueSeasonDrill } from '@/components/queue/QueueSeasonDrill';
import {
  useMissing, selectQueueRows, type MissingSeries, type QueueSort,
} from '@/lib/missing';
import { useTriggerScan, firstScanRunId, NoScanStartedError } from '@/lib/scan-mutations';
import { useInstances } from '@/lib/instances';
import { useInstanceDetail } from '@/lib/instances-mutations';
import { ApiError } from '@/lib/api';

export function InstanceQueue() {
  const { t } = useTranslation();
  useSetPageTitle(t('instanceQueue.title'));
  const { name } = useParams<{ name: string }>();
  const navigate = useNavigate();
  const qc = useQueryClient();

  const missing = useMissing(name);
  const instances = useInstances();
  const instanceDetail = useInstanceDetail(name ?? null);
  const uiUrl = instanceDetail.data?.detail.ui_url;
  const trigger = useTriggerScan();

  const inst = instances.data?.instances?.find((i) => i.name === name);
  const mode: 'auto' | 'manual' = inst?.mode === 'manual' ? 'manual' : 'auto';

  const [q, setQ] = useState('');
  const [sort, setSort] = useState<QueueSort>('debt');
  // Map<series_id, season_number> — multi-row drill open state.
  const [openSeasons, setOpenSeasons] = useState<ReadonlyMap<number, number>>(
    () => new Map(),
  );
  // Lazy "now" anchor: only consulted before the first fetch resolves
  // (after that `missing.dataUpdatedAt` is truthy and wins). Captured
  // once at mount so the QueueHeader gets a stable updatedAtMs prop.
  const [mountNow] = useState<number>(() => Date.now());

  const items = useMemo<readonly MissingSeries[]>(
    () => missing.data?.items ?? [],
    [missing.data],
  );
  const rows = useMemo(() => selectQueueRows(items, q, sort), [items, q, sort]);

  const totalBacklog = items.length;
  const totalEpisodes = useMemo(
    () => items.reduce((acc, s) => acc + (s.total_missing_aired ?? 0), 0),
    [items],
  );

  const onRefresh = useCallback(() => {
    if (!name) return;
    qc.invalidateQueries({ queryKey: ['missing', name] });
  }, [qc, name]);

  const onSeasonToggle = useCallback((seriesId: number, seasonNumber: number) => {
    setOpenSeasons((prev) => {
      const next = new Map(prev);
      if (next.get(seriesId) === seasonNumber) {
        next.delete(seriesId);
      } else {
        next.set(seriesId, seasonNumber);
      }
      return next;
    });
  }, []);

  const onScan = useCallback(
    (row: MissingSeries) => {
      if (!name || row.series_id === undefined) return;
      const seriesId = row.series_id;
      trigger.mutate(
        { instance: name, series_ids: [seriesId] },
        {
          onSuccess: (resp) => {
            try {
              const runId = firstScanRunId(resp);
              toast.success(t('instanceQueue.toast.scanningSeries', {
                title: row.title ?? `#${seriesId}`,
              }));
              navigate(`/scans/${runId}`);
            } catch (err) {
              if (err instanceof NoScanStartedError) {
                toast.error(t('instanceQueue.errors.noScanId'));
              } else {
                throw err;
              }
            }
          },
          onError: (err) => {
            if (err.status === 409) {
              toast.error(t('instanceQueue.errors.alreadyRunning', { name }));
            } else if (err.status === 404) {
              toast.error(t('instanceQueue.errors.unknownInstance', { name }));
            } else {
              toast.error(t('instanceQueue.errors.scanFailed', { error: err.message }));
            }
          },
        },
      );
    },
    [name, trigger, navigate, t],
  );

  if (!name) {
    return (
      <div>
        <Alert variant="destructive">
          <AlertTriangle className="w-4 h-4" />
          <AlertTitle>{t('instanceQueue.errors.missingName')}</AlertTitle>
          <AlertDescription>{t('instanceQueue.errors.malformedUrl')}</AlertDescription>
        </Alert>
      </div>
    );
  }

  const inFlightId =
    trigger.isPending && trigger.variables?.series_ids?.[0];

  return (
    <div data-testid="queue-page">
      <QueueHeader
        name={name}
        mode={mode}
        updatedAtMs={missing.dataUpdatedAt || mountNow}
        isFetching={missing.isFetching}
        onRefresh={onRefresh}
      />

      {missing.isError && (
        <Alert variant="destructive" className="mb-4">
          <AlertTriangle className="w-4 h-4" />
          <AlertTitle>
            {missing.error instanceof ApiError && missing.error.status === 404
              ? t('instanceQueue.errors.unknownInstance', { name })
              : t('instanceQueue.errors.loadFailed')}
          </AlertTitle>
          <AlertDescription className="font-mono text-[12px]">
            {missing.error?.message ?? ''}
          </AlertDescription>
        </Alert>
      )}

      {!missing.isError && (
        <QueueStatsStrip
          backlogCount={totalBacklog}
          missingEpisodes={totalEpisodes}
          monitoredTotal={undefined}
        />
      )}

      {!missing.isError && missing.isPending && (
        <div className="flex flex-col gap-2.5">
          {Array.from({ length: 4 }).map((_, i) => (
            <Skeleton key={i} className="h-[100px] w-full rounded-lg" />
          ))}
        </div>
      )}

      {!missing.isError && !missing.isPending && items.length === 0 && (
        <QueueEmptyState />
      )}

      {!missing.isError && !missing.isPending && items.length > 0 && (
        <>
          <QueueToolbar q={q} sort={sort} onQChange={setQ} onSortChange={setSort} />
          <div className="flex flex-col gap-2.5" data-testid="queue-list">
            {rows.map((row) => {
              const sid = row.series_id ?? 0;
              const openSeason = openSeasons.get(sid) ?? null;
              const isInFlight = Boolean(
                inFlightId !== undefined && inFlightId !== false && inFlightId === sid,
              );
              return (
                <QueueRow
                  key={sid}
                  row={row}
                  instanceUiUrl={uiUrl}
                  openSeason={openSeason}
                  isInFlight={isInFlight}
                  onSeasonToggle={(season) => onSeasonToggle(sid, season)}
                  onScan={() => onScan(row)}
                  drillSlot={
                    openSeason !== null && row.series_id !== undefined ? (
                      <QueueSeasonDrill
                        instanceName={name}
                        seriesId={row.series_id}
                        seasonNumber={openSeason}
                        isScanInFlight={isInFlight}
                        onScanSeason={() => onScan(row)}
                      />
                    ) : null
                  }
                />
              );
            })}
          </div>
          <QueueSweepCTA backlogCount={totalBacklog} />
        </>
      )}
    </div>
  );
}
