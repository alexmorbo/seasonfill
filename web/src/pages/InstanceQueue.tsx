import { useCallback } from 'react';
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
import { SkeletonRows } from '@/components/SkeletonRows';
import { StatusBadge } from '@/components/StatusBadge';
import { StatCard } from '@/components/StatCard';
import { useMissing, type MissingSeries } from '@/lib/missing';
import { useTriggerScan, firstScanRunId, NoScanStartedError } from '@/lib/scan-mutations';
import { useInstances } from '@/lib/instances';
import { ApiError } from '@/lib/api';
import { relativeTime } from '@/lib/format';
import { cn } from '@/lib/utils';

export function InstanceQueue() {
  const { name } = useParams<{ name: string }>();
  const navigate = useNavigate();
  const qc = useQueryClient();

  const missing = useMissing(name);
  const instances = useInstances();
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
              toast.success(`Scanning ${s.title ?? `#${seriesId}`}`);
              navigate(`/scans/${runId}`);
            } catch (err) {
              if (err instanceof NoScanStartedError) {
                toast.error('Server accepted the request but returned no scan id');
              } else {
                throw err;
              }
            }
          },
          onError: (err) => {
            if (err.status === 409) {
              toast.error(`Scan already running for ${name}`);
            } else if (err.status === 404) {
              toast.error(`Unknown instance ${name}`);
            } else {
              toast.error(`Failed to start scan: ${err.message}`);
            }
          },
        },
      );
    },
    [name, trigger, navigate],
  );

  if (!name) {
    return (
      <div className="max-w-[1440px] mx-auto p-6">
        <Alert variant="destructive">
          <AlertTriangle className="w-4 h-4" />
          <AlertTitle>Missing instance name</AlertTitle>
          <AlertDescription>The URL is malformed.</AlertDescription>
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
        <ArrowLeft className="w-3.5 h-3.5 mr-1" /> Back to instances
      </Button>

      <header className="flex flex-col gap-1.5">
        <div className="flex items-center gap-3 flex-wrap">
          <h1 className="text-[22px] font-semibold tracking-tight">
            Queue · <span className="font-mono font-medium">{name}</span>
          </h1>
          <StatusBadge value={mode === 'manual' ? 'pending' : 'completed'} />
          <span className="font-mono text-[11px] text-faint">
            mode: {mode}
          </span>
          <span className="font-mono text-[11px] text-faint">
            updated {relativeTime(missing.dataUpdatedAt || Date.now())}
          </span>
          <Button
            variant="outline"
            size="sm"
            className="h-7 ml-auto"
            onClick={onRefresh}
            disabled={missing.isFetching}
            aria-label="Refresh queue"
          >
            <RefreshCw
              className={cn(
                'w-3.5 h-3.5 mr-1',
                missing.isFetching && 'animate-spin',
              )}
            />
            Refresh
          </Button>
        </div>
        <p className="text-[12.5px] text-muted max-w-prose">
          Monitored series in <span className="font-mono">{name}</span> with
          aired episodes that have no file on disk. Counts come from
          Sonarr's per-series statistics; numbers may flicker briefly mid-
          import. Use "Scan now" to evaluate a single series end-to-end.
        </p>
      </header>

      {missing.isError && (
        <Alert variant="destructive">
          <AlertTriangle className="w-4 h-4" />
          <AlertTitle>
            {missing.error instanceof ApiError && missing.error.status === 404
              ? `Unknown instance ${name}`
              : 'Failed to load queue'}
          </AlertTitle>
          <AlertDescription className="font-mono text-[12px]">
            {missing.error?.message ?? 'unknown error'}
          </AlertDescription>
        </Alert>
      )}

      {!missing.isError && (
        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3">
          <StatCard label="Series with backlog" value={total} />
          <StatCard label="Episodes missing" value={totalEpisodes} />
          <StatCard
            label="Mode"
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
            Missing-aired series{' '}
            <span className="text-faint font-mono text-[11px] ml-2">
              {missing.isPending ? 'loading…' : `${items.length} rows`}
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
              title="No missing-aired episodes"
              body="Every monitored series in this instance has all aired episodes on disk. Nothing to scan right now."
            />
          )}
          {!missing.isPending && !missing.isError && items.length > 0 && (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead className="w-[40%]">Series</TableHead>
                  <TableHead className="w-[15%] text-right">Missing</TableHead>
                  <TableHead>Seasons</TableHead>
                  <TableHead className="w-[140px] text-right">Action</TableHead>
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
                          <span className="font-medium">{s.title ?? '—'}</span>
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
                              aria-label={`Season ${sea.season_number}: ${sea.missing_aired_count} missing`}
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
                          aria-label={`Scan ${s.title ?? `series ${seriesId}`} now`}
                        >
                          {isInFlight ? (
                            <Loader2 className="w-3.5 h-3.5 mr-1 animate-spin" />
                          ) : (
                            <PlayCircle className="w-3.5 h-3.5 mr-1" />
                          )}
                          {isInFlight ? 'Scanning…' : 'Scan now'}
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
        Need to sweep every series?{' '}
        <Link to="/scans?new=1" className="underline hover:text-foreground">
          Trigger a full-instance scan →
        </Link>
      </div>
    </div>
  );
}
