import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';
import { MediaHero } from './MediaHero';
import type { MediaDetailVM } from './view-model';

function wrap(ui: React.ReactElement) {
  return (
    <MemoryRouter>
      <I18nextProvider i18n={i18n}>{ui}</I18nextProvider>
    </MemoryRouter>
  );
}

function baseVM(overrides: Partial<MediaDetailVM> = {}): MediaDetailVM {
  return {
    type: 'movie',
    localizedTitle: 'Dune',
    statusToken: 'unknown',
    yearLabel: '2021',
    genres: [],
    posterAsset: undefined,
    backdropAsset: undefined,
    ratings: {},
    actions: [],
    heroActions: {
      backHref: '/movies',
      backLabel: 'Back',
      showAddToSonarr: false,
      showCaret: false,
      openItems: [],
      addItems: [],
      onAddToSonarr: () => {},
      onAddToInstance: () => {},
      followButton: null,
    },
    sidebarFacts: [],
    keywords: [],
    cast: { members: [], href: '', mediaId: 1 },
    recommendations: {
      items: [],
      isLoading: false,
      visible: false,
      sentinelRef: { current: null },
      renderCard: () => null,
    },
    overview: { label: '', text: '' },
    degraded: [],
    sonarrOnly: false,
    ...overrides,
  };
}

// U-4 sub-step B / §7 last bullet — the only NEW test in this sub-step.
// Series coverage of `MediaHero` is exercised transitively via the frozen
// `SeriesHero.test.tsx` (which renders `<SeriesHero>` → `<MediaHero>`).
describe('<MediaHero />', () => {
  it('renders the movie-hero root testid when vm.type is "movie"', () => {
    render(wrap(<MediaHero vm={baseVM()} />));
    expect(screen.getByTestId('movie-hero')).toBeInTheDocument();
    expect(screen.queryByTestId('series-hero')).not.toBeInTheDocument();
  });

  it('renders heroExtras.nextCard inside hero-next-wrap and bottomStrip below the meta columns', () => {
    render(
      wrap(
        <MediaHero
          vm={baseVM()}
          heroExtras={{
            nextCard: <div data-testid="fixture-next-card">next</div>,
            bottomStrip: <div data-testid="fixture-bottom-strip">strip</div>,
          }}
        />,
      ),
    );

    const nextWrap = screen.getByTestId('hero-next-wrap');
    const nextCard = screen.getByTestId('fixture-next-card');
    expect(nextWrap).toContainElement(nextCard);

    const bottomStrip = screen.getByTestId('fixture-bottom-strip');
    expect(bottomStrip).toBeInTheDocument();
    // The bottom strip renders in `.sd-hero-right`, AFTER `.sd-hero-cols`
    // (which wraps the meta row + next-card) — never inside hero-next-wrap.
    expect(nextWrap).not.toContainElement(bottomStrip);
    expect(
      nextWrap.compareDocumentPosition(bottomStrip) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
  });

  it('does not render hero-next-wrap when heroExtras is omitted', () => {
    render(wrap(<MediaHero vm={baseVM()} />));
    expect(screen.queryByTestId('hero-next-wrap')).not.toBeInTheDocument();
  });
});
