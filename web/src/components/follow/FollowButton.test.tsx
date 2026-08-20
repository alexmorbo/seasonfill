import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import i18n from '@/i18n';
import { FollowButton } from './FollowButton';

const mockApi = vi.fn();
vi.mock('@/lib/api', async () => {
  const actual = await vi.importActual<typeof import('@/lib/api')>('@/lib/api');
  return {
    ...actual,
    api: (...args: unknown[]) => mockApi(...args),
  };
});

vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn() } }));

function renderWithProviders(ui: React.ReactElement) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  });
  return render(
    <I18nextProvider i18n={i18n}>
      <QueryClientProvider client={qc}>{ui}</QueryClientProvider>
    </I18nextProvider>,
  );
}

describe('<FollowButton /> — series (default mediaType, regression)', () => {
  beforeEach(() => mockApi.mockReset());

  it('renders follow-button-<seriesId> and POSTs /follow on click when not followed', async () => {
    mockApi.mockResolvedValueOnce({ items: [] }); // GET /follow
    mockApi.mockResolvedValueOnce(undefined); // POST /follow
    renderWithProviders(<FollowButton seriesId={140} />);

    const btn = await screen.findByTestId('follow-button-140');
    await waitFor(() => expect(mockApi).toHaveBeenCalledWith('/follow'));
    expect(btn).toHaveAttribute('aria-pressed', 'false');

    fireEvent.click(btn);

    await waitFor(() =>
      expect(mockApi).toHaveBeenCalledWith('/follow', {
        method: 'POST',
        body: { series_id: 140 },
      }),
    );
  });

  it('only fetches the series watchlist, never the movie one', async () => {
    mockApi.mockResolvedValueOnce({ items: [] });
    renderWithProviders(<FollowButton seriesId={140} />);

    await screen.findByTestId('follow-button-140');
    await waitFor(() => expect(mockApi).toHaveBeenCalledWith('/follow'));
    expect(mockApi).not.toHaveBeenCalledWith('/follow/movies');
  });
});

describe('<FollowButton /> — movie', () => {
  beforeEach(() => mockApi.mockReset());

  it('renders follow-button-movie-<tmdbId> and POSTs /follow/movies on click when not followed', async () => {
    mockApi.mockResolvedValueOnce({ items: [] }); // GET /follow/movies
    mockApi.mockResolvedValueOnce(undefined); // POST /follow/movies
    renderWithProviders(<FollowButton mediaType="movie" tmdbId={550} />);

    const btn = await screen.findByTestId('follow-button-movie-550');
    await waitFor(() => expect(mockApi).toHaveBeenCalledWith('/follow/movies'));
    expect(btn).toHaveAttribute('aria-pressed', 'false');

    fireEvent.click(btn);

    await waitFor(() =>
      expect(mockApi).toHaveBeenCalledWith('/follow/movies', {
        method: 'POST',
        body: { tmdb_id: 550 },
      }),
    );
  });

  it('shows aria-pressed=true and DELETEs /follow/movies/:tmdb_id when already followed', async () => {
    mockApi.mockResolvedValueOnce({ items: [{ tmdb_id: 550, title: 'Fight Club' }] });
    mockApi.mockResolvedValueOnce(undefined); // DELETE

    renderWithProviders(<FollowButton mediaType="movie" tmdbId={550} />);

    const btn = await screen.findByTestId('follow-button-movie-550');
    await waitFor(() => expect(btn).toHaveAttribute('aria-pressed', 'true'));

    fireEvent.click(btn);

    await waitFor(() =>
      expect(mockApi).toHaveBeenCalledWith('/follow/movies/550', { method: 'DELETE' }),
    );
  });

  it('compact variant renders icon-only (no label span)', async () => {
    mockApi.mockResolvedValueOnce({ items: [] });
    renderWithProviders(<FollowButton mediaType="movie" tmdbId={550} variant="compact" />);

    const btn = await screen.findByTestId('follow-button-movie-550');
    expect(btn.querySelector('span')).toBeNull();
  });
});
