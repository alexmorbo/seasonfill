import {
  useMutation, useQuery, useQueryClient,
  type UseMutationResult, type UseQueryResult,
} from '@tanstack/react-query';
import { ApiError, api } from '@/lib/api';
import type { components } from '@/api/schema';

// Ф8-U-6a request-workflow client. DTOs come from the generated OpenAPI
// schema (mirrors notificationAgents.ts). The list + the single-item
// approve/deny responses share the enriched `rest.requestItem` shape:
// `title` is present only when resolvable from the local catalog, `seasons`
// only on tv rows, `username` may be "".
export type RequestItem = components['schemas']['rest.requestItem'];
type RequestListResponse = components['schemas']['rest.requestListResponse'];

// Narrowed status/media unions for the FE. The wire types these as bare
// strings (openapi-typescript loses the Go enum), so we re-narrow at the
// display boundary.
export type RequestStatus = 'pending' | 'approved' | 'denied';
export type RequestMediaType = 'tv' | 'movie';

export const requestKeys = {
  list: ['requests'] as const,
};

// --- bare client fns ----------------------------------------------------

// CONTRACT: {items} envelope. Returns a plain (mutable) array so the caller
// can sort in place.
export async function listRequests(): Promise<RequestItem[]> {
  const res = await api<RequestListResponse>('/requests');
  return res.items ? [...res.items] : [];
}

export async function approveRequest(id: number): Promise<RequestItem> {
  return api<RequestItem>(`/requests/${id}/approve`, { method: 'POST' });
}

export async function denyRequest(id: number): Promise<RequestItem> {
  return api<RequestItem>(`/requests/${id}/deny`, { method: 'POST' });
}

// --- RQ hooks -----------------------------------------------------------

export function useRequests(): UseQueryResult<RequestItem[], ApiError> {
  return useQuery<RequestItem[], ApiError>({
    queryKey: requestKeys.list,
    queryFn: listRequests,
    staleTime: 30_000,
  });
}

// Approve/deny both reuse the same invalidate-on-settle reconcile. Toasts
// are owned by the page (Requests.tsx) so the confirm-dialog flow controls
// success/error copy — mirrors the InstanceQueue toast-in-page house style.
export function useApproveRequest(): UseMutationResult<RequestItem, ApiError, number> {
  const qc = useQueryClient();
  return useMutation<RequestItem, ApiError, number>({
    mutationFn: approveRequest,
    onSettled: () => {
      void qc.invalidateQueries({ queryKey: requestKeys.list });
    },
  });
}

export function useDenyRequest(): UseMutationResult<RequestItem, ApiError, number> {
  const qc = useQueryClient();
  return useMutation<RequestItem, ApiError, number>({
    mutationFn: denyRequest,
    onSettled: () => {
      void qc.invalidateQueries({ queryKey: requestKeys.list });
    },
  });
}
