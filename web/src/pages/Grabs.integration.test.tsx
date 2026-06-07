import type { ReactElement } from 'react';
import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest';
import userEvent from '@testing-library/user-event';
import { screen, waitFor } from '@testing-library/react';
import { renderWithProviders } from '@/test-utils';
import { Grabs } from './Grabs';
import { InstanceFilterCtx } from '@/lib/instance-filter-context-internal';
import type { Grab } from '@/lib/grabs/chipBuilder';
import { DtoGrabStatus } from '@/api/schema';

const origFetch = globalThis.fetch;
const ctxValue = { filter: 'alpha', setFilter: vi.fn() };

const wrap = (ui: ReactElement) => (
  <InstanceFilterCtx.Provider value={ctxValue}>{ui}</InstanceFilterCtx.Provider>
);

const grabFor = (id: string, over: Partial<Record<string, unknown>> = {}) => {
  const grab: Record<string, unknown> = {
    id,
    instance: 'alpha',
    series_id: 100,
    series_title: 'For All Mankind',
    season_number: 5,
    release_title: `FAM.S05E1-10.${id}`,
    status: DtoGrabStatus.imported,
    scan_run_id: 'scan-1',
    custom_format_score: 180,
    coverage_count: 10,
    size_bytes: 13_325_829_734,
    torrent_hash: 'C2CB0D9EFFAB1234CDEFA71F',
    created_at: '2026-06-07T19:32:00Z',
    updated_at: '2026-06-07T19:32:41Z',
    parsed: {
      codec: 'HEVC',
      source: 'webdl',
      quality: 'WEBDL-2160p',
      resolution: 2160,
      hdr_flags: ['HDR10+', 'DV'],
      dub: 'MVO',
    },
    ...over,
  };
  return grab as Grab;
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
    if (u.endsWith('/grabs') || u.includes('/grabs?')) {
      return Promise.resolve(new Response(JSON.stringify({
        items: [grabFor('g_001'), grabFor('g_root', { replayed_by: ['g_001'] })],
      }), { status: 200, headers: { 'Content-Type': 'application/json' } }));
    }
    return Promise.resolve(new Response('{}', { status: 200 }));
  }) as typeof fetch;
});
afterEach(() => { globalThis.fetch = origFetch; });

describe('<Grabs /> + drawer integration', () => {
  it('opens drawer from ?open=<id> URL state, fetches files', async () => {
    renderWithProviders(wrap(<Grabs />), { route: '/grabs?open=g_001' });
    await waitFor(() => {
      expect(screen.getByTestId('grab-drawer-content')).toBeInTheDocument();
    });
    // Files request fires.
    await waitFor(() => {
      expect(screen.getByText(/FAM\.S05E01\.mkv/)).toBeInTheDocument();
    });
  });

  it('qBit link in drawer uses settings URL', async () => {
    renderWithProviders(wrap(<Grabs />), { route: '/grabs?open=g_001' });
    await waitFor(() => {
      expect(screen.getByTestId('drawer-qbit-link')).toHaveAttribute(
        'href',
        'http://qbit.lan:8080/#/torrent/c2cb0d9effab1234cdefa71f',
      );
    });
  });

  it('clicking re-grab tag toggles thread, does NOT open drawer', async () => {
    const reGrab = grabFor('g_001', { replay_of_id: 'g_root' });
    globalThis.fetch = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({
        items: [reGrab, grabFor('g_root', { id: 'g_root', replayed_by: ['g_001'] })],
      }), { status: 200, headers: { 'Content-Type': 'application/json' } }),
    ) as typeof fetch;
    renderWithProviders(wrap(<Grabs />), { route: '/grabs' });
    await waitFor(() => {
      expect(screen.getAllByText('For All Mankind').length).toBeGreaterThan(0);
    });
    // Click the re-grab tag on g_001.
    const tag = await screen.findByTestId('regrab-tag-g_001');
    await userEvent.click(tag);
    await waitFor(() => {
      expect(screen.queryByTestId('grab-drawer-content')).toBeNull();
      expect(screen.getByTestId('regrab-thread-g_001')).toBeInTheDocument();
    });
  });
});
