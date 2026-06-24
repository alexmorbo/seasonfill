import { describe, it, expect } from 'vitest';
import { act, renderHook } from '@testing-library/react';
import { MemoryRouter, useSearchParams } from 'react-router-dom';
import type { ReactNode } from 'react';
import {
  filterFromSearchParams,
  searchParamsFromFilter,
  useDiscoverFilter,
} from './useDiscoverFilter';

function wrap(initial: string) {
  return ({ children }: { children: ReactNode }) => (
    <MemoryRouter initialEntries={[initial]}>{children}</MemoryRouter>
  );
}

describe('filterFromSearchParams / searchParamsFromFilter', () => {
  it('round-trips arrays, numbers, and dates', () => {
    const p = new URLSearchParams(
      'with_genres=18,35&with_networks=49&with_origin_country=US,GB' +
        '&with_status=returning,ended&with_type=scripted' +
        '&first_air_date_gte=2000-01-01&first_air_date_lte=2024-12-31' +
        '&vote_average_gte=6.5&vote_average_lte=9&sort_by=popularity.desc&page=2',
    );
    const f = filterFromSearchParams(p);
    expect(f.with_genres).toEqual([18, 35]);
    expect(f.with_networks).toEqual([49]);
    expect(f.with_origin_country).toEqual(['US', 'GB']);
    expect(f.with_status).toEqual(['returning', 'ended']);
    expect(f.with_type).toEqual(['scripted']);
    expect(f.first_air_date_gte).toBe('2000-01-01');
    expect(f.first_air_date_lte).toBe('2024-12-31');
    expect(f.vote_average_gte).toBe(6.5);
    expect(f.vote_average_lte).toBe(9);
    expect(f.sort_by).toBe('popularity.desc');
    expect(f.page).toBe(2);

    const back = searchParamsFromFilter(f);
    expect(back.get('with_genres')).toBe('18,35');
    expect(back.get('first_air_date_gte')).toBe('2000-01-01');
    expect(back.get('vote_average_gte')).toBe('6.5');
    expect(back.get('page')).toBe('2');
  });

  it('drops empty arrays, blank strings, and NaN numbers', () => {
    const p = new URLSearchParams(
      'with_genres=&vote_average_gte=&page=abc',
    );
    const f = filterFromSearchParams(p);
    expect(f.with_genres).toBeUndefined();
    expect(f.vote_average_gte).toBeUndefined();
    expect(f.page).toBeUndefined();

    const back = searchParamsFromFilter({});
    expect(back.toString()).toBe('');
  });
});

describe('useDiscoverFilter', () => {
  it('reads initial filter from URL and exposes hasActiveFilter', () => {
    const probe = renderHook(() => useDiscoverFilter(), {
      wrapper: wrap('/?tab=filtered&with_genres=18'),
    });
    expect(probe.result.current.filter.with_genres).toEqual([18]);
    expect(probe.result.current.hasActiveFilter).toBe(true);
  });

  it('hasActiveFilter is false when no managed keys set', () => {
    const probe = renderHook(() => useDiscoverFilter(), {
      wrapper: wrap('/?tab=filtered'),
    });
    expect(probe.result.current.hasActiveFilter).toBe(false);
  });

  it('setFilter merges patches and preserves tab param', () => {
    const probe = renderHook(
      () => {
        const f = useDiscoverFilter();
        const [params] = useSearchParams();
        return { f, params };
      },
      { wrapper: wrap('/?tab=filtered&with_genres=18') },
    );
    act(() => probe.result.current.f.setFilter({ with_networks: [49] }));
    expect(probe.result.current.f.filter.with_genres).toEqual([18]);
    expect(probe.result.current.f.filter.with_networks).toEqual([49]);
    expect(probe.result.current.params.get('tab')).toBe('filtered');
    expect(probe.result.current.params.get('with_genres')).toBe('18');
    expect(probe.result.current.params.get('with_networks')).toBe('49');
  });

  it('setFilter with empty array removes the param', () => {
    const probe = renderHook(
      () => {
        const f = useDiscoverFilter();
        const [params] = useSearchParams();
        return { f, params };
      },
      { wrapper: wrap('/?with_genres=18,35') },
    );
    act(() => probe.result.current.f.setFilter({ with_genres: [] }));
    expect(probe.result.current.f.filter.with_genres).toBeUndefined();
    expect(probe.result.current.params.get('with_genres')).toBeNull();
  });

  it('clearFilter empties managed params but keeps others', () => {
    const probe = renderHook(
      () => {
        const f = useDiscoverFilter();
        const [params] = useSearchParams();
        return { f, params };
      },
      { wrapper: wrap('/?tab=filtered&with_genres=18&sort_by=popularity.desc') },
    );
    act(() => probe.result.current.f.clearFilter());
    expect(probe.result.current.f.hasActiveFilter).toBe(false);
    expect(probe.result.current.params.get('tab')).toBe('filtered');
    expect(probe.result.current.params.get('with_genres')).toBeNull();
    expect(probe.result.current.params.get('sort_by')).toBeNull();
  });
});
