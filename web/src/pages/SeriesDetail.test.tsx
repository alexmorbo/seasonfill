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
                <Route path="/series/:instance/:id" element={<SeriesDetail />} />
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
};

const sonarrOnlyFixture = {
  instance: 'homelab',
  series_id: 42,
  sonarr_series_id: 122,
  synced_at: new Date().toISOString(),
  degraded: ['tmdb', 'omdb'],
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
    renderRoute('/series/homelab/122');
    expect(await screen.findByTestId('series-detail-skeleton')).toBeInTheDocument();
    // Resolve the in-flight query so test teardown does not hang on the
    // dangling promise.
    resolveDetail?.(fullFixture);
    await waitFor(() => expect(screen.queryByTestId('series-detail-skeleton')).not.toBeInTheDocument());
  });

  it('renders the full hero, ratings, library and external links on success', async () => {
    mockApi.mockResolvedValue(fullFixture);
    renderRoute('/series/homelab/122');
    await waitFor(() => expect(screen.getByTestId('series-hero')).toBeInTheDocument());
    expect(screen.getByTestId('hero-title')).toHaveTextContent('For All Mankind');
    expect(screen.getByTestId('rating-tmdb')).toBeInTheDocument();
    expect(screen.getByTestId('rating-imdb')).toBeInTheDocument();
    expect(screen.getByTestId('library-status-card')).toBeInTheDocument();
    expect(screen.getByTestId('library-missing-chip')).toBeInTheDocument();
    expect(screen.getByTestId('external-links-footer')).toBeInTheDocument();
  });

  it('renders the Sonarr-only state with no TMDB blocks', async () => {
    mockApi.mockResolvedValue(sonarrOnlyFixture);
    renderRoute('/series/homelab/122');
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
    renderRoute('/series/homelab/122');
    await waitFor(() => expect(screen.getByTestId('series-detail-error')).toBeInTheDocument());
  });

  it('renders the invalid-params alert when the id is NaN', () => {
    // The hook is disabled when seriesId is NaN; api is never invoked.
    mockApi.mockResolvedValue(undefined);
    renderRoute('/series/homelab/notanumber');
    expect(screen.queryByTestId('series-detail-skeleton')).not.toBeInTheDocument();
    expect(screen.getByText(/Invalid series link/)).toBeInTheDocument();
  });
});
