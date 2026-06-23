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

  it('delegates to /series?instance=:name&state=missing and projects to MissingSeries', async () => {
    const captured: { url?: string } = {};
    // SeriesCacheList wire shape — after 493 useMissing reads
    // the global catalog endpoint and lossily projects rows.
    const payload = {
      items: [
        {
          sonarr_series_id: 122,
          instance_name: 'alpha',
          title: 'Severance',
          title_slug: 'severance',
          monitored: true,
          missing_count: 8,
          updated_at: '2025-01-01T00:00:00Z',
        },
      ],
      total: 1,
      has_more: false,
    };
    globalThis.fetch = vi.fn(async (url: RequestInfo | URL) => {
      captured.url = typeof url === 'string' ? url : url.toString();
      return new Response(JSON.stringify(payload), {
        status: 200, headers: { 'Content-Type': 'application/json' },
      });
    }) as typeof fetch;

    const { result } = renderHook(() => useMissing('alpha'), { wrapper: wrap() });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(captured.url).toContain('/api/v1/series?instance=alpha');
    expect(captured.url).toContain('state=missing');
    expect(result.current.data?.total).toBe(1);
    expect(result.current.data?.items?.[0]?.title).toBe('Severance');
    expect(result.current.data?.items?.[0]?.series_id).toBe(122);
    expect(result.current.data?.items?.[0]?.total_missing_aired).toBe(8);
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
