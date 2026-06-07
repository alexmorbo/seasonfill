import { useQuery, type UseQueryResult } from '@tanstack/react-query';
import { ApiError, api } from './api';
import type { components } from '@/api/schema';

export type Instance = components['schemas']['dto.Instance'];
export type InstanceList = components['schemas']['dto.InstanceList'];

export function useInstances(): UseQueryResult<InstanceList, ApiError> {
  return useQuery<InstanceList, ApiError>({
    queryKey: ['instances'] as const,
    queryFn: () => api<InstanceList>('/instances'),
    staleTime: 15_000,
    refetchInterval: 30_000,
    refetchOnWindowFocus: false,
  });
}
