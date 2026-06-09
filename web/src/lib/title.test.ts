import { describe, it, expect } from 'vitest';
import { formatSeriesTitle } from './title';

describe('formatSeriesTitle', () => {
  it('returns the title verbatim regardless of year (operator R2)', () => {
    expect(formatSeriesTitle('Severance', 2022)).toBe('Severance');
    expect(formatSeriesTitle('Andor')).toBe('Andor');
  });

  it('keeps Sonarr-embedded "(YYYY)" intact', () => {
    expect(formatSeriesTitle('Time (2021)', 2021)).toBe('Time (2021)');
    expect(formatSeriesTitle('The Count of Monte Cristo (2024)', 2025))
      .toBe('The Count of Monte Cristo (2024)');
  });

  it('trims whitespace', () => {
    expect(formatSeriesTitle('  Monster (2022)  ', 2022)).toBe('Monster (2022)');
    expect(formatSeriesTitle('  Andor  ')).toBe('Andor');
  });

  it('returns "" for nullish/empty input regardless of year', () => {
    expect(formatSeriesTitle(null)).toBe('');
    expect(formatSeriesTitle(undefined)).toBe('');
    expect(formatSeriesTitle('')).toBe('');
    expect(formatSeriesTitle('', 2020)).toBe('');
    expect(formatSeriesTitle(null, 2020)).toBe('');
  });

  it('ignores year=0 and negative years (no synthesis)', () => {
    expect(formatSeriesTitle('Andor', 0)).toBe('Andor');
    expect(formatSeriesTitle('Andor', -1)).toBe('Andor');
  });
});
