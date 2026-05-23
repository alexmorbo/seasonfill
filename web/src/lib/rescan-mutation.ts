import { useMutation, useQueryClient } from '@tanstack/react-query';
import { toast } from 'sonner';
import { api, ApiError } from './api';
import type { Decision, DecisionList } from './decisions';

export interface RescanDecisionInput {
  readonly decisionId: string;
}

// Shape of the React Query cache entry for any `useDecisions(...)` —
// useInfiniteQuery wraps pages + pageParams under these keys. We
// only need to mutate `pages` (a non-empty list of DecisionList
// objects). pageParams stays untouched.
interface InfiniteDecisionData {
  readonly pages: readonly DecisionList[];
  readonly pageParams: readonly unknown[];
}

// Prepend `fresh` to the first page of an infinite-query cache entry.
// Used by setQueriesData to seed the new rescan result across every
// useDecisions variant (Decisions list, ScanDetail filter, etc.)
// before invalidations fire — closes the race that would otherwise
// flash an empty "Decision not found" state during navigation.
function prependToFirstPage(
  prev: InfiniteDecisionData | undefined,
  fresh: Decision,
): InfiniteDecisionData | undefined {
  if (!prev?.pages || prev.pages.length === 0) return prev;
  const [first, ...rest] = prev.pages;
  if (!first) return prev;
  const items = first.items ?? [];
  // De-dupe: if invalidation already returned before this seed runs
  // (unlikely but cheap to guard), avoid two copies of the same row.
  if (items.some((d) => d.id === fresh.id)) return prev;
  const nextFirst: DecisionList = { ...first, items: [fresh, ...items] };
  return { ...prev, pages: [nextFirst, ...rest] };
}

// Toast-as-side-effect lives in the hook (mirror grab-mutation).
// 409 branches map to specific user-actionable messages; everything
// else falls through to a generic toast. On 200 we ALSO seed the
// cache so consumers can render the new decision immediately.
export function useRescanDecision() {
  const qc = useQueryClient();
  return useMutation<Decision, ApiError, RescanDecisionInput>({
    mutationFn: ({ decisionId }) =>
      api<Decision>(`/decisions/${decisionId}/rescan`, { method: 'POST' }),
    onSuccess: (fresh) => {
      // Seed BEFORE invalidating. setQueriesData updates every cached
      // ['decisions', ...] entry — list page, scan-detail filter, etc.
      // The drawer's flattenDecisions(...) picks up the new row on
      // the very next render, eliminating the "Decision not found"
      // flash during the auto-navigate (019 §2 race analysis).
      if (fresh.id) {
        qc.setQueriesData<InfiniteDecisionData | undefined>(
          { queryKey: ['decisions'] },
          (prev) => prependToFirstPage(prev, fresh),
        );
      }
      // Decisions list (drawer rows), scan detail counters, and the
      // running scan summary all need refresh. Scan counters do NOT
      // change (017 §3.4: same scan_run_id, no new ScanRecord) but
      // invalidating /scans is cheap and guards against a stale
      // grabs/decisions ratio when the operator rescans during a
      // running scan.
      qc.invalidateQueries({ queryKey: ['decisions'] });
      qc.invalidateQueries({ queryKey: ['scans'] });
      qc.invalidateQueries({ queryKey: ['scan'] });
      // Toast wording reflects the auto-open behaviour added in 019.
      // When the response is missing an id (defensive — current
      // backend always sends one) the caller can't navigate, so we
      // soften the message to plain "complete" to avoid lying.
      toast.success(
        fresh.id
          ? 'Rescan complete — showing new decision'
          : 'Rescan complete',
      );
    },
    onError: (err) => {
      if (err.status === 409) {
        if (err.message.startsWith('decision already superseded')) {
          toast.error('Already rescanned — open the successor');
        } else if (err.message.startsWith('decision already executed')) {
          toast.error('Already grabbed against Sonarr — create a new scan');
        } else {
          toast.error(err.message);
        }
        return;
      }
      toast.error(`Rescan failed: ${err.message}`);
    },
  });
}
