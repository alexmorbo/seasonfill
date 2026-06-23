import { useRef } from 'react';
import { useQuery, type UseQueryResult } from '@tanstack/react-query';
import { ApiError, api } from './api';
import type { components } from '@/api/schema';

export type Instance = components['schemas']['dto.Instance'];
export type InstanceList = components['schemas']['dto.InstanceList'];

// Fast-poll cap — Story 488 (B-14). Once any instance has been
// Bootstrapping for longer than this, we stop the 2s loop and fall
// back to the 30s steady-state. The BE doesn't lie — it'll keep
// returning Bootstrapping; the UI just stops asking faster.
const BOOTSTRAP_FAST_POLL_CAP_MS = 30_000;
const BOOTSTRAP_FAST_POLL_MS = 2_000;
const STEADY_POLL_MS = 30_000;

export function useInstances(): UseQueryResult<InstanceList, ApiError> {
  // Tracks the first time we observed any instance in Bootstrapping.
  // Resets to null when no instance is Bootstrapping anymore.
  const firstObservedRef = useRef<number | null>(null);

  return useQuery<InstanceList, ApiError>({
    queryKey: ['instances'] as const,
    queryFn: () => api<InstanceList>('/admin/instances'),
    staleTime: 15_000,
    refetchInterval: (query) => {
      const data = query.state.data;
      if (!data || !data.instances) return STEADY_POLL_MS;
      const anyBootstrapping = data.instances.some(
        (i) => i.health === 'Bootstrapping',
      );
      if (!anyBootstrapping) {
        firstObservedRef.current = null;
        return STEADY_POLL_MS;
      }
      const now = Date.now();
      if (firstObservedRef.current === null) {
        firstObservedRef.current = now;
      }
      const elapsed = now - firstObservedRef.current;
      if (elapsed > BOOTSTRAP_FAST_POLL_CAP_MS) {
        // Cap exceeded — fall back to steady poll. Operator can refresh
        // manually; the BE will still surface the eventual transition.
        return STEADY_POLL_MS;
      }
      return BOOTSTRAP_FAST_POLL_MS;
    },
    refetchOnWindowFocus: false,
  });
}
