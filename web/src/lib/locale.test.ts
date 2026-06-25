import { describe, it, expect } from 'vitest';
import { toBcp47 } from '@/lib/locale';

describe('toBcp47', () => {
  it.each([
    [undefined, undefined],
    [null, undefined],
    ['', undefined],
    ['en', 'en-US'],
    ['ru', 'ru-RU'],
    ['en-US', 'en-US'],
    ['ru-RU', 'ru-RU'],
    ['fr', 'fr'],
  ])('toBcp47(%p) === %p', (input, want) => {
    expect(toBcp47(input as string | undefined | null)).toBe(want);
  });
});
