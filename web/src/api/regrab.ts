import { useMutation, useQueryClient } from '@tanstack/react-query';
import { toast } from 'sonner';
import i18n from '@/i18n';
import { api, ApiError } from '@/lib/api';
import type { components } from '@/api/schema';

export type SeriesRefreshResponse =
  components['schemas']['dto.SeriesRefreshResponse'];

export async function regrabSeries(
  seriesId: number,
): Promise<SeriesRefreshResponse> {
  return api<SeriesRefreshResponse>(`/series/${seriesId}/regrab`, {
    method: 'POST',
  });
}

export function useRegrabSeries() {
  const qc = useQueryClient();
  return useMutation<SeriesRefreshResponse, ApiError, { seriesId: number }>({
    mutationFn: ({ seriesId }) => regrabSeries(seriesId),
    onSuccess: (_data, vars) => {
      // Invalidate every per-series surface so the next render
      // pulls fresh state for the row whose grab just got
      // re-queued. We avoid invalidating the cache list here —
      // the underlying series_cache row only changes once Sonarr
      // re-emits its OnGrab webhook, which fires its own
      // invalidate via the existing webhook flow.
      qc.invalidateQueries({ queryKey: ['series-detail', vars.seriesId] });
      qc.invalidateQueries({ queryKey: ['series-torrents', vars.seriesId] });
      qc.invalidateQueries({ queryKey: ['series-cast', vars.seriesId] });
      qc.invalidateQueries({ queryKey: ['series-season', vars.seriesId] });
      qc.invalidateQueries({ queryKey: ['grabs'] });
      toast.success(i18n.t('toasts.regrabQueued'));
    },
    onError: (err) => {
      toast.error(
        i18n.t('toasts.regrabFailed', { error: err.message }),
      );
    },
  });
}
