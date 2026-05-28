import {
  useMutation, useQuery, useQueryClient,
  type UseQueryResult,
} from '@tanstack/react-query';
import { toast } from 'sonner';
import { ApiError } from './api';
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
    mutationFn: async (body) => {
      const cached = qc.getQueryData<RuntimeConfigWithMeta>(runtimeConfigKey);
      const ius = cached?.lastModified ?? null;
      const headers: Record<string, string> = {
        'Content-Type': 'application/json',
      };
      if (ius) headers['If-Unmodified-Since'] = ius;
      const res = await fetch('/api/v1/config/runtime', {
        method: 'PUT',
        credentials: 'same-origin',
        headers,
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
        // Carry the response headers on the error so onError can recover
        // the fresh Last-Modified from a 412 without waiting on a refetch.
        throw new ApiError(res.status, msg, parsed, res.headers);
      }
      const config = (await res.json()) as RuntimeConfig;
      return { config, lastModified: res.headers.get('Last-Modified') };
    },
    // Synchronous cache prime closes the rapid-double-Save race. The
    // background refetch from invalidateQueries still runs and will
    // overwrite if the server's UpdatedAt has moved further.
    onSuccess: ({ config, lastModified }) => {
      qc.setQueryData<RuntimeConfigWithMeta>(
        runtimeConfigKey, { config, lastModified },
      );
      qc.invalidateQueries({ queryKey: runtimeConfigKey });
      toast.success('Settings saved');
    },
    onError: async (err) => {
      if (err.status === 412) {
        // The 412 response carries the row's current Last-Modified.
        // Seed it into the cache synchronously so the immediate next
        // save sends the correct If-Unmodified-Since — recovery no
        // longer depends on the background refetch landing first.
        const fresh = err.headers?.get('Last-Modified') ?? null;
        if (fresh) {
          qc.setQueryData<RuntimeConfigWithMeta>(runtimeConfigKey, (prev) =>
            prev ? { ...prev, lastModified: fresh } : prev,
          );
        }
        toast.message('Settings changed by another tab — reloaded');
        // Background refetch also refreshes the displayed config values.
        await qc.invalidateQueries({ queryKey: runtimeConfigKey });
        return;
      }
      if (err.status === 400) {
        toast.error(err.message || 'Invalid settings');
        return;
      }
      toast.error(`Save failed: ${err.message}`);
    },
  });
}
