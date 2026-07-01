import { describe, it, expect } from 'vitest';
import { normalizeLangCode } from '@/i18n';

describe('normalizeLangCode', () => {
  it.each([
    ['en-US', 'en-US'],
    ['ru-RU', 'ru-RU'],
    ['en', 'en-US'],
    ['ru', 'ru-RU'],
    ['en-GB', 'en-US'],
    ['ru-BY', 'ru-RU'],
    ['EN', 'en-US'],
    ['RU-ru', 'ru-RU'],
    ['fr', null],
    ['de-DE', null],
    ['', null],
    [null, null],
    [undefined, null],
  ] as const)('normalizeLangCode(%p) === %p', (input, want) => {
    expect(normalizeLangCode(input as string | null | undefined)).toBe(want);
  });
});
