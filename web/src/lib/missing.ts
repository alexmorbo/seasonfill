import { useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { type UseQueryResult } from '@tanstack/react-query';
import { ApiError } from './api';
import {
  useSeriesCacheInfinite,
  flattenSeriesCachePages,
  type SeriesCacheItem,
} from './api/seriesCache';

// FE-local shapes for the synthesized queue payload. Mirrors the
// deleted BE per-instance dto.Missing* wire types; kept here because
// useMissing now projects the global series-cache feed into the
// legacy shape so QueueRow / lib/missing-sort can stay unchanged.
// Tracked for cleanup with the 493 lossy-projection follow-up.
export interface SeasonEpisodePresence {
  readonly number?: number;
  readonly present?: boolean;
  readonly title?: string;
}

export interface MissingSeasonStat {
  readonly season_number?: number;
  readonly missing_aired_count?: number;
  readonly aired_episode_count?: number;
  readonly episodes?: readonly SeasonEpisodePresence[];
}

export interface MissingSeries {
  readonly series_id?: number;
  readonly title?: string;
  readonly title_slug?: string;
  readonly monitored?: boolean;
  readonly total_missing_aired?: number;
  readonly seasons?: readonly MissingSeasonStat[];
  readonly year?: number;
  readonly poster_hash?: string;
}

export interface MissingSeriesList {
  readonly items?: readonly MissingSeries[];
  readonly total?: number;
}

// 493 / N-1c §scope-9 + §H — lossy projection over the new global
// catalog list. BE 492 deleted the legacy per-instance "missing"
// route; the replacement is `GET /api/v1/series?instance=&state=missing`
// which returns `SeriesCacheItem[]`. That shape is wire-compatible
// at the top level (`items`, `total`) but the per-row payload no
// longer carries `seasons[]`, `dropdowns_count`, or per-season
// stats. 494's queue rewrite will surface the missing fields via
// a new BE projection (see Open Note §5 in story 493). Until then
// QueueRow renders without the per-season strip — empty `seasons`
// drops the chip grid silently.
//
// `MissingSeriesList.total` is left undefined on the wire (the
// new endpoint reports `total` differently — it's the post-filter
// count rather than the raw missing count). The header reads
// `items.length` instead.
export function useMissing(
  name: string | undefined,
): UseQueryResult<MissingSeriesList, ApiError> {
  // Story E-1-B7: forward the user's raw BCP-47 language so the
  // queue/missing rows render localised series titles. Included in
  // the underlying infinite queryKey (via the `q` spread) so a
  // language switch refetches instead of serving en-US from cache.
  const { i18n } = useTranslation();
  const lang = i18n.resolvedLanguage ?? '';
  // B-46 (audit 2026-06-25): BE clamps `limit` to [1, 100]
  // (internal/catalog/rest/instances.go searchMaxLimit). 200 silently
  // 400s and the missing section never renders. useSeriesCacheInfinite
  // is cursor-paginated so capping per-page is lossless.
  const cache = useSeriesCacheInfinite(
    name ?? null,
    { state: 'missing', limit: 100, lang },
  );
  const items = useMemo<readonly MissingSeries[]>(
    () => projectCacheToMissing(flattenSeriesCachePages(cache.data?.pages)),
    [cache.data],
  );
  return useMemo(() => {
    // Synthesize a UseQueryResult-shaped object so call sites
    // don't have to change. Pass through status + error from the
    // underlying infinite query.
    const data: MissingSeriesList = {
      items,
      total: items.length,
    };
    return {
      ...cache,
      data: cache.isSuccess ? data : undefined,
    } as UseQueryResult<MissingSeriesList, ApiError>;
  }, [cache, items]);
}

function projectCacheToMissing(
  rows: readonly SeriesCacheItem[],
): readonly MissingSeries[] {
  return rows.map((r) => {
    // TODO: 493 lossy projection — 494 will rewrite useMissing to
    // surface full counts (`seasons[]`, dropdowns_count,
    // unmonitored_count) from a new BE projection.
    const out: MissingSeries = {
      series_id: r.sonarr_series_id,
      title: r.title,
      title_slug: r.title_slug,
      monitored: r.monitored,
      total_missing_aired: r.missing_count,
      seasons: [],
      ...(r.year !== undefined ? { year: r.year } : {}),
      ...(r.poster_hash ? { poster_hash: r.poster_hash } : {}),
    };
    return out;
  });
}

export type QueueSort = 'debt' | 'title' | 'year';

// Pure selector: filter by title substring (case-insensitive) and
// sort by debt/title/year. The list is bounded (≤500 in production)
// so we sort in place per render — no memo needed. Empty query
// returns the input order. `year` sorts undefined-last.
export function selectQueueRows(
  items: readonly MissingSeries[],
  q: string,
  sort: QueueSort,
): readonly MissingSeries[] {
  const needle = q.trim().toLowerCase();
  const filtered = needle.length === 0
    ? items
    : items.filter((s) => (s.title ?? '').toLowerCase().includes(needle));
  const sorted = [...filtered];
  switch (sort) {
    case 'title':
      sorted.sort((a, b) =>
        (a.title ?? '').localeCompare(b.title ?? '', undefined, { sensitivity: 'base' }),
      );
      break;
    case 'year':
      sorted.sort((a, b) => {
        const ya = a.year ?? -Infinity;
        const yb = b.year ?? -Infinity;
        return yb - ya;
      });
      break;
    case 'debt':
    default:
      sorted.sort((a, b) => (b.total_missing_aired ?? 0) - (a.total_missing_aired ?? 0));
      break;
  }
  return sorted;
}
