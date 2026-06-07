import { describe, expect, it } from 'vitest';
import { formatSize, formatEpisodeRange, formatImportDuration } from './format';

describe('formatSize', () => {
  it.each([
    [13_325_829_734, /12\.4 GB/], // For All Mankind sample
    [8_589_934_592, /8 GB/],      // 8.0 GB rounds — locale dependent
    [524_288_000, /500 MB/],      // < 1 GB falls to MB
    [null, /^—$/],
    [undefined, /^—$/],
  ])('formats %p → %s', (bytes, expected) => {
    expect(formatSize(bytes as number | null | undefined)).toMatch(expected);
  });
});

describe('formatEpisodeRange', () => {
  it('handles full pack', () => {
    expect(formatEpisodeRange(5, [1, 2, 3, 4, 5, 6, 7, 8, 9, 10], 10)).toBe('S05 · E1–10');
  });
  it('handles partial pack with totalInSeason suffix', () => {
    expect(formatEpisodeRange(3, [1, 2, 3, 4, 5, 6], 10)).toBe('S03 · E1–6 / 10');
  });
  it('handles single episode', () => {
    expect(formatEpisodeRange(2, [10], 10)).toBe('S02 · E10');
  });
  it('falls back when episode list missing', () => {
    expect(formatEpisodeRange(7, undefined, undefined)).toBe('S07');
  });
});

describe('formatImportDuration', () => {
  it('seconds-only', () => {
    const a = '2026-06-07T19:32:00Z';
    const b = '2026-06-07T19:32:41Z';
    expect(formatImportDuration(a, b)).toMatch(/41[сs]/);
  });
  it('minutes + seconds', () => {
    const a = '2026-06-07T19:32:00Z';
    const b = '2026-06-07T19:33:22Z';
    expect(formatImportDuration(a, b)).toMatch(/1[мm] 22[сs]/);
  });
  it('missing inputs return empty string', () => {
    expect(formatImportDuration(undefined, undefined)).toBe('');
  });
});
