import {
  useMutation, useQuery, useQueryClient,
  type UseMutationResult, type UseQueryResult,
} from '@tanstack/react-query';
import { ApiError, api } from '@/lib/api';
import type { components } from '@/api/schema';

export type UserRole = 'admin' | 'user';
export type AuthSource = 'forms' | 'oidc' | 'jellyfin';

// The generated schema types role/auth_source as nominal TS enums, which reject
// plain string literals. Override them with structural string unions so call
// sites can pass 'admin'/'user'/'forms' directly.
export type UserItem =
  Omit<components['schemas']['rest.userItem'], 'role' | 'auth_source'>
  & { role?: UserRole; auth_source?: AuthSource };
export type UserPatch =
  Omit<components['schemas']['rest.userPatchRequest'], 'role'>
  & { role?: UserRole };
type UserListResponse = components['schemas']['rest.userListResponse'];

export const PERM_KEYS = [
  'auto_approve', 'request', 'manage_requests', 'manage_users', 'request_4k',
] as const;
export type PermKey = (typeof PERM_KEYS)[number];

export const userKeys = {
  list: ['admin', 'users'] as const,
};

export interface PatchUserVars {
  id: number;
  patch: UserPatch;
}

export async function listUsers(): Promise<UserItem[]> {
  const res = await api<UserListResponse>('/admin/users');
  return res.items ? [...res.items] : [];
}

export async function patchUser(id: number, patch: UserPatch): Promise<UserItem> {
  return api<UserItem>(`/admin/users/${id}`, { method: 'PATCH', body: patch });
}

export async function deleteUser(id: number): Promise<void> {
  await api<void>(`/admin/users/${id}`, { method: 'DELETE' });
}

function applyPatch(u: UserItem, patch: UserPatch): UserItem {
  const { role, ...perms } = patch;
  return {
    ...u,
    ...(role !== undefined ? { role } : {}),
    permissions: { ...u.permissions, ...perms },
  };
}

export function useUsers(): UseQueryResult<UserItem[], ApiError> {
  return useQuery<UserItem[], ApiError>({
    queryKey: userKeys.list,
    queryFn: listUsers,
    staleTime: 30_000,
  });
}

export function usePatchUser(): UseMutationResult<UserItem, ApiError, PatchUserVars, { snapshot: UserItem[] | undefined }> {
  const qc = useQueryClient();
  return useMutation<UserItem, ApiError, PatchUserVars, { snapshot: UserItem[] | undefined }>({
    mutationFn: ({ id, patch }) => patchUser(id, patch),
    onMutate: async ({ id, patch }) => {
      await qc.cancelQueries({ queryKey: userKeys.list });
      const snapshot = qc.getQueryData<UserItem[]>(userKeys.list);
      if (snapshot) {
        qc.setQueryData<UserItem[]>(
          userKeys.list,
          snapshot.map((u) => (u.id === id ? applyPatch(u, patch) : u)),
        );
      }
      return { snapshot };
    },
    onError: (_err, _vars, ctx) => {
      if (ctx?.snapshot) qc.setQueryData(userKeys.list, ctx.snapshot);
    },
    onSettled: () => {
      void qc.invalidateQueries({ queryKey: userKeys.list });
    },
  });
}

export function useDeleteUser(): UseMutationResult<void, ApiError, number> {
  const qc = useQueryClient();
  return useMutation<void, ApiError, number>({
    mutationFn: deleteUser,
    onSettled: () => {
      void qc.invalidateQueries({ queryKey: userKeys.list });
    },
  });
}
