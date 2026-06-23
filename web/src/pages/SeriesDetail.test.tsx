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

const fullFixture = {
  instance: 'homelab',
  series_id: 42,
  sonarr_series_id: 122,
  // Story 495 / N-1e §A1: SeriesDetail picks the primary instance from
  // here instead of the URL. Empty ⇒ TMDB-only series.
  in_library_instances: ['homelab'],
  synced_at: new Date().toISOString(),
  degraded: [],
  hero: {
    title: 'For All Mankind',
    status: 'continuing',
    year_start: 2019,
    runtime_minutes: 45,
    tagline: 'The future is ours to take.',
    poster_asset: 'aaaa',
    backdrop_asset: 'bbbb',
    genres: [{ id: 1, name: 'Drama' }, { id: 2, name: 'Sci-Fi' }],
    networks: [{ id: 1, name: 'Apple TV+' }],
    tmdb_rating: { score: 8.1, votes: 2100 },
    imdb_rating: { score: 8.0, votes: 84_000 },
    content_rating: { rating: 'TV-MA' },
    next_episode: { season_number: 5, episode_number: 3, title: 'Glasnost', air_date: '2026-07-14' },
  },
  library: { episodes_on_disk: 42, episodes_total: 48, missing_count: 6, size_on_disk_bytes: 1024, dominant_quality: 'WEB-DL 1080p' },
  download: { status: 'downloading', title: 'S05E03 · 45%' },
  recent: [{ event_type: 'imported', subject: 'S05E02', at: new Date().toISOString() }],
  overview: { overview: 'Alt-history NASA…', language: 'en-US', keywords: [{ id: 1, name: 'space race' }], awards: '4 wins, 18 nominations' },
  external_links: { imdb_id: 'tt9243946', tmdb_id: 1396 },
  cast: [{ person_id: 1, name: 'Pedro Pascal', character_name: 'Joel', episode_count: 9 }],
  seasons: [{ season_number: 1, episode_count: 1, episodes: [{ episode_number: 1, title: 'Pilot', has_file: true }] }],
  recommendations: [{ series_id: 99, title: 'Other', year: 2022, tmdb_rating: 7.7, in_library: false }],
};

const sonarrOnlyFixture = {
  instance: 'homelab',
  series_id: 42,
  sonarr_series_id: 122,
  in_library_instances: ['homelab'],
  synced_at: new Date().toISOString(),
  // Story 495 / N-1e §C1: composer emits *_series / *_person /
  // _season variants — the prior `['tmdb', 'omdb']` shape never
  // matched live data.
  degraded: ['tmdb_series', 'omdb'],
  hero: { title: 'Cold Show', status: 'ended', year_start: 2010, year_end: 2014 },
  library: { episodes_on_disk: 0, episodes_total: 0, missing_count: 0, size_on_disk_bytes: 0 },
};

describe('<SeriesDetail />', () => {
  beforeEach(() => {
    mockApi.mockReset();
  });

  it('renders the skeleton while loading', async () => {
    let resolveDetail: ((v: unknown) => void) | undefined;
    mockApi.mockImplementation(() => new Promise((res) => { resolveDetail = res; }));
    renderRoute('/series/122');
    expect(await screen.findByTestId('series-detail-skeleton')).toBeInTheDocument();
    // Resolve the in-flight query so test teardown does not hang on the
    // dangling promise.
    resolveDetail?.(fullFixture);
    await waitFor(() => expect(screen.queryByTestId('series-detail-skeleton')).not.toBeInTheDocument());
  });

  it('renders the full hero, ratings, library and external links on success', async () => {
    mockApi.mockResolvedValue(fullFixture);
    renderRoute('/series/122');
    await waitFor(() => expect(screen.getByTestId('series-hero')).toBeInTheDocument());
    expect(screen.getByTestId('hero-title')).toHaveTextContent('For All Mankind');
    expect(screen.getByTestId('rating-tmdb')).toBeInTheDocument();
    expect(screen.getByTestId('rating-imdb')).toBeInTheDocument();
    expect(screen.getByTestId('hero-library-strip')).toBeInTheDocument();
    expect(screen.getByTestId('overview-section')).toBeInTheDocument();
    expect(screen.getByTestId('cast-strip-grid')).toBeInTheDocument();
    expect(screen.getByTestId('rail-card')).toBeInTheDocument();
    // B-36: awards block renders under cast (no longer inside RailCard).
    const awardsBlock = screen.getByTestId('awards-block');
    expect(awardsBlock).toBeInTheDocument();
    expect(screen.getByTestId('awards-text')).toHaveTextContent(
      '4 wins, 18 nominations',
    );
    expect(screen.queryByTestId('rail-row-awards')).toBeNull();
    // DOM order — cast strip must come BEFORE the awards block.
    const castStrip = screen.getByTestId('cast-strip');
    expect(
      castStrip.compareDocumentPosition(awardsBlock) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
    expect(screen.getByTestId('external-links-footer')).toBeInTheDocument();
    expect(screen.getByTestId('seasons-accordion')).toBeInTheDocument();
    expect(screen.getByTestId('recommendations-carousel')).toBeInTheDocument();
    // The three deferred placeholders are gone:
    expect(screen.queryByTestId('placeholder-seasons')).not.toBeInTheDocument();
    expect(screen.queryByTestId('placeholder-cast')).not.toBeInTheDocument();
    expect(screen.queryByTestId('placeholder-recommendations')).not.toBeInTheDocument();
    // Torrents placeholder is gone — K-1 mounts the real TorrentsSection.
    expect(screen.queryByTestId('placeholder-torrents')).not.toBeInTheDocument();
    // Legacy surfaces removed
    expect(screen.queryByTestId('library-status-card')).not.toBeInTheDocument();
    expect(screen.queryByTestId('cast-carousel')).not.toBeInTheDocument();
  });

  it('renders sections in v2 order', async () => {
    mockApi.mockResolvedValue(fullFixture);
    renderRoute('/series/122');
    await waitFor(() => expect(screen.getByTestId('series-hero')).toBeInTheDocument());
    const order = ['series-hero', 'overview-section',
                   'seasons-accordion', 'recommendations-carousel', 'external-links-footer'];
    const elements = order.map(id => screen.getByTestId(id) as HTMLElement);
    for (let i = 1; i < elements.length; i++) {
      const prev = elements[i - 1] as Node;
      const curr = elements[i] as Node;
      expect(prev.compareDocumentPosition(curr))
        .toBe(Node.DOCUMENT_POSITION_FOLLOWING);
    }
  });

  it('renders the Sonarr-only state with no TMDB blocks', async () => {
    mockApi.mockResolvedValue(sonarrOnlyFixture);
    renderRoute('/series/122');
    await waitFor(() => expect(screen.getByTestId('series-hero')).toBeInTheDocument());
    expect(screen.getByTestId('series-hero').getAttribute('data-sonarr-only')).toBe('true');
    expect(screen.queryByTestId('hero-backdrop')).not.toBeInTheDocument();
    expect(screen.queryByTestId('rating-tmdb')).not.toBeInTheDocument();
    expect(screen.queryByTestId('rating-imdb')).not.toBeInTheDocument();
    expect(screen.queryByTestId('hero-action-trailer')).not.toBeInTheDocument();
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
  const baseDegraded = {
    instance: 'homelab',
    series_id: 42,
    sonarr_series_id: 122,
    in_library_instances: ['homelab'],
    synced_at: new Date().toISOString(),
    // No imdb_rating set ⇒ omdb degraded triggers IMDb loading slot.
    hero: {
      title: 'Cold Series',
      status: 'continuing',
      year_start: 2020,
      tmdb_rating: { score: 7.5, votes: 100 },
    },
    library: { episodes_on_disk: 0, episodes_total: 0, missing_count: 0, size_on_disk_bytes: 0 },
    overview: { overview: '', language: 'en-US' },
    cast: [],
    seasons: [],
    recommendations: [],
  } as const;

  beforeEach(() => {
    mockApi.mockReset();
  });

  it('shows overview loading copy + skeleton when tmdb_series is degraded', async () => {
    mockApi.mockResolvedValue({ ...baseDegraded, degraded: ['tmdb_series'] });
    renderRoute('/series/122');
    await waitFor(() => expect(screen.getByTestId('overview-text')).toBeInTheDocument());
    expect(screen.getByTestId('overview-text').textContent).toMatch(/Loading description/i);
    expect(screen.getByTestId('overview-skeleton')).toBeInTheDocument();
  });

  it('shows season skeleton rows when tmdb_season is degraded', async () => {
    mockApi.mockResolvedValue({ ...baseDegraded, degraded: ['tmdb_season'] });
    renderRoute('/series/122');
    await waitFor(() => expect(screen.getByTestId('seasons-accordion')).toBeInTheDocument());
    expect(screen.getByTestId('seasons-loading-label')).toBeInTheDocument();
    expect(screen.getAllByTestId('seasons-skeleton-row')).toHaveLength(5);
  });

  it('shows cast strip loading skeletons when tmdb_person is degraded', async () => {
    mockApi.mockResolvedValue({ ...baseDegraded, degraded: ['tmdb_person'] });
    renderRoute('/series/122');
    await waitFor(() => expect(screen.getByTestId('cast-strip-loading')).toBeInTheDocument());
    expect(screen.getAllByTestId('cast-skeleton-avatar')).toHaveLength(8);
  });

  it('shows IMDb loading chip in hero when omdb is degraded and rating is missing', async () => {
    mockApi.mockResolvedValue({ ...baseDegraded, degraded: ['omdb'] });
    renderRoute('/series/122');
    await waitFor(() => expect(screen.getByTestId('imdb-rating-loading')).toBeInTheDocument());
  });

  it('shows backdrop loading plate when tmdb_series is degraded and no backdrop is present', async () => {
    // hero has no backdrop_asset → MonogramFallback path; tmdb_series
    // degraded ⇒ thin loading plate overlay rendered inside the fallback.
    mockApi.mockResolvedValue({ ...baseDegraded, degraded: ['tmdb_series'] });
    renderRoute('/series/122');
    await waitFor(() => expect(screen.getByTestId('monogram-loading-plate')).toBeInTheDocument());
  });

  it('shows recommendations skeleton tiles when tmdb_series is degraded and list is empty', async () => {
    mockApi.mockResolvedValue({ ...baseDegraded, degraded: ['tmdb_series'] });
    renderRoute('/series/122');
    await waitFor(() => expect(screen.getByTestId('recommendations-carousel-loading')).toBeInTheDocument());
    expect(screen.getAllByTestId('recommendations-skeleton-tile')).toHaveLength(6);
  });
});

describe('URL migration (story 495 / N-1e)', () => {
  beforeEach(() => mockApi.mockReset());

  it('renders page from global URL `/series/:id`', async () => {
    mockApi.mockResolvedValue(fullFixture);
    renderRoute('/series/122');
    await waitFor(() => expect(screen.getByTestId('series-hero')).toBeInTheDocument());
    expect(screen.getByTestId('hero-title')).toHaveTextContent('For All Mankind');
  });

  it('cast-strip view-all link is instance-less', async () => {
    mockApi.mockResolvedValue(fullFixture);
    renderRoute('/series/122');
    await waitFor(() => expect(screen.getByTestId('cast-strip-view-all')).toBeInTheDocument());
    expect(screen.getByTestId('cast-strip-view-all').getAttribute('href')).toBe('/series/122/cast');
  });
});
