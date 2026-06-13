import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import { MemoryRouter } from 'react-router-dom';
import i18n from '@/i18n';
import { LibraryCreditsGrid } from './LibraryCreditsGrid';

function r(node: React.ReactElement) {
  return render(
    <I18nextProvider i18n={i18n}>
      <MemoryRouter>{node}</MemoryRouter>
    </I18nextProvider>,
  );
}

const sample = [
  { series_id: 42, title: 'The Last of Us', year: 2023, role_label: 'Joel Miller · 9 ep.',
    kind: 'cast', instances: ['alpha', '4k'], poster_asset: 'aaa' },
  { series_id: 43, title: 'Game of Thrones', year: 2011, role_label: 'Oberyn Martell · 7 ep.',
    kind: 'cast', instances: ['alpha'], poster_asset: 'bbb' },
];

describe('<LibraryCreditsGrid />', () => {
  it('returns null when credits is empty', () => {
    const { container } = r(
      <LibraryCreditsGrid credits={[]} sort="recent" onSortChange={() => {}} />,
    );
    expect(container.firstChild).toBeNull();
  });

  it('renders one card per credit with correct href', () => {
    r(<LibraryCreditsGrid credits={sample} sort="recent" onSortChange={() => {}} />);
    const cards = screen.getAllByTestId('person-library-card');
    expect(cards).toHaveLength(2);
    expect(cards[0]?.getAttribute('href')).toBe('/series/alpha/42');
    expect(cards[1]?.getAttribute('href')).toBe('/series/alpha/43');
  });

  it('renders title with year and the role label', () => {
    r(<LibraryCreditsGrid credits={sample} sort="recent" onSortChange={() => {}} />);
    expect(screen.getByText('The Last of Us · 2023')).toBeInTheDocument();
    expect(screen.getByText('Joel Miller · 9 ep.')).toBeInTheDocument();
  });

  it('renders a non-interactive div when instances is empty', () => {
    r(<LibraryCreditsGrid
      credits={[{ series_id: 99, title: 'Orphaned', year: 2020, kind: 'cast', instances: [] }]}
      sort="recent"
      onSortChange={() => {}}
    />);
    const card = screen.getByTestId('person-library-card');
    expect(card.getAttribute('href')).toBeNull();
  });

  it('renders the sort control', () => {
    r(<LibraryCreditsGrid credits={sample} sort="recent" onSortChange={vi.fn()} />);
    expect(screen.getByTestId('person-sort-control')).toBeInTheDocument();
  });
});
