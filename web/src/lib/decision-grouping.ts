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
  readonly seasons: readonly SeasonRow[];
  // Max-priority category among seasons; drives sortGroups + the
  // "default-expand when != all_complete" rule in ScanDetail.
  readonly worstCategory: CategoryKind;
}

// Reduces a flat decision list into one group per series_id. Stable:
// seasons sorted ASC by season_number; missing → +Infinity (sort last).
export function groupBySeries(decisions: readonly Decision[]): readonly SeriesGroup[] {
  const acc = new Map<number, { title: string; seasons: SeasonRow[] }>();
  for (const d of decisions) {
    const sid = d.series_id ?? -1;
    const row: SeasonRow = { seasonNumber: d.season_number ?? Number.POSITIVE_INFINITY, decision: d };
    const slot = acc.get(sid);
    if (slot) {
      if (!slot.title && d.series_title) slot.title = d.series_title;
      slot.seasons.push(row);
    } else {
      acc.set(sid, { title: d.series_title ?? '—', seasons: [row] });
    }
  }
  const groups: SeriesGroup[] = [];
  for (const [seriesId, { title, seasons }] of acc) {
    seasons.sort((a, b) => a.seasonNumber - b.seasonNumber);
    let worst: CategoryKind = 'all_complete';
    let worstPriority = CATEGORY.all_complete.priority;
    for (const r of seasons) {
      const k = categoryOf(r.decision.category);
      const p = CATEGORY[k].priority;
      if (p > worstPriority) { worst = k; worstPriority = p; }
    }
    groups.push({ seriesId, seriesTitle: title, seasons, worstCategory: worst });
  }
  return groups;
}

// Sort by worstCategory priority DESC, then title ASC for stable render.
export function sortGroups(groups: readonly SeriesGroup[]): readonly SeriesGroup[] {
  return [...groups].sort((a, b) => {
    const pa = CATEGORY[a.worstCategory].priority;
    const pb = CATEGORY[b.worstCategory].priority;
    if (pa !== pb) return pb - pa;
    return a.seriesTitle.localeCompare(b.seriesTitle);
  });
}
