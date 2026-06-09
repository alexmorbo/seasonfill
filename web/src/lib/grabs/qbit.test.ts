import { describe, expect, it } from 'vitest';
import { isKubeInternalHost } from './qbit';

describe('isKubeInternalHost', () => {
  it.each([
    ['http://qbittorrent-web:10095', true],
    ['http://qbittorrent:8080', true],
    ['https://internal/path', true],
  ])('treats dotless non-localhost host %s as internal', (url, expected) => {
    expect(isKubeInternalHost(url)).toBe(expected);
  });

  it.each([
    ['http://qbit.example.com', false],
    ['https://qbit.lan/', false],
    ['http://localhost:8080', false],
    ['http://127.0.0.1:8080', false],
  ])('treats %s as NOT internal', (url, expected) => {
    expect(isKubeInternalHost(url)).toBe(expected);
  });

  it.each([
    [null],
    [undefined],
    [''],
    ['   '],
    ['not a url'],
  ])('returns false for non-URL input %s', (input) => {
    expect(isKubeInternalHost(input as string | null | undefined)).toBe(false);
  });
});
