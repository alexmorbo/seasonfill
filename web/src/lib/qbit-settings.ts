import { useQuery, type UseQueryResult } from '@tanstack/react-query';
import { ApiError, api } from './api';
import type { components } from '@/api/schema';

// QbitSettings DTO already exists in schema for 042b's discover endpoint;
// `enabled` is the field the watchdog chip reads.
export type QbitSettings = components['schemas']['dto.QbitSettingsDTO'];

export function useQbitSettings(
  instance: string | null,
): UseQueryResult<QbitSettings, ApiError> {
  return useQuery<QbitSettings, ApiError>({
    queryKey: ['qbit', 'settings', instance] as const,
    queryFn: () => api<QbitSettings>(`/instances/${instance}/qbit/settings`),
    enabled: Boolean(instance),
    staleTime: 60_000,
    refetchInterval: 120_000,
    refetchOnWindowFocus: false,
    // qBit may be unconfigured; 404 → return undefined instead of throw.
    retry: (count, err) => count < 1 && (err as ApiError).status !== 404,
  });
}
