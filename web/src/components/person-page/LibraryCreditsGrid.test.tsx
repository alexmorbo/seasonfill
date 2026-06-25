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
  {
    series_id: 42,
    title: 'The Last of Us',
    year: 2023,
    role_label: 'Joel Miller · 9 ep.',
    kind: 'cast',
    instances: [
      { instance: 'alpha', sonarr_series_id: 7001 },
      { instance: '4k', sonarr_series_id: 9001 },
    ],
    poster_asset: 'aaa',
  },
  {
    series_id: 43,
    title: 'Game of Thrones',
    year: 2011,
    role_label: 'Oberyn Martell · 7 ep.',
    kind: 'cast',
    instances: [{ instance: 'alpha', sonarr_series_id: 7050 }],
    poster_asset: 'bbb',
  },
];

describe('<LibraryCreditsGrid />', () => {
  it('returns null when credits is empty', () => {
    const { container } = r(
      <LibraryCreditsGrid credits={[]} sort="recent" onSortChange={() => {}} />,
    );
    expect(container.firstChild).toBeNull();
  });

  it('links to canonical /series/{series_id} URL (Story 537 / B-42e — NOT legacy 3-segment)', () => {
    r(<LibraryCreditsGrid credits={sample} sort="recent" onSortChange={() => {}} />);
    const cards = screen.getAllByTestId('person-library-card');
    expect(cards).toHaveLength(2);
    expect(cards[0]?.getAttribute('href')).toBe('/series/42');
    expect(cards[1]?.getAttribute('href')).toBe('/series/43');
    // Defence-in-depth: ensure the legacy 3-segment shape NEVER ships again.
    for (const c of cards) {
      expect(c.getAttribute('href')).not.toMatch(/\/series\/[^/]+\/\d+$/);
    }
  });

  it('renders title with year and the role label', () => {
    r(<LibraryCreditsGrid credits={sample} sort="recent" onSortChange={() => {}} />);
    expect(screen.getByText('The Last of Us · 2023')).toBeInTheDocument();
    expect(screen.getByText('Joel Miller · 9 ep.')).toBeInTheDocument();
  });

  it('still links to canonical /series/{series_id} when instances array is empty', () => {
    r(
      <LibraryCreditsGrid
        credits={[
          {
            series_id: 99,
            title: 'Orphaned',
            year: 2020,
            kind: 'cast',
            instances: [],
          },
        ]}
        sort="recent"
        onSortChange={() => {}}
      />,
    );
    const card = screen.getByTestId('person-library-card');
    expect(card.getAttribute('href')).toBe('/series/99');
  });

  it('renders non-clickable card when series_id is missing (defensive — should not happen in prod)', () => {
    r(
      <LibraryCreditsGrid
        credits={[
          { title: 'No canon id', kind: 'cast', instances: [] } as never,
        ]}
        sort="recent"
        onSortChange={() => {}}
      />,
    );
    const card = screen.getByTestId('person-library-card');
    expect(card.getAttribute('href')).toBeNull();
    expect(card.tagName.toLowerCase()).toBe('div');
  });

  it('renders the In-library badge on each card', () => {
    r(<LibraryCreditsGrid credits={sample} sort="recent" onSortChange={() => {}} />);
    const badges = screen.getAllByTestId('person-library-card-badge');
    expect(badges).toHaveLength(2);
    for (const b of badges) {
      expect(b.getAttribute('data-badge')).toBe('inLibrary');
    }
  });

  it('renders the sort control', () => {
    r(<LibraryCreditsGrid credits={sample} sort="recent" onSortChange={vi.fn()} />);
    expect(screen.getByTestId('person-sort-control')).toBeInTheDocument();
  });
});
