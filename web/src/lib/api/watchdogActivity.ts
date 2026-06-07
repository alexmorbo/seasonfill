import { useQueries, type UseQueryResult } from '@tanstack/react-query';
import { ApiError, api } from '@/lib/api';

// Activity feed is derived client-side from the existing /grabs
// endpoint plus the blacklist list. See `052-watchdog-redesign.md`
// "Backend gap discovered during planning" for the rationale and the
// future follow-up Story 048 that will replace this with a single
// server-side endpoint.

export type WatchdogActivityType =
  | 'unregistered'
  | 'regrab'
  | 'better'
  | 'no_better'
  | 'blacklist';

export interface WatchdogActivityEvent {
  id: string; // deterministic key: `${type}:${grabId | blId}:${at}`
  at: string; // ISO timestamp
  type: WatchdogActivityType;
  instance: string;
  series_id: number;
  series_title: string;
  season_number: number;
  grab_id?: string | undefined; // present for regrab + better + no_better
  regrab_id?: string | undefined;
  consecutive?: number | undefined; // present for no_better + blacklist
  max_no_better?: number | undefined;
  episodes?: number | undefined; // present for better (size of new pack)
  detail_key:
    | 'regrabFound'
    | 'regrabStarted'
    | 'noBetter'
    | 'blacklistConsec'
    | 'unregistered';
}

// Mirrors the relevant subset of the /grabs row. The full DTO is
// regenerated into `web/src/api/schema.ts` by B1; we read defensively
// so 052a does not block on B1's exact field names.
interface GrabRowLike {
  id: string;
  instance: string;
  series_id: number;
  series_title?: string;
  season_number: number;
  created_at: string;
  status: string;
  replay_of_id?: string | null;
  replay_index?: number | null;
  custom_format_score?: number | null;
  parent_custom_format_score?: number | null;
  consecutive_no_better?: number | null;
  episodes_count?: number | null;
}

interface GrabListLike {
  items: GrabRowLike[];
}

interface BlacklistItemLike {
  id: number;
  instance: string;
  series_id: number;
  series_title?: string;
  season_number: number;
  reason: 'manual' | 'auto_no_better_threshold';
  consecutive: number;
  created_at: string;
}

interface BlacklistListLike {
  items: BlacklistItemLike[];
}

const TYPE_PRIORITY: Record<WatchdogActivityType, number> = {
  unregistered: 0,
  regrab: 1,
  better: 2,
  no_better: 3,
  blacklist: 4,
};

export interface UseWatchdogActivityOptions {
  instance: string;
  limit?: number;
  enabled?: boolean;
  maxNoBetter?: number; // from rollup; used to interpolate `{x}/{max}`
}

export interface WatchdogActivityResult {
  events: WatchdogActivityEvent[];
  isLoading: boolean;
  isError: boolean;
  error: ApiError | null;
}

// Hook returns a single shape (events + flags) so the component does
// not depend on tanstack-query result types. Internally it fans out two
// queries and merges them via `select`.
export function useWatchdogActivity({
  instance,
  limit = 30,
  enabled = true,
  maxNoBetter = 3,
}: UseWatchdogActivityOptions): WatchdogActivityResult {
  const results = useQueries({
    queries: [
      {
        queryKey: ['watchdog', 'activity', 'grabs', instance, limit] as const,
        queryFn: () =>
          api<GrabListLike>(
            `/grabs?instance=${encodeURIComponent(instance)}&limit=${limit}`,
          ),
        enabled: enabled && Boolean(instance),
        refetchInterval: 30_000,
        staleTime: 15_000,
        refetchOnWindowFocus: false,
      },
      {
        queryKey: ['watchdog', 'activity', 'blacklist', instance] as const,
        queryFn: () =>
          api<BlacklistListLike>(
            `/instances/${encodeURIComponent(instance)}/watchdog/blacklist?limit=10`,
          ),
        enabled: enabled && Boolean(instance),
        refetchInterval: 60_000,
        staleTime: 30_000,
        refetchOnWindowFocus: false,
      },
    ],
  });

  const [grabsQ, blQ] = results as [
    UseQueryResult<GrabListLike, ApiError>,
    UseQueryResult<BlacklistListLike, ApiError>,
  ];

  const events: WatchdogActivityEvent[] = [];

  if (grabsQ.data) {
    for (const g of grabsQ.data.items) {
      if (!g.replay_of_id) continue;
      const baseKey = `${g.id}:${g.created_at}`;
      // Each regrab implies a prior unregistered detection.
      events.push({
        id: `unregistered:${baseKey}`,
        at: g.created_at,
        type: 'unregistered',
        instance: g.instance,
        series_id: g.series_id,
        series_title: g.series_title ?? `#${g.series_id}`,
        season_number: g.season_number,
        grab_id: g.replay_of_id,
        detail_key: 'unregistered',
      });
      events.push({
        id: `regrab:${baseKey}`,
        at: g.created_at,
        type: 'regrab',
        instance: g.instance,
        series_id: g.series_id,
        series_title: g.series_title ?? `#${g.series_id}`,
        season_number: g.season_number,
        regrab_id: g.id,
        grab_id: g.replay_of_id,
        detail_key: 'regrabStarted',
      });
      const score = g.custom_format_score ?? null;
      const parent = g.parent_custom_format_score ?? null;
      if (score !== null && parent !== null) {
        if (score > parent) {
          events.push({
            id: `better:${baseKey}`,
            at: g.created_at,
            type: 'better',
            instance: g.instance,
            series_id: g.series_id,
            series_title: g.series_title ?? `#${g.series_id}`,
            season_number: g.season_number,
            regrab_id: g.id,
            episodes: g.episodes_count ?? undefined,
            detail_key: 'regrabFound',
          });
        } else {
          events.push({
            id: `nobetter:${baseKey}`,
            at: g.created_at,
            type: 'no_better',
            instance: g.instance,
            series_id: g.series_id,
            series_title: g.series_title ?? `#${g.series_id}`,
            season_number: g.season_number,
            regrab_id: g.id,
            consecutive: g.consecutive_no_better ?? undefined,
            max_no_better: maxNoBetter,
            detail_key: 'noBetter',
          });
        }
      }
    }
  }

  if (blQ.data) {
    for (const b of blQ.data.items) {
      events.push({
        id: `blacklist:${b.id}:${b.created_at}`,
        at: b.created_at,
        type: 'blacklist',
        instance: b.instance,
        series_id: b.series_id,
        series_title: b.series_title ?? `#${b.series_id}`,
        season_number: b.season_number,
        consecutive: b.consecutive,
        max_no_better: maxNoBetter,
        detail_key: 'blacklistConsec',
      });
    }
  }

  events.sort((a, b) => {
    const tCmp = b.at.localeCompare(a.at);
    if (tCmp !== 0) return tCmp;
    return TYPE_PRIORITY[a.type] - TYPE_PRIORITY[b.type];
  });

  return {
    events: events.slice(0, limit),
    isLoading: grabsQ.isLoading || blQ.isLoading,
    isError: grabsQ.isError || blQ.isError,
    error: (grabsQ.error ?? blQ.error) ?? null,
  };
}
