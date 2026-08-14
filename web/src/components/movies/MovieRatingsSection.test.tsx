import { describe, it, expect, beforeEach, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';
import type { MovieRatingsResponse } from '@/api/movieRatings';
import { MovieRatingsSection } from './MovieRatingsSection';

// The section reads its data through useMovieRatings; mock the hook so each
// test drives a controlled response without a QueryClient/network.
let ratingsData: MovieRatingsResponse | undefined;
vi.mock('@/api/movieRatings', () => ({
  useMovieRatings: () => ({ data: ratingsData }),
}));

function r(node: React.ReactElement) {
  return render(<I18nextProvider i18n={i18n}>{node}</I18nextProvider>);
}

describe('<MovieRatingsSection />', () => {
  beforeEach(() => {
    ratingsData = undefined;
  });

  it('renders TMDB, IMDb, rated and awards when all present', () => {
    ratingsData = {
      tmdb_rating: 8.7,
      tmdb_votes: 24_000,
      imdb_rating: 8.7,
      imdb_votes: 1_900_000,
      rated: 'R',
      awards: 'Won 4 Oscars',
      sources: { tmdb: 'fresh', omdb: 'fresh' },
    };
    r(<MovieRatingsSection tmdbId={603} />);
    expect(screen.getByTestId('movie-ratings-section')).toBeInTheDocument();
    expect(screen.getByTestId('movie-ratings-tmdb')).toHaveTextContent('8.7');
    expect(screen.getByTestId('movie-ratings-imdb')).toHaveTextContent('8.7');
    expect(screen.getByTestId('movie-ratings-rated')).toHaveTextContent('R');
    expect(screen.getByTestId('movie-ratings-awards')).toHaveTextContent('Won 4 Oscars');
  });

  it('renders only TMDB when IMDb is absent (OMDb unavailable)', () => {
    ratingsData = { tmdb_rating: 7.1, sources: { tmdb: 'fresh', omdb: 'unavailable' } };
    r(<MovieRatingsSection tmdbId={603} />);
    expect(screen.getByTestId('movie-ratings-tmdb')).toBeInTheDocument();
    expect(screen.queryByTestId('movie-ratings-imdb')).toBeNull();
    expect(screen.queryByTestId('movie-ratings-rated')).toBeNull();
    expect(screen.queryByTestId('movie-ratings-awards')).toBeNull();
  });

  it('does not render a zero-value or empty/N/A source', () => {
    ratingsData = { imdb_rating: 0, rated: 'N/A', awards: '', sources: { omdb: 'fresh' } };
    r(<MovieRatingsSection tmdbId={603} />);
    expect(screen.queryByTestId('movie-ratings-imdb')).toBeNull();
    expect(screen.queryByTestId('movie-ratings-rated')).toBeNull();
    expect(screen.queryByTestId('movie-ratings-awards')).toBeNull();
  });

  it('renders when awards is empty but a score is present (null-safe partial)', () => {
    ratingsData = { tmdb_rating: 6.5, awards: '', sources: { tmdb: 'fresh' } };
    r(<MovieRatingsSection tmdbId={603} />);
    expect(screen.getByTestId('movie-ratings-section')).toBeInTheDocument();
    expect(screen.getByTestId('movie-ratings-tmdb')).toHaveTextContent('6.5');
    expect(screen.queryByTestId('movie-ratings-awards')).toBeNull();
  });

  it('returns null when no source carries a value', () => {
    ratingsData = { sources: { tmdb: 'unavailable', omdb: 'unavailable' } };
    const { container } = r(<MovieRatingsSection tmdbId={603} />);
    expect(container.firstChild).toBeNull();
  });

  it('returns null when data is undefined (hook not ready)', () => {
    ratingsData = undefined;
    const { container } = r(<MovieRatingsSection tmdbId={undefined} />);
    expect(container.firstChild).toBeNull();
  });
});
