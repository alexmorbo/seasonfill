import { useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { useGrabById } from '@/lib/api/grabEpisodeFiles';
import { cn } from '@/lib/utils';
import { relativeTime } from '@/lib/format';
import type { Grab } from '@/lib/grabs/chipBuilder';

export interface ReGrabThreadProps {
  readonly instance: string | null;
  readonly grab: Grab;
  readonly all: readonly Grab[];   // locally cached rows from useGrabs
  readonly open: boolean;
}

interface ThreadNode {
  readonly id: string;
  readonly isOriginal: boolean;
  readonly index: number;          // 0 = original, 1..N = re-grab order
  readonly time: string;
  readonly episodeCount: number;
}

// Walk replay_of_id upward from `grab` and replayed_by_ids downward.
// Build a linear sequence (original at the bottom, newest at top).
function buildSequence(grab: Grab, byId: ReadonlyMap<string, Grab>): readonly Grab[] {
  const seen = new Set<string>();
  const result: Grab[] = [];

  // Walk upward to find the root.
  let root: Grab = grab;
  while (root.replay_of_id) {
    const parent = byId.get(root.replay_of_id);
    if (!parent || seen.has(parent.id ?? '')) break;
    seen.add(parent.id ?? '');
    root = parent;
  }

  // Walk downward from root via replayed_by[0] (linear chain).
  let cursor: Grab | undefined = root;
  while (cursor) {
    if (cursor.id) {
      if (result.find((x) => x.id === cursor!.id)) break;
      result.push(cursor);
    }
    const nextId: string | undefined = cursor.replayed_by?.[0];
    cursor = nextId ? byId.get(nextId) : undefined;
  }

  return result.reverse();    // newest at the top
}

function nodeFromGrab(g: Grab, index: number, total: number): ThreadNode {
  return {
    id: g.id ?? `unknown-${index}`,
    isOriginal: index === total - 1,
    index: total - 1 - index,
    time: relativeTime(g.created_at ?? null),
    episodeCount: g.coverage_count ?? 0,
  };
}

export function ReGrabThread({
  instance, grab, all, open,
}: ReGrabThreadProps) {
  const { t } = useTranslation();
  const byId = useMemo(() => {
    const m = new Map<string, Grab>();
    for (const g of all) if (g.id) m.set(g.id, g);
    return m;
  }, [all]);

  // Find the missing ancestor (first unresolved replay_of_id) so we
  // can lazy-fetch it. We only fetch ONE ancestor — if it has more
  // unresolved ancestors of its own, the user can click again. This
  // keeps the request count bounded.
  const missingAncestorId = useMemo(() => {
    let cursor: Grab | undefined = grab;
    while (cursor?.replay_of_id) {
      const next = byId.get(cursor.replay_of_id);
      if (!next) return cursor.replay_of_id;
      cursor = next;
    }
    return null;
  }, [grab, byId]);

  // Triggered only when the thread is open AND we have an unresolved
  // ancestor. The fetched grab gets merged into a derived byId below.
  const ancestorQuery = useGrabById(instance, missingAncestorId, {
    enabled: open && Boolean(missingAncestorId),
  });

  const mergedById = useMemo(() => {
    if (!ancestorQuery.data) return byId;
    const m = new Map(byId);
    const a = ancestorQuery.data;
    if (a.id) m.set(a.id, a);
    return m;
  }, [byId, ancestorQuery.data]);

  const sequence = useMemo(
    () => buildSequence(grab, mergedById),
    [grab, mergedById],
  );

  if (sequence.length <= 1) return null;     // no thread to show

  const nodes = sequence.map((g, i) => nodeFromGrab(g, i, sequence.length));

  return (
    <div
      data-testid={`regrab-thread-${grab.id ?? 'unknown'}`}
      className={cn(
        'mt-1 pl-4 relative flex flex-col gap-1.5',
        'before:content-[""] before:absolute before:left-1 before:top-2 before:bottom-2',
        'before:w-[1.5px] before:bg-border-strong',
      )}
    >
      {nodes.map((n) => (
        <div
          key={n.id}
          data-testid={`regrab-node-${n.id}`}
          className="relative font-mono text-[12px] text-tx-muted"
        >
          <span
            aria-hidden="true"
            className={cn(
              'absolute -left-[15px] top-1 size-2 rounded-full border-[1.5px]',
              n.isOriginal
                ? 'bg-bg-surface border-border-strong'
                : 'bg-warn border-warn',
            )}
          />
          {n.isOriginal
            ? t('grabs.regrab.thread.original', {
                time: n.time,
                count: n.episodeCount,
              })
            : t('grabs.regrab.thread.node', {
                index: n.index,
                time: n.time,
                count: n.episodeCount,
              })}
        </div>
      ))}
    </div>
  );
}
