import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import type { UseQueryResult } from '@tanstack/react-query';
import { toast } from 'sonner';
import { api, ApiError } from './api';
import type { components } from '@/api/schema';

export type InstanceDetail = components['schemas']['dto.InstanceDetail'];
export type InstanceCreateRequest = components['schemas']['dto.InstanceCreateRequest'];
export type InstanceUpdateRequest = components['schemas']['dto.InstanceUpdateRequest'];
export type InstanceTestRequest = components['schemas']['dto.InstanceTestRequest'];
export type InstanceTestResponse = components['schemas']['dto.InstanceTestResponse'];

// We bolt Last-Modified onto the data we cache so PUTs can send
// If-Unmodified-Since without a separate header cache. The shape
// stays JSON-friendly so devtools can still inspect it.
export interface InstanceDetailWithMeta {
  readonly detail: InstanceDetail;
  readonly lastModified: string | null;
}

export const instanceDetailKey = (name: string) =>
  ['instance-detail', name] as const;

async function getInstanceDetailRaw(name: string): Promise<InstanceDetailWithMeta> {
  const res = await fetch(`/api/v1/instances/${encodeURIComponent(name)}`, {
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
  const detail = (await res.json()) as InstanceDetail;
  return { detail, lastModified: res.headers.get('Last-Modified') };
}

export function useInstanceDetail(name: string | null): UseQueryResult<InstanceDetailWithMeta, ApiError> {
  return useQuery<InstanceDetailWithMeta, ApiError>({
    queryKey: name ? instanceDetailKey(name) : ['instance-detail', '_none'],
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
  return useMutation<InstanceDetail, ApiError, CreateInstanceInput>({
    mutationFn: ({ body }) =>
      api<InstanceDetail>('/instances', { method: 'POST', body }),
    onSuccess: (data) => {
      qc.invalidateQueries({ queryKey: ['instances'] });
      qc.invalidateQueries({ queryKey: instanceDetailKey(data.name ?? '') });
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
  return useMutation<InstanceDetail, ApiError, UpdateInstanceInput>({
    mutationFn: async ({ name, body }) => {
      const cached = qc.getQueryData<InstanceDetailWithMeta>(instanceDetailKey(name));
      const ius = cached?.lastModified ?? null;
      const headers: Record<string, string> = {};
      if (ius) headers['If-Unmodified-Since'] = ius;
      return api<InstanceDetail>(`/instances/${encodeURIComponent(name)}`, {
        method: 'PUT',
        body,
        headers,
      });
    },
    onSuccess: (_data, vars) => {
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
      await api<void>(`/instances/${encodeURIComponent(name)}`, { method: 'DELETE' });
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

export function useTestInstance() {
  return useMutation<InstanceTestResponse, ApiError, InstanceTestRequest>({
    mutationFn: (body) =>
      api<InstanceTestResponse>('/instances/test', { method: 'POST', body }),
    onSuccess: (resp) => {
      if (resp.ok) {
        const v = resp.version && resp.version.length > 0
          ? `Connected to Sonarr ${resp.version}`
          : 'Connected (version unknown)';
        toast.success(v);
      } else {
        toast.error(resp.reason || 'Connection failed');
      }
    },
    onError: (err) => {
      if (err.status === 504) {
        toast.error('Timed out — Sonarr did not respond');
        return;
      }
      toast.error(`Probe failed: ${err.message}`);
    },
  });
}
