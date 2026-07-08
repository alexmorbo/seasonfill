import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, fireEvent } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { TooltipProvider } from '@/components/ui/tooltip';
import i18n from '@/i18n';
import { PageTitleProvider } from '@/components/shell/page-title-context';
import { SeriesCast } from './SeriesCast';

const mockApi = vi.fn();
vi.mock('@/lib/api', async () => {
  const actual = await vi.importActual<typeof import('@/lib/api')>('@/lib/api');
  return { ...actual, api: (path: string) => mockApi(path) };
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
                {/* Story 495 / N-1e: URL is global — `:instance` segment dropped. */}
                <Route path="/series/:id/cast" element={<SeriesCast />} />
              </Routes>
            </MemoryRouter>
          </TooltipProvider>
        </QueryClientProvider>
      </I18nextProvider>
    </PageTitleProvider>,
  );
}

const fullFixture = {
  instance: 'alpha',
  series_id: 42,
  sonarr_series_id: 1,
  synced_at: new Date().toISOString(),
  total_episode_count: 62,
  series_summary: {
    title: 'The Last of Us',
    poster_url: 'poster-hash',
    status: 'continuing',
    first_aired_year: 2023,
    last_aired_year: 2025,
  },
  cast: [
    { person_id: 1, tmdb_id: 100, name: 'Pedro Pascal', character_name: 'Joel Miller', episode_count: 30, credit_order: 0 },
    { person_id: 2, tmdb_id: 200, name: 'Bella Ramsey', character_name: 'Ellie', episode_count: 30, credit_order: 1 },
  ],
  crew: [
    { person_id: 10, tmdb_id: 1010, name: 'Jane Director', job: 'Director', department: 'Directing', episode_count: 5 },
  ],
};

// Story 1087b-1 — sorting is now server-side. The BE returns the cast already
// ordered per `?sort=`; the FE renders that order verbatim (no client re-sort).
// These three actors, ordered per each sort key the server would apply:
const zoe = { person_id: 1, tmdb_id: 100, name: 'Zoe Alpha', character_name: 'A', episode_count: 2, credit_order: 0 };
const amy = { person_id: 2, tmdb_id: 200, name: 'Amy Beta', character_name: 'B', episode_count: 9, credit_order: 1 };
const mike = { person_id: 3, tmdb_id: 300, name: 'Mike Gamma', character_name: 'C', episode_count: 5, credit_order: 2 };

const sortBase = {
  instance: 'alpha',
  series_id: 42,
  sonarr_series_id: 1,
  synced_at: new Date().toISOString(),
  total_episode_count: 62,
  series_summary: { title: 'X', status: 'continuing' },
  crew: [],
};

// Mock the BE sort: return the cast pre-ordered by whichever `sort=` the URL
// carries, mirroring the server contract the FE now depends on.
function mockServerSortedCast() {
  mockApi.mockImplementation((path: string) => {
    const p = path ?? '';
    let cast: unknown[];
    if (p.includes('sort=credit')) cast = [zoe, amy, mike]; // credit 0,1,2 → 100,200,300
    else if (p.includes('sort=name')) cast = [amy, mike, zoe]; // Amy,Mike,Zoe → 200,300,100
    else cast = [amy, mike, zoe]; // episodes DESC 9,5,2 → 200,300,100
    return Promise.resolve({ ...sortBase, cast });
  });
}

function tmdbOrder(): string[] {
  return screen.getAllByTestId('cast-grid-card').map((c) => c.getAttribute('data-tmdb-id') ?? '');
}

describe('<SeriesCast />', () => {
  beforeEach(() => mockApi.mockReset());

  it('defaults to the episodes sort and renders the server order', async () => {
    mockServerSortedCast();
    renderRoute('/series/42/cast');
    await waitFor(() => expect(screen.getByTestId('cast-grid')).toBeInTheDocument());
    expect(tmdbOrder()).toEqual(['200', '300', '100']); // server episodes order
    expect(mockApi).toHaveBeenCalledWith(expect.not.stringContaining('sort='));
  });

  it('refetches with sort=credit from the dropdown and renders the server order', async () => {
    mockServerSortedCast();
    renderRoute('/series/42/cast');
    await waitFor(() => expect(screen.getByTestId('cast-grid')).toBeInTheDocument());
    fireEvent.change(screen.getByTestId('cast-sort'), { target: { value: 'credit' } });
    await waitFor(() => expect(tmdbOrder()).toEqual(['100', '200', '300']));
    expect(mockApi).toHaveBeenCalledWith(expect.stringContaining('sort=credit'));
  });

  it('refetches with sort=name from the dropdown and renders the server order', async () => {
    mockServerSortedCast();
    renderRoute('/series/42/cast');
    await waitFor(() => expect(screen.getByTestId('cast-grid')).toBeInTheDocument());
    fireEvent.change(screen.getByTestId('cast-sort'), { target: { value: 'name' } });
    await waitFor(() => expect(tmdbOrder()).toEqual(['200', '300', '100'])); // server name order
    expect(mockApi).toHaveBeenCalledWith(expect.stringContaining('sort=name'));
  });

  it('renders skeleton while loading', () => {
    mockApi.mockReturnValueOnce(new Promise(() => {}));
    renderRoute('/series/42/cast');
    expect(screen.getByTestId('cast-page-skeleton')).toBeInTheDocument();
  });

  it('renders both tabs after data loads', async () => {
    mockApi.mockResolvedValueOnce(fullFixture);
    renderRoute('/series/42/cast');
    await waitFor(() => expect(screen.getByTestId('cast-tabs-list')).toBeInTheDocument());
    expect(screen.getByTestId('cast-tab-cast')).toHaveTextContent('Cast (2)');
    expect(screen.getByTestId('cast-tab-crew')).toHaveTextContent('Crew (1)');
  });

  it('filters both tabs from the search input and updates counts', async () => {
    mockApi.mockResolvedValueOnce(fullFixture);
    renderRoute('/series/42/cast');
    await waitFor(() => expect(screen.getByTestId('cast-search')).toBeInTheDocument());
    fireEvent.change(screen.getByTestId('cast-search'), { target: { value: 'Pedro' } });
    expect(screen.getByTestId('cast-tab-cast')).toHaveTextContent('Cast (1 / 2)');
    expect(screen.getByTestId('cast-tab-crew')).toHaveTextContent('Crew (0 / 1)');
  });

  it('shows the empty-search callout with a Clear button', async () => {
    mockApi.mockResolvedValueOnce(fullFixture);
    renderRoute('/series/42/cast');
    await waitFor(() => expect(screen.getByTestId('cast-search')).toBeInTheDocument());
    fireEvent.change(screen.getByTestId('cast-search'), { target: { value: 'zzzzz' } });
    expect(screen.getByTestId('cast-search-empty')).toBeInTheDocument();
  });

  it('renders the page-level empty callout when both lists are empty', async () => {
    mockApi.mockResolvedValueOnce({ instance: 'alpha', series_id: 42, cast: [], crew: [] });
    renderRoute('/series/42/cast');
    await waitFor(() => expect(screen.getByTestId('cast-page-empty')).toBeInTheDocument());
    expect(screen.queryByTestId('cast-tabs-list')).toBeNull();
  });

  it('renders the error alert on a failed fetch', async () => {
    mockApi.mockRejectedValueOnce(new Error('boom'));
    renderRoute('/series/42/cast');
    await waitFor(() => expect(screen.getByTestId('cast-page-error')).toBeInTheDocument());
  });

  it('renders a back link to the series detail page', async () => {
    mockApi.mockResolvedValueOnce(fullFixture);
    renderRoute('/series/42/cast');
    const back = await screen.findByTestId('cast-page-back');
    expect(back.getAttribute('href')).toBe('/series/42');
  });

  it('renders an invalid-params error when the URL is malformed', () => {
    renderRoute('/series/not-a-number/cast');
    expect(screen.getByText(/Invalid series link/i)).toBeInTheDocument();
  });

  it('renders the hero with title + year range from series_summary', async () => {
    mockApi.mockResolvedValueOnce(fullFixture);
    renderRoute('/series/42/cast');
    await waitFor(() => expect(screen.getByTestId('cast-tabs-list')).toBeInTheDocument());
    expect(screen.getByTestId('cast-page-title')).toHaveTextContent('The Last of Us');
    // Year-range text contains the en-dash (formatYearRange in CompactHero).
    expect(screen.getByTestId('cast-compact-hero')).toHaveTextContent('2023');
    expect(screen.getByTestId('cast-compact-hero')).toHaveTextContent('2025');
  });

  // Story 495 / N-1e (B-20): when the cast DTO carries `degraded: ['tmdb_person']`
  // AND both lists are empty, render skeleton tiles instead of the "no
  // data" fallback so operator can distinguish loading from empty.
  it('renders the cast-page loading skeleton when tmdb_person is degraded', async () => {
    mockApi.mockResolvedValueOnce({
      instance: 'alpha',
      series_id: 42,
      cast: [],
      crew: [],
      degraded: ['tmdb_person'],
    });
    renderRoute('/series/42/cast');
    await waitFor(() => expect(screen.getByTestId('cast-page-loading')).toBeInTheDocument());
    expect(screen.queryByTestId('cast-page-empty')).toBeNull();
    expect(screen.getAllByTestId('cast-page-skeleton-tile')).toHaveLength(10);
  });
});
