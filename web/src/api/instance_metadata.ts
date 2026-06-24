// Story 522 / N-4e: hand-authored DTOs for instance metadata endpoints.
// Mirrors internal/admin/rest/instance_metadata_handler.go wire shape.

import { api } from '@/lib/api';

export interface QualityProfile {
  readonly id: number;
  readonly name: string;
}

export interface RootFolder {
  readonly id: number;
  readonly path: string;
  readonly accessible: boolean;
  readonly free_space: number;
}

export type CacheStatus = 'hit' | 'miss' | 'warming' | 'stale';

export interface MetadataResponse<T> {
  readonly items: readonly T[];
  readonly refreshed_at: string;
  readonly cache_status: CacheStatus | string;
  readonly instance_name: string;
}

export interface RefreshMetadataResponse {
  readonly invalidated: boolean;
}

export function getQualityProfiles(
  instanceName: string,
): Promise<MetadataResponse<QualityProfile>> {
  return api<MetadataResponse<QualityProfile>>(
    `/instances/${encodeURIComponent(instanceName)}/quality-profiles`,
  );
}

export function getRootFolders(
  instanceName: string,
): Promise<MetadataResponse<RootFolder>> {
  return api<MetadataResponse<RootFolder>>(
    `/instances/${encodeURIComponent(instanceName)}/root-folders`,
  );
}

export function refreshInstanceMetadata(
  instanceName: string,
): Promise<RefreshMetadataResponse> {
  return api<RefreshMetadataResponse>(
    `/instances/${encodeURIComponent(instanceName)}/refresh-metadata`,
    { method: 'POST' },
  );
}
