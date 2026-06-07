import { describe, expect, it } from 'vitest';
import { buildQbitDeepLink } from './qbit';

describe('buildQbitDeepLink', () => {
  it.each([
    ['http://qbit:8080', 'C2CB0D9E', 'http://qbit:8080/#/torrent/c2cb0d9e'],
    ['http://qbit:8080/', 'AbCdEf', 'http://qbit:8080/#/torrent/abcdef'],
    ['http://qbit:8080///', 'F00f', 'http://qbit:8080/#/torrent/f00f'],
    ['https://qbit.lan', 'ABCD1234', 'https://qbit.lan/#/torrent/abcd1234'],
  ])('builds link for base=%s hash=%s', (base, hash, expected) => {
    expect(buildQbitDeepLink(base, hash)).toBe(expected);
  });

  it.each([
    [null, undefined],
    [undefined, 'abcd'],
    ['http://qbit:8080', null],
    ['', 'abcd'],
    ['  ', 'abcd'],
    ['http://qbit:8080', ''],
  ])('returns null when base=%s or hash=%s missing', (base, hash) => {
    expect(buildQbitDeepLink(base as string | null | undefined, hash as string | null | undefined)).toBeNull();
  });
});
