import { describe, it, expect, beforeEach, vi } from 'vitest';
import { render, screen, waitFor, fireEvent, act } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter } from 'react-router-dom';
import { I18nextProvider } from 'react-i18next';
import { TooltipProvider } from '@/components/ui/tooltip';
import i18n from '@/i18n';
import { FilteredResults } from './FilteredResults';

const mockApi = vi.fn();
vi.mock('@/lib/api', async () => {
  const a = await vi.importActual<typeof import('@/lib/api')>('@/lib/api');
  return { ...a, api: (p: string) => mockApi(p) };
});

function renderResults(props: {
  filter?: Record<string, unknown>;
  hasActiveFilter?: boolean;
}) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0, staleTime: 0 } },
  });
  return render(
    <QueryClientProvider client={qc}>
      <I18nextProvider i18n={i18n}>
        <TooltipProvider delayDuration={0}>
          <MemoryRouter>
            <FilteredResults
              filter={(props.filter ?? {}) as never}
              hasActiveFilter={props.hasActiveFilter ?? false}
            />
          </MemoryRouter>
        </TooltipProvider>
      </I18nextProvider>
    </QueryClientProvider>,
  );
}

const sample = {
  items: [
    { series_id: 91, tmdb_id: 9, title: 'Fargo', year: 2014,
      poster_path: '/f.jpg', in_library_instances: [] },
  ],
};

beforeEach(() => mockApi.mockReset());

describe('<FilteredResults />', () => {
  it('renders the prompt empty-state when no filter is active', () => {
    renderResults({ hasActiveFilter: false });
    expect(screen.queryByTestId('discovery-filtered-skeleton')).toBeNull();
    // EmptyState renders the prompt title.
    expect(screen.getByRole('heading', { level: 3 })).toHaveTextContent(
      'Pick some filters to start',
    );
    expect(mockApi).not.toHaveBeenCalled();
  });

  it('renders skeleton then grid when active', async () => {
    mockApi.mockResolvedValueOnce(sample);
    renderResults({ filter: { with_genres: [18] }, hasActiveFilter: true });
    expect(screen.getByTestId('discovery-filtered-skeleton')).toBeInTheDocument();
    await waitFor(() =>
      expect(screen.getByTestId('discovery-filtered-grid')).toBeInTheDocument());
    expect(screen.getByText('Fargo')).toBeInTheDocument();
    expect(screen.getByTestId('discovery-filtered-load-more')).toBeInTheDocument();
  });

  it('renders error alert on failure', async () => {
    mockApi.mockRejectedValueOnce(new Error('boom'));
    renderResults({ filter: { with_genres: [18] }, hasActiveFilter: true });
    await waitFor(() =>
      expect(screen.getByTestId('discovery-filtered-error')).toBeInTheDocument());
  });

  it('renders empty state when active filter yields no items', async () => {
    mockApi.mockResolvedValueOnce({ items: [] });
    renderResults({ filter: { with_genres: [18] }, hasActiveFilter: true });
    await waitFor(() => {
      expect(screen.queryByTestId('discovery-filtered-skeleton')).toBeNull();
      expect(screen.queryByTestId('discovery-filtered-grid')).toBeNull();
    });
    // EmptyState heading renders the Filter tab title.
    expect(screen.getByRole('heading', { level: 3 })).toBeInTheDocument();
  });

  it('Load more triggers a refetch with page=2', async () => {
    mockApi.mockResolvedValueOnce(sample);
    renderResults({ filter: { with_genres: [18] }, hasActiveFilter: true });
    await waitFor(() =>
      expect(screen.getByTestId('discovery-filtered-grid')).toBeInTheDocument());
    mockApi.mockResolvedValueOnce(sample);
    fireEvent.click(screen.getByTestId('discovery-filtered-load-more'));
    await waitFor(() => {
      const calls = mockApi.mock.calls.map((c) => String(c[0]));
      expect(calls.some((u) => u.includes('page=2'))).toBe(true);
    });
  });

  it('fetches with the active locale and refetches on locale switch', async () => {
    mockApi.mockResolvedValue(sample);
    renderResults({ filter: { with_genres: [18] }, hasActiveFilter: true });
    await waitFor(() =>
      expect(screen.getByTestId('discovery-filtered-grid')).toBeInTheDocument());
    expect(mockApi).toHaveBeenCalledWith(expect.stringContaining('lang=en-US'));
    try {
      await act(async () => { await i18n.changeLanguage('ru-RU'); });
      await waitFor(() =>
        expect(mockApi).toHaveBeenCalledWith(expect.stringContaining('lang=ru-RU')));
    } finally {
      await act(async () => { await i18n.changeLanguage('en-US'); });
    }
  });

  it('shows warming banner + skeleton on cold-start', async () => {
    mockApi.mockResolvedValueOnce({
      items: [], degraded: ['discovery_warming'],
      warming_estimate_seconds: 20,
    });
    renderResults({ filter: { with_genres: [18] }, hasActiveFilter: true });
    await waitFor(() =>
      expect(screen.getByTestId('discovery-filtered-warming')).toBeInTheDocument());
    expect(screen.getByTestId('discovery-warming-banner')).toHaveAttribute(
      'data-kind', 'cold_start',
    );
  });
});
