import { useQuery, type UseQueryResult } from '@tanstack/react-query';
import { api, ApiError } from './api';
import type { components } from '@/api/schema';

export type AuthMode = 'forms' | 'basic' | 'none';
export type AuthConfig = { mode: AuthMode; localBypass: boolean };

type Wire = components['schemas']['dto.AuthConfigDTO'];

export const authConfigQueryKey = ['auth', 'config'] as const;

function narrowMode(raw: string | undefined): AuthMode {
  return raw === 'basic' || raw === 'none' || raw === 'forms' ? raw : 'forms';
}

export async function getAuthConfig(): Promise<AuthConfig> {
  const r = await api<Wire>('/auth/config');
  return { mode: narrowMode(r.mode), localBypass: Boolean(r.local_bypass) };
}

export function useAuthConfig(): UseQueryResult<AuthConfig, ApiError> {
  return useQuery<AuthConfig, ApiError>({
    queryKey: authConfigQueryKey,
    queryFn: getAuthConfig,
    // /auth/config is public and only changes on operator action — keep it
    // stable across the whole session and rely on explicit invalidation
    // (useUpdateRuntimeConfig.onSuccess) to refresh after a Settings save.
    staleTime: Infinity,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
    retry: 0,
  });
}
