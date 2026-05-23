import { useMutation, useQueryClient } from '@tanstack/react-query';
import { toast } from 'sonner';
import { api, ApiError } from './api';
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
