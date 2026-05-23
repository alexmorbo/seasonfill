import { describe, expect, it } from 'vitest';
import { CATEGORY, categoryOf, type CategoryKind } from './decision-category';

const ALL: readonly CategoryKind[] = [
  'all_complete', 'sonarr_handles', 'action_taken', 'blocked',
  'nothing_found', 'error', 'unknown',
];

describe('CATEGORY map', () => {
  it.each(ALL)('descriptor for %s has label + kind + priority', (k) => {
    const d = CATEGORY[k];
    expect(d.label.length).toBeGreaterThan(0);
    expect(d.kind).toMatch(/^(success|danger|warning|info|neutral)$/);
    expect(typeof d.priority).toBe('number');
  });

  it('action_taken has highest priority', () => {
    const sorted = [...ALL].sort((a, b) => CATEGORY[b].priority - CATEGORY[a].priority);
    expect(sorted[0]).toBe('action_taken');
  });
});

describe('categoryOf()', () => {
  it.each(ALL)('passes through known kind %s', (k) => {
    expect(categoryOf(k)).toBe(k);
  });
  it('falls back to unknown for undefined / empty / unrecognised', () => {
    expect(categoryOf(undefined)).toBe('unknown');
    expect(categoryOf('')).toBe('unknown');
    expect(categoryOf('future_category_value')).toBe('unknown');
  });
});
