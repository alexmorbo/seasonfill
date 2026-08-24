import { describe, expect, it } from 'vitest';
import { screen } from '@testing-library/react';
import { renderWithProviders } from '@/test-utils';
import { PersonCard } from './PersonCard';

describe('<PersonCard />', () => {
  it('renders the name, the known-for subtitle and links to /person/:tmdbId', () => {
    renderWithProviders(<PersonCard tmdbId={42} name="Ana de Armas" knownFor="Acting" />);
    expect(screen.getByTestId('person-card-name')).toHaveTextContent('Ana de Armas');
    expect(screen.getByTestId('person-card-known-for')).toHaveTextContent('Acting');
    expect(screen.getByTestId('person-card')).toHaveAttribute('href', '/person/42');
  });

  it('omits the subtitle when knownFor is absent', () => {
    renderWithProviders(<PersonCard tmdbId={7} name="Nobody" />);
    expect(screen.getByTestId('person-card-name')).toHaveTextContent('Nobody');
    expect(screen.queryByTestId('person-card-known-for')).toBeNull();
  });
});
