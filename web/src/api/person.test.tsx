import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import {
  usePerson,
  personQueryKey,
  isPersonStub,
  PERSON_STUB_POLL_MS,
} from './person';

const mockApi = vi.fn();
vi.mock('@/lib/api', async () => {
  const actual = await vi.importActual<typeof import('@/lib/api')>('@/lib/api');
  return { ...actual, api: (path: string) => mockApi(path) };
});

function wrapper() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
  return ({ children }: { children: React.ReactNode }) => (
    <QueryClientProvider client={qc}>{children}</QueryClientProvider>
  );
}

describe('usePerson', () => {
  beforeEach(() => mockApi.mockReset());

  it('builds the URL with tmdbId, lang and sort', async () => {
    mockApi.mockResolvedValueOnce({ person: { tmdb_id: 4495 } });
    const { result } = renderHook(
      () => usePerson({ tmdbId: 4495, lang: 'en-US', sort: 'episodes' }),
      { wrapper: wrapper() },
    );
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(mockApi).toHaveBeenCalledWith('/people/4495?lang=en-US&sort=episodes');
  });

  it('omits the lang query string when none provided but always sends sort', async () => {
    mockApi.mockResolvedValueOnce({});
    renderHook(() => usePerson({ tmdbId: 4495 }), { wrapper: wrapper() });
    await waitFor(() => expect(mockApi).toHaveBeenCalled());
    expect(mockApi).toHaveBeenCalledWith('/people/4495?sort=recent');
  });

  it('disables the query when tmdbId is missing or invalid', () => {
    renderHook(() => usePerson({ tmdbId: undefined }), { wrapper: wrapper() });
    renderHook(() => usePerson({ tmdbId: 0 }), { wrapper: wrapper() });
    renderHook(() => usePerson({ tmdbId: NaN }), { wrapper: wrapper() });
    expect(mockApi).not.toHaveBeenCalled();
  });

  it('exposes a stable queryKey with sort included', () => {
    expect(personQueryKey(4495, 'en-US', 'recent')).toEqual([
      'person', 4495, 'en-US', 'recent',
    ]);
  });

  it('detects stub via degraded[]', () => {
    expect(isPersonStub({ degraded: ['tmdb_person'] })).toBe(true);
    expect(isPersonStub({ degraded: [] })).toBe(false);
    expect(isPersonStub(undefined)).toBe(false);
  });
});

describe('usePerson polling', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    mockApi.mockReset();
  });
  afterEach(() => vi.useRealTimers());

  it('polls every PERSON_STUB_POLL_MS while degraded includes tmdb_person, then stops', async () => {
    mockApi
      .mockResolvedValueOnce({ degraded: ['tmdb_person'], person: { tmdb_id: 4495 } })
      .mockResolvedValueOnce({ degraded: ['tmdb_person'], person: { tmdb_id: 4495 } })
      .mockResolvedValue({ degraded: [], person: { tmdb_id: 4495 } });

    const { result } = renderHook(
      () => usePerson({ tmdbId: 4495 }),
      { wrapper: wrapper() },
    );

    await vi.waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(mockApi).toHaveBeenCalledTimes(1);

    await vi.advanceTimersByTimeAsync(PERSON_STUB_POLL_MS);
    await vi.waitFor(() => expect(mockApi).toHaveBeenCalledTimes(2));

    await vi.advanceTimersByTimeAsync(PERSON_STUB_POLL_MS);
    await vi.waitFor(() => expect(mockApi).toHaveBeenCalledTimes(3));

    // Third response carries degraded:[] → polling halts.
    const before = mockApi.mock.calls.length;
    await vi.advanceTimersByTimeAsync(PERSON_STUB_POLL_MS * 3);
    expect(mockApi.mock.calls.length).toBe(before);
  });
});
