import { describe, it, expect } from 'vitest';
import { aggregateDegraded } from '@/api/series';

describe('aggregateDegraded', () => {
  it('dedupes across inputs', () => {
    const out = aggregateDegraded(
      ['tmdb_series'],
      ['tmdb_series', 'omdb'],
      ['omdb'],
    );
    expect([...out].sort()).toEqual(['omdb', 'tmdb_series']);
  });

  it('drops unknown tokens', () => {
    const out = aggregateDegraded(['tmdb_series', 'unknown_source']);
    expect(out).toEqual(['tmdb_series']);
  });

  it('tolerates undefined / empty inputs', () => {
    expect(aggregateDegraded(undefined, [], undefined)).toEqual([]);
  });

  it('returns empty array when nothing is degraded', () => {
    expect(aggregateDegraded()).toEqual([]);
  });

  it('preserves first-seen insertion order', () => {
    const out = aggregateDegraded(
      ['omdb'],
      ['tmdb_person', 'omdb'],
      ['tmdb_series'],
    );
    expect(out).toEqual(['omdb', 'tmdb_person', 'tmdb_series']);
  });
});
