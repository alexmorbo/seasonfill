import { type ReactElement } from 'react';
import { render, type RenderOptions } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter } from 'react-router-dom';

export function renderWithProviders(
  ui: ReactElement,
  opts: { route?: string } & Omit<RenderOptions, 'wrapper'> = {},
) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0, staleTime: 0 } },
  });
  return {
    qc,
    ...render(ui, {
      wrapper: ({ children }) => (
        <QueryClientProvider client={qc}>
          <MemoryRouter initialEntries={[opts.route ?? '/']}>{children}</MemoryRouter>
        </QueryClientProvider>
      ),
      ...opts,
    }),
  };
}
