import { describe, expect, it, vi, beforeEach } from 'vitest';
import userEvent from '@testing-library/user-event';
import { render, screen, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { I18nextProvider } from 'react-i18next';
import { Toaster } from 'sonner';
import i18n from '@/i18n';
import { AutoFillQbitButton } from '../AutoFillQbitButton';

function wrap(node: React.ReactElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
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

  it('calls onDiscovered with the discovered fields on success', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({
        url: 'http://qbittorrent:8080', username: 'admin', category: 'sonarr',
      }), { status: 200, headers: { 'Content-Type': 'application/json' } }),
    ) as typeof fetch;
    const onDiscovered = vi.fn();
    const user = userEvent.setup();
    render(wrap(<AutoFillQbitButton instanceName="homelab" onDiscovered={onDiscovered} />));
    await user.click(screen.getByTestId('auto-fill-qbit'));
    await waitFor(() => expect(onDiscovered).toHaveBeenCalled());
    expect(onDiscovered.mock.calls[0]?.[0]).toEqual({
      url: 'http://qbittorrent:8080', username: 'admin', category: 'sonarr',
    });
  });

  it('toasts NO_QBIT_FOUND on 404', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ code: 'NO_QBIT_FOUND' }), {
        status: 404, headers: { 'Content-Type': 'application/json' },
      }),
    ) as typeof fetch;
    const onDiscovered = vi.fn();
    const user = userEvent.setup();
    render(wrap(<AutoFillQbitButton instanceName="homelab" onDiscovered={onDiscovered} />));
    await user.click(screen.getByTestId('auto-fill-qbit'));
    await waitFor(() => {
      // Matches both `autoFillNoQbit` translated copy and the raw key
      // (until 057b2 ships the i18n delta).
      expect(screen.getByText(/no qbit|qbittorrent not found|не найден|autoFillNoQbit/i)).toBeInTheDocument();
    });
    expect(onDiscovered).not.toHaveBeenCalled();
  });
});
