import { describe, expect, it } from 'vitest';
import { applyGuidRewrites, isTrackerUrl, type GuidRewriteRule } from './guid-rewrite';

describe('applyGuidRewrites', () => {
  it('returns the input unchanged when the rule list is empty', () => {
    expect(applyGuidRewrites('http://x/y', [])).toBe('http://x/y');
  });

  it('applies a single rule when the substring matches', () => {
    const rules: GuidRewriteRule[] = [
      { from: 'http://rutracker-proxy.servarr.svc.cluster.local', to: 'https://rutracker.org' },
    ];
    expect(
      applyGuidRewrites(
        'http://rutracker-proxy.servarr.svc.cluster.local/forum/viewtopic.php?t=1',
        rules,
      ),
    ).toBe('https://rutracker.org/forum/viewtopic.php?t=1');
  });

  it('leaves the input untouched when no rule matches', () => {
    const rules: GuidRewriteRule[] = [
      { from: 'http://nnm-proxy', to: 'https://nnm-club.me' },
    ];
    expect(applyGuidRewrites('http://kinozal/x', rules)).toBe('http://kinozal/x');
  });

  it('applies rules in order — earlier rule sees the original input, later rule sees the result', () => {
    const rules: GuidRewriteRule[] = [
      { from: 'aaa', to: 'bbb' },
      { from: 'bbb', to: 'ccc' },
    ];
    // After rule 1: 'bbb-tail'. After rule 2: 'ccc-tail'.
    expect(applyGuidRewrites('aaa-tail', rules)).toBe('ccc-tail');
  });

  it('replaces every occurrence in the input, not just the first', () => {
    const rules: GuidRewriteRule[] = [{ from: 'x', to: 'Y' }];
    expect(applyGuidRewrites('xxx', rules)).toBe('YYY');
  });

  it('skips rules with an empty `from` so a partially-typed editor row does not break preview', () => {
    const rules: GuidRewriteRule[] = [
      { from: '', to: 'whatever' },
      { from: 'foo', to: 'bar' },
    ];
    expect(applyGuidRewrites('foo-baz', rules)).toBe('bar-baz');
  });

  it('treats empty `to` as "strip the substring"', () => {
    const rules: GuidRewriteRule[] = [{ from: '.cluster.local', to: '' }];
    expect(
      applyGuidRewrites('http://svc.cluster.local/x', rules),
    ).toBe('http://svc/x');
  });
});

describe('isTrackerUrl', () => {
  it('accepts http and https (case-insensitive scheme)', () => {
    expect(isTrackerUrl('http://x')).toBe(true);
    expect(isTrackerUrl('https://x')).toBe(true);
    expect(isTrackerUrl('HTTPS://x')).toBe(true);
  });

  it('rejects non-http(s) strings', () => {
    expect(isTrackerUrl('ftp://x')).toBe(false);
    expect(isTrackerUrl('magnet:?xt=urn:btih:abc')).toBe(false);
    expect(isTrackerUrl('abc123')).toBe(false);
    expect(isTrackerUrl('')).toBe(false);
  });
});
