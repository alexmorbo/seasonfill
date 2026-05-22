import { QueryClient } from '@tanstack/react-query';
import { ApiError } from './api';

// 401/403/404 are non-retryable: auth failure won't fix itself, and a
// missing resource won't materialise. Network errors and 5xx still
// retry up to 2 attempts.
const NON_RETRYABLE = new Set([401, 403, 404]);

export const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 10_000,
      gcTime: 5 * 60_000,
      refetchOnWindowFocus: false,
      retry: (n, err) => {
        if (err instanceof ApiError && NON_RETRYABLE.has(err.status)) return false;
        return n < 2;
      },
    },
    mutations: { retry: false },
  },
});
