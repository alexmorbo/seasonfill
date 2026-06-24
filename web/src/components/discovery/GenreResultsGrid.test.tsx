import { describe, it, expect, beforeEach, vi } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter } from 'react-router-dom';
import { I18nextProvider } from 'react-i18next';
import { TooltipProvider } from '@/components/ui/tooltip';
import i18n from '@/i18n';
import { GenreResultsGrid } from './GenreResultsGrid';

const mockApi = vi.fn();
vi.mock('@/lib/api', async () => {
  const a = await vi.importActual<typeof import('@/lib/api')>('@/lib/api');
  return { ...a, api: (p: string) => mockApi(p) };
});

function renderGrid(genreId = 18) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0, staleTime: 0 } },
  });
  return render(
    <QueryClientProvider client={qc}>
      <I18nextProvider i18n={i18n}>
        <TooltipProvider delayDuration={0}>
          <MemoryRouter><GenreResultsGrid genreId={genreId} /></MemoryRouter>
        </TooltipProvider>
      </I18nextProvider>
    </QueryClientProvider>,
  );
}

const sample = {
  items: [
    { series_id: 91, tmdb_id: 9, title: 'Fargo', year: 2014,
      poster_path: '/f.jpg', in_library_instances: [] },
  ],
};

beforeEach(() => mockApi.mockReset());

describe('<GenreResultsGrid />', () => {
  it('renders grid after resolve', async () => {
    mockApi.mockResolvedValueOnce(sample);
    renderGrid(18);
    await waitFor(() =>
      expect(screen.getByTestId('discovery-genre-grid')).toBeInTheDocument());
    expect(screen.getByText('Fargo')).toBeInTheDocument();
  });

  it('renders error alert on failure', async () => {
    mockApi.mockRejectedValueOnce(new Error('boom'));
    renderGrid(18);
    await waitFor(() =>
      expect(screen.getByTestId('discovery-genre-error')).toBeInTheDocument());
  });

  it('renders empty state when items is empty', async () => {
    mockApi.mockResolvedValueOnce({ items: [] });
    renderGrid(18);
    await waitFor(() => {
      expect(screen.queryByTestId('discovery-genre-grid')).toBeNull();
      expect(screen.queryByTestId('discovery-genre-skeleton')).toBeNull();
    });
    expect(screen.getByRole('heading', { level: 3 })).toBeInTheDocument();
  });

  it('shows warming banner + skeleton on cold-start', async () => {
    mockApi.mockResolvedValueOnce({
      items: [], degraded: ['discovery_warming'],
      warming_estimate_seconds: 15,
    });
    renderGrid(18);
    await waitFor(() =>
      expect(screen.getByTestId('discovery-genre-warming')).toBeInTheDocument());
    expect(screen.getByTestId('discovery-warming-banner')).toHaveAttribute(
      'data-kind', 'cold_start',
    );
  });
});
