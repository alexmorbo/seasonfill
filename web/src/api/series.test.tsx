import { describe, it, expect, vi, beforeEach } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import {
  useSeries,
  seriesQueryKey,
  isMissingLang,
  adaptHero,
  adaptCast,
  adaptSeasons,
} from './series';
import type { SeriesSkeleton } from './series';

const mockApi = vi.fn();
vi.mock('@/lib/api', async () => {
  const actual = await vi.importActual<typeof import('@/lib/api')>('@/lib/api');
  return { ...actual, api: (path: string) => mockApi(path) };
});

function wrapper() {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  });
  return ({ children }: { children: React.ReactNode }) => (
    <QueryClientProvider client={qc}>{children}</QueryClientProvider>
  );
}

describe('useSeries', () => {
  beforeEach(() => mockApi.mockReset());

  it('builds the global URL with seriesId and lang', async () => {
    mockApi.mockResolvedValueOnce({ id: 42, hero: { title: 'Severance' } });
    const { result } = renderHook(
      () => useSeries({ seriesId: 42, lang: 'en-US' }),
      { wrapper: wrapper() },
    );
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(mockApi).toHaveBeenCalledWith('/series/42?lang=en-US');
  });

  it('omits the lang query string when none provided', async () => {
    mockApi.mockResolvedValueOnce({ id: 42 });
    renderHook(() => useSeries({ seriesId: 42 }), { wrapper: wrapper() });
    await waitFor(() => expect(mockApi).toHaveBeenCalled());
    expect(mockApi).toHaveBeenCalledWith('/series/42');
  });

  it('disables the query when seriesId is missing', () => {
    renderHook(() => useSeries({ seriesId: undefined }), { wrapper: wrapper() });
    expect(mockApi).not.toHaveBeenCalled();
  });

  it('exposes a stable query key (no instance arg)', () => {
    expect(seriesQueryKey(42, 'en-US')).toEqual([
      'series-detail', 42, 'en-US',
    ]);
  });
});

// ── W15-9 — under-localized poll signal. `isMissingLang` gates the useSeries
// refetch loop: KEEP polling while the BE served a fallback-language row
// (`missing_lang` marker + served_language !== requested); STOP once the
// requested-language row lands (marker dropped, served_language === requested).
describe('isMissingLang', () => {
  it('is true while a fallback row is served (marker + served != requested)', () => {
    const data = {
      degraded: ['missing_lang'],
      served_language: 'ru-RU',
    } as SeriesSkeleton;
    expect(isMissingLang(data, 'en-US')).toBe(true);
  });

  it('is false once the requested-language row lands (marker gone, langs match)', () => {
    const data = {
      degraded: [],
      served_language: 'en-US',
    } as SeriesSkeleton;
    expect(isMissingLang(data, 'en-US')).toBe(false);
  });

  it('is false when the marker is absent even if served != requested', () => {
    const data = {
      degraded: ['tmdb_series'],
      served_language: 'ru-RU',
    } as SeriesSkeleton;
    expect(isMissingLang(data, 'en-US')).toBe(false);
  });

  it('is false when the marker is present but served already equals requested', () => {
    const data = {
      degraded: ['missing_lang'],
      served_language: 'en-US',
    } as SeriesSkeleton;
    expect(isMissingLang(data, 'en-US')).toBe(false);
  });

  it('is false for an empty requested lang (no language pinned)', () => {
    const data = {
      degraded: ['missing_lang'],
      served_language: 'ru-RU',
    } as SeriesSkeleton;
    expect(isMissingLang(data, '')).toBe(false);
  });

  it('is false for undefined data / missing served_language', () => {
    expect(isMissingLang(undefined, 'en-US')).toBe(false);
    expect(isMissingLang({ degraded: ['missing_lang'] } as SeriesSkeleton, 'en-US')).toBe(true);
  });
});

// ── C3b (story 968) — pure adapters: SkeletonDTO + lazy DTOs → view-models.
type SkeletonHero = NonNullable<SeriesSkeleton['hero']>;
type SkeletonSidebar = NonNullable<SeriesSkeleton['sidebar']>;

describe('adaptHero', () => {
  const hero: SkeletonHero = {
    title: { value: 'For All Mankind', lang: 'en-US' },
    original_title: { value: 'For All Mankind (orig)' },
    tagline: { value: 'The future is ours to take.' },
    year_start: 2019,
    year_end: 2024,
    runtime_minutes: 45,
    poster_asset: 'aaaa',
    backdrop_asset: 'bbbb',
    genres: [{ tmdb_id: 1, name: 'Drama' }, { tmdb_id: 2, name: 'Sci-Fi' }],
    tmdb_rating: { score: 8.1, votes: 2100 },
    imdb_rating: { score: 8.0, votes: 84_000 },
    content_rating: 'TV-MA',
    trailer_key: 'abc123',
    next_episode: {
      season_number: 5, episode_number: 3,
      title: { value: 'Glasnost' }, air_date: '2026-07-14',
    },
  };
  const sidebar: SkeletonSidebar = {
    status: 'continuing',
    networks: [{ tmdb_id: 1, name: 'Apple TV+', logo_asset: 'net1' }],
    origin_countries: ['US'],
    original_language: 'en',
    first_air_date: '2019-11-01',
    production_companies: [{ tmdb_id: 9, name: 'Sony Pictures TV' }],
  };

  it('unwraps title/original_title/tagline wires and merges sidebar', () => {
    const vm = adaptHero(hero, sidebar);
    expect(vm?.title).toBe('For All Mankind');
    expect(vm?.original_title).toBe('For All Mankind (orig)');
    expect(vm?.tagline).toBe('The future is ours to take.');
    expect(vm?.status).toBe('continuing');
    expect(vm?.year_start).toBe(2019);
    expect(vm?.year_end).toBe(2024);
    expect(vm?.runtime_minutes).toBe(45);
    expect(vm?.original_language).toBe('en');
    expect(vm?.premiere_date).toBe('2019-11-01');
    expect(vm?.countries).toEqual(['US']);
    expect(vm?.studio).toBe('Sony Pictures TV');
  });

  it('maps genres tmdb_id → id and networks from sidebar', () => {
    const vm = adaptHero(hero, sidebar);
    expect(vm?.genres).toEqual([
      { id: 1, name: 'Drama' },
      { id: 2, name: 'Sci-Fi' },
    ]);
    expect(vm?.networks).toEqual([
      { id: 1, name: 'Apple TV+', logo_asset: 'net1' },
    ]);
  });

  it('wraps content_rating string → {rating} and trailer_key → {key,site}', () => {
    const vm = adaptHero(hero, sidebar);
    expect(vm?.content_rating).toEqual({ rating: 'TV-MA' });
    expect(vm?.trailer).toEqual({ key: 'abc123', site: 'youtube' });
  });

  it('unwraps next_episode title wire and keeps numbers', () => {
    const vm = adaptHero(hero, sidebar);
    expect(vm?.next_episode).toEqual({
      season_number: 5, episode_number: 3,
      title: 'Glasnost', air_date: '2026-07-14',
    });
  });

  it('passes rating objects through untouched', () => {
    const vm = adaptHero(hero, sidebar);
    expect(vm?.tmdb_rating).toEqual({ score: 8.1, votes: 2100 });
    expect(vm?.imdb_rating).toEqual({ score: 8.0, votes: 84_000 });
  });

  it('returns undefined when both hero and sidebar are absent', () => {
    expect(adaptHero(undefined, undefined)).toBeUndefined();
  });

  it('renders a cold hero (title only) without crashing', () => {
    const vm = adaptHero({ title: { value: 'Cold Show' } }, undefined);
    expect(vm).toEqual({ title: 'Cold Show' });
    expect(vm?.status).toBeUndefined();
    expect(vm?.tmdb_rating).toBeUndefined();
  });

  it('passes a null-score rating object through (BE can emit null)', () => {
    const vm = adaptHero(
      { title: { value: 'X' }, tmdb_rating: { score: null } as never },
      undefined,
    );
    // object still forwarded — the render guard tolerates a missing score.
    expect(vm?.tmdb_rating).toBeDefined();
  });
});

describe('adaptCast', () => {
  it('renames tmdb_id → tmdb_person_id and keeps other fields', () => {
    const out = adaptCast([
      { person_id: 1, tmdb_id: 5, name: 'Joel Kinnaman', character_name: 'Ed', episode_count: 9 },
    ]);
    expect(out).toEqual([
      { person_id: 1, tmdb_person_id: 5, name: 'Joel Kinnaman', character_name: 'Ed', episode_count: 9 },
    ]);
  });

  it('returns [] for undefined/empty input', () => {
    expect(adaptCast(undefined)).toEqual([]);
    expect(adaptCast([])).toEqual([]);
  });
});

describe('adaptSeasons', () => {
  it('maps air_date_start → air_date; omits absent episode fields', () => {
    const out = adaptSeasons([
      { season_number: 1, name: 'Season 1', episode_count: 10, poster_asset: 'ps1', air_date_start: '2019-11-01' },
    ]);
    expect(out).toEqual([
      { season_number: 1, name: 'Season 1', episode_count: 10, poster_asset: 'ps1', air_date: '2019-11-01' },
    ]);
    // summary carries no episodes / on_disk_count — must be absent, not undefined.
    expect(out[0]).not.toHaveProperty('episodes');
    expect(out[0]).not.toHaveProperty('on_disk_count');
    expect(out[0]).not.toHaveProperty('downloading_count');
  });

  it('returns [] for undefined/empty input', () => {
    expect(adaptSeasons(undefined)).toEqual([]);
    expect(adaptSeasons([])).toEqual([]);
  });
});
