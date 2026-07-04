import { describe, expect, it } from 'vitest';
import { buildListQuery, decisionsListKey } from './decisions';

describe('buildListQuery — lang (W15-8)', () => {
  it('omits lang when empty', () => {
    const q = buildListQuery('main', 'all', '', 200, '');
    expect(q).not.toContain('lang=');
  });

  it('appends lang when set', () => {
    const q = buildListQuery('main', 'all', '', 200, 'ru-RU');
    expect(q).toContain('lang=ru-RU');
  });

  it('defaults lang to empty (backward-compatible arity)', () => {
    const q = buildListQuery('main', 'all', '', 200);
    expect(q).not.toContain('lang=');
  });
});

describe('decisionsListKey — lang scoping (W15-8)', () => {
  it('includes lang in the key', () => {
    expect(decisionsListKey('main', '7d', 'ru-RU')).toEqual([
      'decisions', 'list', 'main', '7d', 'ru-RU',
    ]);
  });

  it('defaults lang to empty', () => {
    expect(decisionsListKey('main', '7d')).toEqual([
      'decisions', 'list', 'main', '7d', '',
    ]);
  });
});
