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

  it('fetches and renders cards once visible', async () => {
    mockApi.mockResolvedValueOnce(payload);
    wrap(<RecommendationsCarousel seriesId={140} />);
    // Story 565 (B-recs-lang) — carousel now forwards i18n.resolvedLanguage as ?lang=.
    await waitFor(() =>
      expect(mockApi).toHaveBeenCalledWith(
        expect.stringMatching(/^\/series\/140\/recommendations\?limit=20&offset=0(&lang=[A-Za-z-]+)?$/),
      ),
    );
    await waitFor(() => expect(screen.getByTestId('recommendations-carousel')).toBeInTheDocument());
    expect(screen.getAllByTestId('recommendation-card')).toHaveLength(2);
  });

  it('wraps ALL recs with valid series_id in a Link (542 — in-library AND out-of-library both navigate)', async () => {
    mockApi.mockResolvedValueOnce(payload);
    wrap(<RecommendationsCarousel seriesId={140} />);
    await waitFor(() => expect(screen.getAllByTestId('recommendation-link')).toHaveLength(2));
    const links = screen.getAllByTestId('recommendation-link') as HTMLAnchorElement[];
    expect(links.map((l) => l.getAttribute('href'))).toEqual(['/series/1', '/series/2']);
  });

  it('out-of-library rec with valid series_id is a Link AND still shows the "Add to Sonarr" hover overlay (542)', async () => {
    mockApi.mockResolvedValueOnce({
      ...payload,
      items: [
        { series_id: 99, title: 'Out Of Lib', year: 2020, tmdb_rating: 7.0, poster_asset: 'x',
          in_library: false },
      ],
    });
    wrap(<RecommendationsCarousel seriesId={140} />);
    await waitFor(() => expect(screen.getByTestId('recommendation-link')).toBeInTheDocument());
    const link = screen.getByTestId('recommendation-link') as HTMLAnchorElement;
    expect(link.getAttribute('href')).toBe('/series/99');
    // The hover-CTA overlay is rendered (visibility gated by group-hover CSS, but presence is asserted).
    expect(screen.getByTestId('recommendation-add-overlay')).toBeInTheDocument();
  });

  it('rec with invalid series_id (0) is rendered as a non-Link div (542 regression)', async () => {
    mockApi.mockResolvedValueOnce({
      ...payload,
      items: [
        { series_id: 0, title: 'Broken Row', year: 2020, tmdb_rating: 0, poster_asset: '',
          in_library: false },
      ],
    });
    wrap(<RecommendationsCarousel seriesId={140} />);
    await waitFor(() => expect(screen.getByTestId('recommendation-card')).toBeInTheDocument());
    expect(screen.queryByTestId('recommendation-link')).toBeNull();
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
