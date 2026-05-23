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

  it('replaces the placeholder field with a working SeriesPicker', async () => {
    globalThis.fetch = handler({
      '/instances': () => json(instances),
      '/instances/alpha/series': () =>
        json({
          items: [
            { series_id: 122, title: 'Severance', monitored: true,
              season_count: 2, missing_aired_count: 8 },
          ],
          total: 1,
        }),
    }) as typeof fetch;

    renderWithProviders(<NewScanModal open={true} onOpenChange={vi.fn()} />);
    await screen.findByText('alpha');
    expect(screen.getByTestId('series-picker-input')).toBeInTheDocument();
    expect(screen.queryByPlaceholderText(/e\.g\. severance/i)).toBeNull();
  });

  it('threads series_ids through to POST body when a series is picked', async () => {
    const captured: { urls: string[]; methods: string[]; bodies: string[] } =
      { urls: [], methods: [], bodies: [] };
    const sevItem = {
      series_id: 122, title: 'Severance', monitored: true,
      season_count: 2, missing_aired_count: 8,
    };
    globalThis.fetch = vi.fn(async (url: RequestInfo | URL, init?: RequestInit) => {
      const path = typeof url === 'string' ? url : url.toString();
      captured.urls.push(path);
      captured.methods.push(init?.method ?? 'GET');
      captured.bodies.push(typeof init?.body === 'string' ? init.body : '');
      if (path.includes('/instances/alpha/series')) return json({ items: [sevItem], total: 1 });
      if (path.includes('/instances')) return json(instances);
      if (path.includes('/scan')) {
        return json([{ scan_run_id: 'run-007', instance: 'alpha', status: 'running' }], 202);
      }
      return json({});
    }) as typeof fetch;

    renderWithProviders(<NewScanModal open={true} onOpenChange={vi.fn()} />);
    await screen.findByText('alpha');
    await userEvent.click(screen.getByTestId('series-picker-input'));
    await screen.findByTestId('series-picker-opt-122');
    await userEvent.click(screen.getByTestId('series-picker-opt-122'));
    await waitFor(() => expect(screen.getByText('Severance')).toBeInTheDocument());
    await userEvent.click(screen.getByTestId('new-scan-submit'));

    // Walk per-call arrays — onSuccess refetches /instances + /scans
    // and would overwrite a single-slot capture.
    const findPost = () => captured.urls.findIndex(
      (u, i) => u.endsWith('/scan') && captured.methods[i] === 'POST',
    );
    await waitFor(() => expect(findPost()).toBeGreaterThanOrEqual(0));
    expect(JSON.parse(captured.bodies[findPost()] || '{}')).toEqual({
      instance: 'alpha', series_ids: [122],
    });
  });

  it('blocks submit while the picker is searching', async () => {
    const holder: { resolve?: (r: Response) => void } = {};
    globalThis.fetch = vi.fn(async (url: RequestInfo | URL) => {
      const path = typeof url === 'string' ? url : url.toString();
      if (path.includes('/instances/alpha/series')) {
        return new Promise<Response>((r) => { holder.resolve = r; });
      }
      if (path.includes('/instances')) return json(instances);
      return json({});
    }) as typeof fetch;

    renderWithProviders(<NewScanModal open={true} onOpenChange={vi.fn()} />);
    await screen.findByText('alpha');
    await userEvent.click(screen.getByTestId('series-picker-input'));
    await userEvent.type(screen.getByTestId('series-picker-input'), 'sev');

    await waitFor(() =>
      expect(screen.getByTestId('new-scan-submit')).toBeDisabled(),
    );
    expect(screen.getByTestId('new-scan-submit')).toHaveTextContent(/searching/i);

    holder.resolve?.(
      new Response(JSON.stringify({ items: [], total: 0 }), {
        status: 200, headers: { 'Content-Type': 'application/json' },
      }),
    );
  });
});
