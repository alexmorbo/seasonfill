import { QueryClient } from '@tanstack/react-query';
import { ApiError } from './api';

export const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 10_000,
      gcTime: 5 * 60_000,
      refetchOnWindowFocus: false,
      retry: (n, err) => {
        if (err instanceof ApiError && (err.status === 401 || err.status === 404)) return false;
        return n < 2;
      },
    },
    mutations: { retry: false },
  },
});
