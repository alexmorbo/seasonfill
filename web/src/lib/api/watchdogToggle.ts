import { useMutation, useQueryClient } from '@tanstack/react-query';
import { toast } from 'sonner';
import i18n from '@/i18n';
import { ApiError, api } from '@/lib/api';
import {
  qbitSettingsKey,
  type QbitSettingsDTO,
  type QbitSettingsUpsertRequest,
} from '@/api/qbit';
import { watchdogRollupsKey, type WatchdogRollup } from './watchdogRollups';

export interface WatchdogToggleInput {
  instance: string;
  enabled: boolean;
  current: QbitSettingsDTO;
}

function buildUpsert(
  current: QbitSettingsDTO,
  enabled: boolean,
): QbitSettingsUpsertRequest {
  return {
    enabled,
    url: current.url ?? '',
    username: current.username ?? '',
    password: '',
    category: current.category ?? '',
    poll_interval_minutes: current.poll_interval_minutes ?? 0,
    regrab_cooldown_hours: current.regrab_cooldown_hours ?? 0,
    max_consecutive_no_better: current.max_consecutive_no_better ?? 0,
    custom_unregistered_msgs: current.custom_unregistered_msgs ?? [],
  };
}

export function useWatchdogToggle() {
  const qc = useQueryClient();
  return useMutation<WatchdogRollup | null, ApiError, WatchdogToggleInput>({
    mutationFn: async ({ instance, enabled, current }) => {
      const body = buildUpsert(current, enabled);
      await api(`/instances/${encodeURIComponent(instance)}/qbit/settings`, {
        method: 'PUT',
        body,
      });
      return null;
    },
    onSuccess: (_data, vars) => {
      qc.invalidateQueries({ queryKey: watchdogRollupsKey() });
      qc.invalidateQueries({ queryKey: qbitSettingsKey(vars.instance) });
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
