import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import i18n from '@/i18n';
import { relativeTime } from './format';

describe('relativeTime locale', () => {
  let saved: string;
  beforeEach(() => { saved = i18n.resolvedLanguage ?? 'en'; });
  afterEach(async () => { await i18n.changeLanguage(saved); });

  it('renders English narrow units by default', async () => {
    await i18n.changeLanguage('en');
    const out = relativeTime(new Date(Date.now() - 3_600_000).toISOString());
    // narrow style: "1h ago" / "1 hr. ago" depending on the JS runtime.
    expect(out).toMatch(/h/);
  });

  it('renders Russian units when language is ru', async () => {
    await i18n.changeLanguage('ru');
    const out = relativeTime(new Date(Date.now() - 3_600_000).toISOString());
    // Cyrillic presence is the assertion — exact substring varies by
    // engine ("ч" vs "час назад").
    expect(out).toMatch(/[а-я]/i);
  });

  it('reverts cleanly back to en on subsequent calls', async () => {
    await i18n.changeLanguage('ru');
    relativeTime(new Date().toISOString());
    await i18n.changeLanguage('en');
    const out = relativeTime(new Date(Date.now() - 60_000).toISOString());
    expect(out).not.toMatch(/[а-я]/i);
  });
});
