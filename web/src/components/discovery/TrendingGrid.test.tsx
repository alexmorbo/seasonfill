import { describe, it, expect, beforeEach, vi } from 'vitest';
import { render, screen, waitFor, act } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter } from 'react-router-dom';
import { I18nextProvider } from 'react-i18next';
import { TooltipProvider } from '@/components/ui/tooltip';
import i18n from '@/i18n';
import { TrendingGrid } from './TrendingGrid';

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
          <MemoryRouter><TrendingGrid /></MemoryRouter>
        </TooltipProvider>
      </I18nextProvider>
    </QueryClientProvider>,
  );
}

const sample = {
  items: [
    { series_id: 31, tmdb_id: 1, title: 'Rick and Morty', year: 2013,
      poster_path: '/a.jpg', in_library_instances: [] },
    { series_id: 32, tmdb_id: 2, title: 'Severance', year: 2022,
      poster_path: '/b.jpg', in_library_instances: ['sonarr-alpha'] },
  ],
  cache_status: 'hit',
};

beforeEach(() => mockApi.mockReset());

describe('<TrendingGrid />', () => {
  it('shows skeleton then grid as the deferred query resolves', async () => {
    let resolve: (v: typeof sample) => void = () => {};
    mockApi.mockReturnValueOnce(new Promise<typeof sample>((r) => { resolve = r; }));
    renderGrid();
    expect(screen.getByTestId('discovery-trending-skeleton')).toBeInTheDocument();
    resolve(sample);
    await waitFor(() =>
      expect(screen.getByTestId('discovery-trending-grid')).toBeInTheDocument());
    expect(screen.getByText('Rick and Morty')).toBeInTheDocument();
    expect(screen.getByText('Severance')).toBeInTheDocument();
  });

  it('renders error alert on failure', async () => {
    mockApi.mockRejectedValueOnce(new Error('boom'));
    renderGrid();
    await waitFor(() =>
      expect(screen.getByTestId('discovery-trending-error')).toBeInTheDocument());
  });

  it('renders empty state when items is empty', async () => {
    mockApi.mockResolvedValueOnce({ items: [], cache_status: 'hit' });
    renderGrid();
    await waitFor(() => {
      expect(screen.queryByTestId('discovery-trending-grid')).toBeNull();
      expect(screen.queryByTestId('discovery-trending-skeleton')).toBeNull();
    });
    expect(screen.getByRole('heading', { level: 3 })).toBeInTheDocument();
  });

  it('shows warming banner + skeleton on cold-start', async () => {
    mockApi.mockResolvedValueOnce({
      items: [], degraded: ['discovery_warming'],
      warming_estimate_seconds: 18, cache_status: 'warming',
    });
    renderGrid();
    await waitFor(() =>
      expect(screen.getByTestId('discovery-trending-warming')).toBeInTheDocument());
    const banner = screen.getByTestId('discovery-warming-banner');
    expect(banner).toHaveAttribute('data-kind', 'cold_start');
    expect(banner.textContent).toMatch(/18/);
    expect(
      screen.getByTestId('discovery-trending-warming-skeleton'),
    ).toBeInTheDocument();
  });

  it('fetches with the active locale and refetches on locale switch', async () => {
    mockApi.mockResolvedValue(sample);
    renderGrid();
    await waitFor(() =>
      expect(screen.getByTestId('discovery-trending-grid')).toBeInTheDocument());
    expect(mockApi).toHaveBeenCalledWith(expect.stringContaining('lang=en-US'));
    try {
      await act(async () => { await i18n.changeLanguage('ru-RU'); });
      await waitFor(() =>
        expect(mockApi).toHaveBeenCalledWith(expect.stringContaining('lang=ru-RU')));
    } finally {
      await act(async () => { await i18n.changeLanguage('en-US'); });
    }
  });

  it('shows banner above grid when degraded but items present', async () => {
    mockApi.mockResolvedValueOnce({
      items: [{ series_id: 31, tmdb_id: 1, title: 'Rick and Morty', year: 2013,
        poster_path: '/a.jpg', in_library_instances: [] }],
      degraded: ['tmdb_throttled'], retry_after_seconds: 4,
    });
    renderGrid();
    await waitFor(() =>
      expect(screen.getByTestId('discovery-trending-grid')).toBeInTheDocument());
    const banner = screen.getByTestId('discovery-warming-banner');
    expect(banner).toHaveAttribute('data-kind', 'tmdb_throttled');
  });
});
