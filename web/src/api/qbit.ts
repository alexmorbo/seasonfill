import {
  useMutation,
  useQuery,
  useQueryClient,
  type UseMutationResult,
  type UseQueryResult,
} from '@tanstack/react-query';
import { toast } from 'sonner';
import i18n from '@/i18n';
import { ApiError, api } from '@/lib/api';
import type { components } from '@/api/schema';

// Type aliases off the regenerated schema (039d + 039e).
export type QbitSettingsDTO = components['schemas']['dto.QbitSettingsDTO'];
export type QbitSettingsUpsertRequest =
  components['schemas']['dto.QbitSettingsUpsertRequest'];
export type QbitDiscoverDTO = components['schemas']['dto.QbitDiscoverDTO'];
export type WebhookInstallDTO = components['schemas']['dto.WebhookInstallDTO'];
export type WebhookStatusDTO = components['schemas']['dto.WebhookStatusDTO'];

// Query keys — exported so tests can prime/inspect the cache without
// guessing string literals.
export const qbitSettingsKey = (name: string) =>
  ['qbit', 'settings', name] as const;
export const qbitDiscoverKey = (name: string) =>
  ['qbit', 'discover', name] as const;
export const webhookStatusKey = (name: string) =>
  ['qbit', 'webhook-status', name] as const;

// Helper to surface the backend's `code` field out of an ApiError body.
// Mirrors the pattern used by instances-mutations.ts errorCode().
function errorCode(err: ApiError): string {
  if (typeof err.body === 'object' && err.body !== null && 'code' in err.body) {
    const c = (err.body as { code: unknown }).code;
    return typeof c === 'string' ? c : '';
  }
  return '';
}

// useQbitSettings — GET with 404-tolerance.
//
// The "no settings yet" state is the most common one for a freshly-
// added instance, so we MUST NOT treat 404 as an error: the form
// renders defaults and the banner instead. Any other non-2xx (401,
// 5xx, etc.) propagates as a normal query error.
export function useQbitSettings(
  name: string | null,
): UseQueryResult<QbitSettingsDTO | null, ApiError> {
  return useQuery<QbitSettingsDTO | null, ApiError>({
    queryKey: name ? qbitSettingsKey(name) : ['qbit', 'settings', '__disabled__'],
    queryFn: async () => {
      if (!name) throw new ApiError(400, 'name required');
      try {
        return await api<QbitSettingsDTO>(
          `/instances/${encodeURIComponent(name)}/qbit/settings`,
        );
      } catch (err) {
        if (err instanceof ApiError && err.status === 404) {
          // 039d AC-1: QBIT_SETTINGS_NOT_FOUND vs INSTANCE_NOT_FOUND
          // share the HTTP status. The form treats both the same way
          // (render defaults); a stale INSTANCE_NOT_FOUND is handled
          // by the parent dialog refusing to open.
          return null;
        }
        throw err;
      }
    },
    enabled: Boolean(name),
    staleTime: 0,
  });
}

export interface UpsertQbitInput {
  readonly body: QbitSettingsUpsertRequest;
}

// useUpsertQbitSettings — PUT with toast + error-code mapping.
//
// 039d codes the handler returns:
//   400 BAD_REQUEST           → field-level validation surfaced inline
//                               by the caller; we still toast a fallback.
//   409 WEBHOOK_NOT_INSTALLED → operator must click "Install webhook"
//                               first; toast guides them there.
//   401 / 403                 → handled centrally by api.ts handle401.
export function useUpsertQbitSettings(
  name: string,
): UseMutationResult<QbitSettingsDTO, ApiError, UpsertQbitInput> {
  const qc = useQueryClient();
  return useMutation<QbitSettingsDTO, ApiError, UpsertQbitInput>({
    mutationFn: ({ body }) =>
      api<QbitSettingsDTO>(
        `/instances/${encodeURIComponent(name)}/qbit/settings`,
        { method: 'PUT', body },
      ),
    onSuccess: (dto) => {
      qc.setQueryData<QbitSettingsDTO>(qbitSettingsKey(name), dto);
      qc.invalidateQueries({ queryKey: qbitSettingsKey(name) });
      toast.success(i18n.t('settings.instances.form.watchdog.actions.saveSuccess'));
    },
    onError: (err) => {
      const code = errorCode(err);
      if (err.status === 409 && code === 'WEBHOOK_NOT_INSTALLED') {
        toast.error(
          i18n.t('settings.instances.form.watchdog.actions.webhookRequired'),
        );
        return;
      }
      if (err.status === 400) {
        // Caller maps per-field errors inline; we still emit a generic
        // fallback toast so the failure isn't silent if the field-error
        // mapper misses something.
        toast.error(err.message || i18n.t('toasts.instanceSaveFailed', { error: err.message }));
        return;
      }
      toast.error(i18n.t('toasts.instanceSaveFailed', { error: err.message }));
    },
  });
}

// useDeleteQbitSettings — present for completeness, but the v1 UI
// does not surface a Delete button. The hook is exported so a future
// "Reset to defaults" CTA can adopt it without touching this module.
export function useDeleteQbitSettings(
  name: string,
): UseMutationResult<void, ApiError, void> {
  const qc = useQueryClient();
  return useMutation<void, ApiError, void>({
    mutationFn: () =>
      api<void>(
        `/instances/${encodeURIComponent(name)}/qbit/settings`,
        { method: 'DELETE' },
      ),
    onSuccess: () => {
      qc.setQueryData<QbitSettingsDTO | null>(qbitSettingsKey(name), null);
      qc.invalidateQueries({ queryKey: qbitSettingsKey(name) });
    },
  });
}

export interface DiscoverOptions {
  readonly enabled: boolean;
}

// useDiscoverQbit — manually triggered. The caller renders the
// "Auto-fill from Sonarr" button and flips `enabled` to true only
// when the user clicks it. Disabled by default so opening the tab
// doesn't fire a Sonarr round-trip.
//
// The hook intentionally uses `refetch` rather than mutation semantics
// because the response is idempotent and ideally cached for the
// dialog lifetime — clicking again should re-poll without showing
// "in flight" twice.
export function useDiscoverQbit(
  name: string | null,
  opts: DiscoverOptions,
): UseQueryResult<QbitDiscoverDTO, ApiError> {
  return useQuery<QbitDiscoverDTO, ApiError>({
    queryKey: name ? qbitDiscoverKey(name) : ['qbit', 'discover', '__disabled__'],
    queryFn: () => {
      if (!name) throw new ApiError(400, 'name required');
      return api<QbitDiscoverDTO>(
        `/instances/${encodeURIComponent(name)}/discover/qbit`,
      );
    },
    enabled: Boolean(name) && opts.enabled,
    staleTime: 0,
    gcTime: 0,
    retry: false,
  });
}

// useWebhookStatus — GET /webhook/status. Queries Sonarr in real-time
// rather than inferring state from the qbit settings row. Re-checks on
// focus so the banner refreshes when the operator installs the webhook
// in Sonarr directly without going through the UI.
export function useWebhookStatus(
  name: string | null,
): UseQueryResult<WebhookStatusDTO, ApiError> {
  return useQuery<WebhookStatusDTO, ApiError>({
    queryKey: name ? webhookStatusKey(name) : ['qbit', 'webhook-status', '__disabled__'],
    queryFn: () => {
      if (!name) throw new ApiError(400, 'name required');
      return api<WebhookStatusDTO>(
        `/instances/${encodeURIComponent(name)}/webhook/status`,
      );
    },
    enabled: Boolean(name),
    staleTime: 10_000,
    refetchOnWindowFocus: true,
  });
}

// useInstallWebhook — POST /webhook/install with friendly error
// mapping. 412 PUBLIC_URL_UNCONFIGURED is the most-likely first-time
// failure and the caller surfaces an inline action linking to
// /settings#webhooks; we still toast a generic message for visibility
// if the dialog has scrolled past the banner.
export function useInstallWebhook(
  name: string,
): UseMutationResult<WebhookInstallDTO, ApiError, void> {
  const qc = useQueryClient();
  return useMutation<WebhookInstallDTO, ApiError, void>({
    mutationFn: () =>
      api<WebhookInstallDTO>(
        `/instances/${encodeURIComponent(name)}/webhook/install`,
        { method: 'POST' },
      ),
    onSuccess: (dto) => {
      // Settings query reflects the (potentially newly-unblocked)
      // webhook state — the enabled Switch reads from the banner's
      // local installed state, but downstream callers may key off
      // settings cache freshness too.
      qc.invalidateQueries({ queryKey: qbitSettingsKey(name) });
      // Status query must refresh so the banner flips to green
      // without needing a window focus event.
      qc.invalidateQueries({ queryKey: webhookStatusKey(name) });
      if (dto.created) {
        toast.success(
          i18n.t('settings.instances.form.watchdog.webhookGate.installSuccess'),
        );
      } else {
        // Idempotent path — 200 with created:false. Still positive,
        // banner flips to "installed" either way.
        toast.success(
          i18n.t('settings.instances.form.watchdog.webhookGate.installed'),
        );
      }
    },
    onError: (err) => {
      const code = errorCode(err);
      if (err.status === 412 && code === 'PUBLIC_URL_UNCONFIGURED') {
        toast.error(
          i18n.t('settings.instances.form.watchdog.webhookGate.publicUrlMissing'),
        );
        return;
      }
      toast.error(i18n.t('toasts.instanceSaveFailed', { error: err.message }));
    },
  });
}
