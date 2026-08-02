import type { Query } from '@tanstack/react-query';
import type { ApiError } from '@/lib/api';
import type { DiscoveryListResponse } from '@/api/discovery';
import {
  useDegradedPollInterval,
  type DegradedPollConfig,
} from '@/hooks/useDegradedPollInterval';

// Story 517 / N-3e: derives polling interval from `degraded`.
// Pure utilities — no React state. Cold-start polls every 5s; TMDB
// throttle honors the server's `retry_after_seconds`, clamped to >=1s.

export type DegradedKind = 'cold_start' | 'tmdb_throttled' | null;

export interface DegradedPollingState {
  readonly isDegraded: boolean;
  readonly degradedKind: DegradedKind;
  readonly refetchInterval: number | false;
  readonly estimateSeconds: number;
  readonly retryAfterSeconds: number;
}

const COLD_START_INTERVAL_MS = 5000;
const MIN_THROTTLE_INTERVAL_MS = 1000;
const DEFAULT_THROTTLE_SECONDS = 3;
const DEFAULT_ESTIMATE_SECONDS = 30;

function classify(data: DiscoveryListResponse | undefined): DegradedKind {
  const flags = data?.degraded ?? [];
  if (flags.includes('discovery_warming')) return 'cold_start';
  if (flags.includes('tmdb_throttled')) return 'tmdb_throttled';
  return null;
}

function intervalFor(
  kind: DegradedKind, data: DiscoveryListResponse | undefined,
): number | false {
  if (kind === 'cold_start') return COLD_START_INTERVAL_MS;
  if (kind === 'tmdb_throttled') {
    const seconds = data?.retry_after_seconds ?? DEFAULT_THROTTLE_SECONDS;
    return Math.max(seconds * 1000, MIN_THROTTLE_INTERVAL_MS);
  }
  return false;
}

export function useDegradedPolling(
  data: DiscoveryListResponse | undefined,
): DegradedPollingState {
  const degradedKind = classify(data);
  return {
    isDegraded: degradedKind !== null,
    degradedKind,
    refetchInterval: intervalFor(degradedKind, data),
    estimateSeconds: data?.warming_estimate_seconds ?? DEFAULT_ESTIMATE_SECONDS,
    retryAfterSeconds: data?.retry_after_seconds ?? DEFAULT_THROTTLE_SECONDS,
  };
}

// Stable callback form for React Query's `refetchInterval` option.
// Reads the latest `query.state.data` on every poll so the interval
// adapts as the server transitions degraded → healthy. Pure (uncapped)
// interval mapping — the capped hook below wraps this via intervalFor.
export function degradedRefetchInterval(
  query: Query<DiscoveryListResponse, ApiError>,
): number | false {
  const data = query.state.data;
  return intervalFor(classify(data), data);
}

// HARDEN-1: absolute tick ceiling for the discovery grids. Cold-start polls
// the `intervalFor` interval (5s warming / retry_after_seconds throttle);
// ~24 ticks ≈ 2 min gives comfortable headroom over the BE's default
// warming_estimate_seconds=30s so a legitimate long cold-start is never cut
// off, yet a poster that never warms cannot poll forever. Unlike overview/
// recs, the discovery degraded flag is STABLE during warm-up, so a
// length-reset would never fire — this is a pure ABSOLUTE ceiling on total
// consecutive degraded ticks (the retry_after throttle VALUE is preserved).
const DISCOVERY_MAX_TICKS = 24;
export function discoveryPollConfig(): DegradedPollConfig<DiscoveryListResponse> {
  return {
    enabled: true,
    isDegraded: (data) => classify(data) !== null,
    intervalFor: (data) => intervalFor(classify(data), data),
    maxTicks: DISCOVERY_MAX_TICKS,
    mode: 'absolute',
  };
}

// Hook wrapper the grids call: holds the tick counter across re-renders and
// returns the `refetchInterval` callback the `useDiscovery*` hooks accept.
// Because `degradedRefetchInterval` was passed bare, the stateful counter
// now lives here (one instance per grid), replacing the uncapped bare fn.
export function useDegradedRefetchInterval(): (
  query: Query<DiscoveryListResponse, ApiError>,
) => number | false {
  return useDegradedPollInterval<DiscoveryListResponse>(discoveryPollConfig());
}
