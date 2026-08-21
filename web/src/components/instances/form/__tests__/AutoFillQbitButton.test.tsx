import { describe, expect, it, vi, beforeEach } from 'vitest';
import type { ReactElement } from 'react';
import userEvent from '@testing-library/user-event';
import { render, screen, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { I18nextProvider } from 'react-i18next';
import { Toaster } from 'sonner';
import i18n from '@/i18n';
import { AutoFillQbitButton, type AutoFillFields } from '../AutoFillQbitButton';

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

describe('<AutoFillQbitButton />', () => {
  beforeEach(() => { globalThis.fetch = vi.fn(); });

  it('calls onApply with the discovered fields on click', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({
        url: 'http://qbittorrent:8080', username: 'admin', category: 'sonarr',
      }), { status: 200, headers: { 'Content-Type': 'application/json' } }),
    ) as typeof fetch;
    const onApply = vi.fn<(fields: AutoFillFields) => { changed: boolean }>(() => ({ changed: true }));
    const user = userEvent.setup();
    render(wrap(<AutoFillQbitButton instanceName="homelab" arrLabel="Sonarr" onApply={onApply} />));
    await user.click(screen.getByTestId('auto-fill-qbit'));
    await waitFor(() => expect(onApply).toHaveBeenCalled());
    expect(onApply.mock.calls[0]?.[0]).toEqual({
      url: 'http://qbittorrent:8080', username: 'admin', category: 'sonarr',
    });
  });

  it('toasts success when onApply reports changed=true', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({
        url: 'http://qbittorrent:8080', category: 'sonarr',
      }), { status: 200, headers: { 'Content-Type': 'application/json' } }),
    ) as typeof fetch;
    const onApply = vi.fn(() => ({ changed: true }));
    const user = userEvent.setup();
    render(wrap(<AutoFillQbitButton instanceName="homelab" arrLabel="Sonarr" onApply={onApply} />));
    await user.click(screen.getByTestId('auto-fill-qbit'));
    await waitFor(() => {
      const toasts = document.querySelectorAll('[data-sonner-toast]');
      expect(toasts.length).toBe(1);
    });
  });

  it('does NOT toast when onApply reports changed=false (idempotent click)', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({
        url: 'http://qbittorrent:8080', category: 'sonarr',
      }), { status: 200, headers: { 'Content-Type': 'application/json' } }),
    ) as typeof fetch;
    const onApply = vi.fn(() => ({ changed: false }));
    const user = userEvent.setup();
    render(wrap(<AutoFillQbitButton instanceName="homelab" arrLabel="Sonarr" onApply={onApply} />));
    await user.click(screen.getByTestId('auto-fill-qbit'));
    await waitFor(() => expect(onApply).toHaveBeenCalled());
    // Give sonner a tick to render any pending toast.
    await new Promise((r) => setTimeout(r, 50));
    const toasts = document.querySelectorAll('[data-sonner-toast]');
    expect(toasts.length).toBe(0);
  });

  it('toasts NO_QBIT_FOUND on 404', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ code: 'NO_QBIT_FOUND' }), {
        status: 404, headers: { 'Content-Type': 'application/json' },
      }),
    ) as typeof fetch;
    const onApply = vi.fn(() => ({ changed: false }));
    const user = userEvent.setup();
    render(wrap(<AutoFillQbitButton instanceName="homelab" arrLabel="Sonarr" onApply={onApply} />));
    await user.click(screen.getByTestId('auto-fill-qbit'));
    await waitFor(() => {
      expect(screen.getByText(/no qbit|qbittorrent not found|не найден|autoFillNoQbit/i)).toBeInTheDocument();
    });
    expect(onApply).not.toHaveBeenCalled();
  });

  it('does NOT fire the discover request without a click (no enabled-flag autorun)', async () => {
    const spy = vi.fn();
    globalThis.fetch = spy as typeof fetch;
    const onApply = vi.fn(() => ({ changed: false }));
    render(wrap(<AutoFillQbitButton instanceName="homelab" arrLabel="Sonarr" onApply={onApply} />));
    // Tick the event loop; nothing should have fetched.
    await new Promise((r) => setTimeout(r, 30));
    expect(spy).not.toHaveBeenCalled();
    expect(onApply).not.toHaveBeenCalled();
  });

  it('issues exactly one network request per click', async () => {
    const spy = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({
        url: 'http://qbittorrent:8080', category: 'sonarr',
      }), { status: 200, headers: { 'Content-Type': 'application/json' } }),
    );
    globalThis.fetch = spy as typeof fetch;
    const onApply = vi.fn(() => ({ changed: true }));
    const user = userEvent.setup();
    render(wrap(<AutoFillQbitButton instanceName="homelab" arrLabel="Sonarr" onApply={onApply} />));
    await user.click(screen.getByTestId('auto-fill-qbit'));
    await waitFor(() => expect(onApply).toHaveBeenCalled());
    expect(spy).toHaveBeenCalledTimes(1);
  });

  it('renders the arrLabel in the button copy (radarr)', async () => {
    const onApply = vi.fn(() => ({ changed: false }));
    render(wrap(
      <AutoFillQbitButton instanceName="rad" arrLabel="Radarr" onApply={onApply} />,
    ));
    expect(screen.getByTestId('auto-fill-qbit')).toHaveTextContent(/radarr/i);
    expect(screen.getByTestId('auto-fill-qbit')).not.toHaveTextContent(/sonarr/i);
  });

  it('interpolates arrLabel into the NO_QBIT_FOUND toast (radarr)', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ code: 'NO_QBIT_FOUND' }), {
        status: 404, headers: { 'Content-Type': 'application/json' },
      }),
    ) as typeof fetch;
    const onApply = vi.fn(() => ({ changed: false }));
    const user = userEvent.setup();
    render(wrap(
      <AutoFillQbitButton instanceName="rad" arrLabel="Radarr" onApply={onApply} />,
    ));
    await user.click(screen.getByTestId('auto-fill-qbit'));
    await waitFor(() => {
      const toast = document.querySelector('[data-sonner-toast]');
      expect(toast).not.toBeNull();
      expect(toast?.textContent ?? '').toMatch(/radarr/i);
    });
    expect(onApply).not.toHaveBeenCalled();
  });
});
