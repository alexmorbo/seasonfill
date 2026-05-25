import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest';
import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders } from '@/test-utils';
import { SecurityTab } from './SecurityTab';

const origFetch = globalThis.fetch;
afterEach(() => { globalThis.fetch = origFetch; });

function mockFetchSecConfig(protocol = 'https:') {
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
        auth: { session_ttl: '12h', secure_cookie: false, trusted_proxies: [] },
        security: { allow_private_targets: false },
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
    mockFetchSecConfig('http:');
    renderWithProviders(<SecurityTab />);
    await waitFor(() => {
      expect(screen.getByText(/TLS not detected/i)).toBeVisible();
    });
    expect(screen.queryByRole('switch', { name: /secure cookie/i })).toBeNull();
  });

  it('shows secure-cookie switch and no HTTP banner when on https://', async () => {
    mockFetchSecConfig('https:');
    renderWithProviders(<SecurityTab />);
    await waitFor(() => {
      expect(screen.getByRole('switch', { name: /secure cookie/i })).toBeVisible();
    });
    expect(screen.queryByText(/TLS not detected/i)).toBeNull();
  });

  it('PUT body contains auth.trusted_proxies = [] when list is empty', async () => {
    const captured: { body?: string } = {};
    mockFetchSecConfig('https:');
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
          auth: { session_ttl: '12h', secure_cookie: false, trusted_proxies: [] },
          security: { allow_private_targets: false },
        }),
        { status: 200, headers: { 'Content-Type': 'application/json', 'Last-Modified': 'Mon, 25 May 2026 12:00:00 GMT' } },
      );
    }) as typeof fetch;

    renderWithProviders(<SecurityTab />);
    // Wait for form to fully settle (Discard button appears only when isDirty=false
    // and data is loaded — Save is disabled, Discard too, just wait for Save button).
    await screen.findByRole('button', { name: /save/i });
    // Re-query after form settles (rhf reset publishes new defaults).
    const ttlInput = screen.getByLabelText(/session ttl/i);
    await userEvent.type(ttlInput, '0');
    await userEvent.click(screen.getByRole('button', { name: /save/i }));

    await waitFor(() => {
      expect(captured.body).toEqual(expect.any(String));
    });
    const sent = JSON.parse(captured.body ?? '{}') as Record<string, unknown>;
    const auth = sent.auth as Record<string, unknown>;
    expect(Array.isArray(auth.trusted_proxies)).toBe(true);
    expect(auth.trusted_proxies).toHaveLength(0);
  });

  it('PUT body contains security.allow_private_targets when toggle flipped on', async () => {
    const captured: { body?: string } = {};
    mockFetchSecConfig('https:');
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
          auth: { session_ttl: '12h', secure_cookie: false, trusted_proxies: [] },
          security: { allow_private_targets: false },
        }),
        { status: 200, headers: { 'Content-Type': 'application/json', 'Last-Modified': 'Mon, 25 May 2026 12:00:00 GMT' } },
      );
    }) as typeof fetch;

    renderWithProviders(<SecurityTab />);
    await screen.findByRole('button', { name: /save/i });

    const toggle = screen.getByRole('switch', { name: /allow private targets/i });
    await userEvent.click(toggle);
    await userEvent.click(screen.getByRole('button', { name: /save/i }));

    await waitFor(() => {
      expect(captured.body).toEqual(expect.any(String));
    });
    const sent = JSON.parse(captured.body ?? '{}') as Record<string, unknown>;
    const sec = sent.security as Record<string, unknown>;
    expect(sec.allow_private_targets).toBe(true);
    // Defence-in-depth — the existing auth block must NOT be wiped out
    // by the security-aware formToPayload (regression guard for spread
    // shadowing — same shape as 028k M-4).
    const auth = sent.auth as Record<string, unknown>;
    expect(auth.session_ttl).toEqual(expect.any(String));
  });

  it('renders the homelab toggle in its loaded state', async () => {
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
          auth: { session_ttl: '12h', secure_cookie: false, trusted_proxies: [] },
          security: { allow_private_targets: true },
        }),
        { status: 200, headers: { 'Content-Type': 'application/json', 'Last-Modified': 'Mon, 25 May 2026 12:00:00 GMT' } },
      );
    }) as typeof fetch;

    renderWithProviders(<SecurityTab />);
    const toggle = await screen.findByRole('switch', { name: /allow private targets/i });
    await waitFor(() => {
      expect(toggle).toHaveAttribute('data-state', 'checked');
    });
  });
});
