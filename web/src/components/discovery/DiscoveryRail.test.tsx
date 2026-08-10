import { describe, it, expect, beforeEach, vi } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter } from 'react-router-dom';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';
import { DiscoveryRail } from './DiscoveryRail';
import type { DiscoveryRow } from '@/api/discoveryRows';

const mockApi = vi.fn();
vi.mock('@/lib/api', async () => {
  const a = await vi.importActual<typeof import('@/lib/api')>('@/lib/api');
  return { ...a, api: (p: string) => mockApi(p) };
});

function renderRail(row: DiscoveryRow) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0, staleTime: 0 } },
  });
  return render(
    <QueryClientProvider client={qc}>
      <I18nextProvider i18n={i18n}>
        <MemoryRouter>
          <DiscoveryRail row={row} />
        </MemoryRouter>
      </I18nextProvider>
    </QueryClientProvider>,
  );
}

const sampleItems = [
  { series_id: 31, tmdb_id: 1, title: 'Rick and Morty', year: 2013, poster_hash: 'abc123', tmdb_rating: 8.7, in_library_instances: [] },
  { series_id: 32, tmdb_id: 2, title: 'Severance', year: 2022, poster_hash: 'def456', tmdb_rating: 8.4, in_library_instances: ['sonarr-alpha'] },
];

function row(partial: Partial<DiscoveryRow>): DiscoveryRow {
  return {
    row_type: 'trending', source: 'tmdb_discover', media_type: 'tv',
    params: {}, position: 0, enabled: true, title: 'Тренды', ...partial,
  };
}

beforeEach(() => {
  mockApi.mockReset();
  mockApi.mockImplementation((p: string) => {
    if (p.startsWith('/admin/instances')) return Promise.resolve({ instances: [] });
    return Promise.resolve({ items: sampleItems });
  });
});

describe('<DiscoveryRail />', () => {
  it('renders SeriesCards from /discovery/trending and posters go through mediaUrl', async () => {
    renderRail(row({ row_type: 'trending', title: 'Тренды' }));
    await waitFor(() =>
      expect(screen.getByText('Rick and Morty')).toBeInTheDocument());
    expect(screen.getByTestId('discovery-rail-trending')).toBeInTheDocument();
    expect(screen.getByText('Severance')).toBeInTheDocument();
    expect(mockApi.mock.calls.some(([p]) => (p as string).startsWith('/discovery/trending'))).toBe(true);
    // Anti-raw-TMDB guard: poster must resolve through /api/v1/media/…
    const imgs = screen.getAllByTestId('series-card-poster-img') as HTMLImageElement[];
    expect(imgs.length).toBeGreaterThan(0);
    for (const img of imgs) {
      expect(img.getAttribute('src') ?? '').toMatch(/^\/api\/v1\/media\//);
    }
  });

  it('calls /discovery/discover with with_genres for a genre row', async () => {
    renderRail(row({
      row_type: 'genre', title: 'Драмы',
      params: { with_genres: '18', sort_by: 'popularity.desc' },
    }));
    await waitFor(() =>
      expect(screen.getByText('Rick and Morty')).toBeInTheDocument());
    expect(screen.getByTestId('discovery-rail-genre')).toBeInTheDocument();
    const discoverCall = mockApi.mock.calls
      .map(([p]) => p as string)
      .find((p) => p.startsWith('/discovery/discover'));
    expect(discoverCall).toBeDefined();
    expect(discoverCall).toContain('with_genres=18');
  });

  it('injects a [today-45d, today] window for the upcoming row', async () => {
    renderRail(row({ row_type: 'upcoming', title: 'Новые сериалы', params: { sort_by: 'popularity.desc', 'vote_count.gte': '10' } }));
    await waitFor(() =>
      expect(screen.getByText('Rick and Morty')).toBeInTheDocument());
    const call = mockApi.mock.calls
      .map(([p]) => p as string)
      .find((p) => p.startsWith('/discovery/discover'));
    expect(call).toBeDefined();
    expect(call).toContain('first_air_date.gte=');
    expect(call).toContain('first_air_date.lte=');
    expect(call).toContain('sort_by=popularity.desc');
  });

  it('injects first_air_date.gte for the upcoming_releases row', async () => {
    renderRail(row({ row_type: 'upcoming_releases', title: 'Скоро на экраны', params: { sort_by: 'first_air_date.asc' } }));
    await waitFor(() =>
      expect(screen.getByText('Rick and Morty')).toBeInTheDocument());
    const call = mockApi.mock.calls
      .map(([p]) => p as string)
      .find((p) => p.startsWith('/discovery/discover'));
    expect(call).toBeDefined();
    expect(call).toContain('first_air_date.gte=');
    expect(call).toContain('sort_by=first_air_date.asc');
  });

  it('renders nothing when the query returns no items', async () => {
    mockApi.mockImplementation((p: string) => {
      if (p.startsWith('/admin/instances')) return Promise.resolve({ instances: [] });
      return Promise.resolve({ items: [] });
    });
    renderRail(row({ row_type: 'trending' }));
    await waitFor(() =>
      expect(screen.queryByTestId('discovery-rail-trending')).toBeNull());
  });

  it('renders nothing on fetch error', async () => {
    mockApi.mockImplementation((p: string) => {
      if (p.startsWith('/admin/instances')) return Promise.resolve({ instances: [] });
      return Promise.reject(new Error('boom'));
    });
    renderRail(row({ row_type: 'trending' }));
    await waitFor(() =>
      expect(screen.queryByTestId('discovery-rail-trending')).toBeNull());
  });
});
