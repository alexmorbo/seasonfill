import { useQuery, type UseQueryResult, keepPreviousData } from '@tanstack/react-query';
import { ApiError, api } from '@/lib/api';

// Inline mirror of dto.WebhookStatusAggregateItem (story 047b L537-547).
// `error` is the raw Go error — NOT surfaced as UI copy; AlertsCard
// renders the i18n bodyFallback and attaches `error` to `title=`.
export interface WebhookStatusItem {
  instance_name: string;
  installed: boolean;
  healthy: boolean;
  notification_id?: number;
  url?: string;
  error?: string;
}
export interface WebhookStatusAggregate {
  items: WebhookStatusItem[];
  healthy_count: number;
  unhealthy_count: number;
}

export function useWebhookStatusAggregate(): UseQueryResult<WebhookStatusAggregate, ApiError> {
  return useQuery<WebhookStatusAggregate, ApiError>({
    queryKey: ['webhook-status'] as const,
    queryFn: () => api<WebhookStatusAggregate>('/webhooks/status'),
    staleTime: 60_000, refetchInterval: 60_000,
    refetchOnWindowFocus: false, placeholderData: keepPreviousData,
  });
}
