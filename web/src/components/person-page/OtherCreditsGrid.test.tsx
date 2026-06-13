import { describe, it, expect } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';
import { OtherCreditsGrid } from './OtherCreditsGrid';

function r(node: React.ReactElement) {
  return render(<I18nextProvider i18n={i18n}>{node}</I18nextProvider>);
}

// Server returns rows ordered by (year DESC, title ASC). The movie
// slots near the top so it falls inside the initial top-10 window once
// the "Include movies" toggle is on. The second test asserts the
// length expansion past the limit, so keep enough TV rows to overflow.
const mixed = [
  { tmdb_media_id: 9999, title: 'A Movie', year: 2023, kind: 'cast',
    media_type: 'movie', role_label: 'Movie Role', poster_path: '/m.jpg' },
  ...Array.from({ length: 12 }, (_, i) => ({
    tmdb_media_id: 1000 + i, title: `TV Show ${i}`, year: 2020 + i, kind: 'cast',
    media_type: 'tv', role_label: `Role ${i}`, poster_path: `/tv${i}.jpg`,
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
    expect(screen.getAllByTestId('person-other-card')).toHaveLength(10);
    fireEvent.click(screen.getByTestId('person-other-show-more'));
    expect(screen.getAllByTestId('person-other-card')).toHaveLength(12);
  });

  it('opens the correct TMDB URL in a new tab', () => {
    // slice(1, 2) skips the movie row (toggle OFF hides it) and grabs
    // the first TV row whose tmdb_media_id is 1000.
    r(<OtherCreditsGrid credits={mixed.slice(1, 2)} />);
    const card = screen.getByTestId('person-other-card');
    expect(card.getAttribute('href')).toBe('https://www.themoviedb.org/tv/1000');
    expect(card.getAttribute('target')).toBe('_blank');
    expect(card.getAttribute('rel')).toContain('noreferrer');
  });

  it('returns null when the filter empties the list (movies-only payload)', () => {
    const { container } = r(<OtherCreditsGrid credits={[
      { tmdb_media_id: 1, title: 'Only Movie', media_type: 'movie', kind: 'cast' },
    ]} />);
    expect(container.firstChild).toBeNull();
  });
});
