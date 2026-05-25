import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import type { UseQueryResult } from '@tanstack/react-query';
import { toast } from 'sonner';
import { ApiError } from './api';
import type { components } from '@/api/schema';

export type InstanceDetail = components['schemas']['dto.InstanceDetail'];
export type InstanceCreateRequest = components['schemas']['dto.InstanceCreateRequest'];
export type InstanceUpdateRequest = components['schemas']['dto.InstanceUpdateRequest'];
export type InstanceTestRequest = components['schemas']['dto.InstanceTestRequest'];
export type InstanceTestResponse = components['schemas']['dto.InstanceTestResponse'];

export interface InstanceDetailWithMeta {
  readonly detail: InstanceDetail;
  readonly lastModified: string | null;
}

export const instanceDetailKey = (name: string) =>
  ['instance-detail', name] as const;

// Sentinel key for the disabled branch of useInstanceDetail (name=null).
// Kept distinct from any real name-keyed entry so it can't collide with
// a hypothetical instance literally named "_none".
const instanceDetailDisabledKey = ['instance-detail-disabled'] as const;

async function jsonFetch<T>(
  url: string,
  init: RequestInit = {},
): Promise<{ data: T; lastModified: string | null }> {
  const res = await fetch(url, {
    credentials: 'same-origin',
    ...init,
    headers: {
      Accept: 'application/json',
      ...(init.body !== undefined ? { 'Content-Type': 'application/json' } : {}),
      ...(init.headers ?? {}),
    },
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
  const data = res.status === 204 ? (undefined as T) : ((await res.json()) as T);
  return { data, lastModified: res.headers.get('Last-Modified') };
}

async function getInstanceDetailRaw(name: string): Promise<InstanceDetailWithMeta> {
  const { data, lastModified } = await jsonFetch<InstanceDetail>(
    `/api/v1/instances/${encodeURIComponent(name)}`,
  );
  return { detail: data, lastModified };
}

export function useInstanceDetail(name: string | null): UseQueryResult<InstanceDetailWithMeta, ApiError> {
  return useQuery<InstanceDetailWithMeta, ApiError>({
    queryKey: name ? instanceDetailKey(name) : instanceDetailDisabledKey,
    queryFn: () => {
      if (!name) throw new ApiError(400, 'name required');
      return getInstanceDetailRaw(name);
    },
    enabled: Boolean(name),
    staleTime: 0,
  });
}

export interface CreateInstanceInput {
  readonly body: InstanceCreateRequest;
}

export function useCreateInstance() {
  const qc = useQueryClient();
  return useMutation<InstanceDetailWithMeta, ApiError, CreateInstanceInput>({
    mutationFn: ({ body }) =>
      jsonFetch<InstanceDetail>('/api/v1/instances', {
        method: 'POST', body: JSON.stringify(body),
      }).then(({ data, lastModified }) => ({ detail: data, lastModified })),
    onSuccess: ({ detail, lastModified }) => {
      // Synchronous cache prime — a follow-up Edit dialog hitting the
      // detail query will see the freshest payload + header.
      if (detail.name) {
        qc.setQueryData<InstanceDetailWithMeta>(
          instanceDetailKey(detail.name), { detail, lastModified },
        );
      }
      qc.invalidateQueries({ queryKey: ['instances'] });
      toast.success('Instance created');
    },
    onError: (err) => {
      if (err.status === 409) {
        toast.error(err.message || 'Conflict: name already exists');
        return;
      }
      toast.error(`Create failed: ${err.message}`);
    },
  });
}

export interface UpdateInstanceInput {
  readonly name: string;
  readonly body: InstanceUpdateRequest;
}

export function useUpdateInstance() {
  const qc = useQueryClient();
  return useMutation<InstanceDetailWithMeta, ApiError, UpdateInstanceInput>({
    mutationFn: async ({ name, body }) => {
      const cached = qc.getQueryData<InstanceDetailWithMeta>(instanceDetailKey(name));
      const ius = cached?.lastModified ?? null;
      const { data, lastModified } = await jsonFetch<InstanceDetail>(
        `/api/v1/instances/${encodeURIComponent(name)}`,
        {
          method: 'PUT',
          body: JSON.stringify(body),
          headers: ius ? { 'If-Unmodified-Since': ius } : {},
        },
      );
      return { detail: data, lastModified };
    },
    // CRITICAL: setQueryData runs BEFORE invalidateQueries so a rapid
    // second Save reads the freshly-captured Last-Modified rather than
    // the pre-PUT one. The invalidate still triggers a background GET
    // that will overwrite if the server's UpdatedAt differs.
    onSuccess: ({ detail, lastModified }, vars) => {
      qc.setQueryData<InstanceDetailWithMeta>(
        instanceDetailKey(vars.name), { detail, lastModified },
      );
      qc.invalidateQueries({ queryKey: ['instances'] });
      qc.invalidateQueries({ queryKey: instanceDetailKey(vars.name) });
      toast.success('Instance saved');
    },
    onError: async (err, vars) => {
      if (err.status === 412) {
        toast.message('Settings changed by another tab — reloaded');
        await qc.invalidateQueries({ queryKey: instanceDetailKey(vars.name) });
        await qc.invalidateQueries({ queryKey: ['instances'] });
        return;
      }
      toast.error(`Save failed: ${err.message}`);
    },
  });
}

export interface DeleteInstanceInput {
  readonly name: string;
}

export function useDeleteInstance() {
  const qc = useQueryClient();
  return useMutation<void, ApiError, DeleteInstanceInput>({
    mutationFn: async ({ name }) => {
      await jsonFetch<void>(`/api/v1/instances/${encodeURIComponent(name)}`, {
        method: 'DELETE',
      });
    },
    onSuccess: (_data, vars) => {
      qc.invalidateQueries({ queryKey: ['instances'] });
      qc.removeQueries({ queryKey: instanceDetailKey(vars.name) });
      toast.success('Instance deleted');
    },
    onError: (err) => {
      if (err.status === 409) {
        toast.error('Cannot delete the last Sonarr instance');
        return;
      }
      toast.error(`Delete failed: ${err.message}`);
    },
  });
}

function errorCode(err: ApiError): string {
  if (typeof err.body === 'object' && err.body !== null && 'code' in err.body) {
    const c = (err.body as { code: unknown }).code;
    return typeof c === 'string' ? c : '';
  }
  return '';
}

// useTestInstance — inline-only feedback on happy path. The dialog
// renders `probeResult` next to the Test button (success/version or
// failure reason). Transport-level failures (504, network error) DO
// surface as toasts because they may have happened off-screen.
export function useTestInstance() {
  return useMutation<InstanceTestResponse, ApiError, InstanceTestRequest>({
    mutationFn: ({ url, api_key }) =>
      jsonFetch<InstanceTestResponse>('/api/v1/instances/test', {
        method: 'POST',
        body: JSON.stringify({ url, api_key }),
      }).then(({ data }) => data),
    // Deliberately no onSuccess toasts — the dialog owns the inline
    // feedback channel. Avoid double-announcing the same event.
    onError: (err) => {
      // Status-first branching (mirrors GeneralTab pattern), with a
      // narrower body.code refinement for the cases where multiple
      // distinct failure shapes share the same HTTP status.
      if (err.status === 504) {
        toast.error('Timed out — Sonarr did not respond');
        return;
      }
      if (err.status === 401 || err.status === 403) {
        toast.error('Unauthorized — check the API key');
        return;
      }
      if (err.status === 400) {
        const code = errorCode(err);
        if (code === 'INVALID_HOST') {
          toast.error('URL resolves to a private or loopback address');
          return;
        }
        // Generic 400 (malformed URL, missing fields, body too large…).
        toast.error(err.message || 'Bad request');
        return;
      }
      toast.error(`Probe failed: ${err.message}`);
    },
  });
}
