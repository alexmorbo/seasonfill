import { describe, expect, it, vi, beforeEach } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { useGrabEpisodeFiles } from './grabEpisodeFiles';

const origFetch = globalThis.fetch;

beforeEach(() => {
  vi.restoreAllMocks();
  globalThis.fetch = origFetch;
});

function wrap(client: QueryClient) {
  return function Wrapper({ children }: { children: React.ReactNode }) {
    return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
  };
}

describe('useGrabEpisodeFiles', () => {
  it('does NOT fire when enabled=false', async () => {
    const fetchSpy = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ items: [] }), { status: 200 }),
    );
    globalThis.fetch = fetchSpy as typeof fetch;
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });

    renderHook(() => useGrabEpisodeFiles('alpha', 'g1', { enabled: false }), {
      wrapper: wrap(qc),
    });
    await new Promise((r) => setTimeout(r, 10));
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  it('does NOT fire when id is null', async () => {
    const fetchSpy = vi.fn();
    globalThis.fetch = fetchSpy as typeof fetch;
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    renderHook(() => useGrabEpisodeFiles('alpha', null, { enabled: true }), {
      wrapper: wrap(qc),
    });
    await new Promise((r) => setTimeout(r, 10));
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  it('fires when both inputs present AND enabled', async () => {
    const fetchSpy = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          items: [
            {
              id: 7001, relative_path: 'Season 02/S02E01.mkv',
              season_number: 2, episode_numbers: [1],
              size_bytes: 13_325_829_734, quality: 'WEBDL-2160p',
            },
          ],
        }),
        { status: 200, headers: { 'Content-Type': 'application/json' } },
      ),
    );
    globalThis.fetch = fetchSpy as typeof fetch;
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });

    const { result } = renderHook(
      () => useGrabEpisodeFiles('alpha', 'g1', { enabled: true }),
      { wrapper: wrap(qc) },
    );
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(result.current.data?.items.length).toBe(1);
    expect(fetchSpy).toHaveBeenCalledTimes(1);
    expect((fetchSpy.mock.calls[0]?.[0] as string)).toMatch(
      /\/api\/v1\/instances\/alpha\/grabs\/g1\/episode-files/,
    );
  });

  it('propagates 502 as ApiError', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ error: 'sonarr unavailable' }), {
        status: 502,
        headers: { 'Content-Type': 'application/json' },
      }),
    ) as typeof fetch;
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });

    const { result } = renderHook(
      () => useGrabEpisodeFiles('alpha', 'g1', { enabled: true }),
      { wrapper: wrap(qc) },
    );
    await waitFor(() => expect(result.current.isError).toBe(true));
    expect(result.current.error?.status).toBe(502);
  });
});
