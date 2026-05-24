import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders } from '@/test-utils';
import { InstanceFormDialog } from './InstanceFormDialog';

const origFetch = globalThis.fetch;
beforeEach(() => {
  Object.defineProperty(window, 'location', {
    writable: true, value: { pathname: '/settings', assign: vi.fn() },
  });
});
afterEach(() => { globalThis.fetch = origFetch; });

const jsonResp = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });

describe('<InstanceFormDialog />', () => {
  it('name input is disabled in edit mode', async () => {
    renderWithProviders(
      <InstanceFormDialog
        open
        onOpenChange={() => {}}
        mode="edit"
        initial={{ name: 'alpha', url: 'http://x', mode: 'auto' }}
      />,
    );
    const nameInput = await screen.findByLabelText(/name/i);
    expect(nameInput).toBeDisabled();
  });

  it('shows the encrypted-at-rest badge', async () => {
    renderWithProviders(
      <InstanceFormDialog open onOpenChange={() => {}} mode="create" />,
    );
    expect(await screen.findByText(/encrypted at rest/i)).toBeVisible();
  });

  it('clicking Test connection calls /instances/test with the form values', async () => {
    const captured: { url?: string; body?: string } = {};
    globalThis.fetch = vi.fn(async (u: RequestInfo | URL, init?: RequestInit) => {
      captured.url = typeof u === 'string' ? u : u.toString();
      if (typeof init?.body === 'string') captured.body = init.body;
      return jsonResp({ ok: true, version: '4.0.0.999' }, 200);
    }) as typeof fetch;

    renderWithProviders(
      <InstanceFormDialog open onOpenChange={() => {}} mode="create" />,
    );
    await userEvent.type(await screen.findByLabelText(/api key/i), 'sekrit');
    await userEvent.click(screen.getByRole('button', { name: /test connection/i }));
    await waitFor(() => {
      expect(captured.url).toBe('/api/v1/instances/test');
    });
    expect(JSON.parse(captured.body ?? '{}')).toEqual({
      url: 'http://sonarr:8989',
      api_key: 'sekrit',
    });
    expect(await screen.findByText(/OK — Sonarr 4\.0\.0\.999/i)).toBeVisible();
  });

  it('edit submit with blank api_key sends api_key="" (server preserves)', async () => {
    const captured: { body?: string | undefined; method?: string | undefined } = {};
    globalThis.fetch = vi.fn(async (_u: RequestInfo | URL, init?: RequestInit) => {
      captured.method = init?.method;
      if (typeof init?.body === 'string') captured.body = init.body;
      return jsonResp({ name: 'alpha', api_key: '***' }, 200);
    }) as typeof fetch;

    renderWithProviders(
      <InstanceFormDialog
        open
        onOpenChange={() => {}}
        mode="edit"
        initial={{ name: 'alpha', url: 'http://x', mode: 'auto' }}
      />,
    );
    await userEvent.click(screen.getByRole('button', { name: /^save$/i }));
    await waitFor(() => expect(captured.method).toBe('PUT'));
    const sent = JSON.parse(captured.body ?? '{}') as { api_key: string };
    expect(sent.api_key).toBe('');
  });
});
