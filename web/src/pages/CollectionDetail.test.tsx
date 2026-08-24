import { describe, expect, it, vi, beforeEach } from 'vitest';
import { screen } from '@testing-library/react';
import { Route, Routes } from 'react-router-dom';
import { renderWithProviders } from '@/test-utils';
import type { UseQueryResult } from '@tanstack/react-query';
import type { ApiError } from '@/lib/api';
import type { InstanceList } from '@/lib/instances';
import type { MovieCollectionDetail } from '@/api/movieCollections';

type CollResult = UseQueryResult<MovieCollectionDetail, ApiError>;
type InstResult = UseQueryResult<InstanceList, ApiError>;

let mockInstances: Partial<InstResult>;
let mockCollection: Partial<CollResult>;

vi.mock('@/hooks/useLanguage', () => ({
  useLanguage: () => ({ current: 'en-US', setLanguage: async () => {} }),
}));
vi.mock('@/lib/instances', () => ({
  useInstances: () => mockInstances,
}));
vi.mock('@/api/movieCollections', () => ({
  useMovieCollection: () => mockCollection,
}));

import { CollectionDetail } from './CollectionDetail';

const radarrInstances: Partial<InstResult> = {
  data: { instances: [{ name: 'radarr-main', type: 'radarr' }] } as InstanceList,
  isLoading: false,
};

function renderPage(route = '/collections/603') {
  return renderWithProviders(
    <Routes>
      <Route path="/collections/:tmdbId" element={<CollectionDetail />} />
    </Routes>,
    { route },
  );
}

beforeEach(() => {
  mockInstances = radarrInstances;
  mockCollection = { isPending: true } as Partial<CollResult>;
});

describe('<CollectionDetail />', () => {
  it('renders the parts grid, each part linking to /movies/:tmdbId', async () => {
    mockCollection = {
      data: {
        name: 'The Matrix Collection',
        overview: 'Neo saga',
        parts: [
          { tmdb_id: 603, title: 'The Matrix', year: 1999, in_library: true },
          { tmdb_id: 604, title: 'Reloaded', year: 2003, in_library: false },
        ],
      } as MovieCollectionDetail,
      isPending: false,
      isError: false,
    } as Partial<CollResult>;

    renderPage();
    expect(await screen.findByTestId('collection-detail-parts')).toBeInTheDocument();
    expect(screen.getByTestId('collection-detail-name')).toHaveTextContent(
      'The Matrix Collection',
    );
    const part = screen.getByTestId('collection-detail-part-603');
    expect(part.getAttribute('href')).toBe('/movies/603');
    expect(screen.getByTestId('collection-detail-part-604').getAttribute('href')).toBe(
      '/movies/604',
    );
  });

  it('shows the loading skeleton while the collection query is pending', async () => {
    mockCollection = { isPending: true } as Partial<CollResult>;
    renderPage();
    expect(await screen.findByTestId('collection-detail-loading')).toBeInTheDocument();
  });

  it('shows the empty state when the collection has no parts', async () => {
    mockCollection = {
      data: { name: 'Empty Coll', parts: [] } as MovieCollectionDetail,
      isPending: false,
      isError: false,
    } as Partial<CollResult>;
    renderPage();
    expect(await screen.findByTestId('collection-detail-empty')).toBeInTheDocument();
  });

  it('shows the no-Radarr-instance state when only a sonarr instance exists', async () => {
    mockInstances = {
      data: { instances: [{ name: 'sonarr-main', type: 'sonarr' }] } as InstanceList,
      isLoading: false,
    };
    renderPage();
    expect(
      await screen.findByTestId('collection-detail-no-instance'),
    ).toBeInTheDocument();
  });

  it('shows the loading skeleton while instances are still loading', async () => {
    mockInstances = { data: undefined, isLoading: true } as Partial<InstResult>;
    renderPage();
    expect(await screen.findByTestId('collection-detail-loading')).toBeInTheDocument();
  });
});
