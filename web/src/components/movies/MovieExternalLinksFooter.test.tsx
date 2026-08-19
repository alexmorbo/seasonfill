import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';
import { MovieExternalLinksFooter } from './MovieExternalLinksFooter';

function r(node: React.ReactElement) {
  return render(<I18nextProvider i18n={i18n}>{node}</I18nextProvider>);
}

describe('<MovieExternalLinksFooter />', () => {
  it('renders TMDB (/movie/ path), IMDb and homepage links with correct hrefs', () => {
    r(
      <MovieExternalLinksFooter
        tmdbId={1315772}
        imdbId="tt9243946"
        homepage="https://example.movie/"
      />,
    );
    expect(screen.getByText('IMDb').closest('a')).toHaveAttribute(
      'href',
      'https://www.imdb.com/title/tt9243946/',
    );
    expect(screen.getByText('TMDB').closest('a')).toHaveAttribute(
      'href',
      'https://www.themoviedb.org/movie/1315772',
    );
    expect(screen.getByText('Homepage').closest('a')).toHaveAttribute(
      'href',
      'https://example.movie/',
    );
  });

  it('omits entries whose data is absent (and never renders a TVDB entry)', () => {
    r(<MovieExternalLinksFooter tmdbId={603} />);
    expect(screen.getByText('TMDB')).toBeInTheDocument();
    expect(screen.queryByText('IMDb')).not.toBeInTheDocument();
    expect(screen.queryByText('Homepage')).not.toBeInTheDocument();
    expect(screen.queryByText('TheTVDB')).not.toBeInTheDocument();
  });

  it('renders nothing when no links are present', () => {
    const { container } = r(<MovieExternalLinksFooter />);
    expect(container.firstChild).toBeNull();
  });

  it('hides TMDB for a zero/invalid id', () => {
    const { container } = r(<MovieExternalLinksFooter tmdbId={0} />);
    expect(container.firstChild).toBeNull();
  });
});
