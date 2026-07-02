import { describe, it, expect } from 'vitest';
import { isSonarrOnly } from '@/api/series';
import type { SeriesSkeleton, SeriesHero, CastMember } from '@/api/series';

// C3b (story 968) compile-time contract. `useSeries` now returns the generated
// `seriesdetail.SkeletonDTO`; the hero/rail view-models are produced by the
// adapters. If the schema drops a field the adapters read, this file fails to
// COMPILE, tripping the build gate before the page silently loses a section.
describe('series C3b retype — SkeletonDTO surface intact', () => {
  it('SeriesSkeleton exposes the hero + sidebar fields adaptHero reads', () => {
    const sample: SeriesSkeleton = {
      series_id: 42,
      in_library_instances: ['homelab'],
      synced_at: new Date().toISOString(),
      degraded: [],
      hero: {
        title: { value: 'For All Mankind', lang: 'en-US' },
        year_start: 2019,
        content_rating: 'TV-MA',
        trailer_key: 'abc',
      },
      sidebar: {
        status: 'continuing',
        networks: [{ tmdb_id: 1, name: 'Apple TV+' }],
        original_language: 'en',
        first_air_date: '2019-11-01',
      },
    };
    expect(sample.hero?.title?.value).toBe('For All Mankind');
    expect(sample.sidebar?.status).toBe('continuing');
    expect(sample.sidebar?.networks?.[0]?.name).toBe('Apple TV+');
  });

  it('SeriesHero carries the rich primitives RatingDuo / RailCard consume', () => {
    const hero: SeriesHero = {
      title: 'X',
      tmdb_rating: { score: 8.1, votes: 2100 },
      content_rating: { rating: 'TV-MA' },
      trailer: { key: 'abc', site: 'YouTube', name: 'Trailer' },
      genres: [{ id: 1, name: 'Drama' }],
      networks: [{ id: 1, name: 'Apple TV+' }],
    };
    expect(hero.tmdb_rating?.score).toBe(8.1);
    expect(hero.content_rating?.rating).toBe('TV-MA');
    expect(hero.trailer?.key).toBe('abc');
  });

  it('isSonarrOnly treats an empty hero as Sonarr-only', () => {
    expect(isSonarrOnly({} as SeriesHero)).toBe(true);
  });

  it('CastMember keeps tmdb_person_id (CastStrip /person link guard)', () => {
    const m: CastMember = { person_id: 1, tmdb_person_id: 42, name: 'A' };
    expect(m.tmdb_person_id).toBe(42);
  });
});
