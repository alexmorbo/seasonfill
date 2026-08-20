import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, fireEvent } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { TooltipProvider } from '@/components/ui/tooltip';
import i18n from '@/i18n';
import { PageTitleProvider } from '@/components/shell/page-title-context';
import { MovieCast } from './MovieCast';

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
                <Route path="/movies/:tmdbId/cast" element={<MovieCast />} />
              </Routes>
            </MemoryRouter>
          </TooltipProvider>
        </QueryClientProvider>
      </I18nextProvider>
    </PageTitleProvider>,
  );
}

const movieFixture = {
  tmdb_id: 438631,
  title: 'Dune',
  poster: 'poster-hash',
  year: 2021,
};

// dto.MovieCastMember fixtures — no crew, no series_summary, no
// total_episode_count on this DTO (movie cast is a flat list).
const chalamet = { person_id: 1, tmdb_id: 100, name: 'Timothée Chalamet', character_name: 'Paul Atreides', credit_order: 0 };
const ferguson = { person_id: 2, tmdb_id: 200, name: 'Rebecca Ferguson', character_name: 'Lady Jessica', credit_order: 1 };
const bardem = { person_id: 3, tmdb_id: 300, name: 'Javier Bardem', character_name: 'Stilgar', credit_order: 2 };

function fullCastList() {
  // 12 members — larger than MediaCastStrip's 8-item preview cap, to prove
  // this page renders the FULL list unlike the detail-page strip.
  return Array.from({ length: 12 }).map((_, i) => ({
    person_id: i + 1,
    tmdb_id: 1000 + i,
    name: `Actor ${i}`,
    character_name: `Role ${i}`,
    credit_order: i,
  }));
}

function mockRoutedApi(cast: readonly unknown[]) {
  mockApi.mockImplementation((path: string) => {
    const p = path ?? '';
    if (p.includes('/cast')) {
      return Promise.resolve({
        tmdb_id: 438631,
        served_language: 'en',
        cast,
      });
    }
    return Promise.resolve(movieFixture);
  });
}

describe('<MovieCast />', () => {
  beforeEach(() => mockApi.mockReset());

  it('renders the full cast list (not capped at 8 like the detail-page strip)', async () => {
    mockRoutedApi(fullCastList());
    renderRoute('/movies/438631/cast');
    await waitFor(() => expect(screen.getByTestId('cast-grid')).toBeInTheDocument());
    expect(screen.getAllByTestId('cast-grid-card')).toHaveLength(12);
  });

  it('renders the hero with the movie title and cast count', async () => {
    mockRoutedApi(fullCastList());
    renderRoute('/movies/438631/cast');
    await waitFor(() => expect(screen.getByTestId('cast-grid')).toBeInTheDocument());
    expect(screen.getByTestId('cast-page-title')).toHaveTextContent('Dune');
    expect(screen.getByTestId('cast-compact-hero')).toHaveTextContent('2021');
    expect(screen.getByTestId('cast-counts')).toHaveTextContent('12 cast members');
    // Movies have no crew list on this DTO — the "· N crew" segment must
    // never render (CompactHero's optional-prop behavior).
    expect(screen.getByTestId('cast-counts').textContent).not.toContain('crew');
    expect(screen.queryByTestId('status-pill')).toBeNull();
  });

  it('refetches with sort=name from the dropdown and renders the server order', async () => {
    mockApi.mockImplementation((path: string) => {
      const p = path ?? '';
      if (p.includes('/cast')) {
        const cast = p.includes('sort=name') ? [ferguson, bardem, chalamet] : [chalamet, ferguson, bardem];
        return Promise.resolve({ tmdb_id: 438631, served_language: 'en', cast });
      }
      return Promise.resolve(movieFixture);
    });
    renderRoute('/movies/438631/cast');
    await waitFor(() => expect(screen.getByTestId('cast-grid')).toBeInTheDocument());
    const order = () => screen.getAllByTestId('cast-grid-card').map((c) => c.getAttribute('data-tmdb-id') ?? '');
    expect(order()).toEqual(['100', '200', '300']);

    fireEvent.change(screen.getByTestId('cast-sort'), { target: { value: 'name' } });
    await waitFor(() => expect(order()).toEqual(['200', '300', '100']));
    expect(mockApi).toHaveBeenCalledWith(expect.stringContaining('sort=name'));
  });

  it('renders the page-level empty callout when the cast list is empty', async () => {
    mockApi.mockImplementation((path: string) => {
      const p = path ?? '';
      if (p.includes('/cast')) {
        return Promise.resolve({ tmdb_id: 438631, served_language: 'en', cast: [] });
      }
      return Promise.resolve(movieFixture);
    });
    renderRoute('/movies/438631/cast');
    await waitFor(() => expect(screen.getByTestId('cast-page-empty')).toBeInTheDocument());
    expect(screen.queryByTestId('cast-grid')).toBeNull();
  });

  it('filters the grid from the search input', async () => {
    mockRoutedApi([chalamet, ferguson, bardem]);
    renderRoute('/movies/438631/cast');
    await waitFor(() => expect(screen.getByTestId('cast-grid')).toBeInTheDocument());
    fireEvent.change(screen.getByTestId('cast-search'), { target: { value: 'Rebecca' } });
    await waitFor(() => expect(screen.getAllByTestId('cast-grid-card')).toHaveLength(1));
    expect(screen.getByText('Rebecca Ferguson')).toBeInTheDocument();
  });

  it('shows the empty-search callout with a Clear button', async () => {
    mockRoutedApi([chalamet, ferguson, bardem]);
    renderRoute('/movies/438631/cast');
    await waitFor(() => expect(screen.getByTestId('cast-grid')).toBeInTheDocument());
    fireEvent.change(screen.getByTestId('cast-search'), { target: { value: 'zzzzz' } });
    expect(screen.getByTestId('cast-search-empty')).toBeInTheDocument();
  });

  it('renders the error alert on a failed fetch', async () => {
    mockApi.mockImplementation((path: string) => {
      const p = path ?? '';
      if (p.includes('/cast')) return Promise.reject(new Error('boom'));
      return Promise.resolve(movieFixture);
    });
    renderRoute('/movies/438631/cast');
    await waitFor(() => expect(screen.getByTestId('cast-page-error')).toBeInTheDocument());
  });

  it('renders a back link to the movie detail page', async () => {
    mockRoutedApi(fullCastList());
    renderRoute('/movies/438631/cast');
    const back = await screen.findByTestId('cast-page-back');
    expect(back.getAttribute('href')).toBe('/movies/438631');
  });

  it('renders an invalid-params error when the URL is malformed', () => {
    renderRoute('/movies/not-a-number/cast');
    expect(screen.getByTestId('movie-cast-invalid')).toBeInTheDocument();
  });
});
