import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { TooltipProvider } from '@/components/ui/tooltip';
import { PageTitleProvider } from '@/components/shell/page-title-context';
import i18n from '@/i18n';
import { MovieDetail } from './MovieDetail';
import { AddToRadarrProvider } from '@/components/movies/AddToRadarrProvider';

const origFetch = globalThis.fetch;

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });

function spyFetch(body: unknown, status = 200) {
  const fn = vi.fn(async () => json(body, status));
  globalThis.fetch = fn as typeof fetch;
}

function movie() {
  return {
    tmdb_id: 438631,
    title: 'Dune',
    year: 2021,
    tagline: 'Beyond fear, destiny awaits.',
    status: 'released',
    runtime_minutes: 155,
    release_date: '2021-10-22',
    overview: 'Paul Atreides arrives on Arrakis.',
    tmdb_rating: 8.1,
    imdb_rating: 8.0,
    imdb_id: 'tt1160419',
    library: [
      { instance_name: 'radarr', monitored: true, has_file: true, availability: 'released' },
    ],
  };
}

function renderRoute(path: string) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0, staleTime: 0 } },
  });
  return render(
    <PageTitleProvider defaultTitle="__INITIAL__">
      <I18nextProvider i18n={i18n}>
        <QueryClientProvider client={qc}>
          <TooltipProvider delayDuration={0}>
            <MemoryRouter initialEntries={[path]}>
              <AddToRadarrProvider>
                <Routes>
                  <Route path="/movies/:tmdbId" element={<MovieDetail />} />
                </Routes>
              </AddToRadarrProvider>
            </MemoryRouter>
          </TooltipProvider>
        </QueryClientProvider>
      </I18nextProvider>
    </PageTitleProvider>,
  );
}

beforeEach(() => {
  Object.defineProperty(window, 'location', {
    writable: true,
    value: { pathname: '/movies/438631', search: '', assign: vi.fn() },
  });
});
afterEach(() => {
  globalThis.fetch = origFetch;
  vi.restoreAllMocks();
});

describe('<MovieDetail />', () => {
  it('renders the hero, ratings, overview and library membership', async () => {
    spyFetch(movie());
    renderRoute('/movies/438631');

    expect(await screen.findByTestId('movie-detail-page')).toBeInTheDocument();
    expect(screen.getByTestId('movie-detail-title')).toHaveTextContent('Dune');
    expect(screen.getByTestId('movie-detail-tagline')).toHaveTextContent('destiny awaits');
    expect(screen.getByTestId('movie-detail-rating-tmdb')).toHaveTextContent('8.1');
    expect(screen.getByTestId('movie-detail-rating-imdb')).toHaveTextContent('8.0');
    expect(screen.getByTestId('movie-detail-imdb-link')).toHaveAttribute(
      'href',
      'https://www.imdb.com/title/tt1160419/',
    );
    expect(screen.getByTestId('movie-detail-overview')).toHaveTextContent('Arrakis');
    expect(screen.getByTestId('movie-library-row-radarr')).toBeInTheDocument();
    expect(screen.getByTestId('movie-library-monitored')).toBeInTheDocument();
    expect(screen.getByTestId('movie-library-hasfile')).toBeInTheDocument();
  });

  it('renders the empty library note when the movie is in no library', async () => {
    spyFetch({ ...movie(), library: [] });
    renderRoute('/movies/438631');

    expect(await screen.findByTestId('movie-detail-library-empty')).toBeInTheDocument();
  });

  it('renders the invalid-param branch for a non-numeric id', async () => {
    spyFetch(movie());
    renderRoute('/movies/not-a-number');

    expect(await screen.findByTestId('movie-detail-invalid')).toBeInTheDocument();
  });

  it('renders the load-error state on a failed fetch', async () => {
    spyFetch({ error: 'boom' }, 500);
    renderRoute('/movies/438631');

    expect(await screen.findByTestId('movie-detail-error')).toBeInTheDocument();
  });

  it('renders the Add-to-Radarr split-button in the hero', async () => {
    spyFetch(movie());
    renderRoute('/movies/438631');

    expect(await screen.findByTestId('movie-detail-add-to-radarr')).toBeInTheDocument();
    expect(screen.getByTestId('movie-detail-add-to-radarr-primary')).toBeInTheDocument();
  });

  it('omits the collection block when the movie has no collection', async () => {
    spyFetch(movie());
    renderRoute('/movies/438631');

    expect(await screen.findByTestId('movie-detail-page')).toBeInTheDocument();
    expect(screen.queryByTestId('movie-collection-block')).toBeNull();
  });

  it('renders the collection block only when collection.tmdb_collection_id is present', async () => {
    // Route the movie detail vs the collection detail vs instances by URL.
    globalThis.fetch = vi.fn(async (input: string | URL | Request) => {
      const url = typeof input === 'string' ? input : input.toString();
      if (url.includes('/collections/')) {
        return json({
          tmdb_collection_id: 726871, name: 'Dune Collection', poster: null,
          radarr_monitored: false,
          parts: [{ tmdb_id: 438631, title: 'Dune', year: 2021, in_library: true, movie_id: 1 }],
        });
      }
      if (url.endsWith('/api/v1/admin/instances')) {
        return json({ instances: [] });
      }
      return json({
        ...movie(),
        collection: { tmdb_collection_id: 726871, name: 'Dune Collection', radarr_monitored: false },
      });
    }) as typeof fetch;
    renderRoute('/movies/438631');

    expect(await screen.findByTestId('movie-collection-block')).toBeInTheDocument();
    expect(screen.getByTestId('movie-collection-name')).toHaveTextContent('Dune Collection');
  });
});
