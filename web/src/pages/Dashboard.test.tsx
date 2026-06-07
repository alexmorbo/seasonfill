import type { ReactElement } from 'react';
import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest';
import { screen } from '@testing-library/react';
import { renderWithProviders } from '@/test-utils';
import { PageTitleProvider } from '@/components/shell/page-title-context';
import { Dashboard } from './Dashboard';
import { InstanceFilterCtx } from '@/lib/instance-filter-context-internal';

function fetchStub(payloads: Record<string, unknown>) {
  return vi.fn(async (url: RequestInfo | URL) => {
    const path = typeof url === 'string' ? url : url.toString();
    for (const key of Object.keys(payloads)) {
      if (path.includes(key)) {
        return new Response(JSON.stringify(payloads[key]), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        });
      }
    }
    return new Response('{}', {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    });
  });
}

const origFetch = globalThis.fetch;
const ctxValue = { filter: null, setFilter: vi.fn() };

beforeEach(() => {
  Object.defineProperty(window, 'location', {
    writable: true,
    value: { pathname: '/', search: '', assign: vi.fn() },
  });
});

afterEach(() => {
  globalThis.fetch = origFetch;
});

const wrap = (ui: ReactElement) => (
  <PageTitleProvider defaultTitle="Dashboard">
    <InstanceFilterCtx.Provider value={ctxValue}>{ui}</InstanceFilterCtx.Provider>
  </PageTitleProvider>
);

describe('<Dashboard /> — 049a smoke test', () => {
  it('mounts without crash and renders first-run state when zero instances', async () => {
    globalThis.fetch = fetchStub({
      '/instances': { instances: [] },
    }) as typeof fetch;
    renderWithProviders(wrap(<Dashboard />));
    expect(await screen.findByTestId('dashboard-first-run')).toBeInTheDocument();
  });
});
