import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest';
import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders } from '@/test-utils';
import { SecurityTab } from './SecurityTab';

const origFetch = globalThis.fetch;
afterEach(() => { globalThis.fetch = origFetch; });

interface CfgOverrides {
  protocol?: string;
  sessionTTL?: string;
  mode?: string;
  localBypass?: boolean;
  localNetworks?: string[];
}

function mockFetchSecConfig({
  protocol = 'https:',
  sessionTTL = '12h',
  mode = 'forms',
  localBypass = false,
  localNetworks = [],
}: CfgOverrides = {}) {
  Object.defineProperty(window, 'location', {
    writable: true,
    value: { pathname: '/settings', assign: vi.fn(), protocol },
  });
  globalThis.fetch = vi.fn(async (_u: RequestInfo | URL, init?: RequestInit) => {
    if (init?.method === 'PUT') {
      return new Response(
        JSON.stringify({}),
        { status: 200, headers: { 'Content-Type': 'application/json' } },
      );
    }
    return new Response(
      JSON.stringify({
        cron: { enabled: true, schedule: '0 */6 * * *', on_start: false, jitter: '1m' },
        scan: { shutdown_grace: '60s', cooldown_sweep: '15m' },
        dry_run: false,
        global_rate_limit: { rpm: 30, burst: 10 },
        auth: {
          session_ttl: sessionTTL, secure_cookie: false, trusted_proxies: [],
          mode, local_bypass: localBypass, local_networks: localNetworks,
          session_epoch: 7,
        },
      }),
      { status: 200, headers: { 'Content-Type': 'application/json', 'Last-Modified': 'Mon, 25 May 2026 12:00:00 GMT' } },
    );
  }) as typeof fetch;
}

describe('<SecurityTab />', () => {
  beforeEach(() => {
    Object.defineProperty(window, 'location', {
      writable: true,
      value: { pathname: '/settings', assign: vi.fn(), protocol: 'https:' },
    });
  });

  it('shows HTTP banner and hides secure-cookie switch when on plain http://', async () => {
    mockFetchSecConfig({ protocol: 'http:' });
    renderWithProviders(<SecurityTab />);
    await waitFor(() => {
      expect(screen.getByText(/TLS not detected/i)).toBeVisible();
    });
    expect(screen.queryByRole('switch', { name: /secure cookie/i })).toBeNull();
  });

  it('shows secure-cookie switch and no HTTP banner when on https://', async () => {
    mockFetchSecConfig({ protocol: 'https:' });
    renderWithProviders(<SecurityTab />);
    await waitFor(() => {
      expect(screen.getByRole('switch', { name: /secure cookie/i })).toBeVisible();
    });
    expect(screen.queryByText(/TLS not detected/i)).toBeNull();
  });

  it('parses Go compound duration "12h0m0s" without surfacing the unparseable banner', async () => {
    mockFetchSecConfig({ sessionTTL: '12h0m0s' });
    renderWithProviders(<SecurityTab />);
    await screen.findByRole('button', { name: /save/i });
    expect(screen.queryByText(/Unparseable session TTL/i)).toBeNull();
    const ttlInput = screen.getByLabelText(/session ttl/i) as HTMLInputElement;
    await waitFor(() => expect(ttlInput.value).toBe('720'));
  });

  it('renders the auth-mode dropdown with the loaded value', async () => {
    mockFetchSecConfig({ mode: 'basic' });
    renderWithProviders(<SecurityTab />);
    await screen.findByRole('button', { name: /save/i });
    await waitFor(() => {
      expect(
        screen.getByText(/Browser shows a Basic Auth popup/i),
      ).toBeVisible();
    });
  });

  it('selecting None reveals the warning Alert', async () => {
    mockFetchSecConfig({ mode: 'forms' });
    renderWithProviders(<SecurityTab />);
    await screen.findByRole('button', { name: /save/i });
    const trigger = screen.getByRole('combobox', { name: /authentication$/i });
    await userEvent.click(trigger);
    await userEvent.click(await screen.findByRole('option', { name: /None \(no authentication\)/i }));
    expect(await screen.findByText(/No authentication enabled/i)).toBeVisible();
  });

  it('selecting Disabled-for-Local reveals the LocalNetworksEditor and seeds defaults', async () => {
    mockFetchSecConfig({ mode: 'forms', localBypass: false, localNetworks: [] });
    renderWithProviders(<SecurityTab />);
    await screen.findByRole('button', { name: /save/i });
    const trigger = screen.getByRole('combobox', { name: /authentication required/i });
    await userEvent.click(trigger);
    await userEvent.click(await screen.findByRole('option', { name: /disabled for local addresses/i }));
    // Section heading appears and the default RFC1918 chip shows up.
    expect(await screen.findByText(/Local networks/i)).toBeVisible();
    expect(screen.getByText('10.0.0.0/8')).toBeVisible();
  });

  it('PUT body contains auth.mode + auth.local_bypass + auth.local_networks; omits session_epoch', async () => {
    const captured: { body?: string } = {};
    mockFetchSecConfig({ mode: 'forms', localBypass: false });
    globalThis.fetch = vi.fn(async (_u: RequestInfo | URL, init?: RequestInit) => {
      if (init?.method === 'PUT') {
        if (typeof init.body === 'string') captured.body = init.body;
        return new Response(
          JSON.stringify({}),
          { status: 200, headers: { 'Content-Type': 'application/json' } },
        );
      }
      return new Response(
        JSON.stringify({
          cron: { enabled: true, schedule: '0 */6 * * *', on_start: false, jitter: '1m' },
          scan: { shutdown_grace: '60s', cooldown_sweep: '15m' },
          dry_run: false,
          global_rate_limit: { rpm: 30, burst: 10 },
          auth: {
            session_ttl: '12h', secure_cookie: false, trusted_proxies: [],
            mode: 'forms', local_bypass: false, local_networks: [],
            session_epoch: 7,
          },
        }),
        { status: 200, headers: { 'Content-Type': 'application/json', 'Last-Modified': 'Mon, 25 May 2026 12:00:00 GMT' } },
      );
    }) as typeof fetch;

    renderWithProviders(<SecurityTab />);
    await screen.findByRole('button', { name: /save/i });
    const ttlInput = screen.getByLabelText(/session ttl/i);
    await userEvent.type(ttlInput, '0');
    await userEvent.click(screen.getByRole('button', { name: /save/i }));

    await waitFor(() => expect(captured.body).toEqual(expect.any(String)));
    const sent = JSON.parse(captured.body ?? '{}') as Record<string, unknown>;
    const auth = sent.auth as Record<string, unknown>;
    expect(auth.mode).toBe('forms');
    expect(auth.local_bypass).toBe(false);
    expect(Array.isArray(auth.local_networks)).toBe(true);
    expect(auth.session_epoch).toBeUndefined();
  });
});
