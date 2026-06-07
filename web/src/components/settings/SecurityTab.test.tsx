import { afterEach, beforeEach, describe, it, expect, vi } from 'vitest';
import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders } from '@/test-utils';
import { SecurityTab } from './SecurityTab';

const origFetch = globalThis.fetch;
let putBody: string | null = null;

beforeEach(() => {
  putBody = null;
  globalThis.fetch = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    if (url.endsWith('/api/v1/config/runtime') && (init?.method === 'PUT')) {
      putBody = init.body as string;
      const merged = JSON.parse(putBody) as Record<string, unknown>;
      return new Response(JSON.stringify({ ...merged, auto_generated_api_key: false, updated_at: new Date().toISOString() }),
        { status: 200, headers: { 'Content-Type': 'application/json' } });
    }
    if (url.endsWith('/api/v1/config/runtime')) {
      return new Response(JSON.stringify({
        cron: { enabled: true, schedule: '0 * * * *', on_start: false, jitter: '1m' },
        scan: { shutdown_grace: '60s', cooldown_sweep: '15m' },
        dry_run: false,
        global_rate_limit: { rpm: 30, burst: 10 },
        auth: {
          mode: 'forms', secure_cookie: false, trusted_proxies: [],
          local_bypass: false, local_networks: [],
          session_ttl: '12h', session_epoch: 1,
          oidc: { issuer: '', client_id: '', redirect_url: '', scopes: ['openid','profile','email'],
            username_claim: 'preferred_username', allowed_groups: [], groups_claim: 'groups',
            client_secret_configured: false, client_secret_env_override: false },
        },
        auto_generated_api_key: false, updated_at: new Date().toISOString(),
      }), { status: 200, headers: { 'Content-Type': 'application/json' } });
    }
    if (url.endsWith('/api/v1/auth/config')) {
      return new Response(JSON.stringify({ mode: 'forms', local_bypass: false, oidc_ready: false }),
        { status: 200, headers: { 'Content-Type': 'application/json' } });
    }
    return new Response('{}', { status: 200, headers: { 'Content-Type': 'application/json' } });
  }) as typeof fetch;
});
afterEach(() => { globalThis.fetch = origFetch; });

describe('<SecurityTab />', () => {
  it('renders the segmented control with current mode pill', async () => {
    renderWithProviders(<SecurityTab />);
    expect(await screen.findByTestId('auth-mode-segmented')).toBeVisible();
    expect(screen.getByTestId('auth-mode-pill').textContent).toMatch(/forms/i);
  });

  it('clicking a non-current mode opens the confirm dialog', async () => {
    renderWithProviders(<SecurityTab />);
    const oidcBtn = await screen.findByRole('radio', { name: 'OIDC' });
    await userEvent.click(oidcBtn);
    expect(await screen.findByTestId('auth-mode-confirm-dialog')).toBeVisible();
  });

  it('ack+confirm switches mode and dirties the form (enables Save)', async () => {
    renderWithProviders(<SecurityTab />);
    const oidcBtn = await screen.findByRole('radio', { name: 'OIDC' });
    await userEvent.click(oidcBtn);
    await userEvent.click(await screen.findByTestId('auth-mode-confirm-ack'));
    await userEvent.click(screen.getByTestId('auth-mode-confirm-confirm'));
    await waitFor(() =>
      expect(screen.getByRole('button', { name: /save|сохранить/i })).toBeEnabled(),
    );
    // OIDC fields visible (auto-fold open).
    expect(screen.getByLabelText(/issuer/i)).toBeVisible();
  });

  it('cancel preserves the current mode and form stays pristine', async () => {
    renderWithProviders(<SecurityTab />);
    const oidcBtn = await screen.findByRole('radio', { name: 'OIDC' });
    await userEvent.click(oidcBtn);
    await userEvent.click(await screen.findByTestId('auth-mode-confirm-cancel'));
    await waitFor(() =>
      expect(screen.queryByTestId('auth-mode-confirm-dialog')).toBeNull(),
    );
    expect(screen.getByRole('button', { name: /save|сохранить/i })).toBeDisabled();
  });

  it('save PUTs auth.mode change to /api/v1/config/runtime', async () => {
    renderWithProviders(<SecurityTab />);
    await userEvent.click(await screen.findByRole('radio', { name: 'Basic' }));
    await userEvent.click(await screen.findByTestId('auth-mode-confirm-ack'));
    await userEvent.click(screen.getByTestId('auth-mode-confirm-confirm'));
    await userEvent.click(screen.getByRole('button', { name: /save|сохранить/i }));
    await waitFor(() => {
      expect(putBody).not.toBeNull();
      const parsed = JSON.parse(putBody!) as { auth: { mode: string } };
      expect(parsed.auth.mode).toBe('basic');
    });
  });
});
