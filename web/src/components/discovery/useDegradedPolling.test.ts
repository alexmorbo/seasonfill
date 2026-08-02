import { describe, it, expect } from 'vitest';
import { renderHook } from '@testing-library/react';
import type { Query } from '@tanstack/react-query';
import type { ApiError } from '@/lib/api';
import type { DiscoveryListResponse } from '@/api/discovery';
import { createDegradedPollInterval } from '@/hooks/useDegradedPollInterval';
import {
  useDegradedPolling,
  degradedRefetchInterval,
  discoveryPollConfig,
} from './useDegradedPolling';

describe('useDegradedPolling', () => {
  it('returns healthy state when data is undefined', () => {
    const { result } = renderHook(() => useDegradedPolling(undefined));
    expect(result.current.isDegraded).toBe(false);
    expect(result.current.degradedKind).toBeNull();
    expect(result.current.refetchInterval).toBe(false);
    expect(result.current.estimateSeconds).toBe(30);
  });

  it('returns healthy state when degraded is an empty array', () => {
    const { result } = renderHook(() =>
      useDegradedPolling({ items: [], degraded: [] }));
    expect(result.current.isDegraded).toBe(false);
    expect(result.current.refetchInterval).toBe(false);
  });

  it('detects cold-start and polls every 5s', () => {
    const { result } = renderHook(() =>
      useDegradedPolling({
        items: [],
        degraded: ['discovery_warming'],
        warming_estimate_seconds: 12,
      }));
    expect(result.current.isDegraded).toBe(true);
    expect(result.current.degradedKind).toBe('cold_start');
    expect(result.current.refetchInterval).toBe(5000);
    expect(result.current.estimateSeconds).toBe(12);
  });

  it('detects tmdb-throttle and honors retry_after_seconds', () => {
    const { result } = renderHook(() =>
      useDegradedPolling({
        items: [],
        degraded: ['tmdb_throttled'],
        retry_after_seconds: 7,
      }));
    expect(result.current.degradedKind).toBe('tmdb_throttled');
    expect(result.current.refetchInterval).toBe(7000);
    expect(result.current.retryAfterSeconds).toBe(7);
  });

  it('clamps throttle interval to >=1s', () => {
    const { result } = renderHook(() =>
      useDegradedPolling({
        items: [],
        degraded: ['tmdb_throttled'],
        retry_after_seconds: 0,
      }));
    expect(result.current.refetchInterval).toBe(1000);
  });
});

function fakeQuery(data: DiscoveryListResponse | undefined):
  Query<DiscoveryListResponse, ApiError> {
  return { state: { data } } as unknown as Query<DiscoveryListResponse, ApiError>;
}

describe('degradedRefetchInterval', () => {
  it('returns false when query data is undefined', () => {
    expect(degradedRefetchInterval(fakeQuery(undefined))).toBe(false);
  });

  it('returns 5000 for cold-start', () => {
    expect(degradedRefetchInterval(fakeQuery({
      items: [], degraded: ['discovery_warming'],
    }))).toBe(5000);
  });

  it('returns retry_after-derived ms for throttle', () => {
    expect(degradedRefetchInterval(fakeQuery({
      items: [], degraded: ['tmdb_throttled'], retry_after_seconds: 6,
    }))).toBe(6000);
  });

  it('returns false when degraded is empty', () => {
    expect(degradedRefetchInterval(fakeQuery({
      items: [{ series_id: 1, tmdb_id: 1, title: 't' }],
    }))).toBe(false);
  });
});

// HARDEN-1: the cold-start warming flag is STABLE, so the discovery poll uses
// an ABSOLUTE tick ceiling (~24 ≈ 2 min) instead of a length-reset — a legit
// long cold-start runs to the cap, a never-warming slice can't poll forever.
describe('discoveryPollConfig — absolute cap', () => {
  const warming: DiscoveryListResponse = { items: [], degraded: ['discovery_warming'] };
  const healthy: DiscoveryListResponse = {
    items: [{ series_id: 1, tmdb_id: 1, title: 't' }],
  };

  it('does NOT stop before the absolute cap (24 warming ticks at 5s)', () => {
    const poll = createDegradedPollInterval(discoveryPollConfig());
    for (let i = 0; i < 24; i += 1) expect(poll(warming)).toBe(5000);
  });

  it('stops exactly at the absolute cap even though the flag never changes', () => {
    const poll = createDegradedPollInterval(discoveryPollConfig());
    for (let i = 0; i < 24; i += 1) poll(warming);
    expect(poll(warming)).toBe(false); // 25th tick — capped
  });

  it('honors the throttle interval value while under the cap', () => {
    const poll = createDegradedPollInterval(discoveryPollConfig());
    const throttled: DiscoveryListResponse = {
      items: [], degraded: ['tmdb_throttled'], retry_after_seconds: 6,
    };
    expect(poll(throttled)).toBe(6000);
  });

  it('returns false when the slice is healthy', () => {
    const poll = createDegradedPollInterval(discoveryPollConfig());
    expect(poll(healthy)).toBe(false);
  });
});
