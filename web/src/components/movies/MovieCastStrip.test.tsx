import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';
import type { MovieCastMember } from '@/api/movieCast';
import { MovieCastStrip } from './MovieCastStrip';

function wrap(ui: React.ReactElement) {
  return (
    <MemoryRouter>
      <I18nextProvider i18n={i18n}>{ui}</I18nextProvider>
    </MemoryRouter>
  );
}

const sample: MovieCastMember[] = [
  { person_id: 1, tmdb_id: 6384, name: 'Keanu Reeves', character_name: 'Neo', profile_asset: 'h1', credit_order: 0 },
  { person_id: 2, tmdb_id: 2975, name: 'Laurence Fishburne', character_name: 'Morpheus', credit_order: 1 },
  { person_id: 3, tmdb_id: 130, name: 'Carrie-Anne Moss', character_name: 'Trinity', profile_asset: 'h3', credit_order: 2 },
];

describe('MovieCastStrip', () => {
  it('returns null when cast is empty and not loading', () => {
    const { container } = render(wrap(<MovieCastStrip tmdbId={603} cast={[]} />));
    expect(container.firstChild).toBeNull();
  });

  it('renders a skeleton row when empty AND loading', () => {
    render(wrap(<MovieCastStrip tmdbId={603} cast={[]} loading />));
    expect(screen.getByTestId('movie-cast-strip-loading')).toBeTruthy();
    expect(screen.getAllByTestId('movie-cast-skeleton-avatar').length).toBeGreaterThan(0);
  });

  it('renders one card per member, capped at limit, in received (credit) order', () => {
    render(wrap(<MovieCastStrip tmdbId={603} cast={sample} limit={2} />));
    const cards = screen.getAllByTestId('movie-cast-strip-card');
    expect(cards).toHaveLength(2);
    // Received order is preserved — NO re-sort.
    const names = screen.getAllByTestId('movie-cast-strip-name').map((n) => n.textContent);
    expect(names).toEqual(['Keanu Reeves', 'Laurence Fishburne']);
  });

  it('does NOT re-sort by credit_order (renders array order verbatim)', () => {
    // Deliberately hand the strip members whose array order != credit_order.
    const outOfOrder: MovieCastMember[] = [
      { person_id: 9, tmdb_id: 9, name: 'Third Billed', character_name: 'C', credit_order: 2 },
      { person_id: 8, tmdb_id: 8, name: 'First Billed', character_name: 'A', credit_order: 0 },
    ];
    render(wrap(<MovieCastStrip tmdbId={603} cast={outOfOrder} />));
    const names = screen.getAllByTestId('movie-cast-strip-name').map((n) => n.textContent);
    // BE owns ordering; the strip must NOT reorder by credit_order.
    expect(names).toEqual(['Third Billed', 'First Billed']);
  });

  it('renders the avatar image when profile_asset is set', () => {
    render(wrap(<MovieCastStrip tmdbId={603} cast={[sample[0]!]} />));
    expect(screen.getByTestId('movie-cast-strip-avatar').querySelector('img')).toBeTruthy();
  });

  it('renders the monogram fallback when profile_asset is missing', () => {
    render(wrap(<MovieCastStrip tmdbId={603} cast={[sample[1]!]} />));
    const av = screen.getByTestId('movie-cast-strip-avatar');
    expect(av.querySelector('img')).toBeFalsy();
    expect(av.textContent).toBeTruthy();
  });

  it('links to /person/${tmdb_id} when TMDB person id present', () => {
    render(wrap(<MovieCastStrip tmdbId={603} cast={[sample[0]!]} />));
    const card = screen.getByTestId('movie-cast-strip-card');
    expect(card.tagName).toBe('A');
    expect(card.getAttribute('href')).toBe('/person/6384');
  });

  it('renders a non-link div for members without a TMDB person id', () => {
    const cast: MovieCastMember[] = [
      { person_id: 42, name: 'Uncredited', character_name: 'Extra' },
    ];
    render(wrap(<MovieCastStrip tmdbId={603} cast={cast} />));
    const card = screen.getByTestId('movie-cast-strip-card');
    expect(card.tagName).toBe('DIV');
    expect(card.getAttribute('href')).toBeNull();
    expect(card.getAttribute('data-no-link')).toBe('true');
  });

  it('shows the language-fallback signal when served_language differs from requested', () => {
    render(wrap(
      <MovieCastStrip
        tmdbId={603}
        cast={sample}
        servedLanguage="en-US"
        requestedLang="ru-RU"
      />,
    ));
    const tag = screen.getByTestId('movie-cast-lang-fallback');
    expect(tag.getAttribute('data-content-lang')).toBe('en');
  });

  it('hides the language-fallback signal when served_language matches requested', () => {
    render(wrap(
      <MovieCastStrip
        tmdbId={603}
        cast={sample}
        servedLanguage="ru-RU"
        requestedLang="ru-RU"
      />,
    ));
    expect(screen.queryByTestId('movie-cast-lang-fallback')).toBeNull();
  });

  it('hides the language-fallback signal when served_language is absent', () => {
    render(wrap(<MovieCastStrip tmdbId={603} cast={sample} requestedLang="ru-RU" />));
    expect(screen.queryByTestId('movie-cast-lang-fallback')).toBeNull();
  });

  it('omits the view-all link when no castHref is given', () => {
    render(wrap(<MovieCastStrip tmdbId={603} cast={sample} />));
    expect(screen.queryByTestId('movie-cast-strip-view-all')).toBeNull();
  });

  it('renders a view-all link when castHref is provided', () => {
    render(wrap(<MovieCastStrip tmdbId={603} cast={sample} castHref="/movies/603/cast" />));
    const link = screen.getByTestId('movie-cast-strip-view-all');
    expect(link.getAttribute('href')).toBe('/movies/603/cast');
  });
});
