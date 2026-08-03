import { useQuery, type UseQueryResult } from '@tanstack/react-query';
import { ApiError, api } from './api';
import type { components } from '@/api/schema';

export type WebhookStatus = components['schemas']['dto.WebhookStatusDTO'];

export function useWebhookStatus(
  instance: string | null,
): UseQueryResult<WebhookStatus, ApiError> {
  return useQuery<WebhookStatus, ApiError>({
    queryKey: ['webhook', 'status', instance] as const,
    queryFn: () => api<WebhookStatus>(`/instances/${instance}/webhook/status`),
    enabled: Boolean(instance),
    staleTime: 30_000,
    refetchInterval: 60_000,
    refetchOnWindowFocus: false,
  });
}

export function webhookHealthy(s: WebhookStatus | undefined): boolean {
  if (!s) return false;
  return Boolean(s.installed) && !s.error;
}

// webhookInstalling reports the S2 pending state: a fresh instance whose
// webhook has not installed yet but is still inside the reconciler grace
// window. The badge/chip prefer a neutral loader over the error while this
// is true. Neutral, not healthy — webhookHealthy still returns false.
export function webhookInstalling(s: WebhookStatus | undefined): boolean {
  return Boolean(s?.installing) && !s?.installed;
}
