import { useQuery, type UseQueryResult } from '@tanstack/react-query';
import { api, ApiError } from './api';
import type { components } from '@/api/schema';

export type AuthMode = 'forms' | 'basic' | 'none' | 'oidc';
export type AuthConfig = {
  mode: AuthMode;
  localBypass: boolean;
  loginUrl?: string;
};

type Wire = components['schemas']['dto.AuthConfigDTO'];

export const authConfigQueryKey = ['auth', 'config'] as const;

function narrowMode(raw: string | undefined): AuthMode {
  return raw === 'basic' || raw === 'none' || raw === 'forms' || raw === 'oidc'
    ? raw
    : 'forms';
}

export async function getAuthConfig(): Promise<AuthConfig> {
  const r = await api<Wire>('/auth/config');
  const cfg: AuthConfig = {
    mode: narrowMode(r.mode),
    localBypass: Boolean(r.local_bypass),
  };
  if (r.login_url) cfg.loginUrl = r.login_url;
  return cfg;
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
