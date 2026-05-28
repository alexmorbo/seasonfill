import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest';
import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders } from '@/test-utils';
import { GeneralTab } from './GeneralTab';

const origFetch = globalThis.fetch;
beforeEach(() => {
  Object.defineProperty(window, 'location', {
    writable: true,
    value: { pathname: '/settings', assign: vi.fn(), protocol: 'http:' },
  });
});
afterEach(() => { globalThis.fetch = origFetch; });

function mockFetchConfig(schedule: string) {
  globalThis.fetch = vi.fn(async (_u: RequestInfo | URL, init?: RequestInit) => {
    if (init?.method === 'PUT') {
      return new Response(
        JSON.stringify({}),
        { status: 200, headers: { 'Content-Type': 'application/json', 'Last-Modified': 'Mon, 25 May 2026 12:00:00 GMT' } },
      );
    }
    return new Response(
      JSON.stringify({
        cron: { enabled: true, schedule, on_start: false, jitter: '1m' },
        scan: { shutdown_grace: '60s', cooldown_sweep: '15m' },
        dry_run: false,
        global_rate_limit: { rpm: 30, burst: 10 },
        auth: { session_ttl: '12h', secure_cookie: false, trusted_proxies: [] },
      }),
      { status: 200, headers: { 'Content-Type': 'application/json', 'Last-Modified': 'Mon, 25 May 2026 12:00:00 GMT' } },
    );
  }) as typeof fetch;
}

describe('<GeneralTab />', () => {
  it('renders cron preview for valid expression', async () => {
    mockFetchConfig('0 */6 * * *');
    renderWithProviders(<GeneralTab />);
    // cronstrue renders a human description — the text contains "every 6 hours"
    await waitFor(() => {
      expect(screen.getByText(/every 6 hours/i)).toBeVisible();
    });
  });

  it('Save is disabled when cron expression is invalid', async () => {
    mockFetchConfig('0 */6 * * *');
    renderWithProviders(<GeneralTab />);
    await screen.findByText(/every 6 hours/i);

    const schedInput = screen.getByPlaceholderText(/0 \*\/6/i);
    await userEvent.clear(schedInput);
    await userEvent.type(schedInput, 'not-a-cron');
    await userEvent.tab(); // trigger onBlur validation

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /save/i })).toBeDisabled();
    });
  });

  it('Save is enabled when cron expression is corrected', async () => {
    mockFetchConfig('bad-cron');
    renderWithProviders(<GeneralTab />);
    await screen.findByRole('button', { name: /save/i });

    const schedInput = screen.getByPlaceholderText(/0 \*\/6/i);
    await userEvent.clear(schedInput);
    await userEvent.type(schedInput, '0 */6 * * *');
    await userEvent.tab();

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /save/i })).not.toBeDisabled();
    });
  });

  it('Save stays disabled while the PUT is in flight (anti-stampede)', async () => {
    // Hold the PUT open with a deferred promise so the mutation stays
    // pending. While it's in flight the Save button must be disabled,
    // preventing the rapid-click 412 stampede.
    let releasePut: () => void = () => {};
    const putGate = new Promise<void>((res) => { releasePut = res; });
    globalThis.fetch = vi.fn(async (_u: RequestInfo | URL, init?: RequestInit) => {
      if (init?.method === 'PUT') {
        await putGate;
        return new Response(
          JSON.stringify({}),
          { status: 200, headers: { 'Content-Type': 'application/json', 'Last-Modified': 'Mon, 25 May 2026 12:00:00 GMT' } },
        );
      }
      return new Response(
        JSON.stringify({
          cron: { enabled: true, schedule: '0 */6 * * *', on_start: false, jitter: '1m' },
          scan: { shutdown_grace: '60s', cooldown_sweep: '15m' },
          dry_run: false,
          global_rate_limit: { rpm: 30, burst: 10 },
          auth: { session_ttl: '12h', secure_cookie: false, trusted_proxies: [] },
        }),
        { status: 200, headers: { 'Content-Type': 'application/json', 'Last-Modified': 'Mon, 25 May 2026 12:00:00 GMT' } },
      );
    }) as typeof fetch;

    renderWithProviders(<GeneralTab />);
    await screen.findByText(/every 6 hours/i);

    // Dirty the form so Save is enabled, then fire the (slow) save.
    const schedInput = screen.getByPlaceholderText(/0 \*\/6/i);
    await userEvent.clear(schedInput);
    await userEvent.type(schedInput, '0 */4 * * *');
    const save = screen.getByRole('button', { name: /save/i });
    await waitFor(() => expect(save).not.toBeDisabled());
    await userEvent.click(save);

    // The mutation is pending (PUT gated) — Save must be disabled so a
    // second click cannot fire another PUT with the same stale header.
    await waitFor(() => {
      expect(screen.getByRole('button', { name: /saving/i })).toBeDisabled();
    });

    // Release the PUT and let the mutation settle.
    releasePut();
    await waitFor(() => {
      expect(screen.queryByRole('button', { name: /saving/i })).toBeNull();
    });
  });

  it('412 PUT response shows stale toast and refetches', async () => {
    let callCount = 0;
    globalThis.fetch = vi.fn(async (_u: RequestInfo | URL, init?: RequestInit) => {
      if (init?.method === 'PUT') {
        return new Response(
          JSON.stringify({ error: 'stale', code: 'STALE_WRITE' }),
          { status: 412, headers: { 'Content-Type': 'application/json' } },
        );
      }
      callCount++;
      return new Response(
        JSON.stringify({
          cron: { enabled: true, schedule: '0 */6 * * *', on_start: false, jitter: '1m' },
          scan: { shutdown_grace: '60s', cooldown_sweep: '15m' },
          dry_run: false,
          global_rate_limit: { rpm: 30, burst: 10 },
          auth: { session_ttl: '12h', secure_cookie: false, trusted_proxies: [] },
        }),
        { status: 200, headers: { 'Content-Type': 'application/json', 'Last-Modified': 'Mon, 25 May 2026 12:00:00 GMT' } },
      );
    }) as typeof fetch;

    renderWithProviders(<GeneralTab />);
    // Wait for the initial GET to settle (form rendered).
    await waitFor(() => screen.getByRole('button', { name: /save/i }));
    // SNAPSHOT calls now — this is the M-1 baseline. The 412 → refetch
    // must advance this counter STRICTLY after the click.
    const callsBefore = callCount;

    // Dirty the form so Save is enabled.
    const schedInput = screen.getByPlaceholderText(/0 \*\/6/i);
    await userEvent.clear(schedInput);
    await userEvent.type(schedInput, '0 */4 * * *');

    await userEvent.click(screen.getByRole('button', { name: /save/i }));

    // After 412, the mutation's onError handler calls qc.invalidateQueries
    // which triggers a re-fetch. callCount must advance past callsBefore.
    await waitFor(() => {
      expect(callCount).toBeGreaterThan(callsBefore);
    });
  });
});
