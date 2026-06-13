import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import { MemoryRouter } from 'react-router-dom';
import i18n from '@/i18n';
import { CastCarousel } from './CastCarousel';

function r(node: React.ReactElement) {
  return render(
    <I18nextProvider i18n={i18n}>
      <MemoryRouter>{node}</MemoryRouter>
    </I18nextProvider>,
  );
}

const cast = [
  { person_id: 1, name: 'Pedro Pascal', character_name: 'Joel', episode_count: 9, profile_asset: 'aaa' },
  { person_id: 2, name: 'Bella Ramsey',  character_name: 'Ellie', episode_count: 9, profile_asset: 'bbb' },
];

describe('<CastCarousel />', () => {
  it('renders cast cards with name + character + episode count', () => {
    r(<CastCarousel instance="alpha" seriesId={42} cast={cast} />);
    expect(screen.getByTestId('cast-carousel')).toBeInTheDocument();
    expect(screen.getAllByTestId('cast-member')).toHaveLength(2);
    expect(screen.getByText('Pedro Pascal')).toBeInTheDocument();
    expect(screen.getByText('as Joel')).toBeInTheDocument();
    expect(screen.getAllByText('9 episodes')).toHaveLength(2);
  });

  it('renders a "View all" link to the cast subpage', () => {
    r(<CastCarousel instance="alpha" seriesId={42} cast={cast} />);
    const link = screen.getByTestId('cast-view-all') as HTMLAnchorElement;
    expect(link.getAttribute('href')).toBe('/series/alpha/42/cast');
  });

  it('returns null when cast is empty', () => {
    const { container } = r(<CastCarousel instance="alpha" seriesId={42} cast={[]} />);
    expect(container.firstChild).toBeNull();
  });

  it('caps at top-10 even when given more', () => {
    const big = Array.from({ length: 20 }, (_, i) => ({
      person_id: i, name: `Cast ${i}`, character_name: `Char ${i}`, episode_count: 1,
    }));
    r(<CastCarousel instance="alpha" seriesId={42} cast={big} />);
    expect(screen.getAllByTestId('cast-member')).toHaveLength(10);
  });
});
