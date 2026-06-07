import {
  useMutation,
  useQuery,
  useQueryClient,
  type UseQueryResult,
} from '@tanstack/react-query';
import { toast } from 'sonner';
import i18n from '@/i18n';
import { ApiError, api } from '@/lib/api';
import { watchdogRollupsKey } from './watchdogRollups';

// Local mirror of PRD §3 B5 / 047b DTO. Swap for
// `components['schemas']['dto.WatchdogBlacklistItem']` once schema.ts
// is regenerated — same JSON, `import type` rename only.
export interface WatchdogBlacklistItem {
  id: number;
  instance_name: string;
  series_id: number;
  series_title: string;
  season_number: number;
  reason: 'consecutive_no_better' | 'manual' | string;
  source: 'auto' | 'manual';
  consecutive: number;
  created_at: string;
  expires_at?: string;
}

export interface WatchdogBlacklistList {
  items: WatchdogBlacklistItem[];
  next_cursor?: string;
}

export const watchdogBlacklistKey = (instance: string) =>
  ['watchdog', 'blacklist', instance] as const;

export function useWatchdogBlacklist(
  instance: string,
  opts: { enabled?: boolean } = {},
): UseQueryResult<WatchdogBlacklistList, ApiError> {
  return useQuery<WatchdogBlacklistList, ApiError>({
    queryKey: watchdogBlacklistKey(instance),
    queryFn: () =>
      api<WatchdogBlacklistList>(
        `/instances/${encodeURIComponent(instance)}/watchdog/blacklist`,
      ),
    enabled: opts.enabled !== false && Boolean(instance),
    refetchInterval: 60_000,
    staleTime: 30_000,
    refetchOnWindowFocus: false,
  });
}

export interface UnBlacklistInput {
  instance: string;
  id: number;
  // For the localised toast — the cache may have already been mutated
  // by the time `onSuccess` fires, so we keep the labels in input.
  seriesTitle: string;
  seasonNumber: number;
}

interface OptimisticContext {
  previous: WatchdogBlacklistList | undefined;
}

// DELETE with optimistic cache update (TanStack v5 pattern):
// onMutate snapshots + mutates, onError rolls back, onSettled
// invalidates rollups + list + 052a's activity-blacklist key.
export function useUnBlacklist() {
  const qc = useQueryClient();
  return useMutation<void, ApiError, UnBlacklistInput, OptimisticContext>({
    mutationFn: async ({ instance, id }) => {
      await api<void>(
        `/instances/${encodeURIComponent(instance)}/watchdog/blacklist/${id}`,
        { method: 'DELETE' },
      );
    },
    onMutate: async ({ instance, id }) => {
      await qc.cancelQueries({ queryKey: watchdogBlacklistKey(instance) });
      const previous = qc.getQueryData<WatchdogBlacklistList>(
        watchdogBlacklistKey(instance),
      );
      if (previous) {
        qc.setQueryData<WatchdogBlacklistList>(
          watchdogBlacklistKey(instance),
          { ...previous, items: previous.items.filter((r) => r.id !== id) },
        );
      }
      return { previous };
    },
    onError: (err, vars, ctx) => {
      if (ctx?.previous) {
        qc.setQueryData(watchdogBlacklistKey(vars.instance), ctx.previous);
      }
      // 404 = row already deleted elsewhere — soft-success: re-apply
      // the removal, show success toast (server agrees row is gone).
      if (err.status === 404) {
        toast.message(
          i18n.t('watchdog.blacklist.unblacklistDone', {
            series: vars.seriesTitle,
            season: vars.seasonNumber,
          }),
        );
        const current = qc.getQueryData<WatchdogBlacklistList>(
          watchdogBlacklistKey(vars.instance),
        );
        if (current) {
          qc.setQueryData<WatchdogBlacklistList>(
            watchdogBlacklistKey(vars.instance),
            { ...current, items: current.items.filter((r) => r.id !== vars.id) },
          );
        }
        return;
      }
      toast.error(
        i18n.t('watchdog.blacklist.unblacklistFailed', { error: err.message }),
      );
    },
    onSuccess: (_data, vars) => {
      toast.success(
        i18n.t('watchdog.blacklist.unblacklistDone', {
          series: vars.seriesTitle,
          season: vars.seasonNumber,
        }),
      );
    },
    onSettled: (_data, _err, vars) => {
      qc.invalidateQueries({ queryKey: watchdogRollupsKey() });
      qc.invalidateQueries({ queryKey: watchdogBlacklistKey(vars.instance) });
      qc.invalidateQueries({
        queryKey: ['watchdog', 'activity', 'blacklist', vars.instance],
      });
    },
  });
}
