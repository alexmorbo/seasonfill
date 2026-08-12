import { api } from '@/lib/api';
import type { components } from '@/api/schema';

// DTOs are sourced from the generated OpenAPI schema (ADR-0016 N1). The
// View is MASKED: it carries `configured` + `scheme` only, NEVER the raw
// shoutrrr URL (which lives AES-GCM encrypted server-side).
export type NotificationAgentView = components['schemas']['dto.NotificationAgentView'];
export type NotificationAgentCreateRequest =
  components['schemas']['dto.NotificationAgentCreateRequest'];
// Pointer semantics on update (mirrors ExternalServiceUpsertRequest.api_key):
// `url` omitted → keep the existing ciphertext; non-empty → replace it. The
// form never sends `url` unless the operator typed a new one.
export type NotificationAgentUpdateRequest =
  components['schemas']['dto.NotificationAgentUpdateRequest'];
export type NotificationTestResponse =
  components['schemas']['dto.NotificationTestResponse'];
type NotificationAgentListResponse =
  components['schemas']['dto.NotificationAgentListResponse'];

// Event dictionary (S1 subset — ADR-0016 D3). Order is the display order in
// the checkbox grid. DEFAULT_EVENT_TYPES = the set checked on a fresh agent
// (grab.ok is intentionally OFF by default).
export const EVENT_TYPES = [
  'grab.failed',
  'import.failed',
  'grab.ok',
  'watchdog.regrab',
  'inbox.dead_letter',
  'season.premiere',
  'air_date.announced',
  'digest.weekly',
  'request.approved',
  'request.denied',
] as const;

export const DEFAULT_EVENT_TYPES: readonly string[] = [
  'grab.failed',
  'import.failed',
  'watchdog.regrab',
  'inbox.dead_letter',
  'season.premiere',
  'air_date.announced',
  'digest.weekly',
  'request.approved',
  'request.denied',
];

const BASE_PATH = '/notification-agents';

export async function listAgents(): Promise<NotificationAgentView[]> {
  const res = await api<NotificationAgentListResponse>(BASE_PATH);
  return res.agents ? [...res.agents] : [];
}

export async function getAgent(id: number): Promise<NotificationAgentView> {
  return api<NotificationAgentView>(`${BASE_PATH}/${id}`);
}

export async function createAgent(
  body: NotificationAgentCreateRequest,
): Promise<NotificationAgentView> {
  return api<NotificationAgentView>(BASE_PATH, { method: 'POST', body });
}

export async function updateAgent(
  id: number,
  body: NotificationAgentUpdateRequest,
): Promise<NotificationAgentView> {
  return api<NotificationAgentView>(`${BASE_PATH}/${id}`, { method: 'PUT', body });
}

export async function deleteAgent(id: number): Promise<void> {
  await api<void>(`${BASE_PATH}/${id}`, { method: 'DELETE' });
}

export async function testAgent(id: number): Promise<NotificationTestResponse> {
  return api<NotificationTestResponse>(`${BASE_PATH}/${id}/test`, { method: 'POST' });
}
