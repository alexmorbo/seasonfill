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
