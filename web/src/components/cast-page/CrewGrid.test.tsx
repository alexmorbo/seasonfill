import { describe, it, expect } from 'vitest';
import { render, screen, within } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import { MemoryRouter } from 'react-router-dom';
import i18n from '@/i18n';
import { CrewGrid } from './CrewGrid';

function r(node: React.ReactElement) {
  return render(
    <I18nextProvider i18n={i18n}>
      <MemoryRouter>{node}</MemoryRouter>
    </I18nextProvider>,
  );
}

const sample = [
  { person_id: 10, tmdb_id: 1010, name: 'Jane Director', job: 'Director', department: 'Directing', episode_count: 5 },
  { person_id: 10, tmdb_id: 1010, name: 'Jane Director', job: 'Executive Producer', department: 'Directing', episode_count: 5 },
  { person_id: 11, tmdb_id: 1011, name: 'Pete Producer', job: 'Producer', department: 'Production', episode_count: 12 },
  { person_id: 12, tmdb_id: 1012, name: 'Wendy Writer', job: 'Writer', department: 'Writing', episode_count: 8 },
];

describe('<CrewGrid />', () => {
  it('groups by department with a header per group', () => {
    r(<CrewGrid crew={sample} />);
    const depts = screen.getAllByTestId('crew-department');
    expect(depts).toHaveLength(3);
    expect(depts.map((d) => d.getAttribute('data-department'))).toEqual([
      'Directing', 'Production', 'Writing',
    ]);
  });

  it('folds multiple jobs on the same person inside one department', () => {
    r(<CrewGrid crew={sample} />);
    const directing = screen.getAllByTestId('crew-department')[0];
    if (!directing) throw new Error('expected Directing section');
    const cards = within(directing).getAllByTestId('crew-grid-card');
    expect(cards).toHaveLength(1);
    expect(within(directing).getByText('Director · Executive Producer')).toBeInTheDocument();
  });

  it('renders /person/:tmdbId links when tmdb_id is present', () => {
    r(<CrewGrid crew={sample} />);
    const cards = screen.getAllByTestId('crew-grid-card');
    expect(cards[0]?.getAttribute('href')).toBe('/person/1010');
  });

  it('shows the empty callout when crew is empty', () => {
    r(<CrewGrid crew={[]} />);
    expect(screen.getByTestId('crew-grid-empty')).toBeInTheDocument();
    expect(screen.queryByTestId('crew-grid')).toBeNull();
  });
});
