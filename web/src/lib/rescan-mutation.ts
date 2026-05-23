import { useMutation, useQueryClient } from '@tanstack/react-query';
import { toast } from 'sonner';
import { api, ApiError } from './api';
import type { Decision } from './decisions';

export interface RescanDecisionInput {
  readonly decisionId: string;
}

// Toast-as-side-effect lives in the hook (mirror grab-mutation).
// 409 branches map to specific user-actionable messages; everything
// else falls through to a generic toast.
export function useRescanDecision() {
  const qc = useQueryClient();
  return useMutation<Decision, ApiError, RescanDecisionInput>({
    mutationFn: ({ decisionId }) =>
      api<Decision>(`/decisions/${decisionId}/rescan`, { method: 'POST' }),
    onSuccess: () => {
      // Decisions list (drawer rows), scan detail counters, and the
      // running scan summary all need refresh. Scan counters do NOT
      // change (017 §3.4: same scan_run_id, no new ScanRecord) but
      // invalidating /scans is cheap and guards against a stale
      // grabs/decisions ratio when the operator rescans during a
      // running scan.
      qc.invalidateQueries({ queryKey: ['decisions'] });
      qc.invalidateQueries({ queryKey: ['scans'] });
      qc.invalidateQueries({ queryKey: ['scan'] });
      toast.success('Rescan dispatched');
    },
    onError: (err) => {
      if (err.status === 409) {
        if (err.message.startsWith('decision already superseded')) {
          toast.error('Already rescanned — open the successor');
        } else if (err.message.startsWith('decision already executed')) {
          toast.error('Already grabbed against Sonarr — create a new scan');
        } else {
          toast.error(err.message);
        }
        return;
      }
      toast.error(`Rescan failed: ${err.message}`);
    },
  });
}
