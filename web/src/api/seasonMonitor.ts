import { useMutation, useQueryClient } from '@tanstack/react-query';
import { toast } from 'sonner';
import i18n from '@/i18n';
import { api, ApiError } from '@/lib/api';
import { seriesLibraryQueryKey } from '@/api/seriesLibrary';
import type { components } from '@/api/schema';

export type MonitorSeasonResponse =
  components['schemas']['rest.monitorSeasonResponse'];

export interface MonitorSeasonVars {
  readonly instance: string;
  readonly seriesId: number;
  readonly seasonNumber: number;
}

// POST /instances/:name/series/:id/seasons/:season/monitor — flips the season's
// Sonarr monitored flag and (search:true) triggers a SeasonSearch. ADR-0012 S2.
export async function monitorSeason({
  instance,
  seriesId,
  seasonNumber,
}: MonitorSeasonVars): Promise<MonitorSeasonResponse> {
  return api<MonitorSeasonResponse>(
    `/instances/${encodeURIComponent(instance)}/series/${seriesId}/seasons/${seasonNumber}/monitor`,
    { method: 'POST', body: { search: true } },
  );
}

export function useMonitorSeason() {
  const qc = useQueryClient();
  return useMutation<MonitorSeasonResponse, ApiError, MonitorSeasonVars>({
    mutationFn: monitorSeason,
    onSuccess: (_data, vars) => {
      // Refresh the per-instance library counts (drives the season monitored
      // badge) and the per-season episode strip so the next render reflects
      // the newly-monitored state.
      void qc.invalidateQueries({
        queryKey: seriesLibraryQueryKey(vars.seriesId, vars.instance),
      });
      void qc.invalidateQueries({
        queryKey: ['series-seasons', vars.seriesId],
      });
      toast.success(i18n.t('seriesDetail.seasons.requestQueued'));
    },
    onError: (err) => {
      toast.error(
        i18n.t('seriesDetail.seasons.requestFailed', { error: err.message }),
      );
    },
  });
}
