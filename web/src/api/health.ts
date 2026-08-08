import { useQuery, type UseQueryResult } from '@tanstack/react-query';
import { ApiError, api } from '@/lib/api';
import type { components } from '@/api/schema';

// I-1a wire types — GET /api/v1/insights/health → dto.HealthDashboardDTO.
// Every field is optional in the generated schema; the FE treats a missing
// signal as count 0 / no items.
export type HealthDashboard = components['schemas']['dto.HealthDashboardDTO'];
export type HealthSeriesSignal = components['schemas']['dto.HealthSeriesSignalDTO'];
export type HealthStaleSignal = components['schemas']['dto.HealthStaleSignalDTO'];
export type HealthGrabSignal = components['schemas']['dto.HealthGrabSignalDTO'];
export type HealthInboxSignal = components['schemas']['dto.HealthInboxSignalDTO'];
export type HealthDeferredSignal = components['schemas']['dto.HealthDeferredSignalDTO'];
export type HealthSeriesItem = components['schemas']['dto.HealthSeriesItemDTO'];
export type HealthStaleItem = components['schemas']['dto.HealthStaleItemDTO'];
export type HealthGrabItem = components['schemas']['dto.HealthGrabItemDTO'];
export type HealthInboxItem = components['schemas']['dto.HealthInboxItemDTO'];

export const healthKey = ['insights', 'health'] as const;

// useHealthDashboard fetches the operator pulse ON DEMAND. No refetchInterval:
// this is a manual-refresh page, not a live monitor. staleTime keeps a fresh
// mount from re-hitting the DB scan when the operator bounces between pages.
export function useHealthDashboard(): UseQueryResult<HealthDashboard, ApiError> {
  return useQuery<HealthDashboard, ApiError>({
    queryKey: healthKey,
    queryFn: () => api<HealthDashboard>('/insights/health'),
    staleTime: 30_000,
    refetchOnWindowFocus: false,
  });
}
