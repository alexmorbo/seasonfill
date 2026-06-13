import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';
import { PersonHero } from './PersonHero';

function r(node: React.ReactElement) {
  return render(<I18nextProvider i18n={i18n}>{node}</I18nextProvider>);
}

describe('<PersonHero />', () => {
  it('renders name, known-for chip, born line and TMDB link', () => {
    r(<PersonHero person={{
      tmdb_id: 4495,
      name: 'Pedro Pascal',
      known_for_department: 'Acting',
      birthday: '1975-04-02',
      place_of_birth: 'Santiago, Chile',
      profile_asset: 'aaaa',
    }} />);
    expect(screen.getByTestId('person-hero-name')).toHaveTextContent('Pedro Pascal');
    expect(screen.getByTestId('person-known-for')).toBeInTheDocument();
    expect(screen.getByTestId('person-born')).toBeInTheDocument();
    expect(screen.getByTestId('person-age')).toBeInTheDocument();
    expect(screen.getByTestId('person-link-tmdb').getAttribute('href'))
      .toBe('https://www.themoviedb.org/person/4495');
  });

  it('renders monogram fallback when no profile asset', () => {
    r(<PersonHero person={{ tmdb_id: 1, name: 'Jane Doe' }} />);
    expect(screen.getByTestId('person-hero-photo')).toHaveTextContent('JD');
  });

  it('renders Died line in red when deathday is set', () => {
    r(<PersonHero person={{
      tmdb_id: 1, name: 'Test Actor', birthday: '1940-01-01', deathday: '2020-01-01',
    }} />);
    expect(screen.getByTestId('person-died')).toBeInTheDocument();
  });

  it('handles missing person gracefully', () => {
    r(<PersonHero person={undefined} />);
    expect(screen.getByTestId('person-hero')).toBeInTheDocument();
  });
});
