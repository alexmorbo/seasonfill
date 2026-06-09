import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import type { UseQueryResult } from '@tanstack/react-query';
import { toast } from 'sonner';
import i18n from '@/i18n';
import { ApiError } from './api';
import type { components } from '@/api/schema';
import {
  qbitSettingsKey,
  webhookStatusKey,
  type QbitSettingsDTO,
  type QbitSettingsUpsertRequest,
} from '@/api/qbit';

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
      toast.success(i18n.t('toasts.instanceCreated'));
    },
    onError: (err) => {
      if (err.status === 409) {
        toast.error(err.message || i18n.t('toasts.instanceNameConflict'));
        return;
      }
      toast.error(i18n.t('toasts.instanceCreateFailed', { error: err.message }));
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
    // Last-write-wins: the PUT is unconditional (no If-Unmodified-Since
    // precondition) so the server can never reject it with a 412. The
    // api_key is protected independently — the form only sends api_key
    // when the user actually typed a new one and the backend preserves
    // the stored key otherwise — so last-write-wins can't clobber it.
    mutationFn: async ({ name, body }) => {
      const { data, lastModified } = await jsonFetch<InstanceDetail>(
        `/api/v1/instances/${encodeURIComponent(name)}`,
        {
          method: 'PUT',
          body: JSON.stringify(body),
        },
      );
      return { detail: data, lastModified };
    },
    onSuccess: ({ detail, lastModified }, vars) => {
      qc.setQueryData<InstanceDetailWithMeta>(
        instanceDetailKey(vars.name), { detail, lastModified },
      );
      qc.invalidateQueries({ queryKey: ['instances'] });
      qc.invalidateQueries({ queryKey: instanceDetailKey(vars.name) });
      toast.success(i18n.t('toasts.instanceSaved'));
    },
    onError: (err) => {
      toast.error(i18n.t('toasts.instanceSaveFailed', { error: err.message }));
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
      qc.invalidateQueries({ queryKey: ['scans'] });
      qc.invalidateQueries({ queryKey: ['decisions'] });
      qc.invalidateQueries({ queryKey: ['grabs'] });
      toast.success(i18n.t('toasts.instanceDeleted'));
    },
    onError: (err) => {
      toast.error(i18n.t('toasts.instanceDeleteFailed', { error: err.message }));
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
        toast.error(i18n.t('toasts.probeTimeout'));
        return;
      }
      if (err.status === 401 || err.status === 403) {
        toast.error(i18n.t('toasts.probeUnauthorized'));
        return;
      }
      if (err.status === 400) {
        const code = errorCode(err);
        if (code === 'INVALID_HOST') {
          toast.error(i18n.t('toasts.probePrivateBlocked'));
          return;
        }
        // Generic 400 (malformed URL, missing fields, body too large…).
        toast.error(err.message || i18n.t('toasts.probeBadRequest'));
        return;
      }
      toast.error(i18n.t('toasts.probeFailed', { error: err.message }));
    },
  });
}

export interface SaveWithQbitInput {
  readonly mode: 'create' | 'edit';
  readonly name: string | undefined;
  readonly instanceBody: InstanceCreateRequest | InstanceUpdateRequest;
  readonly qbitBody?: QbitSettingsUpsertRequest | undefined;
}

export interface SaveWithQbitResult {
  readonly detail: InstanceDetail;
  readonly qbitSaved: boolean;
  readonly qbitError: ApiError | null;
  // Fresh qBit settings DTO returned by the PUT response. Present iff
  // qbitSaved === true. Callers should prefer this over the cached
  // `useQbitSettings()` data when re-seeding form state immediately
  // after save — the cached value is stale until invalidation refetch
  // completes (see operator #3 latent: stale-cache re-seed race).
  readonly qbitDTO: QbitSettingsDTO | null;
}

/**
 * Combined Save orchestrator (057b1).
 *
 * Runs the instance mutation FIRST (POST or PUT), then optionally
 * runs the qBit settings PUT against the saved instance's name.
 * Returns a result with both branches so callers can render a
 * partial-success toast when the instance succeeded but qBit
 * failed.
 *
 * Failure semantics:
 *   - Instance mutation fails → throws (caller handles + toasts).
 *   - Instance succeeds, qBit not provided → `{ qbitSaved: false, qbitError: null }`.
 *   - Both succeed → `{ qbitSaved: true, qbitError: null }`.
 *   - Instance succeeds, qBit fails → resolved promise with
 *     `{ qbitSaved: false, qbitError }`. Caller renders partial
 *     toast. Instance data is already persisted so re-opening the
 *     dialog will show the new instance.
 *
 * Cache invalidations (post-success branch only):
 *   - `['instances']`        — list refetch
 *   - `instanceDetailKey(n)` — detail refetch
 *   - `qbitSettingsKey(n)`   — qbit refetch (whether or not qBit fired)
 *   - `webhookStatusKey(n)`  — badge refetch
 */
export function useSaveInstanceWithQbit() {
  const qc = useQueryClient();
  const create = useCreateInstance();
  const update = useUpdateInstance();
  // We deliberately do NOT compose useUpsertQbitSettings here because
  // its name binding is via a closure on `name`, and in create-mode we
  // only learn the name after POST resolves. Instead we call
  // `api`-equivalent fetch directly so the orchestrator is `name`-
  // agnostic until the create resolves. This mirrors the pattern in
  // 037-oidc-test-orchestrator.
  return useMutation<SaveWithQbitResult, ApiError, SaveWithQbitInput>({
    mutationFn: async ({ mode, name, instanceBody, qbitBody }) => {
      let detail: InstanceDetail;
      let resolvedName: string;
      if (mode === 'create') {
        const out = await create.mutateAsync({ body: instanceBody as InstanceCreateRequest });
        detail = out.detail;
        resolvedName = out.detail.name ?? '';
      } else {
        if (!name) throw new ApiError(400, 'name required');
        const out = await update.mutateAsync({ name, body: instanceBody as InstanceUpdateRequest });
        detail = out.detail;
        resolvedName = name;
      }

      let qbitSaved = false;
      let qbitError: ApiError | null = null;
      let qbitDTO: QbitSettingsDTO | null = null;
      if (qbitBody && resolvedName) {
        try {
          const { data } = await jsonFetch<QbitSettingsDTO>(
            `/api/v1/instances/${encodeURIComponent(resolvedName)}/qbit/settings`,
            { method: 'PUT', body: JSON.stringify(qbitBody) },
          );
          qbitSaved = true;
          qbitDTO = data ?? null;
          // Prime the cache with the fresh DTO so any concurrent
          // useQbitSettings() reader sees the new value synchronously
          // (no refetch flicker).
          if (qbitDTO) {
            qc.setQueryData<QbitSettingsDTO>(qbitSettingsKey(resolvedName), qbitDTO);
          }
        } catch (err) {
          qbitError = err instanceof ApiError ? err : new ApiError(0, String(err));
        }
      }

      qc.invalidateQueries({ queryKey: ['instances'] });
      if (resolvedName) {
        qc.invalidateQueries({ queryKey: instanceDetailKey(resolvedName) });
        qc.invalidateQueries({ queryKey: qbitSettingsKey(resolvedName) });
        qc.invalidateQueries({ queryKey: webhookStatusKey(resolvedName) });
      }

      return { detail, qbitSaved, qbitError, qbitDTO };
    },
  });
}
