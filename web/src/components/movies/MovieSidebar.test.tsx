import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';
import type { MovieDetail } from '@/api/movies';
import { MovieSidebar } from './MovieSidebar';

function r(node: React.ReactElement) {
  return render(<I18nextProvider i18n={i18n}>{node}</I18nextProvider>);
}

const base: MovieDetail = { title: 'Dune', tmdb_id: 438631 };

describe('<MovieSidebar /> — S5 movie fields', () => {
  it('renders budget & revenue formatted as full USD', () => {
    r(<MovieSidebar movie={{ ...base, budget: 85_000_000, revenue: 451_746_275 }} />);
    expect(screen.getByTestId('movie-detail-sidebar-budget-value')).toHaveTextContent(
      '$85,000,000',
    );
    expect(screen.getByTestId('movie-detail-sidebar-revenue-value')).toHaveTextContent(
      '$451,746,275',
    );
  });

  it('hides the money rows when the value is 0 (no reported figure)', () => {
    r(<MovieSidebar movie={{ ...base, budget: 0, revenue: 0, status: 'Released' }} />);
    expect(screen.queryByTestId('movie-detail-sidebar-budget')).toBeNull();
    expect(screen.queryByTestId('movie-detail-sidebar-revenue')).toBeNull();
  });

  it('hides the money rows when the value is undefined', () => {
    r(<MovieSidebar movie={{ ...base, status: 'Released' }} />);
    expect(screen.queryByTestId('movie-detail-sidebar-budget')).toBeNull();
    expect(screen.queryByTestId('movie-detail-sidebar-revenue')).toBeNull();
  });

  it('renders original_title only when it differs from the display title', () => {
    r(
      <MovieSidebar
        movie={{ ...base, title: 'Spirited Away', original_title: 'Sen to Chihiro no kamikakushi' }}
      />,
    );
    expect(screen.getByTestId('movie-detail-sidebar-original-title-value')).toHaveTextContent(
      'Sen to Chihiro no kamikakushi',
    );
  });

  it('omits original_title when it equals the display title', () => {
    r(<MovieSidebar movie={{ ...base, title: 'Dune', original_title: 'Dune' }} />);
    expect(screen.queryByTestId('movie-detail-sidebar-original-title')).toBeNull();
  });

  it('omits original_title when absent', () => {
    r(<MovieSidebar movie={{ ...base, status: 'Released' }} />);
    expect(screen.queryByTestId('movie-detail-sidebar-original-title')).toBeNull();
  });

  it('returns null when the movie has no sidebar-worthy data', () => {
    const { container } = r(<MovieSidebar movie={{ title: 'X' }} />);
    expect(container.firstChild).toBeNull();
  });
});
