import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { I18nextProvider } from 'react-i18next';
import { MemoryRouter } from 'react-router-dom';
import i18n from '@/i18n';
import { RecommendationsCarousel } from './RecommendationsCarousel';

const mockApi = vi.fn();
vi.mock('@/lib/api', async () => {
  const actual = await vi.importActual<typeof import('@/lib/api')>('@/lib/api');
  return { ...actual, api: (path: string) => mockApi(path) };
});

// useIsSectionVisible is re-exported from seriesRecommendations.ts;
// stub it directly on the seriesTorrents module (the source).
const visibleRef = { value: true };
vi.mock('@/api/seriesTorrents', async () => {
  const actual = await vi.importActual<typeof import('@/api/seriesTorrents')>('@/api/seriesTorrents');
  return { ...actual, useIsSectionVisible: () => visibleRef.value };
});

function wrap(node: React.ReactElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
  return render(
    <QueryClientProvider client={qc}>
      <I18nextProvider i18n={i18n}>
        <MemoryRouter>{node}</MemoryRouter>
      </I18nextProvider>
    </QueryClientProvider>,
  );
}

const payload = {
  instance: 'alpha', sonarr_series_id: 1, series_id: 140,
  items: [
    { series_id: 1, title: 'Show A', year: 2022, tmdb_rating: 8.1, poster_asset: 'a',
      in_library: true, instance_name: 'alpha', sonarr_series_id: 11 },
    { series_id: 2, title: 'Show B', year: 2021, tmdb_rating: 7.6, poster_asset: 'b',
      in_library: false },
  ],
  total_count: 2, has_more: false, limit: 20, offset: 0, degraded: [],
};

describe('<RecommendationsCarousel /> (530)', () => {
  beforeEach(() => {
    mockApi.mockReset();
    visibleRef.value = true;
  });

  it('does NOT fetch when section is off-screen', async () => {
    visibleRef.value = false;
    wrap(<RecommendationsCarousel seriesId={140} />);
    // Allow microtask flush.
    await new Promise((r) => setTimeout(r, 0));
    expect(mockApi).not.toHaveBeenCalled();
    expect(screen.getByTestId('recommendations-carousel-sentinel')).toBeInTheDocument();
  });

  it('fetches and renders unified SeriesCards once visible', async () => {
    mockApi.mockResolvedValueOnce(payload);
    wrap(<RecommendationsCarousel seriesId={140} />);
    // Story 565 (B-recs-lang) — carousel forwards the resolved language as ?lang=.
    // Task #1020-B: the value is routed through toBcp47(), so when present it is
    // the canonical BCP-47 tag (e.g. en-US / ru-RU), never a bare short code.
    await waitFor(() =>
      expect(mockApi).toHaveBeenCalledWith(
        expect.stringMatching(/^\/series\/140\/recommendations\?limit=20&offset=0(&lang=[a-z]{2}-[A-Z]{2})?$/),
      ),
    );
    await waitFor(() => expect(screen.getByTestId('recommendations-carousel')).toBeInTheDocument());
    expect(screen.getAllByTestId('series-card')).toHaveLength(2);
  });

  it('renders ★ tmdb_rating on each card', async () => {
    mockApi.mockResolvedValueOnce(payload);
    wrap(<RecommendationsCarousel seriesId={140} />);
    await waitFor(() => expect(screen.getAllByTestId('series-card-rating')).toHaveLength(2));
    expect(screen.getByText('8.1')).toBeInTheDocument();
    expect(screen.getByText('7.6')).toBeInTheDocument();
  });

  it('shows the in-library badge only for in-library recs', async () => {
    mockApi.mockResolvedValueOnce(payload);
    wrap(<RecommendationsCarousel seriesId={140} />);
    // Show A is in_library:true, Show B is in_library:false → exactly one badge.
    await waitFor(() => expect(screen.getAllByTestId('series-card')).toHaveLength(2));
    expect(screen.getAllByTestId('series-card-library-badge')).toHaveLength(1);
  });

  it('routes internally: recs with a canon series_id are anchors to /series/:id (in-library AND out-of-library)', async () => {
    mockApi.mockResolvedValueOnce(payload);
    wrap(<RecommendationsCarousel seriesId={140} />);
    await waitFor(() => expect(screen.getAllByTestId('series-card')).toHaveLength(2));
    const cards = screen.getAllByTestId('series-card') as HTMLAnchorElement[];
    expect(cards.map((c) => c.getAttribute('href'))).toEqual(['/series/1', '/series/2']);
  });

  it('out-of-library rec with valid series_id still routes internally and shows no in-library badge', async () => {
    mockApi.mockResolvedValueOnce({
      ...payload,
      items: [
        { series_id: 99, title: 'Out Of Lib', year: 2020, tmdb_rating: 7.0, poster_asset: 'x',
          in_library: false },
      ],
    });
    wrap(<RecommendationsCarousel seriesId={140} />);
    await waitFor(() => expect(screen.getByTestId('series-card')).toBeInTheDocument());
    const card = screen.getByTestId('series-card') as HTMLAnchorElement;
    expect(card.getAttribute('href')).toBe('/series/99');
    expect(screen.queryByTestId('series-card-library-badge')).toBeNull();
  });

  it('rec without a canon series_id falls back to tmdb_series_id resolve-nav (routes internally)', async () => {
    mockApi.mockResolvedValueOnce({
      ...payload,
      items: [
        { series_id: 0, tmdb_series_id: 555, title: 'Tmdb Only', year: 2020, tmdb_rating: 6.5,
          poster_asset: 'y', in_library: false },
      ],
    });
    wrap(<RecommendationsCarousel seriesId={140} />);
    await waitFor(() => expect(screen.getByTestId('series-card')).toBeInTheDocument());
    const card = screen.getByTestId('series-card');
    // No canon id → SeriesCard renders the resolve-nav button (not a plain anchor).
    expect(card.tagName).not.toBe('A');
    expect(card.getAttribute('data-tmdb-id')).toBe('555');
  });

  it('renders skeleton + loading label when tmdbSeriesLoading=true and items=[]', async () => {
    mockApi.mockResolvedValueOnce({ ...payload, items: [] });
    wrap(<RecommendationsCarousel seriesId={140} tmdbSeriesLoading />);
    await waitFor(() => expect(screen.getByTestId('recommendations-carousel-loading')).toBeInTheDocument());
    expect(screen.getAllByTestId('recommendations-skeleton-tile')).toHaveLength(6);
  });

  it('returns null when items=[] and not loading and visible', async () => {
    mockApi.mockResolvedValueOnce({ ...payload, items: [] });
    const { container } = wrap(<RecommendationsCarousel seriesId={140} />);
    await waitFor(() => expect(mockApi).toHaveBeenCalled());
    // After data arrives, items=[] and tmdbSeriesLoading=false → null.
    await waitFor(() => expect(container.firstChild).toBeNull());
  });
});
