import { describe, expect, it, vi, beforeEach } from 'vitest';
import userEvent from '@testing-library/user-event';
import { screen, waitFor } from '@testing-library/react';
import { useLocation } from 'react-router-dom';
import { renderWithProviders } from '@/test-utils';
import type {
  UnifiedSearchResult,
  SearchGroup,
  SeriesHit,
  MovieHit,
  CollectionHit,
  PersonHit,
} from '@/api/search';

const seriesHit = (over: Partial<SeriesHit> = {}): SeriesHit => ({
  kind: 'series',
  source: 'library',
  id: 10,
  tmdbId: 100,
  title: 'Breaking Bad',
  year: 2008,
  ...over,
});
const movieHit = (over: Partial<MovieHit> = {}): MovieHit => ({
  kind: 'movie',
  source: 'library',
  tmdbId: 200,
  title: 'The Matrix',
  year: 1999,
  ...over,
});
const collectionHit = (over: Partial<CollectionHit> = {}): CollectionHit => ({
  kind: 'collection',
  source: 'library',
  tmdbId: 400,
  name: 'The Matrix Collection',
  ...over,
});
const personHit = (over: Partial<PersonHit> = {}): PersonHit => ({
  kind: 'person',
  source: 'catalog',
  tmdbId: 300,
  name: 'Keanu Reeves',
  knownFor: 'Acting',
  ...over,
});

const emptyGroup: SearchGroup = { series: [], movies: [], collections: [], people: [] };
const baseResult: UnifiedSearchResult = {
  library: emptyGroup,
  catalog: emptyGroup,
  libraryLoading: false,
  catalogSearching: false,
  hasResults: false,
  enabled: false,
};
let mockResult: UnifiedSearchResult = baseResult;

vi.mock('@/api/search', async () => {
  const actual = await vi.importActual<typeof import('@/api/search')>('@/api/search');
  return { ...actual, useUnifiedSearch: () => mockResult };
});

import { SearchPage } from './SearchPage';

function LocationProbe() {
  const loc = useLocation();
  return <div data-testid="loc">{loc.search}</div>;
}

beforeEach(() => {
  mockResult = baseResult;
});

describe('<SearchPage />', () => {
  it('renders series, movie and person results from the hook', async () => {
    mockResult = {
      ...baseResult,
      enabled: true,
      hasResults: true,
      library: { series: [seriesHit()], movies: [movieHit()], collections: [], people: [] },
      catalog: { series: [], movies: [], collections: [], people: [personHit()] },
    };
    renderWithProviders(<SearchPage />, { route: '/search?q=matrix' });
    expect(await screen.findByTestId('series-card')).toBeInTheDocument();
    expect(screen.getByTestId('movie-card')).toBeInTheDocument();
    expect(screen.getByTestId('person-card')).toBeInTheDocument();
  });

  it('filters to movies only when the Movies tab is selected, back to all on All', async () => {
    mockResult = {
      ...baseResult,
      enabled: true,
      hasResults: true,
      library: {
        series: [seriesHit()],
        movies: [movieHit()],
        collections: [collectionHit()],
        people: [personHit()],
      },
      catalog: emptyGroup,
    };
    renderWithProviders(<SearchPage />, { route: '/search?q=matrix' });
    await screen.findByTestId('movie-card');

    await userEvent.click(screen.getByTestId('search-tab-movie'));
    expect(screen.getByTestId('movie-card')).toBeInTheDocument();
    expect(screen.queryByTestId('series-card')).toBeNull();
    expect(screen.queryByTestId('person-card')).toBeNull();

    await userEvent.click(screen.getByTestId('search-tab-all'));
    expect(screen.getByTestId('series-card')).toBeInTheDocument();
    expect(screen.getByTestId('person-card')).toBeInTheDocument();
  });

  it('filters to collections only when the Collections tab is selected', async () => {
    mockResult = {
      ...baseResult,
      enabled: true,
      hasResults: true,
      library: {
        series: [seriesHit()],
        movies: [movieHit()],
        collections: [collectionHit()],
        people: [personHit()],
      },
      catalog: emptyGroup,
    };
    renderWithProviders(<SearchPage />, { route: '/search?q=matrix' });
    await screen.findByTestId('collection-card');

    await userEvent.click(screen.getByTestId('search-tab-collection'));
    expect(screen.getByTestId('collection-card')).toBeInTheDocument();
    expect(screen.queryByTestId('series-card')).toBeNull();
    expect(screen.queryByTestId('movie-card')).toBeNull();
    expect(screen.queryByTestId('person-card')).toBeNull();
  });

  it('seeds the input from ?q= and updates the URL while typing', async () => {
    mockResult = { ...baseResult, enabled: true, hasResults: false };
    renderWithProviders(
      <>
        <SearchPage />
        <LocationProbe />
      </>,
      { route: '/search?q=foo' },
    );
    const input = (await screen.findByTestId('search-page-input')) as HTMLInputElement;
    expect(input.value).toBe('foo');

    await userEvent.clear(input);
    await userEvent.type(input, 'bar');
    await waitFor(() => expect(screen.getByTestId('loc')).toHaveTextContent('q=bar'));
  });

  it('shows only the catalog group when the Catalog segment is selected', async () => {
    mockResult = {
      ...baseResult,
      enabled: true,
      hasResults: true,
      library: { series: [seriesHit()], movies: [], collections: [], people: [] },
      catalog: { series: [], movies: [movieHit()], collections: [], people: [] },
    };
    renderWithProviders(<SearchPage />, { route: '/search?q=matrix' });
    await screen.findByTestId('series-card');
    expect(screen.getByTestId('movie-card')).toBeInTheDocument();

    await userEvent.click(screen.getByTestId('search-scope-catalog'));
    expect(screen.getByTestId('movie-card')).toBeInTheDocument();
    expect(screen.queryByTestId('series-card')).toBeNull();
  });

  it('shows the prompt state below the minimum query length', () => {
    mockResult = { ...baseResult, enabled: false, hasResults: false };
    renderWithProviders(<SearchPage />, { route: '/search' });
    expect(screen.getByTestId('search-prompt')).toBeInTheDocument();
  });
});
