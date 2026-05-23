import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { useSeriesSearch } from './series-search';

function wrap() {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0, staleTime: 0 } },
  });
  return ({ children }: { children: React.ReactNode }) => (
    <QueryClientProvider client={qc}>{children}</QueryClientProvider>
  );
}

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status, headers: { 'Content-Type': 'application/json' },
  });

describe('useSeriesSearch()', () => {
  const origFetch = globalThis.fetch;
  beforeEach(() => {
    Object.defineProperty(window, 'location', {
      writable: true, value: { pathname: '/', assign: vi.fn() },
    });
  });
  afterEach(() => { globalThis.fetch = origFetch; });

  it('builds URL with q, monitored=true (default), limit=30 (default)', async () => {
    const captured: { url?: string } = {};
    globalThis.fetch = vi.fn(async (url: RequestInfo | URL) => {
      captured.url = typeof url === 'string' ? url : url.toString();
      return json({ items: [], total: 0 });
    }) as typeof fetch;

    const { result } = renderHook(
      () => useSeriesSearch({ instance: 'alpha', query: 'sev' }),
      { wrapper: wrap() },
    );
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(captured.url).toBe(
      '/api/v1/instances/alpha/series?q=sev&monitored=true&limit=30',
    );
  });

  it('reflects monitored=false in the URL and omits q when empty', async () => {
    const captured: { url?: string } = {};
    globalThis.fetch = vi.fn(async (url: RequestInfo | URL) => {
      captured.url = typeof url === 'string' ? url : url.toString();
      return json({ items: [], total: 0 });
    }) as typeof fetch;

    const { result } = renderHook(
      () => useSeriesSearch({ instance: 'alpha', query: '', monitored: false, limit: 5 }),
      { wrapper: wrap() },
    );
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(captured.url).toBe('/api/v1/instances/alpha/series?monitored=false&limit=5');
    expect(captured.url).not.toContain('q=');
  });

  it('is disabled when enabled=false (no fetch)', async () => {
    const fetchMock = vi.fn();
    globalThis.fetch = fetchMock as unknown as typeof fetch;
    const { result } = renderHook(
      () => useSeriesSearch({ instance: 'alpha', query: 'x', enabled: false }),
      { wrapper: wrap() },
    );
    await new Promise((r) => setTimeout(r, 0));
    expect(fetchMock).not.toHaveBeenCalled();
    expect(result.current.fetchStatus).toBe('idle');
  });

  it('is disabled when instance is empty', async () => {
    const fetchMock = vi.fn();
    globalThis.fetch = fetchMock as unknown as typeof fetch;
    const { result } = renderHook(
      () => useSeriesSearch({ instance: '', query: 'sev' }),
      { wrapper: wrap() },
    );
    await new Promise((r) => setTimeout(r, 0));
    expect(fetchMock).not.toHaveBeenCalled();
    expect(result.current.fetchStatus).toBe('idle');
  });

  it('surfaces ApiError on 404', async () => {
    globalThis.fetch = vi.fn(async () =>
      json({ error: 'unknown instance: ghost' }, 404),
    ) as typeof fetch;
    const { result } = renderHook(
      () => useSeriesSearch({ instance: 'ghost', query: '' }),
      { wrapper: wrap() },
    );
    await waitFor(() => expect(result.current.isError).toBe(true));
    expect(result.current.error?.status).toBe(404);
    expect(result.current.error?.message).toContain('ghost');
  });
});
