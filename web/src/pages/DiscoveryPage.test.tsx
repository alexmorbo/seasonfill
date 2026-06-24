import { describe, it, expect, beforeEach, vi } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter, Routes, Route, useLocation } from 'react-router-dom';
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

function LocationProbe() {
  const loc = useLocation();
  return <div data-testid="loc">{loc.pathname}{loc.search}</div>;
}

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
              <Routes>
                <Route path="/discovery" element={
                  <><DiscoveryPage /><LocationProbe /></>
                } />
              </Routes>
            </MemoryRouter>
          </PageTitleProvider>
        </TooltipProvider>
      </I18nextProvider>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  mockApi.mockReset();
  mockApi.mockResolvedValue({ items: [], cache_status: 'hit' });
});

const trendingTab = () => screen.getByRole('tab', { name: /trending|тренды/i });
const popularTab = () => screen.getByRole('tab', { name: /popular|популярное/i });
const genresTab = () => screen.getByRole('tab', { name: /genres|жанры/i });

describe('<DiscoveryPage />', () => {
  it('renders 4 tabs and defaults to trending when ?tab is absent or invalid', () => {
    renderPage();
    expect(screen.getByTestId('discovery-tabs').querySelectorAll('[role="tab"]'))
      .toHaveLength(4);
    expect(trendingTab().getAttribute('data-state')).toBe('active');
  });

  it('respects ?tab=popular and falls back to trending when unknown', () => {
    const { unmount } = renderPage('/discovery?tab=popular');
    expect(popularTab().getAttribute('data-state')).toBe('active');
    unmount();
    renderPage('/discovery?tab=garbage');
    expect(trendingTab().getAttribute('data-state')).toBe('active');
  });

  it('updates URL when user activates a different tab', async () => {
    const user = userEvent.setup();
    renderPage();
    await user.click(genresTab());
    await waitFor(() =>
      expect(screen.getByTestId('loc').textContent ?? '').toContain('tab=genres'));
  });
});
