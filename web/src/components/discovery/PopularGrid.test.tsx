import { describe, it, expect, beforeEach, vi } from 'vitest';
import { render, screen, waitFor, act } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter } from 'react-router-dom';
import { I18nextProvider } from 'react-i18next';
import { TooltipProvider } from '@/components/ui/tooltip';
import i18n from '@/i18n';
import { PopularGrid } from './PopularGrid';

const mockApi = vi.fn();
vi.mock('@/lib/api', async () => {
  const a = await vi.importActual<typeof import('@/lib/api')>('@/lib/api');
  return { ...a, api: (p: string) => mockApi(p) };
});

function renderGrid() {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0, staleTime: 0 } },
  });
  return render(
    <QueryClientProvider client={qc}>
      <I18nextProvider i18n={i18n}>
        <TooltipProvider delayDuration={0}>
          <MemoryRouter><PopularGrid /></MemoryRouter>
        </TooltipProvider>
      </I18nextProvider>
    </QueryClientProvider>,
  );
}

const sample = {
  items: [
    { series_id: 81, tmdb_id: 7, title: 'Breaking Bad', year: 2008,
      poster_path: '/bb.jpg', in_library_instances: [] },
    { series_id: 82, tmdb_id: 8, title: 'The Wire', year: 2002,
      poster_path: '/wire.jpg', in_library_instances: ['sonarr-alpha'] },
  ],
  cache_status: 'hit',
};

beforeEach(() => mockApi.mockReset());

describe('<PopularGrid />', () => {
  it('shows skeleton then grid as the deferred query resolves', async () => {
    let resolve: (v: typeof sample) => void = () => {};
    mockApi.mockReturnValueOnce(new Promise<typeof sample>((r) => { resolve = r; }));
    renderGrid();
    expect(screen.getByTestId('discovery-popular-skeleton')).toBeInTheDocument();
    resolve(sample);
    await waitFor(() =>
      expect(screen.getByTestId('discovery-popular-grid')).toBeInTheDocument());
    expect(screen.getByText('Breaking Bad')).toBeInTheDocument();
    expect(screen.getByText('The Wire')).toBeInTheDocument();
  });

  it('renders error alert on failure', async () => {
    mockApi.mockRejectedValueOnce(new Error('boom'));
    renderGrid();
    await waitFor(() =>
      expect(screen.getByTestId('discovery-popular-error')).toBeInTheDocument());
  });

  it('renders empty state when items is empty', async () => {
    mockApi.mockResolvedValueOnce({ items: [], cache_status: 'hit' });
    renderGrid();
    await waitFor(() => {
      expect(screen.queryByTestId('discovery-popular-grid')).toBeNull();
      expect(screen.queryByTestId('discovery-popular-skeleton')).toBeNull();
    });
    expect(screen.getByRole('heading', { level: 3 })).toBeInTheDocument();
  });

  it('fetches with the active locale and refetches on locale switch', async () => {
    mockApi.mockResolvedValue(sample);
    renderGrid();
    await waitFor(() =>
      expect(screen.getByTestId('discovery-popular-grid')).toBeInTheDocument());
    expect(mockApi).toHaveBeenCalledWith(expect.stringContaining('lang=en-US'));
    try {
      await act(async () => { await i18n.changeLanguage('ru-RU'); });
      await waitFor(() =>
        expect(mockApi).toHaveBeenCalledWith(expect.stringContaining('lang=ru-RU')));
    } finally {
      await act(async () => { await i18n.changeLanguage('en-US'); });
    }
  });

  it('shows warming banner + skeleton on cold-start', async () => {
    mockApi.mockResolvedValueOnce({
      items: [], degraded: ['discovery_warming'],
      warming_estimate_seconds: 24, cache_status: 'warming',
    });
    renderGrid();
    await waitFor(() =>
      expect(screen.getByTestId('discovery-popular-warming')).toBeInTheDocument());
    expect(screen.getByTestId('discovery-warming-banner')).toHaveAttribute(
      'data-kind', 'cold_start',
    );
    expect(
      screen.getByTestId('discovery-popular-warming-skeleton'),
    ).toBeInTheDocument();
  });
});
