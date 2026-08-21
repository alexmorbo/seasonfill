import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { TooltipProvider } from '@/components/ui/tooltip';
import i18n from '@/i18n';
import { MovieTorrentsSection } from '../MovieTorrentsSection';

interface FakeSettingsResult {
  data: { enabled: boolean; url: string; username: string } | null;
  isPending: boolean;
  isFetched: boolean;
}
const qbitSettingsResult = vi.fn<() => FakeSettingsResult>(() => ({
  data: { enabled: true, url: 'http://qbit', username: 'u' },
  isPending: false,
  isFetched: true,
}));

vi.mock('@/api/qbit', () => ({
  useQbitSettings: () => qbitSettingsResult(),
}));

vi.mock('@/api/seriesTorrents', async () => {
  const actual = await vi.importActual<typeof import('@/api/seriesTorrents')>('@/api/seriesTorrents');
  return { ...actual, useIsSectionVisible: () => true };
});

const mockApi = vi.fn();
vi.mock('@/lib/api', async () => {
  const actual = await vi.importActual<typeof import('@/lib/api')>('@/lib/api');
  return { ...actual, api: (path: string) => mockApi(path) };
});

function r(node: React.ReactElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
  return render(
    <I18nextProvider i18n={i18n}>
      <QueryClientProvider client={qc}>
        <TooltipProvider>{node}</TooltipProvider>
      </QueryClientProvider>
    </I18nextProvider>,
  );
}

describe('<MovieTorrentsSection />', () => {
  beforeEach(() => {
    mockApi.mockReset();
    qbitSettingsResult.mockReturnValue({
      data: { enabled: true, url: 'http://qbit', username: 'u' },
      isPending: false, isFetched: true,
    });
  });

  it('returns null when qBit is not configured', async () => {
    qbitSettingsResult.mockReturnValue({ data: null, isPending: false, isFetched: true });
    const { container } = r(<MovieTorrentsSection instance="alpha" tmdbId={438631} />);
    expect(container.firstChild).toBeNull();
    expect(mockApi).not.toHaveBeenCalled();
  });

  it('renders the movie-flavored never-empty state when torrents=[]', async () => {
    mockApi.mockResolvedValue({ torrents: [], synced_at: new Date().toISOString(), total_count: 0, live_count: 0 });
    r(<MovieTorrentsSection instance="alpha" tmdbId={438631} />);
    await waitFor(() => expect(screen.getByTestId('torrents-empty').getAttribute('data-variant')).toBe('never'));
    expect(screen.getByText('No torrents for this movie yet')).toBeInTheDocument();
  });

  it('fetches /movies/:tmdbId/torrents and renders a provenance chip per row', async () => {
    mockApi.mockResolvedValue({
      torrents: [
        { hash: 'a', name: 'dune.2021', size_bytes: 1024, present: true, live: true, ratio: 0, provenance: 'radarr_search' },
      ],
      synced_at: new Date().toISOString(),
    });
    r(<MovieTorrentsSection instance="alpha" tmdbId={438631} />);
    await waitFor(() => expect(mockApi).toHaveBeenCalledWith('/movies/438631/torrents'));
    // jsdom does not evaluate the `hidden md:block` / `md:hidden`
    // responsive classes, so BOTH the desktop table row and the mobile
    // card render simultaneously — use findAllByTestId (not the singular
    // findByTestId) and assert on the first match. Deviation from the
    // story's exact test code (B1.5/ADR-0023 impl report).
    const chips = await screen.findAllByTestId('torrent-provenance');
    expect(chips[0]).toHaveTextContent('via Radarr');
  });
});
