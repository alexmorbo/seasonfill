import { describe, it, expect } from 'vitest';
import { formatSeriesTitle, titleHasEmbeddedYear } from './title';

describe('formatSeriesTitle', () => {
  it('appends "(year)" when title has no embedded year', () => {
    expect(formatSeriesTitle('Severance', 2022)).toBe('Severance (2022)');
  });

  it('keeps title as-is when it already ends with "(YYYY)" and year matches', () => {
    expect(formatSeriesTitle('Time (2021)', 2021)).toBe('Time (2021)');
  });

  it('keeps title as-is when it already ends with "(YYYY)" but year mismatches', () => {
    // Sonarr's embedded year wins — see PRD example "The Count of Monte
    // Cristo (2024) (2025)" — we must NOT double it up under any flag.
    expect(formatSeriesTitle('The Count of Monte Cristo (2024)', 2025))
      .toBe('The Count of Monte Cristo (2024)');
  });

  it('returns just the trimmed title when no year is provided', () => {
    expect(formatSeriesTitle('Andor')).toBe('Andor');
  });

  it('returns just the title when year is undefined and title has embedded year', () => {
    expect(formatSeriesTitle('Bodyguard (2018)')).toBe('Bodyguard (2018)');
  });

  it('returns "" when title is null/undefined and no year', () => {
    expect(formatSeriesTitle(null)).toBe('');
    expect(formatSeriesTitle(undefined)).toBe('');
    expect(formatSeriesTitle('')).toBe('');
  });

  it('returns "year" when title is empty but year is provided', () => {
    expect(formatSeriesTitle('', 2020)).toBe('2020');
    expect(formatSeriesTitle(null, 2020)).toBe('2020');
  });

  it('skips year when year is 0 or negative', () => {
    expect(formatSeriesTitle('Andor', 0)).toBe('Andor');
    expect(formatSeriesTitle('Andor', -1)).toBe('Andor');
  });

  it('detects embedded year even with trailing whitespace before close paren', () => {
    expect(formatSeriesTitle('Monster (2022)   ', 2022)).toBe('Monster (2022)');
  });

  it('trims leading/trailing whitespace before regex match', () => {
    expect(formatSeriesTitle('  Monster (2022)  ', 2022)).toBe('Monster (2022)');
  });

  it('does NOT treat (123) or (12345) as a year', () => {
    expect(formatSeriesTitle('Catch-22 (X)', 2019)).toBe('Catch-22 (X) (2019)');
    expect(formatSeriesTitle('Show (12345)', 2019)).toBe('Show (12345) (2019)');
  });
});

describe('titleHasEmbeddedYear', () => {
  it('is true when title ends with "(YYYY)"', () => {
    expect(titleHasEmbeddedYear('Time (2021)')).toBe(true);
    expect(titleHasEmbeddedYear('Bodyguard (2018)  ')).toBe(true);
  });

  it('is false when title has no embedded year', () => {
    expect(titleHasEmbeddedYear('Severance')).toBe(false);
    expect(titleHasEmbeddedYear('')).toBe(false);
    expect(titleHasEmbeddedYear(null)).toBe(false);
    expect(titleHasEmbeddedYear(undefined)).toBe(false);
  });

  it('is false when parens contain non-year content', () => {
    expect(titleHasEmbeddedYear('Catch-22 (US)')).toBe(false);
    expect(titleHasEmbeddedYear('Show (12345)')).toBe(false);
  });
});
