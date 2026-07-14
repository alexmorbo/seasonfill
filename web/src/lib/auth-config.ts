import { useQuery, type UseQueryResult } from '@tanstack/react-query';
import { api, ApiError } from './api';
import type { components } from '@/api/schema';

// AuthConfig mirrors GET /auth/config. There is no server-wide auth mode:
// password (forms) login is always available; OIDC/SSO is additive and only
// surfaced when oidcReady is true (loginUrl is then also populated).
export type AuthConfig = {
  oidcReady: boolean;
  loginUrl?: string;
};

type Wire = components['schemas']['dto.AuthConfigDTO'];

export const authConfigQueryKey = ['auth', 'config'] as const;

export async function getAuthConfig(): Promise<AuthConfig> {
  const r = await api<Wire>('/auth/config');
  const cfg: AuthConfig = {
    oidcReady: Boolean(r.oidc_ready),
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
