import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import { MemoryRouter } from 'react-router-dom';
import { TooltipProvider } from '@/components/ui/tooltip';
import i18n from '@/i18n';
import { MovieCard } from './MovieCard';

function r(node: React.ReactElement) {
  return render(
    <I18nextProvider i18n={i18n}>
      <TooltipProvider delayDuration={0}>
        <MemoryRouter>{node}</MemoryRouter>
      </TooltipProvider>
    </I18nextProvider>,
  );
}

describe('<MovieCard />', () => {
  it('renders the title, year overlay and ★ rating overlay', () => {
    r(<MovieCard title="Dune" year={2021} rating={8.1} tmdbId={438631} />);
    expect(screen.getByTestId('movie-card-title')).toHaveTextContent('Dune');
    const yr = screen.getByTestId('movie-card-year');
    expect(yr).toHaveTextContent('2021');
    expect(yr.className).toContain('bottom-2');
    expect(yr.className).toContain('left-2');
    const rt = screen.getByTestId('movie-card-rating');
    expect(rt).toHaveTextContent('8.1');
    expect(rt.className).toContain('right-2');
  });

  it('links to /movies/:tmdbId', () => {
    r(<MovieCard title="Dune" tmdbId={438631} />);
    const card = screen.getByTestId('movie-card');
    expect(card.tagName.toLowerCase()).toBe('a');
    expect(card.getAttribute('href')).toBe('/movies/438631');
  });

  it('hides the year and rating overlays when absent or zero', () => {
    r(<MovieCard title="Nameless" tmdbId={1} rating={0} />);
    expect(screen.queryByTestId('movie-card-year')).toBeNull();
    expect(screen.queryByTestId('movie-card-rating')).toBeNull();
  });

  it('renders the in-library badge only when libraryBadge is set', () => {
    const { rerender } = r(<MovieCard title="Dune" tmdbId={438631} />);
    expect(screen.queryByTestId('movie-card-library-badge')).toBeNull();
    rerender(
      <I18nextProvider i18n={i18n}>
        <TooltipProvider delayDuration={0}>
          <MemoryRouter>
            <MovieCard title="Dune" tmdbId={438631} libraryBadge />
          </MemoryRouter>
        </TooltipProvider>
      </I18nextProvider>,
    );
    expect(screen.getByTestId('movie-card-library-badge')).toBeInTheDocument();
  });

  it('renders a monogram fallback when there is no poster', () => {
    r(<MovieCard title="Zeta" tmdbId={2} />);
    expect(screen.getByTestId('monogram-fallback')).toBeInTheDocument();
  });
});
