import type { ReactElement, ReactNode } from 'react';
import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest';
import userEvent from '@testing-library/user-event';
import { render, screen, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { I18nextProvider } from 'react-i18next';
import { MemoryRouter } from 'react-router-dom';
import i18n from '@/i18n';
import { GrabDrawer } from './GrabDrawer';
import type { Grab } from '@/lib/grabs/chipBuilder';
import { DtoGrabStatus } from '@/api/schema';
import { InstanceFilterCtx } from '@/lib/instance-filter-context-internal';

const origFetch = globalThis.fetch;
afterEach(() => { globalThis.fetch = origFetch; });

const ctxValue = { filter: 'alpha', setFilter: vi.fn() };

function wrap(ui: ReactElement): ReactNode {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return (
    <MemoryRouter>
      <QueryClientProvider client={qc}>
        <I18nextProvider i18n={i18n}>
          <InstanceFilterCtx.Provider value={ctxValue}>{ui}</InstanceFilterCtx.Provider>
        </I18nextProvider>
      </QueryClientProvider>
    </MemoryRouter>
  );
}

const baseGrab: Grab = {
  id: 'g_001',
  instance: 'alpha',
  series_title: 'For All Mankind',
  series_id: 100,
  season_number: 5,
  release_title: 'For All Mankind / S5E1-10 of 10 [2026, HEVC, HDR10, HDR10+, Dolby Vision, WEB-DL 2160p] 4 x + Original',
  status: DtoGrabStatus.imported,
  scan_run_id: 'scan-uuid-1',
  custom_format_score: 180,
  coverage_count: 10,
  created_at: '2026-06-07T19:32:00Z',
  updated_at: '2026-06-07T19:32:41Z',
  torrent_hash: 'C2CB0D9EFFAB1234CDEFA71F',
  size_bytes: 13_325_829_734,
  parsed: {
    codec: 'HEVC',
    source: 'webdl',
    quality: 'WEBDL-2160p',
    resolution: 2160,
    hdr_flags: ['HDR10+', 'DV'],
    dub: 'MVO',
  },
};

beforeEach(() => {
  globalThis.fetch = vi.fn().mockImplementation((url: string | URL) => {
    const u = url.toString();
    if (u.includes('/episode-files')) {
      return Promise.resolve(new Response(JSON.stringify({
        items: [
          {
            id: 7001, relative_path: 'Season 05/FAM.S05E01.mkv',
            season_number: 5, episode_numbers: [1],
            size_bytes: 1_200_000_000, quality: 'WEBDL-2160p',
          },
        ],
      }), { status: 200, headers: { 'Content-Type': 'application/json' } }));
    }
    if (u.includes('/qbit/settings')) {
      return Promise.resolve(new Response(JSON.stringify({
        url: 'http://qbit.lan:8080', enabled: true,
      }), { status: 200, headers: { 'Content-Type': 'application/json' } }));
    }
    if (u.includes('/decisions')) {
      return Promise.resolve(new Response(JSON.stringify({
        items: [{ id: 'dec-uuid-1', scan_run_id: 'scan-uuid-1', series_id: 100, season_number: 5 }],
      }), { status: 200, headers: { 'Content-Type': 'application/json' } }));
    }
    if (u.includes('/grabs')) {
      return Promise.resolve(new Response(JSON.stringify({ items: [baseGrab] }),
        { status: 200, headers: { 'Content-Type': 'application/json' } }));
    }
    return Promise.resolve(new Response('{}', { status: 200 }));
  }) as typeof fetch;
});

describe('<GrabDrawer />', () => {
  it('renders hero, release section, torrent section, files section', async () => {
    render(wrap(
      <GrabDrawer id="g_001" open={true} onOpenChange={() => {}} rows={[baseGrab]} />,
    ));
    expect(await screen.findByText('For All Mankind')).toBeInTheDocument();
    expect(screen.getByTestId('drawer-release-raw')).toHaveTextContent(/HDR10\+/);
    await waitFor(() => {
      expect(screen.getByTestId('drawer-hash-row')).toHaveTextContent(/c2cb0d9e/i);
    });
    expect(screen.getByTestId('drawer-qbit-link')).toHaveAttribute(
      'href',
      'http://qbit.lan:8080',
    );
    await waitFor(() => {
      expect(screen.getByTestId('drawer-decision-link')).toHaveAttribute(
        'href',
        '/scans/scan-uuid-1?drawer=dec-uuid-1',
      );
    });
    await waitFor(() => {
      expect(screen.getByText(/FAM\.S05E01\.mkv/)).toBeInTheDocument();
    });
  });

  it('copy button writes hash to clipboard', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', {
      value: { writeText },
      writable: true,
      configurable: true,
    });
    render(wrap(
      <GrabDrawer id="g_001" open={true} onOpenChange={() => {}} rows={[baseGrab]} />,
    ));
    await screen.findByTestId('drawer-hash-copy');
    await userEvent.click(screen.getByTestId('drawer-hash-copy'));
    expect(writeText).toHaveBeenCalledWith('C2CB0D9EFFAB1234CDEFA71F');
  });

  it('renders unavailable torrent section when torrent_hash missing', async () => {
    const noHash: Grab = { ...baseGrab };
    const mutable = noHash as Record<string, unknown>;
    delete mutable.torrent_hash;
    render(wrap(
      <GrabDrawer id="g_001" open={true} onOpenChange={() => {}} rows={[noHash]} />,
    ));
    expect(await screen.findByText(/unavailable|недоступен|Phase 12/i)).toBeInTheDocument();
    expect(screen.queryByTestId('drawer-hash-row')).toBeNull();
  });

  it('disables qBit link when qbit settings URL is missing', async () => {
    globalThis.fetch = vi.fn().mockImplementation((url: string | URL) => {
      const u = url.toString();
      if (u.includes('/qbit/settings')) {
        return Promise.resolve(new Response(JSON.stringify({ url: '' }),
          { status: 200, headers: { 'Content-Type': 'application/json' } }));
      }
      if (u.includes('/episode-files')) {
        return Promise.resolve(new Response(JSON.stringify({ items: [] }),
          { status: 200, headers: { 'Content-Type': 'application/json' } }));
      }
      return Promise.resolve(new Response('{}', { status: 200 }));
    }) as typeof fetch;
    render(wrap(
      <GrabDrawer id="g_001" open={true} onOpenChange={() => {}} rows={[baseGrab]} />,
    ));
    await waitFor(() => {
      expect(screen.getByTestId('drawer-qbit-link-disabled')).toBeInTheDocument();
    });
  });

  it('does NOT fire episode-files request when open=false', async () => {
    const fetchSpy = vi.fn().mockResolvedValue(new Response('{}', { status: 200 }));
    globalThis.fetch = fetchSpy as typeof fetch;
    render(wrap(
      <GrabDrawer id="g_001" open={false} onOpenChange={() => {}} rows={[baseGrab]} />,
    ));
    await new Promise((r) => setTimeout(r, 10));
    const efCalls = fetchSpy.mock.calls.filter((c) =>
      String(c[0]).includes('/episode-files'),
    );
    expect(efCalls.length).toBe(0);
  });

  it('renders not-found state when id has no match in rows', () => {
    render(wrap(
      <GrabDrawer id="g_missing" open={true} onOpenChange={() => {}} rows={[baseGrab]} />,
    ));
    expect(screen.getByText(/not found|не найден/i)).toBeInTheDocument();
  });

  it('decision pill degrades to /scans/<id> while lookup pending', async () => {
    let resolveDecisions: (() => void) | null = null;
    globalThis.fetch = vi.fn().mockImplementation((url: string | URL) => {
      const u = url.toString();
      if (u.includes('/decisions')) {
        return new Promise<Response>((resolve) => {
          resolveDecisions = () => resolve(
            new Response(JSON.stringify({ items: [] }),
              { status: 200, headers: { 'Content-Type': 'application/json' } }),
          );
        });
      }
      if (u.includes('/episode-files')) {
        return Promise.resolve(new Response(JSON.stringify({ items: [] }),
          { status: 200, headers: { 'Content-Type': 'application/json' } }));
      }
      if (u.includes('/qbit/settings')) {
        return Promise.resolve(new Response(JSON.stringify({ url: '' }),
          { status: 200, headers: { 'Content-Type': 'application/json' } }));
      }
      return Promise.resolve(new Response('{}', { status: 200 }));
    }) as typeof fetch;
    render(wrap(
      <GrabDrawer id="g_001" open={true} onOpenChange={() => {}} rows={[baseGrab]} />,
    ));
    await waitFor(() => {
      expect(screen.getByTestId('drawer-decision-link')).toHaveAttribute(
        'href',
        '/scans/scan-uuid-1',
      );
    });
    (resolveDecisions as (() => void) | null)?.();
  });

  it('decision pill is absent when grab has no scan_run_id', async () => {
    const noScan: Grab = { ...baseGrab };
    const mutable = noScan as Record<string, unknown>;
    delete mutable.scan_run_id;
    render(wrap(
      <GrabDrawer id="g_001" open={true} onOpenChange={() => {}} rows={[noScan]} />,
    ));
    await waitFor(() => {
      expect(screen.getByTestId('grab-drawer-content')).toBeInTheDocument();
    });
    expect(screen.queryByTestId('drawer-decision-link')).toBeNull();
  });

  it('drawer container has the bumped width class', async () => {
    render(wrap(
      <GrabDrawer id="g_001" open={true} onOpenChange={() => {}} rows={[baseGrab]} />,
    ));
    const content = await screen.findByTestId('grab-drawer-content');
    expect(content.className).toMatch(/sm:max-w-\[640px\]/);
  });

  it('renders DrawerErrorSection with full error_message for a failed grab (N-5)', async () => {
    const longErr =
      'sonarr /api/v3/release returned status=500 body={"message":"Download client failed to add torrent","description":"qBittorrent connection refused: dial tcp 10.0.42.7:10095: i/o timeout","exception":"NzbDroneException"}';
    const failedGrab: Grab = {
      ...baseGrab,
      id: 'g_fail',
      status: DtoGrabStatus.grab_failed,
      error_message: longErr,
      attempts: 3,
    };
    globalThis.fetch = vi.fn().mockImplementation((url: string | URL) => {
      const u = url.toString();
      if (u.includes('/episode-files')) {
        return Promise.resolve(new Response(JSON.stringify({ items: [] }),
          { status: 200, headers: { 'Content-Type': 'application/json' } }));
      }
      if (u.includes('/qbit/settings')) {
        return Promise.resolve(new Response(JSON.stringify({ url: '' }),
          { status: 200, headers: { 'Content-Type': 'application/json' } }));
      }
      return Promise.resolve(new Response('{}', { status: 200 }));
    }) as typeof fetch;
    render(wrap(
      <GrabDrawer id="g_fail" open={true} onOpenChange={() => {}} rows={[failedGrab]} />,
    ));
    const section = await screen.findByTestId('drawer-error-section');
    expect(section).toBeInTheDocument();
    const text = screen.getByTestId('drawer-error-text');
    // Full text must be present — no clamping. The row's preview
    // truncates at 420px CSS, but the drawer renders the whole thing.
    expect(text).toHaveTextContent(longErr);
    // Tag must be a <pre> for whitespace preservation.
    expect(text.tagName.toLowerCase()).toBe('pre');
  });

  it('DrawerErrorSection copy button writes error_message to clipboard (N-5)', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', {
      value: { writeText }, writable: true, configurable: true,
    });
    const failedGrab: Grab = {
      ...baseGrab,
      id: 'g_fail2',
      status: DtoGrabStatus.grab_failed,
      error_message: 'short err',
    };
    globalThis.fetch = vi.fn().mockImplementation((url: string | URL) => {
      const u = url.toString();
      if (u.includes('/episode-files')) {
        return Promise.resolve(new Response(JSON.stringify({ items: [] }),
          { status: 200, headers: { 'Content-Type': 'application/json' } }));
      }
      if (u.includes('/qbit/settings')) {
        return Promise.resolve(new Response(JSON.stringify({ url: '' }),
          { status: 200, headers: { 'Content-Type': 'application/json' } }));
      }
      return Promise.resolve(new Response('{}', { status: 200 }));
    }) as typeof fetch;
    render(wrap(
      <GrabDrawer id="g_fail2" open={true} onOpenChange={() => {}} rows={[failedGrab]} />,
    ));
    const copyBtn = await screen.findByTestId('drawer-error-copy');
    await userEvent.click(copyBtn);
    expect(writeText).toHaveBeenCalledWith('short err');
  });

  it('DrawerErrorSection is absent when error_message is empty (N-5)', async () => {
    // baseGrab has no error_message → section omitted entirely.
    render(wrap(
      <GrabDrawer id="g_001" open={true} onOpenChange={() => {}} rows={[baseGrab]} />,
    ));
    await screen.findByText('For All Mankind');
    expect(screen.queryByTestId('drawer-error-section')).toBeNull();
  });

  // 083 / F-P2-1 — link prefers qbit_public_url, hides on internal fallback
  it('link prefers qbit_public_url when set (083)', async () => {
    globalThis.fetch = vi.fn().mockImplementation((url: string | URL) => {
      const u = url.toString();
      if (u.includes('/qbit/settings')) {
        return Promise.resolve(new Response(JSON.stringify({
          url: 'http://qbittorrent-web:10095',
          qbit_public_url: 'https://qbit.example.com',
        }), { status: 200, headers: { 'Content-Type': 'application/json' } }));
      }
      if (u.includes('/episode-files')) {
        return Promise.resolve(new Response(JSON.stringify({ items: [] }),
          { status: 200, headers: { 'Content-Type': 'application/json' } }));
      }
      return Promise.resolve(new Response('{}', { status: 200 }));
    }) as typeof fetch;
    render(wrap(
      <GrabDrawer id="g_001" open={true} onOpenChange={() => {}} rows={[baseGrab]} />,
    ));
    await waitFor(() => {
      expect(screen.getByTestId('drawer-qbit-link')).toHaveAttribute(
        'href',
        'https://qbit.example.com',
      );
    });
  });

  it('link uses qbit_url when public URL empty and url is public-ish (083)', async () => {
    globalThis.fetch = vi.fn().mockImplementation((url: string | URL) => {
      const u = url.toString();
      if (u.includes('/qbit/settings')) {
        return Promise.resolve(new Response(JSON.stringify({
          url: 'http://qb.example.com',
          qbit_public_url: '',
        }), { status: 200, headers: { 'Content-Type': 'application/json' } }));
      }
      if (u.includes('/episode-files')) {
        return Promise.resolve(new Response(JSON.stringify({ items: [] }),
          { status: 200, headers: { 'Content-Type': 'application/json' } }));
      }
      return Promise.resolve(new Response('{}', { status: 200 }));
    }) as typeof fetch;
    render(wrap(
      <GrabDrawer id="g_001" open={true} onOpenChange={() => {}} rows={[baseGrab]} />,
    ));
    await waitFor(() => {
      expect(screen.getByTestId('drawer-qbit-link')).toHaveAttribute(
        'href',
        'http://qb.example.com',
      );
    });
  });

  it('strips trailing slashes from qBT URL', async () => {
    globalThis.fetch = vi.fn().mockImplementation((url: string | URL) => {
      const u = url.toString();
      if (u.includes('/qbit/settings')) {
        return Promise.resolve(new Response(JSON.stringify({
          url: 'http://qbit.example.com:8080/',
          qbit_public_url: '',
        }), { status: 200, headers: { 'Content-Type': 'application/json' } }));
      }
      if (u.includes('/episode-files')) {
        return Promise.resolve(new Response(JSON.stringify({ items: [] }),
          { status: 200, headers: { 'Content-Type': 'application/json' } }));
      }
      return Promise.resolve(new Response('{}', { status: 200 }));
    }) as typeof fetch;
    render(wrap(
      <GrabDrawer id="g_001" open={true} onOpenChange={() => {}} rows={[baseGrab]} />,
    ));
    await waitFor(() => {
      expect(screen.getByTestId('drawer-qbit-link')).toHaveAttribute(
        'href',
        'http://qbit.example.com:8080',
      );
    });
  });

  it('link is hidden when public URL empty and qbit_url is kube-internal (083)', async () => {
    globalThis.fetch = vi.fn().mockImplementation((url: string | URL) => {
      const u = url.toString();
      if (u.includes('/qbit/settings')) {
        return Promise.resolve(new Response(JSON.stringify({
          url: 'http://qbittorrent-web:10095',
          qbit_public_url: '',
        }), { status: 200, headers: { 'Content-Type': 'application/json' } }));
      }
      if (u.includes('/episode-files')) {
        return Promise.resolve(new Response(JSON.stringify({ items: [] }),
          { status: 200, headers: { 'Content-Type': 'application/json' } }));
      }
      return Promise.resolve(new Response('{}', { status: 200 }));
    }) as typeof fetch;
    render(wrap(
      <GrabDrawer id="g_001" open={true} onOpenChange={() => {}} rows={[baseGrab]} />,
    ));
    await waitFor(() => {
      expect(screen.getByTestId('drawer-qbit-link-disabled')).toBeInTheDocument();
    });
    expect(screen.queryByTestId('drawer-qbit-link')).toBeNull();
  });
});
