import { useMemo, useState } from 'react';
import {
  Accordion, AccordionItem, AccordionTrigger, AccordionContent,
} from '@/components/ui/accordion';
import { groupBySeries, sortGroups } from '@/lib/decision-grouping';
import type { Decision } from '@/lib/api/decisions';
import { reduceLatestPerSeason } from '@/lib/api/decisions';
import { useInstances } from '@/lib/instances';
import { DecisionsSeriesRow } from './DecisionsSeriesRow';
import { DecisionsSeasonRow } from './DecisionsSeasonRow';

export interface DecisionsSeriesAccordionProps {
  readonly rows: readonly Decision[];
  readonly onOpenSeason: (d: Decision) => void;
}

export function DecisionsSeriesAccordion({
  rows, onOpenSeason,
}: DecisionsSeriesAccordionProps) {
  const grouped = useMemo(() => sortGroups(groupBySeries(rows)), [rows]);
  const latest = useMemo(() => reduceLatestPerSeason(rows), [rows]);
  const instancesQ = useInstances();
  const instancePublicURLs = useMemo(() => {
    const out = new Map<string, string>();
    for (const i of instancesQ.data?.instances ?? []) {
      if (i.name && i.public_url && i.public_url.length > 0) {
        out.set(i.name, i.public_url);
      }
    }
    return out;
  }, [instancesQ.data]);

  // Default-open series whose worstCategory is not all_complete.
  const defaultOpenIds = useMemo(
    () => grouped
      .filter((g) => g.worstCategory !== 'all_complete')
      .map((g) => String(g.seriesId)),
    [grouped],
  );

  // Track user-explicit toggles as deltas vs the computed defaults.
  // Forced-open: user opened a default-closed series. Forced-closed:
  // user collapsed a default-open one. Re-deriving the value each render
  // (vs syncing via useEffect) keeps the open-set fresh when polling
  // bumps a series into a higher-priority bucket.
  const [forcedOpen, setForcedOpen] = useState<ReadonlySet<string>>(() => new Set());
  const [forcedClosed, setForcedClosed] = useState<ReadonlySet<string>>(() => new Set());
  const openIds = useMemo(() => {
    const out = new Set<string>(defaultOpenIds);
    for (const id of forcedOpen) out.add(id);
    for (const id of forcedClosed) out.delete(id);
    return Array.from(out);
  }, [defaultOpenIds, forcedOpen, forcedClosed]);

  const onValueChange = (next: string[]) => {
    const nextSet = new Set(next);
    const defaultSet = new Set(defaultOpenIds);
    const fOpen = new Set<string>();
    const fClosed = new Set<string>();
    for (const id of nextSet) if (!defaultSet.has(id)) fOpen.add(id);
    for (const id of defaultSet) if (!nextSet.has(id)) fClosed.add(id);
    setForcedOpen(fOpen);
    setForcedClosed(fClosed);
  };

  if (grouped.length === 0) return null;

  return (
    <Accordion
      type="multiple"
      value={openIds}
      onValueChange={onValueChange}
      className="w-full"
      data-testid="decisions-series-accordion"
    >
      {grouped.map((g) => {
        let stuckCycles = 0;
        for (const s of g.seasons) {
          const key = `${g.seriesId}:${s.seasonNumber}`;
          stuckCycles += latest.get(key)?.streakNothing ?? 0;
        }
        return (
          <AccordionItem
            key={String(g.seriesId)}
            value={String(g.seriesId)}
            className="border-b border-border-faint"
          >
            <AccordionTrigger className="hover:no-underline py-2">
              <DecisionsSeriesRow
                seriesTitle={g.seriesTitle}
                worstCategory={g.worstCategory}
                seasonCount={g.seasons.length}
                stuckCycles={stuckCycles}
                open={openIds.includes(String(g.seriesId))}
                instance={g.instance}
                seriesId={g.seriesId}
                sonarrPublicURL={g.instance ? instancePublicURLs.get(g.instance) : undefined}
              />
            </AccordionTrigger>
            <AccordionContent className="pt-0 pb-2">
              <div className="flex flex-col gap-0.5 pl-6">
                {g.seasons.map((s) => {
                  const key = `${g.seriesId}:${s.seasonNumber}`;
                  const count = latest.get(key)?.count ?? 1;
                  return (
                    <DecisionsSeasonRow
                      key={`${g.seriesId}:${s.seasonNumber}`}
                      decision={s.decision}
                      decisionCount={count}
                      onOpen={onOpenSeason}
                    />
                  );
                })}
              </div>
            </AccordionContent>
          </AccordionItem>
        );
      })}
    </Accordion>
  );
}
