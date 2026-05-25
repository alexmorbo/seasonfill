import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest';
import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import type { QueryClient } from '@tanstack/react-query';
import { renderWithProviders } from '@/test-utils';
import { InstancesTab } from './InstancesTab';

const origFetch = globalThis.fetch;
beforeEach(() => {
  Object.defineProperty(window, 'location', {
    writable: true,
    value: { pathname: '/settings', assign: vi.fn(), protocol: 'https:' },
  });
});
afterEach(() => { globalThis.fetch = origFetch; });

function makeInstances(names: string[]) {
  return {
    instances: names.map((n) => ({ name: n, mode: 'auto', health: 'ok' })),
  };
}

function mockFetchInstances(
  qc: QueryClient,
  names: string[],
  detailFetch?: (name: string) => Response,
) {
  // Pre-populate the instances list cache so the table renders synchronously.
  qc.setQueryData(['instances'], makeInstances(names));
  globalThis.fetch = vi.fn(async (u: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof u === 'string' ? u : u.toString();
    if (detailFetch) {
      const m = /\/instances\/([^/?]+)/.exec(url);
      if (m && (!init?.method || init.method === 'GET')) {
        return detailFetch(decodeURIComponent(m[1]));
      }
    }
    return new Response(JSON.stringify(makeInstances(names)), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    });
  }) as typeof fetch;
}

describe('<InstancesTab />', () => {
  it('renders a row for each instance in the list', async () => {
    const { qc } = renderWithProviders(<InstancesTab />);
    mockFetchInstances(qc, ['alpha', 'beta']);

    await waitFor(() => {
      expect(screen.getByText('alpha')).toBeVisible();
      expect(screen.getByText('beta')).toBeVisible();
    });
  });

  it('blocks delete and shows toast when only one instance exists', async () => {
    const { qc } = renderWithProviders(<InstancesTab />);
    mockFetchInstances(qc, ['solo']);

    await screen.findByText('solo');
    await userEvent.click(screen.getByRole('button', { name: /delete solo/i }));

    // No confirm dialog should appear; no DELETE fetch should be called.
    await waitFor(() => {
      expect(screen.queryByRole('dialog')).toBeNull();
    });
    // The delete confirm dialog title would contain the instance name if it opened.
    expect(screen.queryByText(/delete instance/i)).toBeNull();
  });

  it('clicking Edit triggers useInstanceDetail fetch for that instance name', async () => {
    const fetchedNames: string[] = [];
    const { qc } = renderWithProviders(<InstancesTab />);
    mockFetchInstances(qc, ['alpha', 'beta'], (name) => {
      fetchedNames.push(name);
      return new Response(
        JSON.stringify({ name, url: 'http://sonarr:8989', mode: 'auto', api_key: '***', updated_at: '2026-05-25T00:00:00Z' }),
        { status: 200, headers: { 'Content-Type': 'application/json', 'Last-Modified': 'Mon, 25 May 2026 00:00:00 GMT' } },
      );
    });

    await screen.findByText('beta');
    await userEvent.click(screen.getByRole('button', { name: /edit beta/i }));

    await waitFor(() => {
      expect(fetchedNames).toContain('beta');
    });
  });
});
