import { useQuery, type UseQueryResult } from '@tanstack/react-query';
import { ApiError, api } from '@/lib/api';
import type { MeResponse } from '@/lib/me-types';

// Shared query key for the /api/v1/me cache entry. N-7c's useLanguage
// mutation will optimistically setQueryData on this key — the value
// MUST stay aligned across the codebase. Lock the literal here.
export const ME_QUERY_KEY = ['me'] as const;

// useMe returns the current user envelope from GET /api/v1/me.
//
// 401 handling: the global api() wrapper (lib/api.ts:60-73) intercepts
// 401, refreshes auth config, and window.location.assigns the operator
// to /login or the OIDC start URL. The hook itself just propagates the
// ApiError so a loading state never wedges in stale data — react-query
// surfaces error to the consumer who can render whatever placeholder
// they like in the brief moment before the global redirect resolves.
//
// retry semantics inherit from the default queryClient (lib/query-client.ts):
// 401/403/404 non-retryable, 5xx + network up to 2 attempts. Hook
// overrides staleTime to 30s (matches useSession) to keep the cache
// hot across tab switches.
export function useMe(): UseQueryResult<MeResponse, ApiError> {
  return useQuery({
    queryKey: ME_QUERY_KEY,
    queryFn: () => api<MeResponse>('/me'),
    staleTime: 30_000,
    refetchOnWindowFocus: false,
  });
}
