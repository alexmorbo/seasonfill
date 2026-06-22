import { useMutation, useQuery, useQueryClient, type UseQueryResult, type UseMutationResult } from '@tanstack/react-query';
import { ApiError, api } from '@/lib/api';
import type { MeResponse } from '@/lib/me-types';

// Shared query key for the /api/v1/me cache entry. useLanguage's
// mutation optimistically setQueryData on this key — the value MUST
// stay aligned across the codebase. Lock the literal here.
export const ME_QUERY_KEY = ['me'] as const;

// useMe returns the current user envelope from GET /api/v1/me.
//
// 401 handling: the global api() wrapper (lib/api.ts:60-73) intercepts
// 401, refreshes auth config, and window.location.assigns the operator
// to /login or the OIDC start URL. The hook itself just propagates the
// ApiError.
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

// PATCH /api/v1/me/settings body (BE allowlist: preferred_language,
// avatar_mode). Sent as an arbitrary subset — undefined keys are
// dropped before serialization. The BE accepts one key/value pair per
// call (per N-7a contract); the hook concatenates into a single object
// for ergonomics. Callers using only one key see the single field on
// the wire.
export interface MeSettingsPatch {
  readonly preferred_language?: string;
  readonly avatar_mode?: 'auto' | 'monogram' | 'gravatar';
}

// useUpdateMeSettings is the mutation entry point for both N-6
// (preferred_language dual-write) and N-7c AppearanceSection
// avatar_mode save. Callers do NOT implement optimistic updates inside
// the hook — useLanguage handles that for the language case. For
// avatar_mode, AppearanceSection invalidates ['me'] after success and
// lets React Query refetch the truth.
//
// Why not split into two hooks: both PATCHes hit the same endpoint with
// the same response handling. Keeping them shared avoids duplicating
// the on-error toast wiring and lets the hook stay <30 LOC.
export function useUpdateMeSettings(): UseMutationResult<MeResponse, ApiError, MeSettingsPatch> {
  const qc = useQueryClient();
  return useMutation<MeResponse, ApiError, MeSettingsPatch>({
    mutationFn: (body) => api<MeResponse>('/me/settings', { method: 'PATCH', body }),
    onSuccess: (next) => {
      qc.setQueryData<MeResponse>(ME_QUERY_KEY, next);
      void qc.invalidateQueries({ queryKey: ME_QUERY_KEY });
    },
  });
}

// POST /api/v1/me/change-password body. N-7a accepts current + new
// passwords; the new password must be ≥ MinPasswordLen=8 server-side.
// The form layer adds a stricter ≥12 client-side check (see story
// Decision §5).
export interface ChangePasswordBody {
  readonly current_password: string;
  readonly new_password: string;
}

// useChangePassword wraps the POST. Success returns 204 (no body); the
// mutation's onSuccess handler bumps the session epoch server-side
// (other tabs get 401 on the next request — handled by global api()
// wrapper). No cache invalidation needed: ['me'] stays valid because
// the active session is the one that changed the password.
export function useChangePassword(): UseMutationResult<void, ApiError, ChangePasswordBody> {
  return useMutation<void, ApiError, ChangePasswordBody>({
    mutationFn: async (body) => {
      await api<void>('/me/change-password', { method: 'POST', body });
    },
  });
}
