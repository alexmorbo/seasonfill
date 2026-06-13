import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { TooltipProvider } from '@/components/ui/tooltip';
import i18n from '@/i18n';
import { TorrentsSection } from '../TorrentsSection';

// Default mock = qBit configured + enabled. Override per-test below.
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

// Force the visibility composer to true so the polling + stale-banner
// branches both render under happy-dom (which exposes IO but never
// fires intersection entries). Without this the banner branch never
// gates open.
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

describe('<TorrentsSection />', () => {
  beforeEach(() => {
    mockApi.mockReset();
    qbitSettingsResult.mockReturnValue({
      data: { enabled: true, url: 'http://qbit', username: 'u' },
      isPending: false, isFetched: true,
    });
  });

  it('returns null when qBit is not configured', async () => {
    qbitSettingsResult.mockReturnValue({ data: null, isPending: false, isFetched: true });
    const { container } = r(<TorrentsSection instance="alpha" seriesId={42} />);
    expect(container.firstChild).toBeNull();
    expect(mockApi).not.toHaveBeenCalled();
  });

  it('returns null when qBit is configured but disabled', async () => {
    qbitSettingsResult.mockReturnValue({
      data: { enabled: false, url: '', username: '' },
      isPending: false, isFetched: true,
    });
    const { container } = r(<TorrentsSection instance="alpha" seriesId={42} />);
    expect(container.firstChild).toBeNull();
  });

  it('renders the never-grabbed empty state when torrents=[]', async () => {
    mockApi.mockResolvedValue({ torrents: [], synced_at: new Date().toISOString(), total_count: 0, live_count: 0 });
    r(<TorrentsSection instance="alpha" seriesId={42} />);
    await waitFor(() => expect(screen.getByTestId('torrents-empty').getAttribute('data-variant')).toBe('never'));
  });

  it('renders the all-deleted note when every row has present=false', async () => {
    mockApi.mockResolvedValue({
      torrents: [
        { hash: 'a', name: 'old.s01', size_bytes: 1024, present: false, live: false, ratio: 1.5 },
      ],
      synced_at: new Date().toISOString(),
    });
    r(<TorrentsSection instance="alpha" seriesId={42} />);
    await waitFor(() => expect(screen.getByTestId('torrents-all-deleted-note')).toBeInTheDocument());
  });

  it('renders the stale banner when synced_at is older than the threshold', async () => {
    const old = new Date(Date.now() - 120_000).toISOString();
    mockApi.mockResolvedValue({
      torrents: [{ hash: 'a', name: 'x', size_bytes: 1024, present: true, live: false, ratio: 1.5 }],
      synced_at: old,
    });
    r(<TorrentsSection instance="alpha" seriesId={42} />);
    await waitFor(() => expect(screen.getByTestId('torrents-stale-banner')).toBeInTheDocument());
  });
});
