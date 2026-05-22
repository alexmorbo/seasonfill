import { useMutation, useQueryClient } from '@tanstack/react-query';
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
