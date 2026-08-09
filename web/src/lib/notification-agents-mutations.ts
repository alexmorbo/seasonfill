import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import type { UseQueryResult } from '@tanstack/react-query';
import { toast } from 'sonner';
import i18n from '@/i18n';
import { ApiError } from './api';
import {
  createAgent,
  deleteAgent,
  getAgent,
  listAgents,
  testAgent,
  updateAgent,
  type NotificationAgentCreateRequest,
  type NotificationAgentUpdateRequest,
  type NotificationAgentView,
  type NotificationTestResponse,
} from '@/api/notificationAgents';

export const agentsKey = ['notification-agents'] as const;
export const agentDetailKey = (id: number) => ['notification-agent', id] as const;
const agentDetailDisabledKey = ['notification-agent-disabled'] as const;

export function useAgents(): UseQueryResult<NotificationAgentView[], ApiError> {
  return useQuery<NotificationAgentView[], ApiError>({
    queryKey: agentsKey,
    queryFn: listAgents,
  });
}

export function useAgent(
  id: number | null,
): UseQueryResult<NotificationAgentView, ApiError> {
  return useQuery<NotificationAgentView, ApiError>({
    queryKey: id != null ? agentDetailKey(id) : agentDetailDisabledKey,
    queryFn: () => {
      if (id == null) throw new ApiError(400, 'id required');
      return getAgent(id);
    },
    enabled: id != null,
  });
}

export function useCreateAgent() {
  const qc = useQueryClient();
  return useMutation<NotificationAgentView, ApiError, NotificationAgentCreateRequest>({
    mutationFn: (body) => createAgent(body),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: agentsKey });
      toast.success(i18n.t('settings.agents.savedOk'));
    },
    onError: (err) => {
      toast.error(i18n.t('settings.agents.savedErr', { err: err.message }));
    },
  });
}

export interface UpdateAgentInput {
  readonly id: number;
  readonly body: NotificationAgentUpdateRequest;
}

export function useUpdateAgent() {
  const qc = useQueryClient();
  return useMutation<NotificationAgentView, ApiError, UpdateAgentInput>({
    mutationFn: ({ id, body }) => updateAgent(id, body),
    onSuccess: (_data, vars) => {
      qc.invalidateQueries({ queryKey: agentsKey });
      qc.invalidateQueries({ queryKey: agentDetailKey(vars.id) });
      toast.success(i18n.t('settings.agents.savedOk'));
    },
    onError: (err) => {
      toast.error(i18n.t('settings.agents.savedErr', { err: err.message }));
    },
  });
}

export function useDeleteAgent() {
  const qc = useQueryClient();
  return useMutation<void, ApiError, number>({
    mutationFn: (id) => deleteAgent(id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: agentsKey });
      toast.success(i18n.t('settings.agents.deletedOk'));
    },
    onError: (err) => {
      toast.error(i18n.t('settings.agents.savedErr', { err: err.message }));
    },
  });
}

// useTestAgent — POST /:id/test. The BE decrypts the stored config and fires
// a fixed test message; the response is a bare `{ ok: boolean }`. ok:false is
// a delivery failure (surfaced as an error toast); a thrown ApiError is a
// transport failure (also an error toast, with the message interpolated).
export function useTestAgent() {
  return useMutation<NotificationTestResponse, ApiError, number>({
    mutationFn: (id) => testAgent(id),
    onSuccess: (res) => {
      if (res.ok) {
        toast.success(i18n.t('settings.agents.testOk'));
      } else {
        toast.error(i18n.t('settings.agents.testErr', { err: '' }));
      }
    },
    onError: (err) => {
      toast.error(i18n.t('settings.agents.testErr', { err: `: ${err.message}` }));
    },
  });
}
