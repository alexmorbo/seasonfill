import { describe, it, expect, vi, afterEach } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { useSeasonEpisodes, episodeState } from './queueSeasonEpisodes';
import type { SeasonEpisodeItem } from './queueSeasonEpisodes';

const origFetch = globalThis.fetch;
const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status, headers: { 'Content-Type': 'application/json' },
  });

afterEach(() => {
  globalThis.fetch = origFetch;
  vi.restoreAllMocks();
});

function wrapper() {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const Wrapper = ({ children }: { children: React.ReactNode }) => (
    <QueryClientProvider client={qc}>{children}</QueryClientProvider>
  );
  return Wrapper;
}

describe('useSeasonEpisodes', () => {
  it('is disabled when name or seriesId is missing', () => {
    const { result } = renderHook(
      () => useSeasonEpisodes(undefined, undefined, null),
      { wrapper: wrapper() },
    );
    expect(result.current.isPending).toBe(true);
    expect(result.current.fetchStatus).toBe('idle');
  });

  it('fetches and exposes data when fully populated', async () => {
    globalThis.fetch = vi.fn(async () =>
      json({
        items: [{ number: 1, monitored: true, has_file: true, aired: true, air_date_utc: '2024-01-01T00:00:00Z' }],
        total: 1, have: 1, miss: 0,
      }),
    ) as typeof fetch;
    const { result } = renderHook(
      () => useSeasonEpisodes('alpha', 122, 2),
      { wrapper: wrapper() },
    );
    await waitFor(() => expect(result.current.data).toBeDefined());
    expect(result.current.data?.have).toBe(1);
    expect(result.current.data?.miss).toBe(0);
  });
});

describe('episodeState', () => {
  const base: SeasonEpisodeItem = {
    number: 1, monitored: true, has_file: false, aired: false,
    air_date_utc: '',
  };
  it('returns have when has_file', () => {
    expect(episodeState({ ...base, has_file: true })).toBe('have');
  });
  it('returns miss when monitored + aired + !has_file', () => {
    expect(episodeState({ ...base, aired: true })).toBe('miss');
  });
  it('returns upcoming when monitored + !aired', () => {
    expect(episodeState(base)).toBe('upcoming');
  });
  it('returns upcoming when unmonitored + aired + !has_file', () => {
    expect(episodeState({ ...base, monitored: false, aired: true })).toBe('upcoming');
  });
});
