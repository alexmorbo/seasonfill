import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { toast } from 'sonner';
import i18n from '@/i18n';
import { api, ApiError } from '@/lib/api';
import type { components } from '@/api/schema';

export type FollowListResponse =
  components['schemas']['rest.followListResponse'];
export type FollowedItem =
  components['schemas']['rest.followedItemResponse'];

const FOLLOW_KEY = ['follow'] as const;

// --- reads ---------------------------------------------------------------

export async function listFollowed(lang?: string): Promise<FollowListResponse> {
  const qs = lang ? `?lang=${encodeURIComponent(lang)}` : '';
  return api<FollowListResponse>(`/follow${qs}`);
}

// useFollowed returns the full watchlist (Dashboard «Слежу» row).
export function useFollowed(lang?: string) {
  return useQuery({
    queryKey: [...FOLLOW_KEY, lang ?? 'en-US'],
    queryFn: () => listFollowed(lang),
    staleTime: 60_000,
  });
}

// useFollowedIds derives a Set<number> of followed series_ids for O(1)
// button/badge membership checks. Shares the same query cache as useFollowed.
export function useFollowedIds(lang?: string): Set<number> {
  const { data } = useFollowed(lang);
  return new Set(
    (data?.items ?? [])
      .map((i) => i.series_id)
      .filter((id): id is number => typeof id === 'number'),
  );
}

// --- writes --------------------------------------------------------------

export async function followSeries(seriesId: number): Promise<void> {
  await api<void>(`/follow`, {
    method: 'POST',
    body: { series_id: seriesId },
  });
}

export async function unfollowSeries(seriesId: number): Promise<void> {
  await api<void>(`/follow/${seriesId}`, { method: 'DELETE' });
}

export function useFollowSeries() {
  const qc = useQueryClient();
  return useMutation<void, ApiError, { seriesId: number }>({
    mutationFn: ({ seriesId }) => followSeries(seriesId),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: FOLLOW_KEY });
      toast.success(i18n.t('toasts.followed'));
    },
    onError: (err) =>
      toast.error(i18n.t('toasts.followFailed', { error: err.message })),
  });
}

export function useUnfollowSeries() {
  const qc = useQueryClient();
  return useMutation<void, ApiError, { seriesId: number }>({
    mutationFn: ({ seriesId }) => unfollowSeries(seriesId),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: FOLLOW_KEY });
      toast.success(i18n.t('toasts.unfollowed'));
    },
    onError: (err) =>
      toast.error(i18n.t('toasts.unfollowFailed', { error: err.message })),
  });
}
