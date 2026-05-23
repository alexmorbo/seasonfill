import { describe, expect, it } from 'vitest';
import { readExpanded, writeExpanded } from './url-expand';

describe('readExpanded()', () => {
  it('returns empty Set for empty / missing param', () => {
    expect(readExpanded('').size).toBe(0);
    expect(readExpanded('outcome=grab').size).toBe(0);
    expect(readExpanded('expanded=').size).toBe(0);
  });
  it('decodes comma-separated members', () => {
    const got = readExpanded('expanded=Severance,Andor');
    expect(got.has('Severance')).toBe(true);
    expect(got.has('Andor')).toBe(true);
  });
  it('decodes URL-encoded special chars (spaces + apostrophes)', () => {
    const enc = `${encodeURIComponent('Dark Matter')},${encodeURIComponent("X-Men '97")}`;
    const got = readExpanded(`expanded=${enc}`);
    expect(got.has('Dark Matter')).toBe(true);
    expect(got.has("X-Men '97")).toBe(true);
  });
});

describe('writeExpanded()', () => {
  it('keeps an empty `expanded=` param when set is empty (user-override signal)', () => {
    const out = writeExpanded('expanded=A,B&outcome=grab', new Set());
    const sp = new URLSearchParams(out);
    expect(sp.has('expanded')).toBe(true);
    expect(sp.get('expanded')).toBe('');
    expect(sp.get('outcome')).toBe('grab');
  });
  it('encodes special chars on write (single-encoded `%20`, not `+`)', () => {
    const out = writeExpanded('', new Set(["X-Men '97"]));
    expect(out).toContain('expanded=');
    expect(out).toContain(encodeURIComponent("X-Men '97"));
  });
  it('round-trips a non-trivial set', () => {
    const original = new Set(['Severance', "X-Men '97", 'Dark Matter']);
    expect(readExpanded(writeExpanded('', original))).toEqual(original);
  });
  it('preserves other params untouched', () => {
    const sp = new URLSearchParams(writeExpanded('outcome=grab&q=foo', new Set(['Severance'])));
    expect(sp.get('outcome')).toBe('grab');
    expect(sp.get('q')).toBe('foo');
    expect(sp.get('expanded')).toContain('Severance');
  });
});
