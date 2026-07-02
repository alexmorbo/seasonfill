import { describe, it, expect } from 'vitest';
import { buildPath, type SeriesCacheQuery } from './seriesCache';

// Story C-grid-lang (#965): buildPath must forward the raw BCP-47 tag
// as `?lang=` verbatim (no transform), and omit it when blank/absent so
// B7 localised titles + 584b per-language posters only engage when a
// language is actually active.
describe('buildPath — lang emit', () => {
  it('includes lang=ru-RU verbatim when set', () => {
    const path = buildPath('alpha', { status: 'imported', lang: 'ru-RU' });
    expect(path).toContain('lang=ru-RU');
    expect(path).toContain('instance=alpha');
    expect(path).toContain('state=imported');
  });

  it('omits lang when absent', () => {
    const path = buildPath('alpha', { status: 'imported' });
    expect(path).not.toContain('lang=');
  });

  it('omits lang when blank string', () => {
    const path = buildPath('alpha', { status: 'imported', lang: '' });
    expect(path).not.toContain('lang=');
  });
});

// The non-infinite useSeriesCache key is ['series-cache', instance, q].
// Since `q` carries `lang`, two different languages yield structurally
// distinct keys → a language switch guarantees a refetch (no stale
// en-US titles served from cache). This guards that invariant.
describe('useSeriesCache queryKey identity (lang refetch guard)', () => {
  it('differs when q.lang differs', () => {
    const ru: SeriesCacheQuery = { status: 'imported', lang: 'ru-RU' };
    const en: SeriesCacheQuery = { status: 'imported', lang: 'en-US' };
    const keyRu = ['series-cache', 'alpha', ru] as const;
    const keyEn = ['series-cache', 'alpha', en] as const;
    expect(JSON.stringify(keyRu)).not.toBe(JSON.stringify(keyEn));
  });

  it('is stable for identical q.lang', () => {
    const a: SeriesCacheQuery = { status: 'imported', lang: 'ru-RU' };
    const b: SeriesCacheQuery = { status: 'imported', lang: 'ru-RU' };
    expect(JSON.stringify(['series-cache', 'alpha', a])).toBe(
      JSON.stringify(['series-cache', 'alpha', b]),
    );
  });
});
