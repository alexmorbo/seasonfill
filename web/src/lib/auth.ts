import { useQuery } from '@tanstack/react-query';
import { ApiError, api } from './api';

export type AuthState = 'pending' | 'authenticated' | 'unauthenticated';

export function useAuth(): { state: AuthState; isAuthed: boolean } {
  const q = useQuery({
    queryKey: ['auth', 'session'] as const,
    queryFn: () => api<{ ok: true }>('/auth/session'),
    retry: (n, err) => !(err instanceof ApiError) || (err.status !== 401 && n < 2),
    staleTime: 30_000,
    refetchOnWindowFocus: false,
  });
  if (q.isPending) return { state: 'pending', isAuthed: false };
  if (q.isError) return { state: 'unauthenticated', isAuthed: false };
  return { state: 'authenticated', isAuthed: true };
}

export async function login(apiKey: string): Promise<void> {
  await api<{ ok: true }>('/auth/login', { method: 'POST', body: { api_key: apiKey } });
}

export async function logout(): Promise<void> {
  await api<void>('/auth/session', { method: 'DELETE' });
}
