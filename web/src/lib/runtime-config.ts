import {
  useMutation, useQuery, useQueryClient,
  type UseQueryResult,
} from '@tanstack/react-query';
import { toast } from 'sonner';
import { ApiError } from './api';
import { authConfigQueryKey } from './auth-config';
import type { components } from '@/api/schema';

export type RuntimeConfig = components['schemas']['dto.RuntimeConfigDTO'];

export interface RuntimeConfigWithMeta {
  readonly config: RuntimeConfig;
  readonly lastModified: string | null;
}

export const runtimeConfigKey = ['runtime-config'] as const;

async function fetchRuntimeConfig(): Promise<RuntimeConfigWithMeta> {
  const res = await fetch('/api/v1/config/runtime', {
    credentials: 'same-origin',
    headers: { Accept: 'application/json' },
  });
  if (res.status === 401) {
    if (typeof window !== 'undefined' && window.location.pathname !== '/login') {
      window.location.assign('/login');
    }
    throw new ApiError(401, 'unauthorized');
  }
  if (!res.ok) {
    let parsed: unknown;
    try { parsed = await res.json(); } catch { parsed = undefined; }
    const msg = typeof parsed === 'object' && parsed && 'error' in parsed
      ? String((parsed as { error: unknown }).error)
      : res.statusText;
    throw new ApiError(res.status, msg, parsed);
  }
  const config = (await res.json()) as RuntimeConfig;
  return { config, lastModified: res.headers.get('Last-Modified') };
}

export function useRuntimeConfig(): UseQueryResult<RuntimeConfigWithMeta, ApiError> {
  return useQuery<RuntimeConfigWithMeta, ApiError>({
    queryKey: runtimeConfigKey,
    queryFn: fetchRuntimeConfig,
    staleTime: 0,
  });
}

export function useUpdateRuntimeConfig() {
  const qc = useQueryClient();
  return useMutation<RuntimeConfigWithMeta, ApiError, RuntimeConfig>({
    // Runtime config is a single-admin singleton row, so we use
    // last-write-wins: the PUT is unconditional (no If-Unmodified-Since
    // precondition) and the server can never reject it with a 412.
    mutationFn: async (body) => {
      const res = await fetch('/api/v1/config/runtime', {
        method: 'PUT',
        credentials: 'same-origin',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      });
      if (res.status === 401) {
        if (typeof window !== 'undefined' && window.location.pathname !== '/login') {
          window.location.assign('/login');
        }
        throw new ApiError(401, 'unauthorized');
      }
      if (!res.ok) {
        let parsed: unknown;
        try { parsed = await res.json(); } catch { parsed = undefined; }
        const msg = typeof parsed === 'object' && parsed && 'error' in parsed
          ? String((parsed as { error: unknown }).error)
          : res.statusText;
        throw new ApiError(res.status, msg, parsed);
      }
      const config = (await res.json()) as RuntimeConfig;
      return { config, lastModified: res.headers.get('Last-Modified') };
    },
    onSuccess: ({ config, lastModified }) => {
      qc.setQueryData<RuntimeConfigWithMeta>(
        runtimeConfigKey, { config, lastModified },
      );
      qc.invalidateQueries({ queryKey: runtimeConfigKey });
      // OIDC config may have changed — drop the cached
      // /auth/config snapshot so Login, TopBar, banner re-evaluate the
      // next render. Must run BEFORE the mutation promise resolves so
      // Settings doesn't show the old mode briefly after Save.
      qc.invalidateQueries({ queryKey: authConfigQueryKey });
      toast.success('Settings saved');
    },
    onError: (err) => {
      if (err.status === 400) {
        toast.error(err.message || 'Invalid settings');
        return;
      }
      toast.error(`Save failed: ${err.message}`);
    },
  });
}
