import { afterAll, beforeAll, describe, expect, it } from 'vitest';
import i18n from '@/i18n';

describe('RU plural — missing episodes count', () => {
  let originalLang: string;

  beforeAll(async () => {
    originalLang = i18n.language;
    await i18n.changeLanguage('ru');
  });

  afterAll(async () => {
    await i18n.changeLanguage(originalLang);
  });

  describe('series.tile.missing', () => {
    it('uses genitive singular for count=1 (_one)', () => {
      expect(i18n.t('series.tile.missing', { count: 1 })).toBe('нет 1 серии');
    });

    it('uses genitive plural for count=2 (_few)', () => {
      expect(i18n.t('series.tile.missing', { count: 2 })).toBe('нет 2 серий');
    });

    it('uses genitive plural for count=5 (_many)', () => {
      expect(i18n.t('series.tile.missing', { count: 5 })).toBe('нет 5 серий');
    });

    it('uses genitive singular for count=21 (_one)', () => {
      expect(i18n.t('series.tile.missing', { count: 21 })).toBe('нет 21 серии');
    });
  });

  describe('seriesDetail.library.missing', () => {
    it('uses genitive singular for count=1 (_one)', () => {
      expect(i18n.t('seriesDetail.library.missing', { count: 1 })).toBe('нет 1 серии');
    });

    it('uses genitive plural for count=2 (_few)', () => {
      expect(i18n.t('seriesDetail.library.missing', { count: 2 })).toBe('нет 2 серий');
    });

    it('uses genitive plural for count=5 (_many)', () => {
      expect(i18n.t('seriesDetail.library.missing', { count: 5 })).toBe('нет 5 серий');
    });

    it('uses genitive singular for count=21 (_one)', () => {
      expect(i18n.t('seriesDetail.library.missing', { count: 21 })).toBe('нет 21 серии');
    });
  });
});
