import { describe, it, expect, beforeEach, vi } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter } from 'react-router-dom';
import { I18nextProvider } from 'react-i18next';
import { TooltipProvider } from '@/components/ui/tooltip';
import i18n from '@/i18n';
import { SearchResults } from './SearchResults';

const mockApi = vi.fn();
vi.mock('@/lib/api', async () => {
  const a = await vi.importActual<typeof import('@/lib/api')>('@/lib/api');
  return { ...a, api: (p: string) => mockApi(p) };
});

function renderResults(q: string) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0, staleTime: 0 } },
  });
  return render(
    <QueryClientProvider client={qc}>
      <I18nextProvider i18n={i18n}>
        <TooltipProvider delayDuration={0}>
          <MemoryRouter><SearchResults q={q} /></MemoryRouter>
        </TooltipProvider>
      </I18nextProvider>
    </QueryClientProvider>,
  );
}

const sample = {
  items: [
    { series_id: 71, tmdb_id: 5, title: 'Fargo', year: 2014,
      poster_path: '/f.jpg', in_library_instances: [] },
  ],
};

beforeEach(() => mockApi.mockReset());

describe('<SearchResults />', () => {
  it('renders nothing when q is shorter than 2 chars', () => {
    const { container } = renderResults('a');
    expect(container.firstChild).toBeNull();
    expect(mockApi).not.toHaveBeenCalled();
  });

  it('renders skeleton then grid for a valid query', async () => {
    let resolve: (v: typeof sample) => void = () => {};
    mockApi.mockReturnValueOnce(new Promise<typeof sample>((r) => { resolve = r; }));
    renderResults('fargo');
    expect(screen.getByTestId('discovery-search-skeleton')).toBeInTheDocument();
    resolve(sample);
    await waitFor(() =>
      expect(screen.getByTestId('discovery-search-grid')).toBeInTheDocument());
    expect(screen.getByText('Fargo')).toBeInTheDocument();
  });

  it('renders error alert on failure', async () => {
    mockApi.mockRejectedValueOnce(new Error('boom'));
    renderResults('fargo');
    await waitFor(() =>
      expect(screen.getByTestId('discovery-search-error')).toBeInTheDocument());
  });

  it('renders no_results empty state when items is empty', async () => {
    mockApi.mockResolvedValueOnce({ items: [] });
    renderResults('xyzzy');
    await waitFor(() => {
      expect(screen.queryByTestId('discovery-search-grid')).toBeNull();
      expect(screen.queryByTestId('discovery-search-skeleton')).toBeNull();
    });
    // Heading uses the interpolated query — assert that the query is
    // surfaced verbatim somewhere in the heading text.
    const heading = screen.getByRole('heading', { level: 3 });
    expect(heading.textContent).toContain('xyzzy');
  });
});
