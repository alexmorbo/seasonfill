import { useMutation, useQueryClient } from '@tanstack/react-query';
import { toast } from 'sonner';
import i18n from '@/i18n';
import { api, ApiError } from './api';
import type { Grab } from './grabs';

export interface GrabDecisionInput {
  readonly decisionId: string;
}

// Toast-as-side-effect lives in the hook (not the caller) because the 409
// branch needs the raw error body to distinguish "already grabbed" from
// "cooldown" — the caller would otherwise have to duplicate the same
// substring inspection. UI components stay declarative.
export function useGrabDecision() {
  const qc = useQueryClient();
  return useMutation<Grab, ApiError, GrabDecisionInput>({
    mutationFn: ({ decisionId }) =>
      api<Grab>(`/decisions/${decisionId}/grab`, { method: 'POST' }),
    onSuccess: () => {
      // Broad invalidation: list rows, drawer detail, scan counters all
      // depend on the Decision being marked "has a grab record now".
      qc.invalidateQueries({ queryKey: ['decisions'] });
      qc.invalidateQueries({ queryKey: ['grabs'] });
      qc.invalidateQueries({ queryKey: ['scans'] });
      qc.invalidateQueries({ queryKey: ['scan'] });
      toast.success(i18n.t('toasts.grabDispatched'));
    },
    onError: (err) => {
      if (err.status === 409) {
        // Backend message shape: "already grabbed: <id>" or
        // "blocked by cooldown: series:..." — substring match is OK here
        // because both prefixes are stable contract from 011a §7.
        if (err.message.startsWith('blocked by cooldown')) {
          toast.error(i18n.t('toasts.onCooldown'));
        } else if (err.message.startsWith('already grabbed')) {
          toast.error(i18n.t('toasts.alreadyGrabbed'));
        } else if (err.message.startsWith('already executed')) {
          toast.error(i18n.t('toasts.alreadyExecuted'));
        } else {
          toast.error(err.message);
        }
        return;
      }
      toast.error(i18n.t('toasts.grabFailed', { error: err.message }));
    },
  });
}
