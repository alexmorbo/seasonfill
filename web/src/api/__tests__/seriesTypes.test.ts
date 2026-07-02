import { describe, it, expect } from 'vitest';
import { isSonarrOnly } from '@/api/series';
import type { SeriesDetailResponse, SeriesHero, CastMember } from '@/api/series';

// C3 (story 966) compile-time contract. If a later change drops a field the
// SeriesDetail component tree reads, this file fails to COMPILE (the object
// literals stop satisfying the interface), tripping the build gate before the
// page silently loses a section. This is the C3-scope stand-in for the
// "useSeries returns SkeletonDTO with hero + sidebar" assertion, which belongs
// to C3b once useSeries actually returns the skeleton shape.
describe('series C3 retype — fat-compat surface intact', () => {
  it('SeriesDetailResponse exposes the section fields SeriesDetail.tsx reads', () => {
    const sample: SeriesDetailResponse = {
      series_id: 42,
      in_library_instances: ['homelab'],
      synced_at: new Date().toISOString(),
      degraded: [],
      hero: { title: 'For All Mankind', status: 'continuing', year_start: 2019 },
      download: { status: 'downloading', title: 'S05E03' },
      recent: [],
      external_links: { imdb_id: 'tt9243946', tmdb_id: 1396 },
      cast: [{ person_id: 1, tmdb_person_id: 1001, name: 'Joel', character_name: 'Ed' }],
      seasons: [],
    };
    expect(sample.hero?.title).toBe('For All Mankind');
    expect(sample.cast?.[0]?.tmdb_person_id).toBe(1001);
    expect(sample.external_links?.imdb_id).toBe('tt9243946');
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
