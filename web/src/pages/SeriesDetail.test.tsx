import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { TooltipProvider } from '@/components/ui/tooltip';
import i18n from '@/i18n';
import { PageTitleProvider } from '@/components/shell/page-title-context';
import { SeriesDetail } from './SeriesDetail';

const mockApi = vi.fn();
vi.mock('@/lib/api', async () => {
  const actual = await vi.importActual<typeof import('@/lib/api')>('@/lib/api');
  return { ...actual, api: (path: string) => mockApi(path) };
});

// `useInstancePublicURL` reads /instances via useInstances; stub it so
// the Sonarr-link branch in <SeriesHero> is exercised deterministically.
vi.mock('@/lib/instances', () => ({
  useInstances: () => ({ data: { instances: [{ name: 'homelab', public_url: 'http://sonarr' }] }, isPending: false }),
}));

// Story 530 — RecommendationsCarousel is gated by `useIsSectionVisible`
// (intersection-observer composer). In happy-dom IO never fires, so
// the section stays in sentinel-mode. Stub the composer to force
// visible=true so the carousel fetches + renders in integration tests.
vi.mock('@/api/seriesTorrents', async () => {
  const actual = await vi.importActual<typeof import('@/api/seriesTorrents')>('@/api/seriesTorrents');
  return { ...actual, useIsSectionVisible: () => true };
});
vi.mock('@/api/seriesRecommendations', async () => {
  const actual = await vi.importActual<typeof import('@/api/seriesRecommendations')>('@/api/seriesRecommendations');
  return { ...actual, useIsSectionVisible: () => true };
});

function renderRoute(path: string) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0, staleTime: 0 } } });
  return render(
    <PageTitleProvider defaultTitle="__INITIAL__">
      <I18nextProvider i18n={i18n}>
        <QueryClientProvider client={qc}>
          <TooltipProvider delayDuration={0}>
            <MemoryRouter initialEntries={[path]}>
              <Routes>
                {/* Story 495 / N-1e: global URL — `:instance` segment dropped. */}
                <Route path="/series/:id" element={<SeriesDetail />} />
              </Routes>
            </MemoryRouter>
          </TooltipProvider>
        </QueryClientProvider>
      </I18nextProvider>
    </PageTitleProvider>,
  );
}

// ── C3b (story 968): GET /series/:id serves seriesdetail.SkeletonDTO (wire-
// wrapped hero + sidebar); heavy sections load from their own lazy endpoints.
// The single mockResolvedValue is therefore replaced by a PATH-ROUTED mock.
const skeletonFixture = {
  series_id: 42,
  in_library_instances: ['homelab'],
  synced_at: new Date().toISOString(),
  degraded: [] as string[],
  lang: 'en-US',
  season_count: 5,
  hero: {
    title: { value: 'For All Mankind', lang: 'en-US' },
    year_start: 2019,
    runtime_minutes: 45,
    tagline: { value: 'The future is ours to take.' },
    poster_asset: 'aaaa',
    backdrop_asset: 'bbbb',
    genres: [{ tmdb_id: 1, name: 'Drama' }, { tmdb_id: 2, name: 'Sci-Fi' }],
    tmdb_rating: { score: 8.1, votes: 2100 },
    imdb_rating: { score: 8.0, votes: 84_000 },
    content_rating: 'TV-MA',
    next_episode: { season_number: 5, episode_number: 3, title: { value: 'Glasnost' }, air_date: '2026-07-14' },
  },
  sidebar: {
    status: 'continuing',
    networks: [{ tmdb_id: 1, name: 'Apple TV+' }],
    origin_countries: ['US'],
    original_language: 'en',
    first_air_date: '2019-11-01',
    production_companies: [{ tmdb_id: 9, name: 'Sony Pictures TV' }],
  },
  external_links: {
    imdb_id: 'tt7772588',
    tmdb_id: 87917,
    tvdb_id: 355093,
    homepage: 'https://www.apple.com/tv-pr/originals/for-all-mankind/',
  },
};
const overviewFixture = {
  overview: { overview: 'Alt-history NASA…', language: 'en-US', keywords: [{ id: 1, name: 'space race' }], awards: '4 wins, 18 nominations' },
  degraded: [] as string[],
};
const castFixture = {
  cast: [{ person_id: 1, tmdb_id: 5, name: 'Joel Kinnaman', character_name: 'Ed', episode_count: 9 }],
  degraded: [] as string[],
};
const seasonsFixture = {
  seasons: [{ season_number: 1, name: 'Season 1', episode_count: 10, poster_asset: 'ps1', air_date_start: '2019-11-01' }],
  degraded: [] as string[],
};
const libraryFixture = {
  instance: 'homelab',
  library: { episodes_on_disk: 42, episodes_total: 48, episodes_aired: 48, missing_count: 6, size_on_disk_bytes: 1024, dominant_quality: 'WEB-DL 1080p' },
  recent: [{ event_type: 'imported', subject: 'S05E02', at: new Date().toISOString() }],
};
const recsFixture = {
  items: [{ series_id: 99, title: 'Other', year: 2022, tmdb_rating: 7.7, in_library: false }],
  total_count: 1,
  has_more: false,
  limit: 20,
  offset: 0,
  degraded: [] as string[],
};
// W18-7b: canonical detail-page ratings surface loads from its own SWR
// /ratings endpoint (awards migrated in from the removed AwardsBlock).
const ratingsFixture = {
  tmdb_rating: 8.1,
  tmdb_votes: 2100,
  imdb_rating: 8.0,
  imdb_votes: 84_000,
  rated: 'TV-MA',
  awards: '4 wins, 18 nominations',
  sources: { tmdb: 'fresh', omdb: 'fresh' },
};

interface RouteOverrides {
  readonly skeleton?: Record<string, unknown>;
  readonly overview?: Record<string, unknown>;
  readonly cast?: Record<string, unknown>;
  readonly seasons?: Record<string, unknown>;
  readonly library?: Record<string, unknown>;
  readonly recs?: Record<string, unknown>;
  readonly ratings?: Record<string, unknown>;
}

// Install a path-routed mock. Overrides shallow-merge over the section
// defaults (so `{ hero: … }` replaces the whole hero, `{ degraded: … }`
// replaces the whole list — matching how the tests drive per-section state).
function installRoutes(over: RouteOverrides = {}) {
  const skeleton = { ...skeletonFixture, ...over.skeleton };
  const overview = { ...overviewFixture, ...over.overview };
  const cast = { ...castFixture, ...over.cast };
  const seasons = { ...seasonsFixture, ...over.seasons };
  const library = { ...libraryFixture, ...over.library };
  const recs = { ...recsFixture, ...over.recs };
  const ratings = { ...ratingsFixture, ...over.ratings };
  mockApi.mockImplementation((path: string) => {
    // Default to the skeleton for any unrecognized / transient path so a
    // late-resolving query during cross-test teardown can't throw.
    if (typeof path !== 'string') return Promise.resolve(skeleton);
    if (path.includes('/overview')) return Promise.resolve(overview);
    if (path.includes('/recommendations')) return Promise.resolve(recs);
    if (path.includes('/cast')) return Promise.resolve(cast);
    if (path.includes('/seasons')) return Promise.resolve(seasons);
    if (path.includes('/library')) return Promise.resolve(library);
    if (path.includes('/ratings')) return Promise.resolve(ratings);
    return Promise.resolve(skeleton); // /series/:id
  });
}

describe('<SeriesDetail />', () => {
  beforeEach(() => {
    mockApi.mockReset();
  });

  it('renders the skeleton while loading', async () => {
    // Every in-flight query (skeleton + lazy sections) must settle during
    // teardown; capture each resolver so we can flush them all.
    const resolvers: Array<(v: unknown) => void> = [];
    mockApi.mockImplementation(() => new Promise((res) => { resolvers.push(res); }));
    renderRoute('/series/122');
    expect(await screen.findByTestId('series-detail-skeleton')).toBeInTheDocument();
    for (const resolve of resolvers) {
      resolve(skeletonFixture);
    }
    await waitFor(() => expect(screen.queryByTestId('series-detail-skeleton')).not.toBeInTheDocument());
  });

  it('renders the full hero, ratings, library, cast and seasons on success', async () => {
    installRoutes();
    renderRoute('/series/122');
    await waitFor(() => expect(screen.getByTestId('series-hero')).toBeInTheDocument());
    expect(screen.getByTestId('hero-title')).toHaveTextContent('For All Mankind');
    expect(screen.getByTestId('rating-tmdb')).toBeInTheDocument();
    expect(screen.getByTestId('rating-imdb')).toBeInTheDocument();
    expect(screen.getByTestId('hero-library-strip')).toBeInTheDocument();
    expect(screen.getByTestId('overview-section')).toBeInTheDocument();
    await waitFor(() => expect(screen.getByTestId('cast-strip-grid')).toBeInTheDocument());
    expect(screen.getByTestId('rail-card')).toBeInTheDocument();
    // W18-7b: ratings section renders under cast (replaces AwardsBlock;
    // awards migrated in). Backed by the SWR /ratings endpoint.
    const ratingsSection = await screen.findByTestId('ratings-section');
    expect(ratingsSection).toBeInTheDocument();
    expect(screen.getByTestId('ratings-awards')).toHaveTextContent(
      '4 wins, 18 nominations',
    );
    expect(screen.queryByTestId('rail-row-awards')).toBeNull();
    // DOM order — cast strip must come BEFORE the ratings section.
    const castStrip = screen.getByTestId('cast-strip');
    expect(
      castStrip.compareDocumentPosition(ratingsSection) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
    expect(screen.getByTestId('seasons-accordion')).toBeInTheDocument();
    // Story 530 — carousel mounts on its own /recommendations query.
    await waitFor(() => expect(screen.getByTestId('recommendations-carousel')).toBeInTheDocument());
    // The three deferred placeholders are gone:
    expect(screen.queryByTestId('placeholder-seasons')).not.toBeInTheDocument();
    expect(screen.queryByTestId('placeholder-cast')).not.toBeInTheDocument();
    expect(screen.queryByTestId('placeholder-recommendations')).not.toBeInTheDocument();
    // Torrents placeholder is gone — K-1 mounts the real TorrentsSection.
    expect(screen.queryByTestId('placeholder-torrents')).not.toBeInTheDocument();
    // C3c-1 — external-links footer restored from skeleton.external_links.
    expect(screen.getByTestId('external-links-footer')).toBeInTheDocument();
    // Legacy surfaces removed
    expect(screen.queryByTestId('library-status-card')).not.toBeInTheDocument();
    expect(screen.queryByTestId('cast-carousel')).not.toBeInTheDocument();
  });

  it('renders the recent grab strip from the library endpoint', async () => {
    installRoutes();
    renderRoute('/series/122');
    await waitFor(() => expect(screen.getByText(/S05E02/)).toBeInTheDocument());
  });

  it('renders sections in v2 order', async () => {
    installRoutes();
    renderRoute('/series/122');
    await waitFor(() => expect(screen.getByTestId('series-hero')).toBeInTheDocument());
    // Story 530 — carousel mounts on its own /recommendations query.
    await waitFor(() => expect(screen.getByTestId('recommendations-carousel')).toBeInTheDocument());
    // C3c-1 — external-links-footer restored at the tail of the section order.
    const order = ['series-hero', 'overview-section',
                   'seasons-accordion', 'recommendations-carousel',
                   'external-links-footer'];
    const elements = order.map(id => screen.getByTestId(id) as HTMLElement);
    for (let i = 1; i < elements.length; i++) {
      const prev = elements[i - 1] as Node;
      const curr = elements[i] as Node;
      expect(prev.compareDocumentPosition(curr))
        .toBe(Node.DOCUMENT_POSITION_FOLLOWING);
    }
  });

  it('renders the Sonarr-only state with no TMDB blocks', async () => {
    // in_library_instances empty ⇒ TMDB-only: library query disabled, hero
    // renders with a sparse (title-only) skeleton and no crash.
    installRoutes({
      skeleton: {
        in_library_instances: [],
        degraded: ['tmdb_series', 'omdb'],
        hero: { title: { value: 'Cold Show' }, year_start: 2010, year_end: 2014 },
        sidebar: { status: 'ended' },
      },
    });
    renderRoute('/series/122');
    await waitFor(() => expect(screen.getByTestId('series-hero')).toBeInTheDocument());
    expect(screen.getByTestId('series-hero').getAttribute('data-sonarr-only')).toBe('true');
    expect(screen.queryByTestId('hero-backdrop')).not.toBeInTheDocument();
    expect(screen.queryByTestId('rating-tmdb')).not.toBeInTheDocument();
    expect(screen.queryByTestId('rating-imdb')).not.toBeInTheDocument();
    expect(screen.queryByTestId('hero-action-trailer')).not.toBeInTheDocument();
    // library query disabled ⇒ on-disk strip renders its empty state.
    expect(screen.getByText(/Nothing on disk yet/)).toBeInTheDocument();
  });

  it('renders an error alert when the API fails', async () => {
    mockApi.mockImplementation(() => Promise.reject(new Error('boom')));
    renderRoute('/series/122');
    await waitFor(() => expect(screen.getByTestId('series-detail-error')).toBeInTheDocument());
  });

  it('renders the invalid-params alert when the id is NaN', () => {
    // The hook is disabled when seriesId is NaN; api is never invoked.
    mockApi.mockResolvedValue(undefined);
    renderRoute('/series/notanumber');
    expect(screen.queryByTestId('series-detail-skeleton')).not.toBeInTheDocument();
    expect(screen.getByText(/Invalid series link/)).toBeInTheDocument();
  });
});

describe('B-20 degraded per-section', () => {
  // Sparse hero WITH a tmdb_rating (so isSonarrOnly=false ⇒ cast/rating
  // sections render) but NO imdb_rating / backdrop (drives the loading UX).
  const coldHero = {
    title: { value: 'Cold Series' },
    year_start: 2020,
    tmdb_rating: { score: 7.5, votes: 100 },
  };
  const coldSidebar = { status: 'continuing' };

  beforeEach(() => {
    mockApi.mockReset();
  });

  it('shows overview loading copy + skeleton when tmdb_series is degraded', async () => {
    installRoutes({
      skeleton: { degraded: ['tmdb_series'], hero: coldHero, sidebar: coldSidebar },
      overview: { overview: { overview: '', language: 'en-US' }, degraded: [] },
    });
    renderRoute('/series/122');
    await waitFor(() => expect(screen.getByTestId('overview-text')).toBeInTheDocument());
    expect(screen.getByTestId('overview-text').textContent).toMatch(/Loading description/i);
    expect(screen.getByTestId('overview-skeleton')).toBeInTheDocument();
  });

  it('shows season skeleton rows when tmdb_season is degraded', async () => {
    installRoutes({
      skeleton: { degraded: ['tmdb_season'], hero: coldHero, sidebar: coldSidebar },
      seasons: { seasons: [], degraded: [] },
    });
    renderRoute('/series/122');
    await waitFor(() => expect(screen.getByTestId('seasons-accordion')).toBeInTheDocument());
    expect(screen.getByTestId('seasons-loading-label')).toBeInTheDocument();
    expect(screen.getAllByTestId('seasons-skeleton-row')).toHaveLength(5);
  });

  it('shows cast strip loading skeletons when tmdb_person is degraded', async () => {
    installRoutes({
      skeleton: { degraded: ['tmdb_person'], hero: coldHero, sidebar: coldSidebar },
      cast: { cast: [], degraded: [] },
    });
    renderRoute('/series/122');
    await waitFor(() => expect(screen.getByTestId('cast-strip-loading')).toBeInTheDocument());
    expect(screen.getAllByTestId('cast-skeleton-avatar')).toHaveLength(8);
  });

  it('shows IMDb loading chip in hero when omdb is degraded and rating is missing', async () => {
    installRoutes({
      skeleton: { degraded: ['omdb'], hero: coldHero, sidebar: coldSidebar },
    });
    renderRoute('/series/122');
    await waitFor(() => expect(screen.getByTestId('imdb-rating-loading')).toBeInTheDocument());
  });

  it('shows backdrop loading plate when tmdb_series is degraded and no backdrop is present', async () => {
    // hero has no backdrop_asset → MonogramFallback path; tmdb_series
    // degraded ⇒ thin loading plate overlay rendered inside the fallback.
    installRoutes({
      skeleton: { degraded: ['tmdb_series'], hero: coldHero, sidebar: coldSidebar },
    });
    renderRoute('/series/122');
    await waitFor(() => expect(screen.getByTestId('monogram-loading-plate')).toBeInTheDocument());
  });

  it('shows recommendations skeleton tiles when tmdb_series is degraded and list is empty', async () => {
    installRoutes({
      skeleton: { degraded: ['tmdb_series'], hero: coldHero, sidebar: coldSidebar },
      recs: { items: [], total_count: 0, degraded: ['tmdb_series'] },
    });
    renderRoute('/series/122');
    await waitFor(() => expect(screen.getByTestId('recommendations-carousel-loading')).toBeInTheDocument());
    expect(screen.getAllByTestId('recommendations-skeleton-tile')).toHaveLength(6);
  });

  // Story 531 — aggregated degraded chip.
  it('renders the aggregated degraded chip when a single source is degraded', async () => {
    // Same source in the skeleton + overview + recs lists — dedupe must
    // collapse the 3 occurrences to one.
    installRoutes({
      skeleton: { degraded: ['tmdb_series'], hero: coldHero, sidebar: coldSidebar },
      overview: { overview: { overview: '', language: 'en-US' }, degraded: ['tmdb_series'] },
      recs: { items: [], degraded: ['tmdb_series'] },
    });
    renderRoute('/series/122');
    await waitFor(() =>
      expect(screen.getByTestId('series-degraded-chip')).toBeInTheDocument(),
    );
    expect(
      screen.getByTestId('series-degraded-chip').getAttribute('data-sources'),
    ).toBe('tmdb_series');
  });

  it('dedupes overlapping degraded sources across parent + per-section hooks', async () => {
    installRoutes({
      skeleton: { degraded: ['tmdb_series', 'omdb'], hero: coldHero, sidebar: coldSidebar },
      overview: { overview: { overview: '', language: 'en-US' }, degraded: ['tmdb_series'] },
      recs: { items: [], degraded: ['omdb'] },
    });
    renderRoute('/series/122');
    await waitFor(() =>
      expect(screen.getByTestId('series-degraded-chip')).toBeInTheDocument(),
    );
    const chip = screen.getByTestId('series-degraded-chip');
    expect(chip.getAttribute('data-sources')).toBe('tmdb_series,omdb');
  });

  it('hides the chip when no sources are degraded', async () => {
    installRoutes(); // all degraded: []
    renderRoute('/series/122');
    await waitFor(() =>
      expect(screen.getByTestId('series-hero')).toBeInTheDocument(),
    );
    expect(
      screen.queryByTestId('series-degraded-chip'),
    ).not.toBeInTheDocument();
  });
});

describe('URL migration (story 495 / N-1e)', () => {
  beforeEach(() => mockApi.mockReset());

  it('renders page from global URL `/series/:id`', async () => {
    installRoutes();
    renderRoute('/series/122');
    await waitFor(() => expect(screen.getByTestId('series-hero')).toBeInTheDocument());
    expect(screen.getByTestId('hero-title')).toHaveTextContent('For All Mankind');
  });

  it('cast-strip view-all link is instance-less', async () => {
    installRoutes();
    renderRoute('/series/122');
    await waitFor(() => expect(screen.getByTestId('cast-strip-view-all')).toBeInTheDocument());
    expect(screen.getByTestId('cast-strip-view-all').getAttribute('href')).toBe('/series/122/cast');
  });
});
