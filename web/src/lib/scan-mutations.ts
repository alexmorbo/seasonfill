import { useEffect, useRef } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { api, ApiError } from './api';
import { useInstanceLatestScan } from './scans';
import type { components } from '@/api/schema';

export type ScanTriggerRequest = components['schemas']['dto.ScanTriggerRequest'];
export type ScanTriggerItem = components['schemas']['dto.ScanTriggerItem'];

export class NoScanStartedError extends Error {
  constructor() {
    super('server returned an empty scan list');
    this.name = 'NoScanStartedError';
  }
}

export function useTriggerScan() {
  const qc = useQueryClient();
  return useMutation<readonly ScanTriggerItem[], ApiError, ScanTriggerRequest>({
    mutationFn: (req) =>
      api<readonly ScanTriggerItem[]>('/scan', { method: 'POST', body: req }),
    onSuccess: (_data, vars) => {
      qc.invalidateQueries({ queryKey: ['scans'] });
      qc.invalidateQueries({ queryKey: ['instances'] });
      // 010b: when scan targets a specific instance, refresh that
      // instance's missing-list so post-scan re-evaluation is visible
      // on the queue page once the user navigates back.
      if (vars.instance) {
        qc.invalidateQueries({ queryKey: ['missing', vars.instance] });
      }
    },
  });
}

export function firstScanRunId(items: readonly ScanTriggerItem[]): string {
  const first = items[0];
  if (!first?.scan_run_id) throw new NoScanStartedError();
  return first.scan_run_id;
}

export interface CancelScanInput {
  readonly id: string;
}

/**
 * useForceScanButton wraps useTriggerScan + useInstanceLatestScan into a
 * single hook for the Force-scan button on InstanceHero. It tracks the
 * "is a scan currently in flight for this instance" state by polling the
 * latest run (6 s while running, off otherwise) and surfaces:
 *
 * - `isRunning` — latest scan run for the instance is in `running` state,
 *   either because we just POSTed (server returned status='running' or
 *   202), or because a scan was already in flight when the page mounted.
 * - `isPending`  — the mutation itself is in-flight (fetch round-trip).
 * - `disabled`   — `isPending || isRunning`. The click handler also
 *   no-ops when this is true, so rapid clicks can't pile up 409s.
 * - `start`      — fires the mutation; surfaces toasts for the three
 *   states the operator cares about: success start, 409 already-running,
 *   and arbitrary failure.
 *
 * Completion is detected via a useEffect that watches latestScan.status
 * for a `running → terminal` transition and fires the correct toast.
 * The local ref prevents firing on initial mount (no previous status).
 */
export function useForceScanButton(instance: string) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const trigger = useTriggerScan();
  const latest = useInstanceLatestScan(instance);
  const status = latest.data?.status;
  const isRunning = status === 'running';

  // Track the previous status so we can fire a "completed"/"failed"
  // toast exactly once on the running→terminal transition. We also
  // need to gate the toast on whether the user actually triggered the
  // scan from this hook (no toast if some other surface ran it).
  const prevStatusRef = useRef<string | undefined>(undefined);
  const ownedRef = useRef(false);

  useEffect(() => {
    const prev = prevStatusRef.current;
    prevStatusRef.current = status;
    if (!ownedRef.current) return;
    if (prev !== 'running') return;
    if (status === 'completed') {
      toast.success(t('instances.hero.forceScan.toastFinished'));
      ownedRef.current = false;
    } else if (status === 'failed' || status === 'aborted') {
      toast.error(t('instances.hero.forceScan.toastFailed'));
      ownedRef.current = false;
    } else if (status === 'cancelled') {
      ownedRef.current = false;
    }
  }, [status, t]);

  const disabled = trigger.isPending || isRunning;

  const start = () => {
    if (disabled || !instance) return;
    trigger.mutate(
      { instance },
      {
        onSuccess: () => {
          ownedRef.current = true;
          toast.success(t('instances.hero.forceScan.toastStarted', { instance }));
        },
        onError: (err) => {
          if (err instanceof ApiError && err.status === 409) {
            // Adopt the in-flight scan so we still fire the completion
            // toast when it finishes — and so the button stays busy.
            // Invalidate the latest-run cache; the regular trigger
            // success path does this, but errors don't, so we'd never
            // pick up the in-flight scan otherwise.
            ownedRef.current = true;
            qc.invalidateQueries({ queryKey: ['scans', 'latest', instance] });
            toast.error(t('instances.hero.forceScan.toastAlreadyRunning'));
            return;
          }
          const msg = err instanceof Error ? err.message : String(err);
          toast.error(t('instances.hero.forceScan.toastError', { error: msg }));
        },
      },
    );
  };

  return {
    start,
    isRunning,
    isPending: trigger.isPending,
    disabled,
    latestStatus: status,
  };
}

export function useCancelScan() {
  const qc = useQueryClient();
  return useMutation<{ ok: true }, ApiError, CancelScanInput>({
    mutationFn: ({ id }) =>
      api<{ ok: true }>(`/scans/${id}/cancel`, { method: 'POST' }),
    onSuccess: (_data, vars) => {
      // Polling on /scans/:id picks up status="cancelled" within one
      // 2 s tick; explicit invalidation makes it instant on detail + list.
      qc.invalidateQueries({ queryKey: ['scans'] });
      qc.invalidateQueries({ queryKey: ['scan', vars.id] });
      toast.success('Scan cancellation requested');
    },
    onError: (err) => {
      if (err.status === 404) {
        // 2 s poll already transitioned the scan to terminal before the
        // POST landed — informational, not an error.
        toast.message('Scan already finished');
        return;
      }
      toast.error(`Cancel failed: ${err.message}`);
    },
  });
}
