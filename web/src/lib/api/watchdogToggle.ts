import { useMutation, useQueryClient } from '@tanstack/react-query';
import { toast } from 'sonner';
import i18n from '@/i18n';
import { ApiError, api } from '@/lib/api';
import { watchdogRollupsKey, type WatchdogRollup } from './watchdogRollups';

// Local mirror of the qBit settings shape. The real schema is on
// `components['schemas']['dto.QbitSettingsDTO']`; we only need the
// `enabled` toggle + the preserved fields to round-trip a PUT here.
// The full settings editor remains in InstanceFormDialog (story 040).
interface QbitSettingsPartial {
  enabled: boolean;
  url?: string;
  username?: string;
  category?: string;
  poll_interval_min?: number;
  regrab_cooldown_h?: number;
  max_no_better?: number;
}

export interface WatchdogToggleInput {
  instance: string;
  enabled: boolean;
  previous: QbitSettingsPartial; // fetched from /qbit/settings or rollup
}

// Wraps the existing PUT /instances/:name/qbit/settings call with
// watchdog-namespaced toasts and broad cache invalidation (rollups +
// per-instance settings + instances list).
export function useWatchdogToggle() {
  const qc = useQueryClient();
  return useMutation<WatchdogRollup | null, ApiError, WatchdogToggleInput>({
    mutationFn: async ({ instance, enabled, previous }) => {
      const body: QbitSettingsPartial = { ...previous, enabled };
      await api(`/instances/${encodeURIComponent(instance)}/qbit/settings`, {
        method: 'PUT',
        body,
      });
      return null;
    },
    onSuccess: (_data, vars) => {
      qc.invalidateQueries({ queryKey: watchdogRollupsKey() });
      qc.invalidateQueries({ queryKey: ['qbit', 'settings', vars.instance] });
      qc.invalidateQueries({ queryKey: ['instances'] });
      toast.success(
        i18n.t('watchdog.toggleSuccess', {
          state: vars.enabled
            ? i18n.t('watchdog.config.toggle.on')
            : i18n.t('watchdog.config.toggle.off'),
        }),
      );
    },
    onError: (err) => {
      toast.error(i18n.t('watchdog.toggleFailed', { error: err.message }));
    },
  });
}
