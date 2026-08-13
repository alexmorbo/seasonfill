import { describe, it, expect, beforeEach, vi } from 'vitest';
import { render, screen, waitFor, act, fireEvent } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter } from 'react-router-dom';
import { I18nextProvider } from 'react-i18next';
import { TooltipProvider } from '@/components/ui/tooltip';
import { AddToSonarrProvider } from '@/components/discovery/AddToSonarrProvider';
import i18n from '@/i18n';
import { SearchResults } from './SearchResults';

const mockApi = vi.fn();
vi.mock('@/lib/api', async () => {
  const a = await vi.importActual<typeof import('@/lib/api')>('@/lib/api');
  return { ...a, api: (p: string) => mockApi(p) };
});

function renderResults(q: string) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0, staleTime: 0 } },
  });
  return render(
    <QueryClientProvider client={qc}>
      <I18nextProvider i18n={i18n}>
        <TooltipProvider delayDuration={0}>
          <MemoryRouter><AddToSonarrProvider><SearchResults q={q} /></AddToSonarrProvider></MemoryRouter>
        </TooltipProvider>
      </I18nextProvider>
    </QueryClientProvider>,
  );
}

const sample = {
  items: [
    { series_id: 71, tmdb_id: 5, title: 'Fargo', year: 2014,
      poster_path: '/f.jpg', in_library_instances: [] },
  ],
};

beforeEach(() => mockApi.mockReset());

describe('<SearchResults />', () => {
  it('renders nothing when q is shorter than 2 chars', () => {
    const { container } = renderResults('a');
    expect(container.firstChild).toBeNull();
    expect(mockApi).not.toHaveBeenCalled();
  });

  it('renders skeleton then grid for a valid query', async () => {
    let resolve: (v: typeof sample) => void = () => {};
    mockApi.mockReturnValueOnce(new Promise<typeof sample>((r) => { resolve = r; }));
    renderResults('fargo');
    expect(screen.getByTestId('discovery-search-skeleton')).toBeInTheDocument();
    resolve(sample);
    await waitFor(() =>
      expect(screen.getByTestId('discovery-search-grid')).toBeInTheDocument());
    expect(screen.getByText('Fargo')).toBeInTheDocument();
  });

  it('renders error alert on failure', async () => {
    mockApi.mockRejectedValueOnce(new Error('boom'));
    renderResults('fargo');
    await waitFor(() =>
      expect(screen.getByTestId('discovery-search-error')).toBeInTheDocument());
  });

  it('fetches with the active locale and refetches on locale switch', async () => {
    mockApi.mockResolvedValue(sample);
    renderResults('fargo');
    await waitFor(() =>
      expect(screen.getByTestId('discovery-search-grid')).toBeInTheDocument());
    expect(mockApi).toHaveBeenCalledWith(expect.stringContaining('lang=en-US'));
    try {
      await act(async () => { await i18n.changeLanguage('ru-RU'); });
      await waitFor(() =>
        expect(mockApi).toHaveBeenCalledWith(expect.stringContaining('lang=ru-RU')));
    } finally {
      await act(async () => { await i18n.changeLanguage('en-US'); });
    }
  });

  it('renders no_results empty state when items is empty', async () => {
    mockApi.mockResolvedValueOnce({ items: [] });
    renderResults('xyzzy');
    await waitFor(() => {
      expect(screen.queryByTestId('discovery-search-grid')).toBeNull();
      expect(screen.queryByTestId('discovery-search-skeleton')).toBeNull();
    });
    // Heading uses the interpolated query — assert that the query is
    // surfaced verbatim somewhere in the heading text.
    const heading = screen.getByRole('heading', { level: 3 });
    expect(heading.textContent).toContain('xyzzy');
  });

  it('Movies tab queries the movie search endpoint and renders movie tiles', async () => {
    const movieSample = {
      items: [
        { movie_id: 900, tmdb_id: 27205, title: 'Inception', year: 2010,
          poster_hash: 'abc', tmdb_rating: 8.4 },
      ],
      page: 1, per_page: 20, cache_status: 'hit',
    };
    mockApi.mockImplementation((p?: string) => {
      if (typeof p === 'string' && p.includes('/discovery/movie/search')) {
        return Promise.resolve(movieSample);
      }
      return Promise.resolve({ items: [] }); // TV tab
    });

    renderResults('inception');
    fireEvent.click(screen.getByTestId('discovery-search-tab-movie'));

    await waitFor(() =>
      expect(screen.getByTestId('discovery-search-movie-grid')).toBeInTheDocument());
    expect(screen.getByText('Inception')).toBeInTheDocument();
    expect(
      mockApi.mock.calls.some(([p]) => typeof p === 'string' && p.includes('/discovery/movie/search')),
    ).toBe(true);
  });
});
