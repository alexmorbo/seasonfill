import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';
import { MediaRatingsSection } from './MediaRatingsSection';

function r(node: React.ReactElement) {
  return render(<I18nextProvider i18n={i18n}>{node}</I18nextProvider>);
}

describe('<MediaRatingsSection />', () => {
  it('renders TMDB, IMDb, rated and awards when all present', () => {
    r(<MediaRatingsSection
      tmdbRating={8.1} tmdbVotes={2100}
      imdbRating={8.6} imdbVotes={84_000}
      rated="TV-MA" awards="Won 16 Primetime Emmys"
    />);
    expect(screen.getByTestId('ratings-section')).toBeInTheDocument();
    expect(screen.getByTestId('ratings-tmdb')).toHaveTextContent('8.1');
    expect(screen.getByTestId('ratings-imdb')).toHaveTextContent('8.6');
    expect(screen.getByTestId('ratings-rated')).toHaveTextContent('TV-MA');
    expect(screen.getByTestId('ratings-awards')).toHaveTextContent('Won 16 Primetime Emmys');
  });

  it('renders only the sources that carry a value', () => {
    r(<MediaRatingsSection tmdbRating={7.4} />);
    expect(screen.getByTestId('ratings-tmdb')).toBeInTheDocument();
    expect(screen.queryByTestId('ratings-imdb')).toBeNull();
    expect(screen.queryByTestId('ratings-rated')).toBeNull();
    expect(screen.queryByTestId('ratings-awards')).toBeNull();
  });

  it('does not render a zero-value or empty source', () => {
    r(<MediaRatingsSection imdbRating={0} rated="N/A" awards="" />);
    expect(screen.queryByTestId('ratings-imdb')).toBeNull();
    expect(screen.queryByTestId('ratings-rated')).toBeNull();
    expect(screen.queryByTestId('ratings-awards')).toBeNull();
  });

  it('returns null when no source carries a value', () => {
    const { container } = r(<MediaRatingsSection />);
    expect(container.firstChild).toBeNull();
  });

  it('F-07 — renders OMDb `rated` as its own label, NOT the TMDB content-rating badge', () => {
    r(<MediaRatingsSection rated="TV-14" />);
    const rated = screen.getByTestId('ratings-rated');
    expect(rated).toHaveTextContent('TV-14');
    // The section must not borrow the TMDB ContentRatingBadge testid.
    expect(screen.queryByTestId('content-rating-badge')).toBeNull();
  });
});
