import { describe, it, expect, beforeEach, vi } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter } from 'react-router-dom';
import { I18nextProvider } from 'react-i18next';
import { TooltipProvider } from '@/components/ui/tooltip';
import i18n from '@/i18n';
import { PageTitleProvider } from '@/components/shell/page-title-context';
import { DiscoveryPage } from './DiscoveryPage';

const mockApi = vi.fn();
vi.mock('@/lib/api', async () => {
  const a = await vi.importActual<typeof import('@/lib/api')>('@/lib/api');
  return { ...a, api: (p: string) => mockApi(p) };
});

function renderPage(initialPath = '/discovery') {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0, staleTime: 0 } },
  });
  return render(
    <QueryClientProvider client={qc}>
      <I18nextProvider i18n={i18n}>
        <TooltipProvider delayDuration={0}>
          <PageTitleProvider defaultTitle="__INITIAL__">
            <MemoryRouter initialEntries={[initialPath]}>
              <DiscoveryPage />
            </MemoryRouter>
          </PageTitleProvider>
        </TooltipProvider>
      </I18nextProvider>
    </QueryClientProvider>,
  );
}

const searchSample = {
  items: [
    { series_id: 91, tmdb_id: 9, title: 'Foundation', year: 2021, poster_hash: 'xyz', tmdb_rating: 8.1, in_library_instances: [] },
  ],
};

beforeEach(() => {
  mockApi.mockReset();
  mockApi.mockImplementation((p: string) => {
    if (p.startsWith('/discovery/rows')) return Promise.resolve({ rows: [] });
    if (p.startsWith('/discovery/search')) return Promise.resolve(searchSample);
    if (p.startsWith('/admin/instances')) return Promise.resolve({ instances: [] });
    return Promise.resolve({ items: [] });
  });
});

describe('<DiscoveryPage />', () => {
  it('renders the search bar and the discovery rails by default', async () => {
    renderPage();
    expect(screen.getByTestId('discovery-search-bar')).toBeInTheDocument();
    await waitFor(() =>
      expect(screen.getByTestId('discovery-rails')).toBeInTheDocument());
  });

  it('swaps rails for search results when >= 2 chars are typed', async () => {
    const user = userEvent.setup();
    renderPage();
    await waitFor(() =>
      expect(screen.getByTestId('discovery-rails')).toBeInTheDocument());

    await user.type(screen.getByTestId('discovery-search-input'), 'fo');

    await waitFor(() =>
      expect(screen.getByTestId('discovery-search-grid')).toBeInTheDocument());
    expect(screen.queryByTestId('discovery-rails')).toBeNull();
    expect(screen.getByText('Foundation')).toBeInTheDocument();
  });
});
