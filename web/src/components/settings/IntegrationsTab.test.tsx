import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders } from '@/test-utils';
import { IntegrationsTab } from './IntegrationsTab';

const origFetch = globalThis.fetch;

let installCalled: string | null = null;
function defaultFetch(aggregate: unknown) {
  return vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    if (url.endsWith('/api/v1/webhooks/status')) {
      return new Response(JSON.stringify(aggregate),
        { status: 200, headers: { 'Content-Type': 'application/json' } });
    }
    if (url.endsWith('/api/v1/config/runtime')) {
      return new Response(JSON.stringify({
        cron: {}, scan: {}, dry_run: false, global_rate_limit: {},
        auth: {}, auto_generated_api_key: false, updated_at: new Date().toISOString(),
      }), { status: 200, headers: { 'Content-Type': 'application/json' } });
    }
    if (url.match(/\/api\/v1\/instances\/[^/]+\/webhook\/install$/) && init?.method === 'POST') {
      installCalled = url;
      return new Response(JSON.stringify({ ok: true }),
        { status: 200, headers: { 'Content-Type': 'application/json' } });
    }
    return new Response('{}', { status: 200, headers: { 'Content-Type': 'application/json' } });
  }) as typeof fetch;
}

beforeEach(() => { installCalled = null; });
afterEach(() => { globalThis.fetch = origFetch; });

describe('<IntegrationsTab />', () => {
  it('renders skeletons while aggregate is pending, then rows', async () => {
    globalThis.fetch = defaultFetch({
      items: [
        { instance_name: 'homelab', installed: true,  healthy: true,
          notification_id: 1, url: 'http://x/webhook/sonarr/homelab' },
        { instance_name: '4k',      installed: false, healthy: false,
          error: 'reconcile failed' },
      ],
      healthy_count: 1, unhealthy_count: 1,
    });
    renderWithProviders(<IntegrationsTab />);
    await screen.findByText('homelab');
    expect(screen.getByText('4k')).toBeVisible();
  });

  it('renders ok and error pills with correct status data-attrs', async () => {
    globalThis.fetch = defaultFetch({
      items: [
        { instance_name: 'homelab', installed: true,  healthy: true },
        { instance_name: '4k',      installed: false, healthy: false, error: 'boom' },
      ],
      healthy_count: 1, unhealthy_count: 1,
    });
    renderWithProviders(<IntegrationsTab />);
    await waitFor(() => {
      const okPill = document.querySelector('[data-status="ok"]');
      const errPill = document.querySelector('[data-status="error"]');
      expect(okPill).toBeTruthy();
      expect(errPill).toBeTruthy();
    });
  });

  it('Reinstall button fires POST /api/v1/instances/<name>/webhook/install', async () => {
    globalThis.fetch = defaultFetch({
      items: [{ instance_name: '4k', installed: false, healthy: false, error: 'boom' }],
      healthy_count: 0, unhealthy_count: 1,
    });
    renderWithProviders(<IntegrationsTab />);
    const btn = await screen.findByTestId('integrations-webhook-reinstall');
    await userEvent.click(btn);
    await waitFor(() => {
      expect(installCalled).toMatch(/\/api\/v1\/instances\/4k\/webhook\/install$/);
    });
  });

  it('renders the no-instances copy with /instances link when aggregate is empty', async () => {
    globalThis.fetch = defaultFetch({ items: [], healthy_count: 0, unhealthy_count: 0 });
    renderWithProviders(<IntegrationsTab />);
    await waitFor(() => {
      expect(screen.getByRole('link', { name: /instances|инстансы/i })).toHaveAttribute('href', '/instances');
    });
  });

  it('renders perInstanceNote when qbit_defaults is absent on runtime config', async () => {
    globalThis.fetch = defaultFetch({ items: [], healthy_count: 0, unhealthy_count: 0 });
    renderWithProviders(<IntegrationsTab />);
    await waitFor(() => {
      expect(
        screen.getByText(/configured per-instance only|настраивается на инстансе/i),
      ).toBeVisible();
    });
  });
});
