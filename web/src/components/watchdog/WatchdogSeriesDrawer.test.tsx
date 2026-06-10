import { describe, expect, it, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import {
  MemoryRouter,
  Routes,
  Route,
  useSearchParams,
} from 'react-router-dom';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';
import { WatchdogSeriesDrawer } from './WatchdogSeriesDrawer';

vi.mock('@/lib/api', async () => {
  const actual = await vi.importActual<typeof import('@/lib/api')>('@/lib/api');
  return { ...actual, api: vi.fn() };
});

vi.mock('sonner', () => ({
  toast: {
    success: vi.fn(),
    error: vi.fn(),
  },
}));

import { api } from '@/lib/api';

function makeClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0, staleTime: 0 } },
  });
}

interface HarnessProps {
  initialEntry: string;
}

function Harness({ initialEntry }: HarnessProps) {
  return (
    <QueryClientProvider client={makeClient()}>
      <I18nextProvider i18n={i18n}>
        <MemoryRouter initialEntries={[initialEntry]}>
          <Routes>
            <Route path="/watchdog" element={<HarnessRoute />} />
          </Routes>
        </MemoryRouter>
      </I18nextProvider>
    </QueryClientProvider>
  );
}

function HarnessRoute() {
  const [params, setParams] = useSearchParams();
  const seriesIDRaw = params.get('series_id');
  const instance = params.get('instance');
  const seriesID = seriesIDRaw ? Number(seriesIDRaw) : null;
  return (
    <>
      <div data-testid="probe-series-id">{seriesIDRaw ?? ''}</div>
      <div data-testid="probe-instance">{instance ?? ''}</div>
      <WatchdogSeriesDrawer
        seriesID={seriesID}
        instance={instance}
        onOpenChange={(open) => {
          if (!open) {
            const next = new URLSearchParams(params);
            next.delete('series_id');
            next.delete('instance');
            setParams(next, { replace: true });
          }
        }}
      />
    </>
  );
}

const fullPayload = {
  instance: 'homelab',
  series_id: 169,
  series_title: 'Your Friends & Neighbors',
  monitored: true,
  seasons: [
    {
      season_number: 1,
      stats: {
        aired_episode_count: 8,
        episode_file_count: 8,
        missing_aired_count: 0,
      },
      origin: {
        indexer: 'RuTracker (Prowlarr)',
        first_seen_at: new Date(Date.now() - 86_400_000).toISOString(),
        last_seen_at: new Date(Date.now() - 3_600_000).toISOString(),
        last_used_at: new Date(Date.now() - 3_600_000).toISOString(),
      },
      cooldown: null,
      blacklist: null,
      no_better_counter: { consecutive: 0, max: 3 },
      recent_decisions: [],
      recent_grabs: [],
    },
    {
      season_number: 2,
      stats: {
        aired_episode_count: 10,
        episode_file_count: 9,
        missing_aired_count: 1,
      },
      origin: {
        indexer: 'Kinozal',
        first_seen_at: new Date(Date.now() - 2 * 86_400_000).toISOString(),
        last_seen_at: new Date(Date.now() - 7_200_000).toISOString(),
        last_used_at: new Date(Date.now() - 7_200_000).toISOString(),
      },
      cooldown: {
        expires_at: new Date(Date.now() + 3_600_000).toISOString(),
        reason: 'series_after_grab',
      },
      blacklist: null,
      no_better_counter: { consecutive: 2, max: 3 },
      recent_decisions: Array.from({ length: 7 }).map((_, i) => ({
        id: `dec-${i}`,
        decision: i % 2 === 0 ? 'skip' : 'grab',
        reason: i % 2 === 0 ? 'skip_all_complete' : 'grab_selected',
        created_at: new Date(Date.now() - (i + 1) * 60_000).toISOString(),
        scan_run_id: `scan-${i}`,
      })),
      recent_grabs: Array.from({ length: 7 }).map((_, i) => ({
        id: `grab-${i}`,
        status: i % 2 === 0 ? 'imported' : 'grabbed',
        release_title: `Severance.S02E${String(i + 1).padStart(2, '0')}.2160p.example`,
        replay_of_id: i === 1 ? 'grab-0' : null,
        created_at: new Date(Date.now() - (i + 1) * 90_000).toISOString(),
      })),
    },
  ],
};

beforeEach(() => {
  vi.mocked(api).mockReset();
});

describe('<WatchdogSeriesDrawer />', () => {
  it('renders nothing when both params are null', () => {
    render(<Harness initialEntry="/watchdog" />);
    expect(
      screen.queryByTestId('watchdog-series-drawer'),
    ).not.toBeInTheDocument();
    expect(vi.mocked(api)).not.toHaveBeenCalled();
  });

  it('shows the loading state while fetching', async () => {
    vi.mocked(api).mockImplementation(() => new Promise(() => {}));
    render(<Harness initialEntry="/watchdog?series_id=169&instance=homelab" />);
    expect(
      await screen.findByTestId('watchdog-series-drawer-loading'),
    ).toBeInTheDocument();
  });

  it('shows the empty state when no seasons are returned', async () => {
    vi.mocked(api).mockResolvedValueOnce({
      instance: 'homelab',
      series_id: 169,
      series_title: 'Empty Series',
      monitored: true,
      seasons: [],
    });
    render(<Harness initialEntry="/watchdog?series_id=169&instance=homelab" />);
    expect(
      await screen.findByText(/has not been scanned/i),
    ).toBeInTheDocument();
  });

  it('renders the full multi-season payload', async () => {
    vi.mocked(api).mockResolvedValueOnce(fullPayload);
    render(<Harness initialEntry="/watchdog?series_id=169&instance=homelab" />);

    expect(
      await screen.findByText(/Your Friends & Neighbors/),
    ).toBeInTheDocument();
    // Latest season is opened by default → assert sections are present.
    await waitFor(() => {
      expect(screen.getByTestId('drawer-section-origin')).toBeInTheDocument();
    });
    expect(screen.getByTestId('drawer-section-stats')).toBeInTheDocument();
    expect(screen.getByTestId('drawer-section-cooldown')).toBeInTheDocument();
    expect(screen.getByTestId('drawer-section-nobetter')).toBeInTheDocument();
    expect(screen.getByTestId('drawer-section-decisions')).toBeInTheDocument();
    expect(screen.getByTestId('drawer-section-grabs')).toBeInTheDocument();
    // Most recent season (S02) is rendered first; both season triggers exist.
    expect(
      screen.getByTestId('watchdog-series-drawer-season-2'),
    ).toBeInTheDocument();
    expect(
      screen.getByTestId('watchdog-series-drawer-season-1'),
    ).toBeInTheDocument();
    // Indexer text from S02 (the default-open accordion item).
    expect(screen.getByText(/Kinozal/)).toBeInTheDocument();
    // no-better 2/3
    expect(screen.getByText('2/3')).toBeInTheDocument();
  });

  it('expands and collapses the recent decisions list', async () => {
    vi.mocked(api).mockResolvedValueOnce(fullPayload);
    render(<Harness initialEntry="/watchdog?series_id=169&instance=homelab" />);

    const toggle = await screen.findByTestId('drawer-decisions-toggle');
    const list = screen.getByTestId('drawer-decisions-list');
    // Initially 5 rows visible (cap at INITIAL_VISIBLE).
    expect(list.querySelectorAll('li').length).toBe(5);
    fireEvent.click(toggle);
    await waitFor(() => {
      expect(
        screen.getByTestId('drawer-decisions-list').querySelectorAll('li').length,
      ).toBe(7);
    });
    // Collapse back.
    fireEvent.click(screen.getByTestId('drawer-decisions-toggle'));
    await waitFor(() => {
      expect(
        screen.getByTestId('drawer-decisions-list').querySelectorAll('li').length,
      ).toBe(5);
    });
  });

  it('expands the recent grabs list', async () => {
    vi.mocked(api).mockResolvedValueOnce(fullPayload);
    render(<Harness initialEntry="/watchdog?series_id=169&instance=homelab" />);

    const toggle = await screen.findByTestId('drawer-grabs-toggle');
    const list = screen.getByTestId('drawer-grabs-list');
    expect(list.querySelectorAll('li').length).toBe(5);
    fireEvent.click(toggle);
    await waitFor(() => {
      expect(
        screen.getByTestId('drawer-grabs-list').querySelectorAll('li').length,
      ).toBe(7);
    });
  });

  it('uses ?open=<id> for the grab-drawer link (Grabs page reads `open`)', async () => {
    vi.mocked(api).mockResolvedValueOnce(fullPayload);
    render(<Harness initialEntry="/watchdog?series_id=169&instance=homelab" />);

    const links = await screen.findAllByTestId('drawer-grab-open');
    expect(links.length).toBeGreaterThan(0);
    expect(links[0]).toHaveAttribute('href', '/grabs?open=grab-0');
    // All links use the canonical `open` param (matching Grabs.tsx).
    for (const link of links) {
      expect(link.getAttribute('href')).toMatch(/^\/grabs\?open=/);
    }
  });

  it('clears URL params when the drawer is closed', async () => {
    vi.mocked(api).mockResolvedValueOnce(fullPayload);
    render(<Harness initialEntry="/watchdog?series_id=169&instance=homelab" />);

    expect(
      await screen.findByTestId('watchdog-series-drawer'),
    ).toBeInTheDocument();
    // Click the built-in Sheet close button (rendered with aria-label="Close").
    const closeBtn = screen.getByRole('button', { name: /close/i });
    fireEvent.click(closeBtn);
    await waitFor(() => {
      expect(screen.getByTestId('probe-series-id').textContent).toBe('');
      expect(screen.getByTestId('probe-instance').textContent).toBe('');
    });
  });

  it('renders the torrent hash row when origin.torrent_hash is present', async () => {
    const hash = 'a1b2c3d4e5f60718293a4b5c6d7e8f9001122334';
    const baseSeason = fullPayload.seasons[0]!;
    const payload = {
      ...fullPayload,
      seasons: [
        {
          ...baseSeason,
          origin: {
            ...baseSeason.origin,
            torrent_hash: hash,
          },
        },
      ],
    };
    vi.mocked(api).mockResolvedValueOnce(payload);
    render(<Harness initialEntry="/watchdog?series_id=169&instance=homelab" />);

    const node = await screen.findByTestId('drawer-origin-torrent-hash-value');
    expect(node).toBeInTheDocument();
    // Truncated, not full hash, in the visible text.
    expect(node.textContent).not.toBe(hash);
    expect(node.textContent).toMatch(/^a1b2c3d4…/);
    // Full hash preserved in the title attribute for tooltip + a11y.
    expect(node.getAttribute('title')).toBe(hash);
    // The copy affordance is present.
    expect(
      screen.getByTestId('drawer-origin-torrent-hash-copy'),
    ).toBeInTheDocument();
  });

  it('copies the full hash to clipboard on copy click', async () => {
    const hash = 'deadbeefcafe11223344556677889900aabbccdd';
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { writeText },
    });
    const baseSeason = fullPayload.seasons[0]!;
    const payload = {
      ...fullPayload,
      seasons: [
        {
          ...baseSeason,
          origin: {
            ...baseSeason.origin,
            torrent_hash: hash,
          },
        },
      ],
    };
    vi.mocked(api).mockResolvedValueOnce(payload);
    render(<Harness initialEntry="/watchdog?series_id=169&instance=homelab" />);

    const btn = await screen.findByTestId('drawer-origin-torrent-hash-copy');
    fireEvent.click(btn);
    await waitFor(() => {
      expect(writeText).toHaveBeenCalledWith(hash);
    });
  });

  it('omits the torrent hash row when origin.torrent_hash is absent', async () => {
    // fullPayload.seasons[*].origin already has no torrent_hash field.
    vi.mocked(api).mockResolvedValueOnce(fullPayload);
    render(<Harness initialEntry="/watchdog?series_id=169&instance=homelab" />);

    expect(
      await screen.findByTestId('drawer-section-origin'),
    ).toBeInTheDocument();
    expect(
      screen.queryByTestId('drawer-origin-torrent-hash'),
    ).not.toBeInTheDocument();
  });
});
