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
                <Route path="/series/:instance/:id/cast" element={<SeriesCast />} />
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
  cast: [
    { person_id: 1, tmdb_id: 100, name: 'Pedro Pascal', character_name: 'Joel Miller', episode_count: 30, credit_order: 0 },
    { person_id: 2, tmdb_id: 200, name: 'Bella Ramsey', character_name: 'Ellie', episode_count: 30, credit_order: 1 },
  ],
  crew: [
    { person_id: 10, tmdb_id: 1010, name: 'Jane Director', job: 'Director', department: 'Directing', episode_count: 5 },
  ],
};

describe('<SeriesCast />', () => {
  beforeEach(() => mockApi.mockReset());

  it('renders skeleton while loading', () => {
    mockApi.mockReturnValueOnce(new Promise(() => {}));
    renderRoute('/series/alpha/42/cast');
    expect(screen.getByTestId('cast-page-skeleton')).toBeInTheDocument();
  });

  it('renders both tabs after data loads', async () => {
    mockApi.mockResolvedValueOnce(fullFixture);
    renderRoute('/series/alpha/42/cast');
    await waitFor(() => expect(screen.getByTestId('cast-tabs-list')).toBeInTheDocument());
    expect(screen.getByTestId('cast-tab-cast')).toHaveTextContent('Cast (2)');
    expect(screen.getByTestId('cast-tab-crew')).toHaveTextContent('Crew (1)');
  });

  it('filters both tabs from the search input and updates counts', async () => {
    mockApi.mockResolvedValueOnce(fullFixture);
    renderRoute('/series/alpha/42/cast');
    await waitFor(() => expect(screen.getByTestId('cast-search')).toBeInTheDocument());
    fireEvent.change(screen.getByTestId('cast-search'), { target: { value: 'Pedro' } });
    expect(screen.getByTestId('cast-tab-cast')).toHaveTextContent('Cast (1 / 2)');
    expect(screen.getByTestId('cast-tab-crew')).toHaveTextContent('Crew (0 / 1)');
  });

  it('shows the empty-search callout with a Clear button', async () => {
    mockApi.mockResolvedValueOnce(fullFixture);
    renderRoute('/series/alpha/42/cast');
    await waitFor(() => expect(screen.getByTestId('cast-search')).toBeInTheDocument());
    fireEvent.change(screen.getByTestId('cast-search'), { target: { value: 'zzzzz' } });
    expect(screen.getByTestId('cast-search-empty')).toBeInTheDocument();
  });

  it('renders the page-level empty callout when both lists are empty', async () => {
    mockApi.mockResolvedValueOnce({ instance: 'alpha', series_id: 42, cast: [], crew: [] });
    renderRoute('/series/alpha/42/cast');
    await waitFor(() => expect(screen.getByTestId('cast-page-empty')).toBeInTheDocument());
    expect(screen.queryByTestId('cast-tabs-list')).toBeNull();
  });

  it('renders the error alert on a failed fetch', async () => {
    mockApi.mockRejectedValueOnce(new Error('boom'));
    renderRoute('/series/alpha/42/cast');
    await waitFor(() => expect(screen.getByTestId('cast-page-error')).toBeInTheDocument());
  });

  it('renders a back link to the series detail page', async () => {
    mockApi.mockResolvedValueOnce(fullFixture);
    renderRoute('/series/alpha/42/cast');
    const back = await screen.findByTestId('cast-page-back');
    expect(back.getAttribute('href')).toBe('/series/alpha/42');
  });

  it('renders an invalid-params error when the URL is malformed', () => {
    renderRoute('/series/alpha/not-a-number/cast');
    expect(screen.getByText(/Invalid series link/i)).toBeInTheDocument();
  });
});
