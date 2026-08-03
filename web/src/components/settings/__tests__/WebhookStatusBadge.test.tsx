import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { screen, waitFor } from '@testing-library/react';
import { renderWithProviders } from '@/test-utils';
import { WebhookStatusBadge } from '../WebhookStatusBadge';

const origFetch = globalThis.fetch;
beforeEach(() => {
  Object.defineProperty(window, 'location', {
    writable: true, value: { pathname: '/instances', assign: vi.fn() },
  });
});
afterEach(() => { globalThis.fetch = origFetch; });

const jsonResp = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });

describe('<WebhookStatusBadge />', () => {
  it('renders the loading skeleton while the status query is pending', async () => {
    globalThis.fetch = vi.fn(() => new Promise(() => {})) as typeof fetch;
    renderWithProviders(<WebhookStatusBadge name="alpha" />);
    expect(
      await screen.findByTestId('webhook-status-badge-loading'),
    ).toBeVisible();
  });

  it('renders the installed state when installed=true and no error', async () => {
    globalThis.fetch = vi.fn(async () =>
      jsonResp({ installed: true, notification_id: 7 }),
    ) as typeof fetch;
    renderWithProviders(<WebhookStatusBadge name="alpha" />);
    const badge = await screen.findByTestId('webhook-status-badge');
    expect(badge).toHaveAttribute('data-state', 'installed');
    expect(badge).toHaveTextContent(/webhook installed|вебхук установлен/i);
  });

  it('renders the not-installed state when installed=false and no error', async () => {
    globalThis.fetch = vi.fn(async () =>
      jsonResp({ installed: false }),
    ) as typeof fetch;
    renderWithProviders(<WebhookStatusBadge name="alpha" />);
    const badge = await screen.findByTestId('webhook-status-badge');
    expect(badge).toHaveAttribute('data-state', 'not-installed');
    expect(badge).toHaveTextContent(/webhook not installed|не установлен/i);
  });

  it('renders the error state with the message embedded', async () => {
    globalThis.fetch = vi.fn(async () =>
      jsonResp({ installed: false, error: 'sonarr unauthorized' }),
    ) as typeof fetch;
    renderWithProviders(<WebhookStatusBadge name="alpha" />);
    const badge = await screen.findByTestId('webhook-status-badge');
    expect(badge).toHaveAttribute('data-state', 'error');
    expect(badge).toHaveTextContent(/sonarr unauthorized/i);
    expect(badge).toHaveAttribute('title', 'sonarr unauthorized');
  });

  it('installing wins over error: renders the loader, not the red badge', async () => {
    globalThis.fetch = vi.fn(async () =>
      jsonResp({
        installed: false,
        installing: true,
        error: 'test_notification: sonarr could not reach seasonfill',
      }),
    ) as typeof fetch;
    renderWithProviders(<WebhookStatusBadge name="alpha" />);
    const badge = await screen.findByTestId('webhook-status-badge');
    expect(badge).toHaveAttribute('data-state', 'installing');
    expect(badge).not.toHaveAttribute('data-state', 'error');
    expect(badge).toHaveAttribute('role', 'status');
    expect(badge).toHaveTextContent(/setting up webhook|настройка вебхука/i);
  });

  it('error wins once installing clears (installing=false, error set)', async () => {
    globalThis.fetch = vi.fn(async () =>
      jsonResp({
        installed: false,
        installing: false,
        error: 'sonarr unauthorized',
      }),
    ) as typeof fetch;
    renderWithProviders(<WebhookStatusBadge name="alpha" />);
    const badge = await screen.findByTestId('webhook-status-badge');
    expect(badge).toHaveAttribute('data-state', 'error');
    expect(badge).toHaveTextContent(/sonarr unauthorized/i);
  });

  it('error precedence: error wins over installed=true', async () => {
    globalThis.fetch = vi.fn(async () =>
      jsonResp({ installed: true, error: 'reconciler crash' }),
    ) as typeof fetch;
    renderWithProviders(<WebhookStatusBadge name="alpha" />);
    await waitFor(() => {
      expect(screen.getByTestId('webhook-status-badge'))
        .toHaveAttribute('data-state', 'error');
    });
  });

  it('truncates long errors with ellipsis and keeps full text in title', async () => {
    const long = 'x'.repeat(120);
    globalThis.fetch = vi.fn(async () =>
      jsonResp({ installed: false, error: long }),
    ) as typeof fetch;
    renderWithProviders(<WebhookStatusBadge name="alpha" />);
    const badge = await screen.findByTestId('webhook-status-badge');
    expect(badge.textContent).toMatch(/…/);
    expect((badge.textContent ?? '').length).toBeLessThan(80);
    expect(badge).toHaveAttribute('title', long);
  });
});
