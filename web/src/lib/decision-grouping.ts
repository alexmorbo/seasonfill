import type { Decision } from './decisions';
import { CATEGORY, categoryOf, type CategoryKind } from './decision-category';

export interface SeasonRow {
  readonly seasonNumber: number;
  readonly decision: Decision;
}

export interface SeriesGroup {
  // series_id is the join key; -1 sentinel groups all decisions missing
  // the id into a single bucket (vs one bucket per orphan row).
  readonly seriesId: number;
  // First non-empty title wins. Doubles as the URL-expand key (see §4).
  readonly seriesTitle: string;
  // First non-empty instance wins. Drives Sonarr deep-link lookup.
  readonly instance?: string | undefined;
  readonly seasons: readonly SeasonRow[];
  // Max-priority category among seasons; drives sortGroups + the
  // "default-expand when != all_complete" rule in ScanDetail.
  readonly worstCategory: CategoryKind;
  // Index of this series' first decision in the input array. Used as
  // a deterministic tiebreaker in sortGroups so live updates that bump
  // worstCategory don't shuffle the existing layout (Fix 4).
  readonly firstSeenIndex: number;
}

// Reduces a flat decision list into one group per series_id. Stable:
// seasons sorted ASC by season_number; missing → +Infinity (sort last).
// Records firstSeenIndex per series so sortGroups can keep render order
// stable across live polling updates (Fix 4).
export function groupBySeries(decisions: readonly Decision[]): readonly SeriesGroup[] {
  const acc = new Map<number, { title: string; instance: string | undefined; seasons: SeasonRow[]; firstSeenIndex: number }>();
  for (let i = 0; i < decisions.length; i++) {
    const d = decisions[i]!;
    const sid = d.series_id ?? -1;
    const row: SeasonRow = { seasonNumber: d.season_number ?? Number.POSITIVE_INFINITY, decision: d };
    const slot = acc.get(sid);
    if (slot) {
      if (!slot.title && d.series_title) slot.title = d.series_title;
      if (!slot.instance && d.instance) slot.instance = d.instance;
      slot.seasons.push(row);
    } else {
      acc.set(sid, {
        title: d.series_title ?? '—',
        instance: d.instance,
        seasons: [row],
        firstSeenIndex: i,
      });
    }
  }
  const groups: SeriesGroup[] = [];
  for (const [seriesId, { title, instance, seasons, firstSeenIndex }] of acc) {
    seasons.sort((a, b) => a.seasonNumber - b.seasonNumber);
    let worst: CategoryKind = 'all_complete';
    let worstPriority = CATEGORY.all_complete.priority;
    for (const r of seasons) {
      const k = categoryOf(r.decision.category);
      const p = CATEGORY[k].priority;
      if (p > worstPriority) { worst = k; worstPriority = p; }
    }
    groups.push({ seriesId, seriesTitle: title, instance, seasons, worstCategory: worst, firstSeenIndex });
  }
  return groups;
}

// Sort by worstCategory priority DESC, then firstSeenIndex ASC so live
// polling updates that shift a group's worstCategory (e.g. a fresh
// `error_*` decision lands) keep that group in its existing visual slot
// relative to siblings sharing the same priority (Fix 4). seriesTitle
// remains the final deterministic tiebreaker for groups with identical
// firstSeenIndex (impossible in practice, but keeps the order total).
export function sortGroups(groups: readonly SeriesGroup[]): readonly SeriesGroup[] {
  return [...groups].sort((a, b) => {
    const pa = CATEGORY[a.worstCategory].priority;
    const pb = CATEGORY[b.worstCategory].priority;
    if (pa !== pb) return pb - pa;
    if (a.firstSeenIndex !== b.firstSeenIndex) return a.firstSeenIndex - b.firstSeenIndex;
    return a.seriesTitle.localeCompare(b.seriesTitle);
  });
}
