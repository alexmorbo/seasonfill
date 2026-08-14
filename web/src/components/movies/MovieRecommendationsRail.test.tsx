import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import { MemoryRouter } from 'react-router-dom';
import i18n from '@/i18n';
import { MovieRecommendationsRail } from './MovieRecommendationsRail';

// Mock the data hook so the rail is driven by fixture state (no network / QC).
const mockHook = vi.fn();
vi.mock('@/api/movieRecommendations', () => ({
  useMovieRecommendations: (args: unknown) => mockHook(args),
}));

// eslint-disable-next-line @typescript-eslint/no-explicit-any
function state(over: Record<string, any>) {
  return { data: undefined, isLoading: false, isError: false, isSuccess: false, ...over };
}

function r(node: React.ReactElement) {
  return render(
    <I18nextProvider i18n={i18n}>
      <MemoryRouter>{node}</MemoryRouter>
    </I18nextProvider>,
  );
}

const ITEMS = [
  { tmdb_id: 604, title: 'The Matrix Reloaded', year: 2003, tmdb_rating: 7.2, poster_asset: 'aaa' },
  { tmdb_id: 605, title: 'The Matrix Revolutions', year: 2003, tmdb_rating: 6.7, poster_asset: 'bbb' },
  { tmdb_id: 606, title: 'John Wick', year: 2014, tmdb_rating: 7.4, poster_asset: 'ccc' },
];

describe('<MovieRecommendationsRail />', () => {
  beforeEach(() => mockHook.mockReset());

  it('renders each item as a MovieCard in server rank order', () => {
    mockHook.mockReturnValue(state({ data: { items: ITEMS, degraded: [] }, isSuccess: true }));
    r(<MovieRecommendationsRail tmdbId={603} />);
    const cards = screen.getAllByTestId('movie-card');
    expect(cards).toHaveLength(3);
    expect(cards.map((c) => c.getAttribute('href'))).toEqual([
      '/movies/604', '/movies/605', '/movies/606',
    ]);
  });

  it('each card links to /movies/{tmdb_id} (F-04 — no in-library link)', () => {
    mockHook.mockReturnValue(state({ data: { items: [ITEMS[0]], degraded: [] }, isSuccess: true }));
    r(<MovieRecommendationsRail tmdbId={603} />);
    const card = screen.getByTestId('movie-card');
    expect(card.getAttribute('href')).toBe('/movies/604');
    // F-04: no in-library badge is ever rendered.
    expect(screen.queryByTestId('movie-card-library-badge')).toBeNull();
  });

  it('shows title, year and ★ rating for each card', () => {
    mockHook.mockReturnValue(state({ data: { items: [ITEMS[0]], degraded: [] }, isSuccess: true }));
    r(<MovieRecommendationsRail tmdbId={603} />);
    expect(screen.getByTestId('movie-card-title')).toHaveTextContent('The Matrix Reloaded');
    expect(screen.getByTestId('movie-card-year')).toHaveTextContent('2003');
    expect(screen.getByTestId('movie-card-rating')).toHaveTextContent('7.2');
  });

  it('renders the poster via /api/v1/media/{hash}', () => {
    mockHook.mockReturnValue(state({ data: { items: [ITEMS[0]], degraded: [] }, isSuccess: true }));
    const { container } = r(<MovieRecommendationsRail tmdbId={603} />);
    const img = container.querySelector('img');
    expect(img).not.toBeNull();
    expect(img?.getAttribute('src')).toBe('/api/v1/media/aaa');
  });

  it('renders nothing when the list is empty and settled', () => {
    mockHook.mockReturnValue(state({ data: { items: [], degraded: [] }, isSuccess: true }));
    const { container } = r(<MovieRecommendationsRail tmdbId={603} />);
    expect(container).toBeEmptyDOMElement();
    expect(screen.queryByTestId('movie-recommendations')).toBeNull();
  });

  it('renders nothing on the error path', () => {
    mockHook.mockReturnValue(state({ isError: true }));
    const { container } = r(<MovieRecommendationsRail tmdbId={603} />);
    expect(container).toBeEmptyDOMElement();
  });

  it('renders skeleton tiles + loading label while loading', () => {
    mockHook.mockReturnValue(state({ isLoading: true }));
    r(<MovieRecommendationsRail tmdbId={603} />);
    expect(screen.getByTestId('movie-recommendations-loading')).toBeInTheDocument();
    expect(screen.getByTestId('movie-recommendations-loading-label')).toBeInTheDocument();
    expect(screen.getAllByTestId('movie-recommendations-skeleton-tile')).toHaveLength(6);
  });

  it('shows skeletons when empty but degraded carries tmdb_movie (warming)', () => {
    mockHook.mockReturnValue(state({ data: { items: [], degraded: ['tmdb_movie'] }, isSuccess: true }));
    r(<MovieRecommendationsRail tmdbId={603} />);
    expect(screen.getByTestId('movie-recommendations-loading')).toBeInTheDocument();
    expect(screen.getAllByTestId('movie-recommendations-skeleton-tile')).toHaveLength(6);
  });

  it('drops items without a tmdb_id but preserves the order of the rest', () => {
    const mixed = [
      { title: 'Unlinkable', year: 2000 },
      ITEMS[0],
      ITEMS[2],
    ];
    mockHook.mockReturnValue(state({ data: { items: mixed, degraded: [] }, isSuccess: true }));
    r(<MovieRecommendationsRail tmdbId={603} />);
    const cards = screen.getAllByTestId('movie-card');
    expect(cards.map((c) => c.getAttribute('href'))).toEqual(['/movies/604', '/movies/606']);
  });

  it('threads the current BCP-47 lang into the hook', () => {
    mockHook.mockReturnValue(state({ data: { items: [], degraded: [] }, isSuccess: true }));
    r(<MovieRecommendationsRail tmdbId={603} />);
    // i18n default resolvedLanguage is 'en' → toBcp47 → 'en-US'.
    expect(mockHook).toHaveBeenCalledWith(
      expect.objectContaining({ tmdbId: 603, offset: 0, lang: 'en-US' }),
    );
  });
});
