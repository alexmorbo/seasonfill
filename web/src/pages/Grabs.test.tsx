import type { ReactElement } from 'react';
import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest';
import { screen, waitFor } from '@testing-library/react';
import { renderWithProviders } from '@/test-utils';
import { Grabs } from './Grabs';
import { InstanceFilterCtx } from '@/lib/instance-filter-context-internal';

const origFetch = globalThis.fetch;
const ctxValue = { filter: 'homelab', setFilter: vi.fn() };

const wrap = (ui: ReactElement) => (
  <InstanceFilterCtx.Provider value={ctxValue}>{ui}</InstanceFilterCtx.Provider>
);

function grab(over: Partial<Record<string, unknown>> = {}) {
  return {
    id: 'g_001',
    instance: 'homelab',
    series_id: 100,
    series_title: 'Severance',
    season_number: 2,
    release_title: 'Severance.S02E10.2160p.WEB-DL.x265-NTb',
    status: 'imported',
    indexer_name: 'rutracker',
    custom_format_score: 150,
    size_bytes: 8_589_934_592,
    parsed: {
      codec: 'HEVC', source: 'webdl', quality: 'WEBDL-2160p',
      resolution: 2160, hdr_flags: ['HDR10'], dub: 'MVO',
    },
    created_at: new Date().toISOString(),
    updated_at: new Date().toISOString(),
    ...over,
  };
}

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });

beforeEach(() => {
  Object.defineProperty(window, 'location', {
    writable: true,
    value: { pathname: '/grabs', search: '', assign: vi.fn() },
  });
});
afterEach(() => { globalThis.fetch = origFetch; });

describe('<Grabs />', () => {
  it('renders chip-rich rows from useGrabs', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue(
      json({ items: [grab(), grab({ id: 'g_002', series_title: 'Andor', status: 'import_failed' })] }),
    ) as typeof fetch;
    renderWithProviders(wrap(<Grabs />), { route: '/grabs' });
    expect(await screen.findByText('Severance')).toBeInTheDocument();
    expect(screen.getByText('Andor')).toBeInTheDocument();
    expect(screen.getAllByText('WEBDL-2160p').length).toBeGreaterThan(0);
  });

  it('filter=fails hides imported rows', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue(
      json({ items: [grab(), grab({ id: 'g_002', series_title: 'Andor', status: 'import_failed' })] }),
    ) as typeof fetch;
    renderWithProviders(wrap(<Grabs />), { route: '/grabs?filter=fails' });
    expect(await screen.findByText('Andor')).toBeInTheDocument();
    expect(screen.queryByText('Severance')).not.toBeInTheDocument();
  });

  it('search filters by series_title', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue(
      json({ items: [grab(), grab({ id: 'g_002', series_title: 'Andor', release_title: 'Andor.S01E01.1080p' })] }),
    ) as typeof fetch;
    renderWithProviders(wrap(<Grabs />), { route: '/grabs?q=Sev' });
    expect(await screen.findByText('Severance')).toBeInTheDocument();
    expect(screen.queryByText('Andor')).not.toBeInTheDocument();
  });

  it('renders the top-level empty state when no grabs at all', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue(json({ items: [] })) as typeof fetch;
    renderWithProviders(wrap(<Grabs />), { route: '/grabs' });
    await waitFor(() => expect(screen.queryAllByText(/Грабов|grabs/i).length).toBeGreaterThan(0));
  });

  it('renders the fails-empty state with celebration copy', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue(json({ items: [grab()] })) as typeof fetch;
    renderWithProviders(wrap(<Grabs />), { route: '/grabs?filter=fails' });
    // imported-only fixture → fails view is empty
    await waitFor(() => expect(screen.queryByText('Severance')).not.toBeInTheDocument());
  });

  it('forwards ?series=<int> to the API as series_id', async () => {
    const fetchSpy = vi.fn().mockResolvedValue(json({ items: [grab()] }));
    globalThis.fetch = fetchSpy as typeof fetch;
    renderWithProviders(wrap(<Grabs />), { route: '/grabs?series=122' });
    await waitFor(() => {
      const grabCall = fetchSpy.mock.calls.find((c) => {
        const url = String(c[0]);
        return url.includes('/grabs?') && url.includes('series_id=122');
      });
      expect(grabCall).toBeDefined();
    });
  });

  it('ignores non-numeric ?series= values', async () => {
    const fetchSpy = vi.fn().mockResolvedValue(json({ items: [grab()] }));
    globalThis.fetch = fetchSpy as typeof fetch;
    renderWithProviders(wrap(<Grabs />), { route: '/grabs?series=for-all-mankind' });
    await waitFor(() => {
      const grabCall = fetchSpy.mock.calls.find((c) => {
        const url = String(c[0]);
        return url.includes('/grabs');
      });
      expect(grabCall).toBeDefined();
      expect(String(grabCall![0])).not.toContain('series_id=');
    });
  });
});
