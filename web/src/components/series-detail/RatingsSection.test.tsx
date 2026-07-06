import { describe, it, expect, beforeEach, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';
import type { SeriesRatingsResponse } from '@/api/seriesRatings';
import { RatingsSection } from './RatingsSection';

// The section reads its data through useSeriesRatings; mock the hook so each
// test drives a controlled response without a QueryClient/network.
let ratingsData: SeriesRatingsResponse | undefined;
vi.mock('@/api/seriesRatings', () => ({
  useSeriesRatings: () => ({ data: ratingsData }),
}));

function r(node: React.ReactElement) {
  return render(<I18nextProvider i18n={i18n}>{node}</I18nextProvider>);
}

describe('<RatingsSection />', () => {
  beforeEach(() => {
    ratingsData = undefined;
  });

  it('renders TMDB, IMDb, rated and awards when all present', () => {
    ratingsData = {
      tmdb_rating: 8.1,
      tmdb_votes: 2100,
      imdb_rating: 8.6,
      imdb_votes: 84_000,
      rated: 'TV-MA',
      awards: 'Won 16 Primetime Emmys',
      sources: { tmdb: 'fresh', omdb: 'fresh' },
    };
    r(<RatingsSection seriesId={42} />);
    expect(screen.getByTestId('ratings-section')).toBeInTheDocument();
    expect(screen.getByTestId('ratings-tmdb')).toHaveTextContent('8.1');
    expect(screen.getByTestId('ratings-imdb')).toHaveTextContent('8.6');
    expect(screen.getByTestId('ratings-rated')).toHaveTextContent('TV-MA');
    expect(screen.getByTestId('ratings-awards')).toHaveTextContent('Won 16 Primetime Emmys');
  });

  it('renders only the sources that carry a value', () => {
    ratingsData = { tmdb_rating: 7.4, sources: { tmdb: 'fresh', omdb: 'unavailable' } };
    r(<RatingsSection seriesId={42} />);
    expect(screen.getByTestId('ratings-tmdb')).toBeInTheDocument();
    expect(screen.queryByTestId('ratings-imdb')).toBeNull();
    expect(screen.queryByTestId('ratings-rated')).toBeNull();
    expect(screen.queryByTestId('ratings-awards')).toBeNull();
  });

  it('does not render a zero-value or empty source', () => {
    ratingsData = { imdb_rating: 0, rated: 'N/A', awards: '', sources: { omdb: 'fresh' } };
    r(<RatingsSection seriesId={42} />);
    expect(screen.queryByTestId('ratings-imdb')).toBeNull();
    expect(screen.queryByTestId('ratings-rated')).toBeNull();
    expect(screen.queryByTestId('ratings-awards')).toBeNull();
  });

  it('returns null when no source carries a value', () => {
    ratingsData = { sources: { tmdb: 'unavailable', omdb: 'unavailable' } };
    const { container } = r(<RatingsSection seriesId={42} />);
    expect(container.firstChild).toBeNull();
  });

  it('F-07 — renders OMDb `rated` as its own label, NOT the TMDB content-rating badge', () => {
    ratingsData = { rated: 'TV-14', sources: { omdb: 'fresh' } };
    r(<RatingsSection seriesId={42} />);
    const rated = screen.getByTestId('ratings-rated');
    expect(rated).toHaveTextContent('TV-14');
    // The section must not borrow the TMDB ContentRatingBadge testid.
    expect(screen.queryByTestId('content-rating-badge')).toBeNull();
  });
});
