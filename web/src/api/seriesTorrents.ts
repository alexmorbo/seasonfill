import { useQuery, type UseQueryResult } from '@tanstack/react-query';
import { useEffect, useState, type RefObject } from 'react';
import { api } from '@/lib/api';
import type { components } from '@/api/schema';

export type SeriesTorrentsResponse = components['schemas']['dto.SeriesTorrentsResponse'];
export type TorrentRow = components['schemas']['dto.TorrentRow'];

// One of the 8 backend state-group tokens (PRD §4.3, schema.ts
// state_group docstring) plus the synthetic "deleted" UI state.
// Anything else falls back to "unknown" inside the chip component.
export type StateGroup =
  | 'downloading'
  | 'seeding'
  | 'stalled'
  | 'queued'
  | 'paused'
  | 'checking'
  | 'error'
  | 'unknown';

export interface UseSeriesTorrentsParams {
  readonly instance: string | undefined;
  readonly seriesId: number | undefined;
  // visible drives refetchInterval gating. The component composes
  // tab-visibility AND viewport-intersection into a single boolean
  // (see useIsSectionVisible) and feeds the result here.
  readonly visible: boolean;
  // mounted lets the page-level qBit-configured check disable the
  // query entirely (no key, no fetch). Default `true` for tests.
  readonly enabled?: boolean | undefined;
}

export function seriesTorrentsQueryKey(
  instance: string,
  seriesId: number,
): readonly ['series-torrents', string, number] {
  return ['series-torrents', instance, seriesId] as const;
}

// useSeriesTorrents — pollable per-series torrents inventory.
//
// Gating layers (in order, outermost first):
//   1) `enabled` — page-level qBit-configured guard.
//   2) `visible` — section visibility AND tab visibility (composer).
//   3) `refetchInterval` — 3000ms only when visible.
//
// refetchOnWindowFocus is intentionally true: when the operator
// alt-tabs back to a stale section, we want a tick BEFORE the
// next 3s boundary so the stale-banner clears immediately.
export function useSeriesTorrents({
  instance,
  seriesId,
  visible,
  enabled = true,
}: UseSeriesTorrentsParams): UseQueryResult<SeriesTorrentsResponse> {
  const ready =
    enabled && Boolean(instance) && typeof seriesId === 'number' && seriesId > 0;
  return useQuery<SeriesTorrentsResponse>({
    queryKey: ready
      ? seriesTorrentsQueryKey(instance as string, seriesId as number)
      : (['series-torrents', '', 0] as const),
    queryFn: () =>
      api<SeriesTorrentsResponse>(
        `/instances/${encodeURIComponent(instance as string)}/series/${seriesId}/torrents`,
      ),
    enabled: ready,
    refetchInterval: visible ? 3000 : false,
    refetchOnWindowFocus: true,
    staleTime: 0,
  });
}

// useIsSectionVisible — visibility composer. Returns true iff:
//   - document.visibilityState === 'visible'   (tab active), AND
//   - the watched element is intersecting the viewport.
//
// In tests (happy-dom / JSDOM) IntersectionObserver may exist but
// never fires, so the hook would stay `inView=false`. The fallback
// branch sets `inView=true` when IO is unavailable so tests that
// stub IO away exercise the data branches.
export function useIsSectionVisible(
  ref: RefObject<Element | null>,
): boolean {
  const [tabVisible, setTabVisible] = useState<boolean>(() =>
    typeof document === 'undefined' ? true : document.visibilityState === 'visible',
  );
  // Initial inView seeds true when IntersectionObserver is unavailable
  // (SSR + test envs) so polling can run; otherwise false until the
  // observer reports its first entry.
  const [inView, setInView] = useState<boolean>(() => typeof IntersectionObserver === 'undefined');

  useEffect(() => {
    if (typeof document === 'undefined') return;
    const onVis = () => setTabVisible(document.visibilityState === 'visible');
    document.addEventListener('visibilitychange', onVis);
    return () => document.removeEventListener('visibilitychange', onVis);
  }, []);

  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    if (typeof IntersectionObserver === 'undefined') {
      // Already seeded to true in initial state — nothing to observe.
      return;
    }
    const obs = new IntersectionObserver(
      (entries) => {
        const entry = entries[0];
        if (entry) setInView(entry.isIntersecting);
      },
      { rootMargin: '0px' },
    );
    obs.observe(el);
    return () => obs.disconnect();
  }, [ref]);

  return tabVisible && inView;
}
