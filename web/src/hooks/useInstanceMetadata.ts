// Story 522 / N-4e: React Query wrappers for the N-4b instance metadata
// endpoints. Quality profiles + root folders change rarely (operator
// reconfigures their Sonarr setup), so a 10-minute staleTime is enough
// to avoid hammering Sonarr while still picking up admin reconfigures
// within a reasonable window. The BE itself invalidates the cache when
// the instance is reconfigured (N-4d), so a full refetch on next access
// reflects the truth.

import {
  useMutation,
  useQuery,
  useQueryClient,
  type UseMutationResult,
  type UseQueryResult,
} from '@tanstack/react-query';
import { ApiError } from '@/lib/api';
import {
  getQualityProfiles,
  getRootFolders,
  refreshInstanceMetadata,
  type MetadataResponse,
  type QualityProfile,
  type RefreshMetadataResponse,
  type RootFolder,
} from '@/api/instance_metadata';

const TEN_MINUTES = 10 * 60 * 1000;

export const instanceMetadataKeys = {
  qualityProfiles: (name: string) =>
    ['instance-metadata', 'quality-profiles', name] as const,
  rootFolders: (name: string) =>
    ['instance-metadata', 'root-folders', name] as const,
};

export function useQualityProfiles(
  instanceName: string | undefined,
  enabled = true,
): UseQueryResult<MetadataResponse<QualityProfile>, ApiError> {
  const name = (instanceName ?? '').trim();
  const eff = enabled && name.length > 0;
  return useQuery<MetadataResponse<QualityProfile>, ApiError>({
    queryKey: instanceMetadataKeys.qualityProfiles(name),
    queryFn: () => getQualityProfiles(name),
    enabled: eff,
    staleTime: TEN_MINUTES,
  });
}

export function useRootFolders(
  instanceName: string | undefined,
  enabled = true,
): UseQueryResult<MetadataResponse<RootFolder>, ApiError> {
  const name = (instanceName ?? '').trim();
  const eff = enabled && name.length > 0;
  return useQuery<MetadataResponse<RootFolder>, ApiError>({
    queryKey: instanceMetadataKeys.rootFolders(name),
    queryFn: () => getRootFolders(name),
    enabled: eff,
    staleTime: TEN_MINUTES,
  });
}

export function useRefreshInstanceMetadata(): UseMutationResult<
  RefreshMetadataResponse,
  ApiError,
  string
> {
  const qc = useQueryClient();
  return useMutation<RefreshMetadataResponse, ApiError, string>({
    mutationFn: (instanceName) => refreshInstanceMetadata(instanceName),
    onSuccess: (_res, instanceName) => {
      void qc.invalidateQueries({
        queryKey: instanceMetadataKeys.qualityProfiles(instanceName),
      });
      void qc.invalidateQueries({
        queryKey: instanceMetadataKeys.rootFolders(instanceName),
      });
    },
  });
}
