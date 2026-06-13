import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import { MemoryRouter } from 'react-router-dom';
import i18n from '@/i18n';
import { RecommendationsCarousel } from './RecommendationsCarousel';

function r(node: React.ReactElement) {
  return render(
    <I18nextProvider i18n={i18n}>
      <MemoryRouter>{node}</MemoryRouter>
    </I18nextProvider>,
  );
}

const recs = [
  { series_id: 1, title: 'Show A', year: 2022, tmdb_rating: 8.1, poster_asset: 'a',
    in_library: true, instance_name: 'alpha', sonarr_series_id: 11 },
  { series_id: 2, title: 'Show B', year: 2021, tmdb_rating: 7.6, poster_asset: 'b',
    in_library: false },
];

describe('<RecommendationsCarousel />', () => {
  it('renders cards with title, year, rating', () => {
    r(<RecommendationsCarousel recommendations={recs} />);
    expect(screen.getByTestId('recommendations-carousel')).toBeInTheDocument();
    expect(screen.getAllByTestId('recommendation-card')).toHaveLength(2);
    expect(screen.getByText('Show A')).toBeInTheDocument();
    expect(screen.getByText('8.1')).toBeInTheDocument();
  });

  it('wraps in-library items with a Link to their detail page', () => {
    r(<RecommendationsCarousel recommendations={recs} />);
    const link = screen.getByTestId('recommendation-link') as HTMLAnchorElement;
    expect(link.getAttribute('href')).toBe('/series/alpha/11');
    expect(screen.getByTestId('recommendation-in-library')).toBeInTheDocument();
  });

  it('renders an inert Add-to-Sonarr overlay for non-library items', () => {
    r(<RecommendationsCarousel recommendations={recs} />);
    const overlays = screen.getAllByTestId('recommendation-add-overlay');
    expect(overlays).toHaveLength(1);
  });

  it('returns null when recommendations is empty', () => {
    const { container } = r(<RecommendationsCarousel recommendations={[]} />);
    expect(container.firstChild).toBeNull();
  });
});
