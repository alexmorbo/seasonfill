import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter, Routes, Route, useSearchParams } from 'react-router-dom';
import { I18nextProvider } from 'react-i18next';
import i18n from '@/i18n';
import { WatchdogSeriesDrawer } from '../WatchdogSeriesDrawer';

// The drawer reads watchdog detail via `api` and the runtime config via
// raw fetch. Mock both. We keep them independent so each test can assert
// "with rules vs without rules" purely via the runtime config endpoint.

vi.mock('@/lib/api', async () => {
  const actual = await vi.importActual<typeof import('@/lib/api')>('@/lib/api');
  return { ...actual, api: vi.fn() };
});

import { api } from '@/lib/api';

const origFetch = globalThis.fetch;

interface RewriteRule { from: string; to: string }
let currentRules: RewriteRule[] = [];

function setupFetch() {
  globalThis.fetch = vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    if (url.endsWith('/api/v1/config/runtime')) {
      return new Response(JSON.stringify({
        cron: {}, scan: {}, dry_run: false, global_rate_limit: {}, auth: {},
        auto_generated_api_key: false, updated_at: new Date().toISOString(),
        guid_rewrites: currentRules,
      }), { status: 200, headers: { 'Content-Type': 'application/json' } });
    }
    return new Response('{}', { status: 200, headers: { 'Content-Type': 'application/json' } });
  }) as typeof fetch;
}

function makeClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0, staleTime: 0 } },
  });
}

function Harness({ initialEntry }: { initialEntry: string }) {
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
  );
}

function payloadWithGuid(guid: string) {
  return {
    instance: 'homelab',
    series_id: 169,
    series_title: 'Severance',
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
          guid,
          indexer: 'RuTracker',
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
    ],
  };
}

beforeEach(() => {
  vi.mocked(api).mockReset();
  currentRules = [];
  setupFetch();
});
afterEach(() => { globalThis.fetch = origFetch; });

describe('<WatchdogSeriesDrawer /> · tracker link · guid rewrites', () => {
  it('renders the link with the raw cluster URL when no rule matches (rewritten string still starts with http)', async () => {
    // Per the spec: render the link whenever the result-after-rewrite starts
    // with http(s). A cluster URL with no matching rule still satisfies that;
    // the operator can still click through from inside the cluster network.
    currentRules = [];
    vi.mocked(api).mockResolvedValueOnce(payloadWithGuid(
      'http://rutracker-proxy.servarr.svc.cluster.local/forum/viewtopic.php?t=1',
    ));
    render(<Harness initialEntry="/watchdog?series_id=169&instance=homelab" />);

    const link = await screen.findByTestId('drawer-origin-tracker-link');
    expect(link).toHaveAttribute(
      'href',
      'http://rutracker-proxy.servarr.svc.cluster.local/forum/viewtopic.php?t=1',
    );
  });

  it('renders the tracker link with the rewritten href when a matching rule exists', async () => {
    currentRules = [
      {
        from: 'http://rutracker-proxy.servarr.svc.cluster.local',
        to: 'https://rutracker.org',
      },
    ];
    vi.mocked(api).mockResolvedValueOnce(payloadWithGuid(
      'http://rutracker-proxy.servarr.svc.cluster.local/forum/viewtopic.php?t=1',
    ));
    render(<Harness initialEntry="/watchdog?series_id=169&instance=homelab" />);

    // The runtime-config query resolves on a microtask after the watchdog
    // detail. Wait for the href to flip from the raw GUID to the rewritten
    // value rather than racing on the initial render.
    const link = await screen.findByTestId('drawer-origin-tracker-link');
    await waitFor(() => {
      expect(link).toHaveAttribute(
        'href',
        'https://rutracker.org/forum/viewtopic.php?t=1',
      );
    });
    expect(link).toHaveAttribute('target', '_blank');
    expect(link).toHaveAttribute('rel', 'noopener noreferrer');
  });

  it('renders the tracker link with the raw GUID when the GUID is already a public http(s) URL', async () => {
    currentRules = [];
    vi.mocked(api).mockResolvedValueOnce(payloadWithGuid(
      'https://rutracker.org/forum/viewtopic.php?t=2',
    ));
    render(<Harness initialEntry="/watchdog?series_id=169&instance=homelab" />);
    const link = await screen.findByTestId('drawer-origin-tracker-link');
    expect(link).toHaveAttribute(
      'href',
      'https://rutracker.org/forum/viewtopic.php?t=2',
    );
  });

  it('does NOT render the tracker link when the GUID is opaque (e.g. magnet, hash)', async () => {
    currentRules = [];
    vi.mocked(api).mockResolvedValueOnce(payloadWithGuid('magnet:?xt=urn:btih:abc'));
    render(<Harness initialEntry="/watchdog?series_id=169&instance=homelab" />);
    await screen.findByTestId('drawer-section-origin');
    await waitFor(() => {
      expect(screen.queryByTestId('drawer-origin-tracker-link')).not.toBeInTheDocument();
    });
  });
});
