import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';
import type { SeriesHero as HeroDTO } from '@/api/series';
import type { SeriesRatingsResponse } from '@/api/seriesRatings';
import { SeriesHero } from './SeriesHero';
import { AddToSonarrProvider } from '@/components/discovery/AddToSonarrProvider';
import {
  AddToSonarrCtx,
  type AddToSonarrTarget,
} from '@/components/discovery/add-to-sonarr-context';

// The hero single-sources its ★ from useSeriesRatings; mock the hook so each
// test drives a controlled /ratings response without a QueryClient/network.
let heroRatings: SeriesRatingsResponse | undefined;
vi.mock('@/api/seriesRatings', () => ({
  useSeriesRatings: () => ({ data: heroRatings }),
}));

// The split-button reads the instance roster + per-instance public_url from
// useInstances; mock it so each test drives a controlled instance list without
// a network fetch. Empty by default so pre-existing tests keep their prior
// behaviour (no sonarrHref, no caret).
let mockInstances: Array<{ name: string; public_url?: string }> = [];
vi.mock('@/lib/instances', () => ({
  useInstances: () => ({ data: { instances: mockInstances }, isPending: false }),
}));

const origFetch = globalThis.fetch;
beforeEach(() => {
  heroRatings = undefined;
  mockInstances = [];
  globalThis.fetch = vi.fn(async () =>
    new Response('{}', { status: 200, headers: { 'Content-Type': 'application/json' } }),
  ) as typeof fetch;
});
afterEach(() => { globalThis.fetch = origFetch; });

function wrap(ui: React.ReactElement) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0, staleTime: 0 } },
  });
  return (
    <QueryClientProvider client={qc}>
      <MemoryRouter>
        <I18nextProvider i18n={i18n}>
          <AddToSonarrProvider>{ui}</AddToSonarrProvider>
        </I18nextProvider>
      </MemoryRouter>
    </QueryClientProvider>
  );
}

const baseHero = {
  title: 'For All Mankind',
  status: 'continuing',
  year_start: 2019,
  runtime_minutes: 45,
  genres: [{ id: 1, name: 'Drama', language: 'en-US' }],
  networks: [{ id: 1, name: 'AppleTV+' }],
  backdrop_asset: 'fake-hash',
  poster_asset: 'fake-poster',
  studio: 'Sony Pictures TV',
  country: 'US',
};

describe('SeriesHero v2 bleed', () => {
  it('does NOT render a StatusPill in the title row', () => {
    render(wrap(<SeriesHero instance="homelab" seriesId={369} hero={baseHero as unknown as HeroDTO} />));
    expect(screen.queryByTestId('status-pill')).toBeNull();
  });

  it('does NOT render the networks strip in the meta row', () => {
    render(wrap(<SeriesHero instance="homelab" seriesId={369} hero={baseHero as unknown as HeroDTO} />));
    expect(screen.queryByText(/AppleTV\+/i)).toBeNull();
  });

  it('renders the glass NextEpisodeCard wrapper', () => {
    const heroWithNext = {
      ...baseHero,
      next_episode: { season_number: 5, episode_number: 3, title: 'Glasnost',
        air_date: new Date(Date.now() + 4*86400_000).toISOString() },
    };
    render(wrap(<SeriesHero instance="homelab" seriesId={369} hero={heroWithNext as unknown as HeroDTO} />));
    expect(screen.getByTestId('hero-next-wrap')).toBeInTheDocument();
    expect(screen.getByTestId('next-episode-card').dataset['variant']).toBe('default');
  });

  it('renders the HeroLibraryStrip with dark tone over the scrim', () => {
    render(wrap(<SeriesHero
      instance="homelab"
      seriesId={369}
      hero={baseHero as unknown as HeroDTO}
      library={{ monitored: true, episodes_total: 48, episodes_on_disk: 42,
                 missing_count: 6, size_on_disk_bytes: 12_000_000_000, dominant_quality: '' }}
    />));
    const strip = screen.getByTestId('hero-library-strip');
    expect(strip.dataset['tone']).toBe('dark');
  });

  it('falls back to sonarr-only flat header (no scrim layer)', () => {
    render(wrap(<SeriesHero
      instance="homelab"
      seriesId={369}
      hero={{ title: 'Cold', status: 'unknown' } as unknown as HeroDTO}
    />));
    expect(screen.getByTestId('series-hero').dataset['fallback']).toBe('sonarr-only');
    expect(screen.queryByTestId('hero-scrim')).toBeNull();
  });

  it('renders the in-hero back-link with the seriesDetail.back label', () => {
    render(wrap(<SeriesHero instance="homelab" seriesId={369} hero={baseHero as unknown as HeroDTO} />));
    const link = screen.getByTestId('hero-back-link');
    expect(link).toBeInTheDocument();
    expect(link.getAttribute('href')).toBe('/series');
    // Legacy testid preserved as inner span for backwards selector compat.
    expect(screen.getByTestId('series-detail-back')).toBeInTheDocument();
  });

  it('renders the in-hero back-link in the sonarr-only fallback too', () => {
    render(wrap(<SeriesHero
      instance="homelab"
      seriesId={369}
      hero={{ title: 'Cold', status: 'unknown' } as unknown as HeroDTO}
    />));
    expect(screen.getByTestId('hero-back-link')).toBeInTheDocument();
  });
});

describe('SeriesHero — single-source ratings (#1059 / F-11-FE)', () => {
  it('renders the hero ★ from the live /ratings value', () => {
    heroRatings = { tmdb_rating: 9.2, tmdb_votes: 5_000, sources: { tmdb: 'fresh' } };
    render(wrap(<SeriesHero
      instance="homelab"
      seriesId={369}
      hero={baseHero as unknown as HeroDTO}
    />));
    expect(screen.getByTestId('rating-tmdb')).toHaveTextContent('9.2');
  });

  it('falls back to the skeleton rating for instant first paint when /ratings is unresolved', () => {
    heroRatings = undefined;
    const heroWithRating = { ...baseHero, tmdb_rating: { score: 7.5, votes: 100 } };
    render(wrap(<SeriesHero
      instance="homelab"
      seriesId={369}
      hero={heroWithRating as unknown as HeroDTO}
    />));
    expect(screen.getByTestId('rating-tmdb')).toHaveTextContent('7.5');
  });

  it('prefers the live /ratings value over the skeleton (single source, no divergence)', () => {
    heroRatings = { tmdb_rating: 9.9, tmdb_votes: 10, sources: { tmdb: 'fresh' } };
    const heroWithRating = { ...baseHero, tmdb_rating: { score: 7.5, votes: 100 } };
    render(wrap(<SeriesHero
      instance="homelab"
      seriesId={369}
      hero={heroWithRating as unknown as HeroDTO}
    />));
    const tmdb = screen.getByTestId('rating-tmdb');
    expect(tmdb).toHaveTextContent('9.9');
    expect(tmdb).not.toHaveTextContent('7.5');
  });
});

describe('SeriesHero — Add to Sonarr (not-in-library)', () => {
  const target: AddToSonarrTarget = {
    title: 'For All Mankind', tvdbId: 355093, tmdbId: 87917,
  };

  it('renders the hero Add-to-Sonarr button for a TMDB-only series (no instance)', () => {
    render(wrap(<SeriesHero
      instance={undefined}
      seriesId={369}
      hero={baseHero as unknown as HeroDTO}
      addToSonarrTarget={target}
    />));
    expect(screen.getByTestId('hero-action-add-to-sonarr')).toBeInTheDocument();
    // Mutually exclusive with the in-library "Open in Sonarr" button.
    expect(screen.queryByTestId('hero-action-sonarr')).toBeNull();
  });

  it('opens the Add-to-Sonarr modal on click', () => {
    render(wrap(<SeriesHero
      instance={undefined}
      seriesId={369}
      hero={baseHero as unknown as HeroDTO}
      addToSonarrTarget={target}
    />));
    fireEvent.click(screen.getByTestId('hero-action-add-to-sonarr'));
    expect(screen.getByTestId('add-to-sonarr-modal')).toBeInTheDocument();
  });

  it('does NOT render the Add-to-Sonarr button when no target is provided', () => {
    render(wrap(<SeriesHero
      instance="homelab"
      seriesId={369}
      hero={baseHero as unknown as HeroDTO}
    />));
    expect(screen.queryByTestId('hero-action-add-to-sonarr')).toBeNull();
  });
});

describe('SeriesHero — Sonarr deep-link title_slug (S7)', () => {
  const cyrillicHero = { ...baseHero, title: 'Тед Лассо' };

  // title_slug prop present → deep-link uses Sonarr's authoritative slug,
  // NOT a slugified Cyrillic title (which would be empty → blank page).
  it('uses the passed titleSlug for the Sonarr deep-link over a Cyrillic title', () => {
    mockInstances = [{ name: 'homelab', public_url: 'http://homelab' }];
    render(wrap(<SeriesHero
      instance="homelab"
      seriesId={369}
      hero={cyrillicHero as unknown as HeroDTO}
      inLibraryInstances={['homelab']}
      titleSlug="ted-lasso"
    />));
    const href = screen.getByTestId('hero-action-sonarr').closest('a')?.getAttribute('href');
    expect(href).toBe('http://homelab/series/ted-lasso');
  });

  // Regression guard: WITHOUT the prop, a Cyrillic title slugifies to empty →
  // ".../series/" blank page — the exact bug the titleSlug wiring prevents.
  it('yields a blank /series/ slug for a Cyrillic title without titleSlug', () => {
    mockInstances = [{ name: 'homelab', public_url: 'http://homelab' }];
    render(wrap(<SeriesHero
      instance="homelab"
      seriesId={369}
      hero={cyrillicHero as unknown as HeroDTO}
      inLibraryInstances={['homelab']}
    />));
    const href = screen.getByTestId('hero-action-sonarr').closest('a')?.getAttribute('href');
    expect(href).toMatch(/\/series\/$/);
  });
});

describe('SeriesHero — split-button multi-instance (S3)', () => {
  const target: AddToSonarrTarget = {
    title: 'For All Mankind', tvdbId: 355093, tmdbId: 87917,
  };

  // (a) two instances, one in-library → open the primary + Add-to-other menu.
  it('shows the Open button and a caret with add/open items', async () => {
    mockInstances = [
      { name: 'homelab', public_url: 'http://homelab' },
      { name: 'backup', public_url: 'http://backup' },
    ];
    render(wrap(<SeriesHero
      instance="homelab"
      seriesId={369}
      hero={baseHero as unknown as HeroDTO}
      inLibraryInstances={['homelab']}
      addToSonarrTarget={target}
    />));
    expect(screen.getByTestId('hero-action-sonarr')).toBeInTheDocument();

    const user = userEvent.setup();
    await user.click(screen.getByTestId('hero-action-caret'));
    expect(await screen.findByTestId('hero-menu-add-backup')).toBeInTheDocument();
    expect(screen.getByTestId('hero-menu-open-homelab')).toBeInTheDocument();
  });

  // (b) both instances in-library → only open items, no add items.
  it('shows open items for every in-library instance and no add items', async () => {
    mockInstances = [
      { name: 'homelab', public_url: 'http://homelab' },
      { name: 'backup', public_url: 'http://backup' },
    ];
    render(wrap(<SeriesHero
      instance="homelab"
      seriesId={369}
      hero={baseHero as unknown as HeroDTO}
      inLibraryInstances={['homelab', 'backup']}
      addToSonarrTarget={target}
    />));

    const user = userEvent.setup();
    await user.click(screen.getByTestId('hero-action-caret'));
    expect(await screen.findByTestId('hero-menu-open-homelab')).toBeInTheDocument();
    expect(screen.getByTestId('hero-menu-open-backup')).toBeInTheDocument();
    expect(screen.queryByTestId('hero-menu-add-homelab')).toBeNull();
    expect(screen.queryByTestId('hero-menu-add-backup')).toBeNull();
  });

  // (c) single instance → no caret, just the Open button.
  it('renders no caret for a single instance', () => {
    mockInstances = [{ name: 'homelab', public_url: 'http://homelab' }];
    render(wrap(<SeriesHero
      instance="homelab"
      seriesId={369}
      hero={baseHero as unknown as HeroDTO}
      inLibraryInstances={['homelab']}
      addToSonarrTarget={target}
    />));
    expect(screen.queryByTestId('hero-action-caret')).toBeNull();
    expect(screen.getByTestId('hero-action-sonarr')).toBeInTheDocument();
  });

  // (d) not in any library → primary Add button, no Open button.
  it('renders the primary Add button when in no library', () => {
    mockInstances = [
      { name: 'homelab', public_url: 'http://homelab' },
      { name: 'backup', public_url: 'http://backup' },
    ];
    render(wrap(<SeriesHero
      instance={undefined}
      seriesId={369}
      hero={baseHero as unknown as HeroDTO}
      inLibraryInstances={[]}
      addToSonarrTarget={target}
    />));
    expect(screen.getByTestId('hero-action-add-to-sonarr')).toBeInTheDocument();
    expect(screen.queryByTestId('hero-action-sonarr')).toBeNull();
  });

  // (e) selecting an add-item opens the launcher with the chosen instanceName.
  it('opens the launcher with the chosen instanceName from a menu add-item', async () => {
    mockInstances = [
      { name: 'homelab', public_url: 'http://homelab' },
      { name: 'backup', public_url: 'http://backup' },
    ];
    const openSpy = vi.fn();
    const qc = new QueryClient({
      defaultOptions: { queries: { retry: false, gcTime: 0, staleTime: 0 } },
    });
    render(
      <QueryClientProvider client={qc}>
        <MemoryRouter>
          <I18nextProvider i18n={i18n}>
            <AddToSonarrCtx.Provider
              value={{ target: null, openAddToSonarr: openSpy, close: vi.fn() }}
            >
              <SeriesHero
                instance="homelab"
                seriesId={369}
                hero={baseHero as unknown as HeroDTO}
                inLibraryInstances={['homelab']}
                addToSonarrTarget={target}
              />
            </AddToSonarrCtx.Provider>
          </I18nextProvider>
        </MemoryRouter>
      </QueryClientProvider>,
    );

    const user = userEvent.setup();
    await user.click(screen.getByTestId('hero-action-caret'));
    await user.click(await screen.findByTestId('hero-menu-add-backup'));
    expect(openSpy).toHaveBeenCalledWith(
      expect.objectContaining({ instanceName: 'backup' }),
    );
  });
});
