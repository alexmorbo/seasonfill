import { describe, it, expect } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { I18nextProvider } from 'react-i18next';
import { MemoryRouter } from 'react-router-dom';
import i18n from '@/i18n';
import { OtherCreditsGrid } from './OtherCreditsGrid';

function r(node: React.ReactElement) {
  return render(
    <I18nextProvider i18n={i18n}>
      <MemoryRouter>{node}</MemoryRouter>
    </I18nextProvider>,
  );
}

// Server returns rows ordered by (year DESC, title ASC). The movie
// slots near the top so it falls inside the initial top-10 window once
// the "Include movies" toggle is on. The second test asserts the
// length expansion past the limit, so keep enough TV rows to overflow.
const mixed = [
  { tmdb_media_id: 9999, title: 'A Movie', year: 2023, kind: 'cast',
    media_type: 'movie', role_label: 'Movie Role',
    poster_asset: 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa' },
  ...Array.from({ length: 12 }, (_, i) => ({
    tmdb_media_id: 1000 + i, title: `TV Show ${i}`, year: 2020 + i, kind: 'cast',
    media_type: 'tv', role_label: `Role ${i}`,
    poster_asset: `${String(i).padStart(2, '0')}${'b'.repeat(62)}`,
  })),
];

describe('<OtherCreditsGrid />', () => {
  it('returns null when credits is empty', () => {
    const { container } = r(<OtherCreditsGrid credits={[]} />);
    expect(container.firstChild).toBeNull();
  });

  it('hides movies by default and reveals them when toggle is on', async () => {
    r(<OtherCreditsGrid credits={mixed} />);
    expect(screen.queryByText('A Movie · 2023')).toBeNull();
    await userEvent.click(screen.getByTestId('person-include-movies'));
    expect(screen.getByText('A Movie · 2023')).toBeInTheDocument();
  });

  it('limits to top 10 by default and expands on "Show more"', () => {
    r(<OtherCreditsGrid credits={mixed} />);
    // TV credits render via the unified SeriesCard (testid "series-card").
    expect(screen.getAllByTestId('series-card')).toHaveLength(10);
    fireEvent.click(screen.getByTestId('person-other-show-more'));
    expect(screen.getAllByTestId('series-card')).toHaveLength(12);
  });

  it('routes TV credits WITHOUT a canon series_id internally via resolve-nav (tmdbId)', () => {
    // slice(1, 2) skips the movie row (toggle OFF hides it) and grabs the
    // first TV row (tmdb_media_id 1000). No series_id ⇒ SeriesCard falls back
    // to the lazy tmdb resolve-nav path, NOT an external escape.
    r(<OtherCreditsGrid credits={mixed.slice(1, 2)} />);
    const card = screen.getByTestId('series-card');
    // Resolve-nav card is a role=button div carrying data-tmdb-id, no href.
    expect(card.tagName.toLowerCase()).toBe('div');
    expect(card.getAttribute('data-tmdb-id')).toBe('1000');
    expect(card.getAttribute('href')).toBeNull();
  });

  it('links TV credits internally to /series/{series_id} when the canon row exists', () => {
    const rows = [
      {
        tmdb_media_id: 1000,
        title: 'Canon No Cache',
        media_type: 'tv',
        kind: 'cast',
        year: 2024,
        series_id: 777,
      },
    ];
    r(<OtherCreditsGrid credits={rows} />);
    const card = screen.getByTestId('series-card');
    expect(card.getAttribute('href')).toBe('/series/777');
    expect(card.getAttribute('data-series-id')).toBe('777');
    // Internal links are <Link>, NOT <a target="_blank">.
    expect(card.getAttribute('target')).toBeNull();
  });

  it('renders ★ rating from vote_average on TV credits', () => {
    const rows = [
      { tmdb_media_id: 1000, title: 'Rated Show', media_type: 'tv',
        kind: 'cast', year: 2024, vote_average: 7.6, series_id: 5 },
    ];
    r(<OtherCreditsGrid credits={rows} />);
    expect(screen.getByTestId('series-card-rating')).toHaveTextContent('7.6');
  });

  it('never renders a tmdbOnly badge on TV credits', () => {
    const rows = [
      { tmdb_media_id: 1000, title: 'Show', media_type: 'tv', kind: 'cast',
        year: 2024, series_id: 5 },
    ];
    r(<OtherCreditsGrid credits={rows} />);
    expect(screen.queryByTestId('series-card-badge')).toBeNull();
    expect(screen.queryByText('TMDB')).toBeNull();
  });

  it('keeps movies as an EXTERNAL themoviedb.org anchor', () => {
    // Need at least one TV row so the section renders (filtered.length > 0).
    const rows = [
      {
        tmdb_media_id: 1,
        title: 'A TV Show',
        media_type: 'tv',
        kind: 'cast',
        year: 2024,
      },
      {
        tmdb_media_id: 9999,
        title: 'A Movie',
        media_type: 'movie',
        kind: 'cast',
        year: 2024,
      },
    ];
    r(<OtherCreditsGrid credits={rows} />);
    // Movies hidden by default — toggle them on.
    fireEvent.click(screen.getByTestId('person-include-movies'));
    // Movie card is the thin external CreditCard (testid person-other-card).
    const movieCard = screen.getByTestId('person-other-card');
    expect(movieCard.getAttribute('data-media-type')).toBe('movie');
    expect(movieCard.getAttribute('href')).toBe('https://www.themoviedb.org/movie/9999');
    expect(movieCard.getAttribute('target')).toBe('_blank');
    expect(movieCard.getAttribute('rel')).toContain('noreferrer');
    // TV row rides the internal SeriesCard.
    expect(screen.getByTestId('series-card')).toBeInTheDocument();
  });

  it('returns null when the filter empties the list (movies-only payload)', () => {
    const { container } = r(<OtherCreditsGrid credits={[
      { tmdb_media_id: 1, title: 'Only Movie', media_type: 'movie', kind: 'cast' },
    ]} />);
    expect(container.firstChild).toBeNull();
  });

  it('sort=votes_desc reorders rows by vote_count', () => {
    const rows = [
      { tmdb_media_id: 1, title: 'A', media_type: 'tv', kind: 'cast', vote_count: 100, year: 2024 },
      { tmdb_media_id: 2, title: 'B', media_type: 'tv', kind: 'cast', vote_count: 1000, year: 2023 },
    ];
    r(<OtherCreditsGrid credits={rows} />);
    // Default order — A first (BE order preserved).
    expect(screen.getAllByTestId('series-card')[0]).toHaveTextContent('A');
    // Switch sort to votes_desc.
    fireEvent.click(screen.getByTestId('person-other-sort-trigger'));
    fireEvent.click(screen.getByTestId('person-other-sort-option-votes_desc'));
    // B has higher vote_count; should now be first.
    expect(screen.getAllByTestId('series-card')[0]).toHaveTextContent('B');
  });

  it('renders department pill only for kind="crew" with department set', () => {
    const crew = [
      { tmdb_media_id: 1, title: 'Show', media_type: 'tv', kind: 'crew',
        department: 'Production', role_label: 'Producer' },
    ];
    const { unmount: u1 } = r(<OtherCreditsGrid credits={crew} />);
    expect(screen.getByTestId('person-other-dept-pill')).toHaveTextContent('Production');
    u1();

    const cast = [
      { tmdb_media_id: 2, title: 'Show', media_type: 'tv', kind: 'cast',
        department: 'Acting', role_label: 'Lead' },
    ];
    const { unmount: u2 } = r(<OtherCreditsGrid credits={cast} />);
    expect(screen.queryByTestId('person-other-dept-pill')).toBeNull();
    u2();

    const crewNullDept = [
      { tmdb_media_id: 3, title: 'Show', media_type: 'tv', kind: 'crew',
        role_label: 'Crew' },
    ];
    r(<OtherCreditsGrid credits={crewNullDept} />);
    expect(screen.queryByTestId('person-other-dept-pill')).toBeNull();
  });

  it('renders original_title subtitle only when it differs from title', () => {
    const differs = [
      { tmdb_media_id: 1, title: 'Yojimbo', media_type: 'tv', kind: 'cast',
        original_title: 'Yôjinbô' },
    ];
    const { unmount: u1 } = r(<OtherCreditsGrid credits={differs} />);
    expect(screen.getByTestId('person-other-original-title')).toHaveTextContent('Yôjinbô');
    u1();

    const sameDifferentCase = [
      { tmdb_media_id: 2, title: 'Yojimbo', media_type: 'tv', kind: 'cast',
        original_title: 'yojimbo' },
    ];
    const { unmount: u2 } = r(<OtherCreditsGrid credits={sameDifferentCase} />);
    expect(screen.queryByTestId('person-other-original-title')).toBeNull();
    u2();

    const missing = [
      { tmdb_media_id: 3, title: 'X', media_type: 'tv', kind: 'cast' },
    ];
    r(<OtherCreditsGrid credits={missing} />);
    expect(screen.queryByTestId('person-other-original-title')).toBeNull();
  });
});
