import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { useMissing } from './missing';

function wrap() {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0, staleTime: 0 } },
  });
  return ({ children }: { children: React.ReactNode }) => (
    <QueryClientProvider client={qc}>{children}</QueryClientProvider>
  );
}

describe('useMissing()', () => {
  const origFetch = globalThis.fetch;
  beforeEach(() => {
    Object.defineProperty(window, 'location', {
      writable: true, value: { pathname: '/', assign: vi.fn() },
    });
  });
  afterEach(() => { globalThis.fetch = origFetch; });

  it('hits /instances/:name/missing and returns parsed list', async () => {
    const captured: { url?: string } = {};
    const payload = {
      items: [
        {
          series_id: 122, title: 'Severance', monitored: true,
          total_missing_aired: 8,
          seasons: [{ season_number: 2, missing_aired_count: 8 }],
        },
      ],
      total: 1,
    };
    globalThis.fetch = vi.fn(async (url: RequestInfo | URL) => {
      captured.url = typeof url === 'string' ? url : url.toString();
      return new Response(JSON.stringify(payload), {
        status: 200, headers: { 'Content-Type': 'application/json' },
      });
    }) as typeof fetch;

    const { result } = renderHook(() => useMissing('alpha'), { wrapper: wrap() });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(captured.url).toBe('/api/v1/instances/alpha/missing');
    expect(result.current.data?.total).toBe(1);
    expect(result.current.data?.items?.[0]?.title).toBe('Severance');
  });

  it('is disabled when name is undefined (no fetch)', async () => {
    const fetchMock = vi.fn();
    globalThis.fetch = fetchMock as unknown as typeof fetch;
    const { result } = renderHook(() => useMissing(undefined), { wrapper: wrap() });
    // give react-query a microtask to settle
    await new Promise((r) => setTimeout(r, 0));
    expect(fetchMock).not.toHaveBeenCalled();
    expect(result.current.fetchStatus).toBe('idle');
  });
});
