import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders } from '@/test-utils';
import { Settings } from './Settings';
import i18n from '@/i18n';
import { renderPageWithTitle } from '@/test-utils-title';

const navigateSpy = vi.fn();
vi.mock('react-router-dom', async () => {
  const actual = await vi.importActual<typeof import('react-router-dom')>('react-router-dom');
  return { ...actual, useNavigate: () => navigateSpy };
});

const origFetch = globalThis.fetch;
function setHash(hash: string) {
  Object.defineProperty(window, 'location', {
    writable: true,
    value: { pathname: '/settings', hash, assign: vi.fn() },
  });
}

beforeEach(() => {
  globalThis.fetch = vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    if (url.endsWith('/api/v1/config/runtime')) {
      return new Response(JSON.stringify({
        cron: { enabled: true, schedule: '0 * * * *', on_start: false, jitter: '1m' },
        scan: { shutdown_grace: '60s', cooldown_sweep: '15m' },
        dry_run: false,
        global_rate_limit: { rpm: 30, burst: 10 },
        auth: { mode: 'forms', secure_cookie: false, trusted_proxies: [], local_bypass: false, local_networks: [], session_ttl: '12h', session_epoch: 0, oidc: { issuer: '', client_id: '', redirect_url: '', scopes: ['openid'], username_claim: 'preferred_username', allowed_groups: [], groups_claim: 'groups', client_secret_configured: false, client_secret_env_override: false } },
        auto_generated_api_key: false,
        updated_at: new Date().toISOString(),
      }), { status: 200, headers: { 'Content-Type': 'application/json' } });
    }
    if (url.endsWith('/api/v1/auth/config')) {
      return new Response(JSON.stringify({ mode: 'forms', local_bypass: false, oidc_ready: false }),
        { status: 200, headers: { 'Content-Type': 'application/json' } });
    }
    if (url.endsWith('/api/v1/webhooks/status')) {
      return new Response(JSON.stringify({ items: [], healthy_count: 0, unhealthy_count: 0 }),
        { status: 200, headers: { 'Content-Type': 'application/json' } });
    }
    return new Response('{}', { status: 200, headers: { 'Content-Type': 'application/json' } });
  }) as typeof fetch;
  navigateSpy.mockReset();
});
afterEach(() => { globalThis.fetch = origFetch; });

describe('<Settings />', () => {
  it('renders three tab triggers (General + Security + Integrations)', async () => {
    setHash('');
    renderWithProviders(<Settings />, { route: '/settings' });
    expect(await screen.findByRole('tab', { name: /general/i })).toBeVisible();
    expect(screen.getByRole('tab', { name: /security/i })).toBeVisible();
    expect(screen.getByRole('tab', { name: /integrations|интеграции/i })).toBeVisible();
    expect(screen.queryByRole('tab', { name: /^instances$/i })).toBeNull();
  });

  it('default tab is General when hash is empty', async () => {
    setHash('');
    renderWithProviders(<Settings />, { route: '/settings' });
    await waitFor(() => expect(screen.getByLabelText(/cron expression/i)).toBeVisible());
  });

  it('switches to Security tab on click', async () => {
    setHash('');
    renderWithProviders(<Settings />, { route: '/settings' });
    await userEvent.click(screen.getByRole('tab', { name: /security/i }));
    await waitFor(() => expect(screen.getByText(/session ttl/i)).toBeVisible());
  });

  it('switches to Integrations tab on click', async () => {
    setHash('');
    renderWithProviders(<Settings />, { route: '/settings' });
    await userEvent.click(screen.getByRole('tab', { name: /integrations|интеграции/i }));
    await waitFor(() =>
      expect(screen.getByTestId('integrations-tab')).toBeVisible(),
    );
  });

  it('selects Integrations tab from #integrations hash on mount', async () => {
    setHash('#integrations');
    renderWithProviders(<Settings />, { route: '/settings#integrations' });
    await waitFor(() =>
      expect(screen.getByTestId('integrations-tab')).toBeVisible(),
    );
  });

  it('redirects to /instances when legacy /settings#instances hash is detected', async () => {
    setHash('#instances');
    renderWithProviders(<Settings />, { route: '/settings#instances' });
    await waitFor(() => {
      expect(navigateSpy).toHaveBeenCalledWith('/instances', { replace: true });
    });
  });

  it('Integrations tab renders the webhook health aggregate panel', async () => {
    setHash('');
    renderWithProviders(<Settings />, { route: '/settings' });
    await userEvent.click(screen.getByRole('tab', { name: /integrations|интеграции/i }));
    await waitFor(() =>
      expect(screen.getByTestId('integrations-tab')).toBeVisible(),
    );
  });

  it('sets the topbar page title via useSetPageTitle', async () => {
    setHash('');
    const { getTitle } = renderPageWithTitle(<Settings />, { route: '/settings' });
    await waitFor(() => {
      expect(getTitle()).toBe(i18n.t('settings.title'));
    });
  });
});
