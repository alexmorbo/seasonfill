import { describe, it, expect } from 'vitest';
import { buildSonarrSeriesHref, slugifyTitle } from './sonarrUrl';

describe('slugifyTitle()', () => {
  it('lowercases and dash-collapses non-alphanumerics', () => {
    expect(slugifyTitle('For All Mankind')).toBe('for-all-mankind');
  });

  it('drops leading/trailing dashes', () => {
    expect(slugifyTitle('  ...Severance...  ')).toBe('severance');
  });

  it('collapses repeated punctuation into single dashes', () => {
    expect(slugifyTitle('Mr. & Mrs. Smith')).toBe('mr-mrs-smith');
  });

  it('keeps numerics (year-disambiguated titles)', () => {
    expect(slugifyTitle('Doctor Who (2005)')).toBe('doctor-who-2005');
  });

  it('returns empty string for null / undefined', () => {
    expect(slugifyTitle(null)).toBe('');
    expect(slugifyTitle(undefined)).toBe('');
  });

  it('strips combining diacritics so accented titles still slug', () => {
    expect(slugifyTitle('Café Society')).toBe('cafe-society');
  });
});

describe('buildSonarrSeriesHref()', () => {
  it('joins a clean base + slug', () => {
    expect(buildSonarrSeriesHref('https://sonarr.example.com', 'severance'))
      .toBe('https://sonarr.example.com/series/severance');
  });

  it('strips trailing slashes from the base', () => {
    expect(buildSonarrSeriesHref('https://sonarr.example.com/', 'andor'))
      .toBe('https://sonarr.example.com/series/andor');
    expect(buildSonarrSeriesHref('https://sonarr.example.com///', 'andor'))
      .toBe('https://sonarr.example.com/series/andor');
  });
});
