import { describe, it, expect } from 'vitest';
import {
  reasonCategory,
  categoryToBucket,
  REASON_CATEGORY_OPTIONS,
  REASON_CATEGORY_DOT_CLASS,
} from './reasonCategory';

describe('reasonCategory()', () => {
  const cases: ReadonlyArray<[string | undefined, ReturnType<typeof reasonCategory>]> = [
    ['grab_selected', 'done'],
    ['upgrade_available', 'done'],
    ['nothing_above_threshold', 'none'],
    ['no_candidates', 'none'],
    ['error_fetch_releases', 'none'],
    ['blocked_cooldown', 'blocked'],
    ['skip_series_in_cooldown', 'blocked'],
    ['sonarr_handles', 'sonarr'],
    ['skip_unmonitored_season', 'sonarr'],
    ['all_complete', 'ok'],
    ['skip_no_missing_episodes', 'ok'],
    ['some_unknown_reason_xyz', 'none'],
    [undefined, 'none'],
    [null as unknown as undefined, 'none'],
    ['', 'none'],
  ];
  it.each(cases)('maps %s → %s', (reason, want) => {
    expect(reasonCategory(reason)).toBe(want);
  });
});

describe('categoryToBucket()', () => {
  it.each([
    ['action_taken', 'done'],
    ['nothing_found', 'none'],
    ['error', 'none'],
    ['blocked', 'blocked'],
    ['sonarr_handles', 'sonarr'],
    ['all_complete', 'ok'],
    ['unknown', 'none'],
    [undefined, 'none'],
    [null, 'none'],
  ] as const)('maps %s → %s', (cat, want) => {
    expect(categoryToBucket(cat)).toBe(want);
  });
});

describe('constants', () => {
  it('REASON_CATEGORY_OPTIONS has 5 entries', () => {
    expect(REASON_CATEGORY_OPTIONS).toHaveLength(5);
  });
  it('every option has a dot class', () => {
    for (const opt of REASON_CATEGORY_OPTIONS) {
      expect(REASON_CATEGORY_DOT_CLASS[opt]).toMatch(/^bg-/);
    }
  });
});
