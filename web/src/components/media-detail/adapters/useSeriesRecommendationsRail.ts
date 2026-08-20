import { useRef } from 'react';
import type { RefObject } from 'react';
import { useTranslation } from 'react-i18next';
import { toBcp47 } from '@/lib/locale';
import {
  useSeriesRecommendations,
  useIsSectionVisible,
} from '@/api/seriesRecommendations';
import type { RecommendationItem } from '../view-model';

export interface UseSeriesRecommendationsRailResult {
  readonly items: readonly RecommendationItem[];
  readonly isLoading: boolean;
  readonly visible: boolean;
  readonly sentinelRef: RefObject<HTMLElement | null>;
}

// U-4 sub-step B / §7 R1 — extracted verbatim from
// `series-detail/RecommendationsCarousel.tsx` (the query + visibility hook
// + sentinel ref), leaving the JSX render behind in the now data-only
// `MediaRecommendationsRail`. The ref/observer semantics are UNCHANGED:
// same `useIsSectionVisible` composer, same query params/gating, same
// `tmdb_series`-degraded-driven loading derivation — only the render is
// gone from this file.
export function useSeriesRecommendationsRail(
  seriesId: number | undefined,
  limit = 20,
): UseSeriesRecommendationsRailResult {
  const { i18n } = useTranslation();
  const ref = useRef<HTMLElement | null>(null);
  const visible = useIsSectionVisible(ref);
  const lang = toBcp47(i18n.resolvedLanguage);

  const query = useSeriesRecommendations({
    seriesId,
    limit,
    offset: 0,
    ...(lang ? { lang } : {}),
    enabled: visible,
    pollWhileDegraded: true,
  });

  const items = query.data?.items ?? [];
  const tmdbDegradedLocal = (query.data?.degraded ?? []).includes('tmdb_series');
  const isLoading = query.isLoading || (items.length === 0 && tmdbDegradedLocal);

  return { items, isLoading, visible, sentinelRef: ref };
}
