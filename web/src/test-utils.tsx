import { type ReactElement } from 'react';
import { render, type RenderOptions } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter } from 'react-router-dom';
import { TooltipProvider } from '@/components/ui/tooltip';
import { PageTitleProvider } from './components/shell/page-title-context';

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
        <PageTitleProvider defaultTitle="__INITIAL__">
          <QueryClientProvider client={qc}>
            <TooltipProvider delayDuration={0}>
              <MemoryRouter initialEntries={[opts.route ?? '/']}>{children}</MemoryRouter>
            </TooltipProvider>
          </QueryClientProvider>
        </PageTitleProvider>
      ),
      ...opts,
    }),
  };
}
