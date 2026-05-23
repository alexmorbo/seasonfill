import { describe, expect, it } from 'vitest';
import { groupBySeries, sortGroups } from './decision-grouping';
import type { Decision } from './decisions';
import { DtoDecisionCategory, DtoDecisionDecision } from '@/api/schema';

const dec = (over: Partial<Decision>): Decision => ({
  id: Math.random().toString(36).slice(2),
  instance: 'alpha', scan_run_id: 'run-1', decision: DtoDecisionDecision.skip,
  reason: 'skip_no_missing', category: DtoDecisionCategory.all_complete,
  created_at: new Date().toISOString(),
  ...over,
});

describe('groupBySeries()', () => {
  it('returns [] for empty input', () => { expect(groupBySeries([])).toEqual([]); });

  it('reduces by series_id and sorts seasons ASC', () => {
    const groups = groupBySeries([
      dec({ series_id: 1, series_title: 'A', season_number: 3 }),
      dec({ series_id: 1, series_title: 'A', season_number: 1 }),
      dec({ series_id: 1, series_title: 'A', season_number: 2 }),
      dec({ series_id: 2, series_title: 'B', season_number: 1 }),
    ]);
    expect(groups).toHaveLength(2);
    expect(groups.find((g) => g.seriesId === 1)!.seasons.map((s) => s.seasonNumber)).toEqual([1, 2, 3]);
  });

  it('worstCategory is max-priority among seasons (error>blocked>all_complete)', () => {
    const [g] = groupBySeries([
      dec({ series_id: 5, series_title: 'Dark', season_number: 1, category: DtoDecisionCategory.all_complete }),
      dec({ series_id: 5, series_title: 'Dark', season_number: 2, category: DtoDecisionCategory.error }),
      dec({ series_id: 5, series_title: 'Dark', season_number: 3, category: DtoDecisionCategory.blocked }),
    ]);
    expect(g!.worstCategory).toBe('error');
  });

  it('buckets decisions missing series_id under one synthetic group', () => {
    const groups = groupBySeries([dec({ series_title: 'Lost' }), dec({ series_title: 'Lost 2' })]);
    expect(groups).toHaveLength(1);
    expect(groups[0]!.seasons).toHaveLength(2);
  });
});

describe('sortGroups()', () => {
  it('orders by worstCategory priority DESC, then title ASC', () => {
    // priorities: action_taken=5, error=4, all_complete=0 (decision-category.ts).
    const groups = groupBySeries([
      dec({ series_id: 1, series_title: 'Andor', category: DtoDecisionCategory.all_complete }),
      dec({ series_id: 2, series_title: 'Severance', category: DtoDecisionCategory.action_taken }),
      dec({ series_id: 3, series_title: 'Beat', category: DtoDecisionCategory.error }),
      dec({ series_id: 4, series_title: 'Zoo', category: DtoDecisionCategory.action_taken }),
    ]);
    expect(sortGroups(groups).map((g) => g.seriesTitle)).toEqual(['Severance', 'Zoo', 'Beat', 'Andor']);
  });

  it('falls back to firstSeenIndex ASC when worstCategory ties (Fix 4)', () => {
    // B appears first in the input → must render first even though A < B
    // alphabetically. Locks the order against future live updates.
    const groups = groupBySeries([
      dec({ series_id: 1, series_title: 'B', category: DtoDecisionCategory.all_complete }),
      dec({ series_id: 2, series_title: 'A', category: DtoDecisionCategory.all_complete }),
    ]);
    expect(sortGroups(groups).map((g) => g.seriesTitle)).toEqual(['B', 'A']);
  });

  it('preserves position when a later decision raises worstCategory (Fix 4)', () => {
    // Initial pass: Andor (all_complete, idx 0), Beat (all_complete, idx 1).
    const initial = groupBySeries([
      dec({ series_id: 1, series_title: 'Andor', category: DtoDecisionCategory.all_complete }),
      dec({ series_id: 2, series_title: 'Beat', category: DtoDecisionCategory.all_complete }),
    ]);
    expect(sortGroups(initial).map((g) => g.seriesTitle)).toEqual(['Andor', 'Beat']);

    // Live update: Beat's S2 hits an error — both groups still share the
    // same priority bucket from the user's POV when they were positioned;
    // Beat moves above Andor because error > all_complete, but among
    // its new peers position is governed by firstSeenIndex, not title.
    const updated = groupBySeries([
      dec({ series_id: 1, series_title: 'Andor', season_number: 1, category: DtoDecisionCategory.all_complete }),
      dec({ series_id: 2, series_title: 'Beat', season_number: 1, category: DtoDecisionCategory.all_complete }),
      dec({ series_id: 2, series_title: 'Beat', season_number: 2, category: DtoDecisionCategory.error }),
    ]);
    expect(sortGroups(updated).map((g) => g.seriesTitle)).toEqual(['Beat', 'Andor']);
  });
});
