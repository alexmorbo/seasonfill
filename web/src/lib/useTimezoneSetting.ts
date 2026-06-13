// Mutation hook for PATCH /api/v1/settings/timezone. Read side lives in
// TimezoneProvider (lib/timezone.tsx) — the Settings page reads the same
// cached query via useTimezoneState().
//
// On success we update the cache directly with the server's authoritative
// response (so requires_restart flips on the same render that toast fires)
// AND invalidate so any other consumer that did its own .useQuery refetches.

import { useMutation, useQueryClient } from '@tanstack/react-query';
import { toast } from 'sonner';
import { api, ApiError } from './api';
import { timezoneSettingKey, type TimezoneState } from './timezone';

interface TimezonePatchBody {
  timezone: string;
}

interface TimezoneApiResponse {
  timezone?: string;
  source?: string;
  requires_restart?: boolean;
}

export function useUpdateTimezone() {
  const qc = useQueryClient();
  return useMutation<TimezoneState, ApiError, string>({
    mutationFn: async (timezone) => {
      const body: TimezonePatchBody = { timezone };
      const res = await api<TimezoneApiResponse>('/settings/timezone', {
        method: 'PATCH',
        body,
      });
      const tz = (res.timezone ?? timezone).trim() || timezone;
      const source: TimezoneState['source'] =
        res.source === 'db' || res.source === 'env' || res.source === 'default'
          ? res.source
          : 'fallback';
      return {
        timezone: tz,
        source,
        requiresRestart: Boolean(res.requires_restart),
      };
    },
    onSuccess: (state) => {
      qc.setQueryData<TimezoneState>(timezoneSettingKey, state);
      qc.invalidateQueries({ queryKey: timezoneSettingKey });
      toast.success('Timezone updated');
    },
    onError: (err) => {
      if (err.status === 400) {
        toast.error(err.message || 'Invalid timezone');
        return;
      }
      toast.error(`Save failed: ${err.message}`);
    },
  });
}
