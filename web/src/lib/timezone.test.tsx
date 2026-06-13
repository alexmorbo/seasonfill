import { describe, expect, it } from 'vitest';
import { formatDate, currentHourIn, listIANAZones } from './timezone';

describe('formatDate', () => {
  it('renders the given Date in the requested zone (short)', () => {
    const d = new Date('2026-06-15T12:00:00Z'); // 12:00 UTC
    const out = formatDate(d, 'time', { tz: 'Asia/Tokyo', locale: 'en-GB' });
    expect(out).toBe('21:00'); // Tokyo = UTC+9
  });
  it('returns the fallback for null / NaN input', () => {
    expect(formatDate(null)).toBe('—');
    expect(formatDate('not-a-date')).toBe('—');
    expect(formatDate(null, 'date', { fallback: 'n/a' })).toBe('n/a');
  });
  it('shortDateTime keeps dd.MM HH:mm layout in the requested zone', () => {
    const d = new Date('2026-06-15T23:30:00Z');
    const out = formatDate(d, 'shortDateTime', { tz: 'Europe/Moscow' });
    expect(out).toBe('16.06 02:30'); // Moscow = UTC+3 → next day
  });
  it('falls back to browser zone when the supplied tz is invalid', () => {
    const d = new Date('2026-06-15T12:00:00Z');
    expect(() => formatDate(d, 'time', { tz: 'Not/A/Zone', locale: 'en-GB' })).not.toThrow();
  });
});

describe('currentHourIn', () => {
  it('returns 0..23 in the given zone', () => {
    const at = new Date('2026-06-15T00:30:00Z');
    expect(currentHourIn('Asia/Tokyo', at)).toBe(9); // 09:30 JST
    expect(currentHourIn('America/New_York', at)).toBe(20); // 20:30 prev day EDT
  });
});

describe('listIANAZones', () => {
  it('returns a non-empty list', () => {
    const zs = listIANAZones();
    expect(zs.length).toBeGreaterThan(20);
    // Sanity-check the list shape — must contain a well-known IANA name
    // recognised across every runtime. UTC / Etc/UTC are not always
    // listed by Intl.supportedValuesOf; Europe/London is.
    expect(zs).toContain('Europe/London');
  });
});
