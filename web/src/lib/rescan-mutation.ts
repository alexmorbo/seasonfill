import { useMutation, useQueryClient } from '@tanstack/react-query';
import { toast } from 'sonner';
import i18n from '@/i18n';
import { api, ApiError } from './api';
import type { ScanTriggerItem } from './scan-mutations';

export interface RescanDecisionInput {
  readonly decisionId: string;
}

// Backend dispatches the rescan as a background scan (per-instance
// single-flight) and returns the same ScanTriggerItem[] shape as
// POST /scan. The caller drives navigation to /scans/<run-id> via a
// per-mutate onSuccess so the running-spinner + 2 s polling UI takes
// over identically to a regular scan trigger.
export function useRescanDecision() {
  const qc = useQueryClient();
  return useMutation<readonly ScanTriggerItem[], ApiError, RescanDecisionInput>({
    mutationFn: ({ decisionId }) =>
      api<readonly ScanTriggerItem[]>(`/decisions/${decisionId}/rescan`, {
        method: 'POST',
      }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['decisions'] });
      qc.invalidateQueries({ queryKey: ['scans'] });
      qc.invalidateQueries({ queryKey: ['instances'] });
      toast.success(i18n.t('toasts.rescanStarted'));
    },
    onError: (err) => {
      if (err.status === 409) {
        if (err.message.startsWith('decision already superseded')) {
          toast.error(i18n.t('toasts.alreadyRescanned'));
        } else if (err.message.startsWith('decision already executed')) {
          toast.error(i18n.t('toasts.alreadyGrabbedSonarr'));
        } else {
          // SCAN_IN_PROGRESS or any other 409 from the new conflict envelope.
          toast.error(err.message);
        }
        return;
      }
      toast.error(i18n.t('toasts.rescanFailed', { error: err.message }));
    },
  });
}
