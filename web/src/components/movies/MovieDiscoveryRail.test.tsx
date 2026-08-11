// Ф6-R-6b Wave B — MovieDiscoveryRail dispatches a movie row to the movie
// discovery endpoint and renders MovieCards with an Add-to-Radarr overlay.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter } from 'react-router-dom';
import { TooltipProvider } from '@/components/ui/tooltip';
import i18n from '@/i18n';
import { MovieDiscoveryRail } from './MovieDiscoveryRail';
import { AddToRadarrProvider } from './AddToRadarrProvider';
import type { DiscoveryRow } from '@/api/discoveryRows';

const fetchMock = vi.fn();
const origFetch = globalThis.fetch;

const TRENDING = {
  items: [
    { movie_id: 1, tmdb_id: 438631, title: 'Dune', year: 2021, tmdb_rating: 8.1, poster_hash: 'abc' },
    { movie_id: 2, tmdb_id: 693134, title: 'Dune: Part Two', year: 2024, tmdb_rating: 8.4 },
  ],
  page: 1, per_page: 20, cache_status: 'hit',
};
const EMPTY = { items: [], page: 1, per_page: 20, cache_status: 'hit' };

const calledUrls: string[] = [];

function j(b: unknown): Response {
  return new Response(JSON.stringify(b), { status: 200, headers: { 'Content-Type': 'application/json' } });
}

function movieRow(overrides: Partial<DiscoveryRow> = {}): DiscoveryRow {
  return {
    row_type: 'trending', source: 'tmdb_discover', media_type: 'movie',
    params: {}, position: 0, enabled: true, title: 'В тренде (фильмы)',
    ...overrides,
  };
}

function renderRail(row: DiscoveryRow) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0, staleTime: 0 } },
  });
  return render(
    <I18nextProvider i18n={i18n}>
      <QueryClientProvider client={qc}>
        <TooltipProvider delayDuration={0}>
          <MemoryRouter>
            <AddToRadarrProvider>
              <MovieDiscoveryRail row={row} />
            </AddToRadarrProvider>
          </MemoryRouter>
        </TooltipProvider>
      </QueryClientProvider>
    </I18nextProvider>,
  );
}

beforeEach(() => {
  fetchMock.mockReset();
  calledUrls.length = 0;
  fetchMock.mockImplementation(async (input: string | URL | Request) => {
    const url = typeof input === 'string' ? input : input.toString();
    calledUrls.push(url);
    if (url.includes('/discovery/movie/trending')) return j(TRENDING);
    return j(EMPTY);
  });
  globalThis.fetch = fetchMock as typeof fetch;
});
afterEach(() => { globalThis.fetch = origFetch; });

describe('<MovieDiscoveryRail />', () => {
  it('fetches the movie trending endpoint and renders MovieCards', async () => {
    renderRail(movieRow());
    expect(await screen.findByTestId('movie-discovery-rail-trending')).toBeInTheDocument();

    await waitFor(() =>
      expect(calledUrls.some((u) => u.includes('/discovery/movie/trending'))).toBe(true));

    const cards = await screen.findAllByTestId('movie-card');
    expect(cards.length).toBe(2);
    expect(screen.getByText('Dune')).toBeInTheDocument();
    // Each card carries an Add-to-Radarr overlay action.
    expect(screen.getAllByTestId('movie-discovery-add').length).toBe(2);
  });

  it('renders nothing for a library-sourced row (no movie endpoint this wave)', async () => {
    const { container } = renderRail(
      movieRow({ row_type: 'recently_added', source: 'library' }),
    );
    await waitFor(() =>
      expect(container.querySelector('[data-testid^="movie-discovery-rail-"]')).toBeNull());
  });
});
