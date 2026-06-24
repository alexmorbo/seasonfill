import type { Query } from '@tanstack/react-query';
import type { ApiError } from '@/lib/api';
import type { DiscoveryListResponse } from '@/api/discovery';

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
// adapts as the server transitions degraded → healthy.
export function degradedRefetchInterval(
  query: Query<DiscoveryListResponse, ApiError>,
): number | false {
  const data = query.state.data;
  return intervalFor(classify(data), data);
}
