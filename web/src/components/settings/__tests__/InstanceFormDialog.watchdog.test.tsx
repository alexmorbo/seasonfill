import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest';
import type { ReactElement } from 'react';
import userEvent from '@testing-library/user-event';
import { render, screen, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { I18nextProvider } from 'react-i18next';
import { Toaster } from 'sonner';
import i18n from '@/i18n';
import { InstanceFormDialog } from '@/components/settings/InstanceFormDialog';

function wrap(node: ReactElement) {
  const qc = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0, staleTime: 0 },
      mutations: { retry: false },
    },
  });
  return (
    <I18nextProvider i18n={i18n}>
      <QueryClientProvider client={qc}>
        {node}
        <Toaster />
      </QueryClientProvider>
    </I18nextProvider>
  );
}

const origFetch = globalThis.fetch;

interface DefaultFetchOpts {
  webhookInstalled?: boolean;
}

function defaultFetch({ webhookInstalled = true }: DefaultFetchOpts = {}) {
  globalThis.fetch = vi.fn((url: RequestInfo | URL, init?: RequestInit) => {
    const u = typeof url === 'string' ? url : url.toString();
    const method = (init?.method ?? 'GET').toUpperCase();
    if (u.endsWith('/instances/homelab') && method === 'GET') {
      return Promise.resolve(new Response(JSON.stringify({
        name: 'homelab',
        url: 'http://sonarr:80',
        api_key: '***',
        mode: 'auto',
        dry_run: null,
      }), { status: 200, headers: { 'Content-Type': 'application/json' } }));
    }
    if (u.endsWith('/webhook/status')) {
      return Promise.resolve(new Response(JSON.stringify({ installed: webhookInstalled }), {
        status: 200, headers: { 'Content-Type': 'application/json' },
      }));
    }
    if (u.endsWith('/qbit/settings') && method === 'GET') {
      return Promise.resolve(new Response(JSON.stringify({
        url: 'http://qbittorrent:8080',
        username: 'admin',
        password_set: true,
        category: 'sonarr',
        poll_interval_minutes: 30,
        regrab_cooldown_hours: 120,
        max_consecutive_no_better: 3,
        custom_unregistered_msgs: [],
        enabled: true,
      }), { status: 200, headers: { 'Content-Type': 'application/json' } }));
    }
    if (u.endsWith('/discover/qbit')) {
      return Promise.resolve(new Response(JSON.stringify({
        url: 'http://qbit-discovered:8080',
        username: 'discovered',
        category: 'sonarr',
        name: 'qbit',
      }), { status: 200, headers: { 'Content-Type': 'application/json' } }));
    }
    return Promise.resolve(new Response('{}', { status: 200 }));
  }) as typeof fetch;
}

beforeEach(() => {
  Object.defineProperty(window, 'location', {
    writable: true, value: { pathname: '/instances', assign: vi.fn() },
  });
  defaultFetch();
});
afterEach(() => { globalThis.fetch = origFetch; });

describe('<InstanceFormDialog /> watchdog accordion integration', () => {
  it('renders the watchdog accordion section after the user expands it (edit mode)', async () => {
    const user = userEvent.setup();
    render(wrap(
      <InstanceFormDialog open onOpenChange={vi.fn()} mode="edit" initial={{ name: 'homelab' }} />,
    ));
    // Header is always rendered; body is mounted only when expanded.
    const watchdogHeader = await screen.findByText(/qbittorrent.*watchdog|qbittorrent\/watchdog/i);
    await user.click(watchdogHeader);
    await waitFor(() => {
      expect(screen.getByTestId('watchdog-section')).toBeInTheDocument();
    });
  });

  it('auto-fill button populates the qBittorrent URL into the parent form', async () => {
    const user = userEvent.setup();
    render(wrap(
      <InstanceFormDialog open onOpenChange={vi.fn()} mode="edit" initial={{ name: 'homelab' }} />,
    ));
    await user.click(await screen.findByText(/qbittorrent.*watchdog|qbittorrent\/watchdog/i));
    const fill = await screen.findByTestId('auto-fill-qbit');
    await user.click(fill);
    await waitFor(() => {
      const urlInput = screen.getByLabelText(/^qbittorrent url$/i) as HTMLInputElement;
      expect(urlInput.value).toBe('http://qbit-discovered:8080');
    });
  });

  it('disables the enabled-switch when webhook.installed = false', async () => {
    defaultFetch({ webhookInstalled: false });
    const user = userEvent.setup();
    render(wrap(
      <InstanceFormDialog open onOpenChange={vi.fn()} mode="edit" initial={{ name: 'homelab' }} />,
    ));
    await user.click(await screen.findByText(/qbittorrent.*watchdog|qbittorrent\/watchdog/i));
    const sw = await screen.findByRole('switch', { name: /watchdog enabled/i });
    await waitFor(() => expect(sw).toBeDisabled());
  });
});
