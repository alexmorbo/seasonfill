import type { ReactElement } from 'react';
import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest';
import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import type { QueryClient } from '@tanstack/react-query';
import { renderWithProviders } from '@/test-utils';
import { Instances } from './Instances';
import { InstanceFilterCtx } from '@/lib/instance-filter-context-internal';

vi.mock('sonner', () => ({
  toast: {
    error: vi.fn(),
    success: vi.fn(),
  },
}));

const origFetch = globalThis.fetch;
const ctxValue = { filter: null, setFilter: vi.fn() };

beforeEach(() => {
  Object.defineProperty(window, 'location', {
    writable: true,
    value: { pathname: '/instances', search: '', assign: vi.fn(), protocol: 'https:' },
  });
});

afterEach(() => {
  globalThis.fetch = origFetch;
});

const wrap = (ui: ReactElement) => (
  <InstanceFilterCtx.Provider value={ctxValue}>{ui}</InstanceFilterCtx.Provider>
);

function makeInstanceList(names: string[]) {
  return {
    instances: names.map((n) => ({
      name: n,
      mode: 'auto',
      health: 'available',
      last_check_at: new Date().toISOString(),
      transitions_count: 0,
    })),
  };
}

function mockInstanceFetch(
  qc: QueryClient,
  names: string[],
  detailFetch?: (name: string) => Response,
  recorder?: (req: { url: string; method: string; body?: string | undefined }) => void,
) {
  qc.setQueryData(['instances'], makeInstanceList(names));
  globalThis.fetch = vi.fn(async (u: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof u === 'string' ? u : u.toString();
    const method = init?.method ?? 'GET';
    if (recorder) {
      const body = typeof init?.body === 'string' ? init.body : undefined;
      recorder({
        url,
        method,
        body,
      });
    }
    if (detailFetch) {
      const m = /\/instances\/([^/?]+)$/.exec(url);
      if (m && m[1] && method === 'GET') {
        return detailFetch(decodeURIComponent(m[1]));
      }
    }
    if (method === 'DELETE') {
      return new Response(null, { status: 204 });
    }
    if (method === 'POST' || method === 'PUT') {
      return new Response(JSON.stringify({ name: 'unused' }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      });
    }
    return new Response(JSON.stringify(makeInstanceList(names)), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    });
  }) as typeof fetch;
}

describe('<Instances /> — list rendering', () => {
  it('renders one card per instance with mode chip + queue link', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          instances: [
            {
              name: 'alpha',
              mode: 'manual',
              health: 'available',
              last_check_at: new Date().toISOString(),
              transitions_count: 0,
            },
            {
              name: 'beta',
              mode: 'auto',
              health: 'degraded',
              last_check_at: new Date().toISOString(),
              transitions_count: 3,
              last_error: 'connection refused',
            },
          ],
        }),
        { status: 200, headers: { 'Content-Type': 'application/json' } },
      ),
    ) as typeof fetch;

    renderWithProviders(wrap(<Instances />));
    expect(await screen.findByText('alpha')).toBeInTheDocument();
    expect(screen.getByText('beta')).toBeInTheDocument();
    expect(screen.getByText(/connection refused/i)).toBeInTheDocument();

    expect(screen.getByTestId('mode-alpha')).toHaveTextContent('manual');
    expect(screen.getByTestId('mode-beta')).toHaveTextContent('auto');

    const alphaLink = screen.getByRole('link', { name: /open queue for alpha/i });
    expect(alphaLink).toHaveAttribute('href', '/instances/alpha/queue');
    expect(alphaLink).toHaveTextContent(/open queue/i);

    const betaLink = screen.getByRole('link', { name: /open queue for beta/i });
    expect(betaLink).toHaveAttribute('href', '/instances/beta/queue');
    expect(betaLink).toHaveTextContent(/view queue/i);
  });

  it('renders empty state when instances=[]', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ instances: [] }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    ) as typeof fetch;

    renderWithProviders(wrap(<Instances />));
    expect(await screen.findByText(/no instances configured/i)).toBeInTheDocument();
  });
});

describe('<Instances /> — CRUD', () => {
  it('clicking the page-level Add button opens the create dialog', async () => {
    const { qc } = renderWithProviders(wrap(<Instances />));
    mockInstanceFetch(qc, ['alpha', 'beta']);

    await screen.findByText('alpha');
    await userEvent.click(screen.getByRole('button', { name: /add.*new.*instance/i }));

    // InstanceFormDialog renders a heading containing "Add instance"
    // in create mode (033a). Hit the dialog role to keep this resilient.
    await waitFor(() => {
      expect(screen.getByRole('dialog')).toBeInTheDocument();
    });
  });

  it('clicking Edit on a card triggers useInstanceDetail fetch for that name', async () => {
    const fetchedNames: string[] = [];
    const { qc } = renderWithProviders(wrap(<Instances />));
    mockInstanceFetch(qc, ['alpha', 'beta'], (name) => {
      fetchedNames.push(name);
      return new Response(
        JSON.stringify({
          name,
          url: 'http://sonarr:8989',
          mode: 'auto',
          api_key: '***',
          updated_at: '2026-05-25T00:00:00Z',
        }),
        {
          status: 200,
          headers: {
            'Content-Type': 'application/json',
            'Last-Modified': 'Mon, 25 May 2026 00:00:00 GMT',
          },
        },
      );
    });

    await screen.findByText('beta');
    await userEvent.click(screen.getByRole('button', { name: /edit beta/i }));

    await waitFor(() => {
      expect(fetchedNames).toContain('beta');
    });
  });

  it('clicking Delete then confirming fires a DELETE for that instance', async () => {
    const requests: { url: string; method: string }[] = [];
    const { qc } = renderWithProviders(wrap(<Instances />));
    mockInstanceFetch(qc, ['alpha', 'beta'], undefined, (req) => {
      requests.push({ url: req.url, method: req.method });
    });

    await screen.findByText('beta');
    await userEvent.click(screen.getByRole('button', { name: /delete beta/i }));

    // Confirm dialog must appear with the instance name in its title.
    const confirm = await screen.findByRole('dialog');
    expect(confirm).toHaveTextContent(/beta/);

    await userEvent.click(
      screen.getByRole('button', { name: /^delete$/i }),
    );

    await waitFor(() => {
      const del = requests.find(
        (r) => r.method === 'DELETE' && /\/instances\/beta$/.test(r.url),
      );
      expect(del).toBeTruthy();
    });
  });

  it('blocks delete and shows toast when only one instance exists', async () => {
    const { toast } = await import('sonner');
    const { qc } = renderWithProviders(wrap(<Instances />));
    mockInstanceFetch(qc, ['solo']);

    await screen.findByText('solo');
    await userEvent.click(screen.getByRole('button', { name: /delete solo/i }));

    // No confirm dialog should appear; no DELETE fetch should be called.
    await waitFor(() => {
      // The InstanceFormDialog is never opened here, so no role=dialog.
      expect(screen.queryByRole('dialog')).toBeNull();
    });
    expect(screen.queryByText(/delete instance/i)).toBeNull();
    // Assert that toast.error was called with the i18n message
    expect(toast.error).toHaveBeenCalledWith(expect.stringContaining('Cannot delete the last'));
  });
});
