import { useMutation, useQueryClient, type UseMutationResult } from '@tanstack/react-query';
import { toast } from 'sonner';
import i18n from '@/i18n';
import { ApiError, api } from '@/lib/api';

export type TorrentActionKind = 'pause' | 'resume' | 'recheck';

export interface TorrentActionInput {
  readonly instance: string;
  readonly hash: string;
  readonly action: TorrentActionKind;
}

// Per-action success toast key. Kept explicit (not interpolated) so the
// three outcomes read naturally in both catalogs.
const OK_TOAST: Record<TorrentActionKind, string> = {
  pause: 'toasts.torrentPaused',
  resume: 'toasts.torrentResumed',
  recheck: 'toasts.torrentRechecking',
};

// useTorrentAction — POST pause/resume/recheck for one hash on one instance.
//
// The three endpoints are idempotent 200 no-ops; the response body
// (dto.TorrentActionResponse) is not needed by the UI, so we type it void
// and let `api` discard it. On success we invalidate BOTH the
// `series-torrents` AND `movie-torrents` query families (prefix
// invalidation — mirrors grab-mutation invalidating by prefix) so the row
// reflects the new state on the next poll tick without threading
// seriesId/tmdbId down here. The mutation is instance+hash keyed, so it has
// no static way to know which panel (series or movie) the row came from —
// invalidating the family that isn't mounted is a cheap no-op (react-query
// only refetches ACTIVE queries). B1.5/ADR-0023.
export function useTorrentAction(): UseMutationResult<void, ApiError, TorrentActionInput> {
  const qc = useQueryClient();
  return useMutation<void, ApiError, TorrentActionInput>({
    mutationFn: ({ instance, hash, action }) =>
      api<void>(
        `/instances/${encodeURIComponent(instance)}/torrents/${hash}/${action}`,
        { method: 'POST' },
      ),
    onSuccess: (_data, { action }) => {
      qc.invalidateQueries({ queryKey: ['series-torrents'] });
      qc.invalidateQueries({ queryKey: ['movie-torrents'] });
      toast.success(i18n.t(OK_TOAST[action]));
    },
    onError: (err) => {
      // 502 = qBit unreachable — distinct copy so the operator knows it's
      // the client, not their action, that failed.
      if (err.status === 502) {
        toast.error(i18n.t('toasts.torrentQbitDown'));
        return;
      }
      toast.error(i18n.t('toasts.torrentActionFailed', { error: err.message }));
    },
  });
}
