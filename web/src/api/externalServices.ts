import { api } from '@/lib/api';

export type ExternalServiceName = 'tmdb' | 'omdb' | 'tvdb';

export type ExternalServiceOutcome =
  | 'ok'
  | 'auth_failed'
  | 'network'
  | 'timeout'
  | 'proxy_failed'
  | 'dns_blocked';

export interface ExternalServiceDTO {
  service: ExternalServiceName;
  enabled: boolean;
  api_key_masked: string;
  api_key_configured: boolean;
  proxy_url_set: boolean;
  proxy_auth_set: boolean;
  proxy_scheme?: string;
  proxy_host?: string;
  last_test_at?: string;
  last_test_outcome?: ExternalServiceOutcome;
  last_test_message?: string;
  // Story 489 (B-17): runtime validation status. Empty when the service
  // was never validated. 'valid' = inline probe or POST /test succeeded.
  // 'invalid_key' = either a live 401 was reported by the TMDB client
  // OR a validate-on-save Upsert was rejected by the upstream.
  last_validation_at?: string;
  last_validation_status?: 'valid' | 'invalid_key';
  last_validation_message?: string;
}

export interface ExternalServiceUpsertRequest {
  enabled: boolean;
  // Pointer semantics: omit → unchanged, "" → clear, non-empty → set.
  api_key?: string;
  proxy_url?: string;
  proxy_username?: string;
  proxy_password?: string;
}

export interface ExternalServiceTestResponse {
  outcome: ExternalServiceOutcome;
  message?: string;
  latency_ms: number;
}

export async function listExternalServices(): Promise<ExternalServiceDTO[]> {
  const res = await api<{ services: ExternalServiceDTO[] }>('/external-services');
  return res.services;
}

export async function upsertExternalService(
  service: ExternalServiceName,
  body: ExternalServiceUpsertRequest,
): Promise<ExternalServiceDTO> {
  return api<ExternalServiceDTO>(`/external-services/${service}`, {
    method: 'PUT',
    body,
  });
}

export async function testExternalService(
  service: ExternalServiceName,
): Promise<ExternalServiceTestResponse> {
  return api<ExternalServiceTestResponse>(`/external-services/${service}/test`, {
    method: 'POST',
  });
}
