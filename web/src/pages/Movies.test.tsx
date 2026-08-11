import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders } from '@/test-utils';
import { Movies } from './Movies';

const origFetch = globalThis.fetch;

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });

function library() {
  return {
    total: 2,
    has_more: false,
    items: [
      { tmdb_id: 438631, title: 'Dune', year: 2021, tmdb_rating: 8.1, instances: ['radarr'] },
      { tmdb_id: 693134, title: 'Dune: Part Two', year: 2024, instances: [] },
    ],
  };
}

function spyFetch(body: unknown = library(), status = 200) {
  const urls: string[] = [];
  const fn = vi.fn(async (url: RequestInfo | URL) => {
    urls.push(typeof url === 'string' ? url : url.toString());
    return json(body, status);
  });
  globalThis.fetch = fn as typeof fetch;
  return urls;
}

beforeEach(() => {
  Object.defineProperty(window, 'location', {
    writable: true,
    value: { pathname: '/movies', search: '', assign: vi.fn() },
  });
});
afterEach(() => {
  globalThis.fetch = origFetch;
  vi.restoreAllMocks();
});

describe('<Movies />', () => {
  it('renders the grid from the stubbed /movies list', async () => {
    spyFetch();
    renderWithProviders(<Movies />, { route: '/movies' });

    expect(await screen.findByTestId('movies-page')).toBeInTheDocument();
    expect(await screen.findByTestId('movie-grid')).toBeInTheDocument();
    expect(screen.getByText('Dune')).toBeInTheDocument();
    expect(screen.getByText('Dune: Part Two')).toBeInTheDocument();
  });

  it('shows the empty state when the library is empty', async () => {
    spyFetch({ total: 0, has_more: false, items: [] });
    renderWithProviders(<Movies />, { route: '/movies' });

    expect(await screen.findByTestId('movies-empty')).toBeInTheDocument();
  });

  it('re-queries with state=downloaded when the state filter changes', async () => {
    const urls = spyFetch();
    renderWithProviders(<Movies />, { route: '/movies' });

    await screen.findByTestId('movie-grid');
    await userEvent.selectOptions(screen.getByTestId('movies-filter-state'), 'downloaded');

    await vi.waitFor(() => {
      expect(urls.some((u) => u.includes('state=downloaded'))).toBe(true);
    });
  });
});
