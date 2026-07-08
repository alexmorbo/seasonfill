import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import { MemoryRouter } from 'react-router-dom';
import i18n from '@/i18n';
import { CastGrid } from './CastGrid';

function r(node: React.ReactElement) {
  return render(
    <I18nextProvider i18n={i18n}>
      <MemoryRouter>{node}</MemoryRouter>
    </I18nextProvider>,
  );
}

const sample = [
  { person_id: 1, tmdb_id: 100, name: 'Pedro Pascal', character_name: 'Joel Miller', episode_count: 30, profile_asset: 'aaa' },
  { person_id: 2, tmdb_id: 200, name: 'Bella Ramsey', character_name: 'Ellie', episode_count: 30, profile_asset: 'bbb' },
  { person_id: 3, name: 'Unknown Actor', character_name: 'Extra', episode_count: 1 },
];

describe('<CastGrid />', () => {
  it('renders one card per cast member', () => {
    r(<CastGrid cast={sample} totalEpisodeCount={62} />);
    expect(screen.getAllByTestId('cast-grid-card')).toHaveLength(3);
    expect(screen.getByText('Pedro Pascal')).toBeInTheDocument();
    expect(screen.getByText('as Ellie')).toBeInTheDocument();
  });

  it('renders /person/:tmdbId links when tmdb_id is present', () => {
    r(<CastGrid cast={sample} totalEpisodeCount={62} />);
    const cards = screen.getAllByTestId('cast-grid-card');
    const [first, second, third] = cards;
    expect(first?.getAttribute('href')).toBe('/person/100');
    expect(second?.getAttribute('href')).toBe('/person/200');
    // Card without tmdb_id renders a non-link <div>.
    expect(third?.getAttribute('href')).toBeNull();
    expect(third?.getAttribute('data-tmdb-id')).toBe('');
  });

  it('shows the empty callout when cast is empty', () => {
    r(<CastGrid cast={[]} totalEpisodeCount={0} />);
    expect(screen.getByTestId('cast-grid-empty')).toBeInTheDocument();
    expect(screen.queryByTestId('cast-grid')).toBeNull();
  });

  it('renders one card per row even when person_id is duplicated across roles', () => {
    // Mimics series «ER»: Cress Williams (person_id 231805) credited twice
    // with two different character names. A bare person_id key would collide.
    const dup = [
      { person_id: 231805, tmdb_id: 231805, name: 'Cress Williams', character_name: 'Officer Reggie Moore', episode_count: 5, profile_asset: 'ccc' },
      { person_id: 231805, tmdb_id: 231805, name: 'Cress Williams', character_name: 'Reggie Moore', episode_count: 3, profile_asset: 'ccc' },
    ];
    r(<CastGrid cast={dup} totalEpisodeCount={62} />);
    expect(screen.getAllByTestId('cast-grid-card')).toHaveLength(dup.length);
  });

  it('does NOT render role badges in v1 (feature flag off)', () => {
    r(<CastGrid cast={sample} totalEpisodeCount={62} />);
    expect(screen.queryByTestId('cast-role-badge-main')).toBeNull();
    expect(screen.queryByTestId('cast-role-badge-recurring')).toBeNull();
    expect(screen.queryByTestId('cast-role-badge-guest')).toBeNull();
  });
});
