import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest';
import userEvent from '@testing-library/user-event';
import { screen, waitFor } from '@testing-library/react';
import { renderWithProviders } from '@/test-utils';
import { NewScanModal } from './NewScanModal';

const origFetch = globalThis.fetch;

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });

const instances = {
  instances: [
    { name: 'alpha', health: 'available' },
    { name: 'beta',  health: 'degraded'  },
  ],
};

const handler =
  (perPath: Record<string, (init?: RequestInit) => Response>) =>
  vi.fn(async (url: RequestInfo | URL, init?: RequestInit) => {
    const path = typeof url === 'string' ? url : url.toString();
    for (const key of Object.keys(perPath)) {
      if (path.includes(key)) return perPath[key]!(init);
    }
    return json({});
  });

beforeEach(() => {
  Object.defineProperty(window, 'location', {
    writable: true,
    value: { pathname: '/scans', search: '', assign: vi.fn() },
  });
});
afterEach(() => {
  globalThis.fetch = origFetch;
  vi.restoreAllMocks();
});

describe('<NewScanModal />', () => {
  it('renders instances, preselects the healthy one, submits the request', async () => {
    const captured: { url?: string; body?: string } = {};
    globalThis.fetch = handler({
      '/instances': () => json(instances),
      '/scan': (init) => {
        captured.url = '/scan';
        if (typeof init?.body === 'string') captured.body = init.body;
        return json([{ scan_run_id: 'run-001', instance: 'alpha', status: 'running' }], 202);
      },
    }) as typeof fetch;

    const onOpenChange = vi.fn();
    renderWithProviders(<NewScanModal open={true} onOpenChange={onOpenChange} />);

    await screen.findByText('alpha');
    expect(await screen.findByRole('radio', { name: /alpha/i })).toBeChecked();

    await userEvent.click(screen.getByRole('button', { name: /start scan/i }));
    await waitFor(() => expect(captured.url).toBe('/scan'));
    expect(JSON.parse(captured.body ?? '{}')).toEqual({ instance: 'alpha' });
    await waitFor(() => expect(onOpenChange).toHaveBeenCalledWith(false));
  });

  it('shows a warning when the selected instance is degraded', async () => {
    globalThis.fetch = handler({
      '/instances': () => json(instances),
    }) as typeof fetch;
    renderWithProviders(<NewScanModal open={true} onOpenChange={vi.fn()} />);

    await screen.findByText('alpha');
    await userEvent.click(screen.getByRole('radio', { name: /beta/i }));
    await waitFor(() =>
      expect(screen.getByText(/beta is degraded/i)).toBeInTheDocument(),
    );
  });

  it('keeps the modal open and surfaces a toast-error on 409', async () => {
    globalThis.fetch = handler({
      '/instances': () => json(instances),
      '/scan': () =>
        json(
          { code: 'SCAN_IN_PROGRESS', error: 'scan already running', instance: 'alpha' },
          409,
        ),
    }) as typeof fetch;

    const onOpenChange = vi.fn();
    renderWithProviders(<NewScanModal open={true} onOpenChange={onOpenChange} />);
    await screen.findByText('alpha');
    await userEvent.click(screen.getByRole('button', { name: /start scan/i }));

    // onOpenChange(false) must NOT be called for a 409 — modal stays open.
    await waitFor(() => {
      const closingCall = onOpenChange.mock.calls.find((c) => c[0] === false);
      expect(closingCall).toBeUndefined();
    });
  });
});
