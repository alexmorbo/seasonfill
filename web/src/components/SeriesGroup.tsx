import { ChevronRight, ArrowUpRight, AlertCircle } from 'lucide-react';
import { cn } from '@/lib/utils';
import { CategoryChip } from '@/components/CategoryChip';
import { StatusBadge } from '@/components/StatusBadge';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import type { SeriesGroup as SeriesGroupModel } from '@/lib/decision-grouping';

export function SeriesGroup({ group, expanded, onToggle, onOpenDecision }: {
  group: SeriesGroupModel;
  expanded: boolean;
  onToggle: () => void;
  onOpenDecision: (id: string) => void;
}) {
  const seasonCount = group.seasons.length;
  return (
    <div className="border-b border-border-faint last:border-b-0">
      <button type="button" onClick={onToggle}
        aria-expanded={expanded} aria-controls={`series-body-${group.seriesId}`}
        className={cn(
          'w-full flex items-center gap-3 px-4 py-3 text-left transition focus:outline-hidden focus-visible:bg-surface-2',
          expanded ? 'bg-surface-2' : 'hover:bg-surface-2',
        )}>
        <ChevronRight className={cn('w-3.5 h-3.5 text-muted transition-transform', expanded && 'rotate-90')} />
        <span className="font-medium min-w-[200px] truncate" data-testid="series-title">{group.seriesTitle}</span>
        <CategoryChip value={group.worstCategory} variant="compact" />
        <span className="text-[11px] text-faint font-mono">
          {seasonCount} season{seasonCount === 1 ? '' : 's'}
        </span>
      </button>
      {expanded && (
        <ul id={`series-body-${group.seriesId}`}
          className="flex flex-col gap-1 px-12 py-2"
          aria-label={`Seasons for ${group.seriesTitle}`}>
          {group.seasons.map((row) => {
            const d = row.decision;
            const guidShort = d.selected_guid ? d.selected_guid.slice(0, 10) : null;
            return (
              <li
                key={d.id}
                className={
                  'flex items-center gap-2 text-[12px] font-mono px-2 py-1.5 rounded bg-surface' +
                  (d.superseded_by_id ? ' line-through opacity-60' : '')
                }
                data-testid={d.superseded_by_id ? 'series-row-superseded' : 'series-row'}
              >
                <span className="text-faint shrink-0 w-10">S{String(row.seasonNumber).padStart(2, '0')}</span>
                <CategoryChip value={d.category} variant="compact" />
                {d.category === 'error' && d.error_detail && (
                  <Tooltip>
                    <TooltipTrigger asChild>
                      <button
                        type="button"
                        aria-label={`Error: ${d.error_detail}`}
                        data-testid="series-row-error-icon"
                        className="inline-flex items-center text-status-danger shrink-0 cursor-help focus:outline-hidden focus-visible:ring-1 focus-visible:ring-status-danger rounded-sm"
                      >
                        <AlertCircle className="w-3.5 h-3.5" aria-hidden="true" />
                      </button>
                    </TooltipTrigger>
                    <TooltipContent
                      side="top"
                      className="max-w-md whitespace-pre-wrap font-mono text-[11px]"
                    >
                      {d.error_detail}
                    </TooltipContent>
                  </Tooltip>
                )}
                <StatusBadge value={d.decision} mode="outcome" />
                <span className="text-muted truncate flex-1">{d.reason ?? ''}</span>
                {guidShort && <span className="text-faint">{guidShort}…</span>}
                <button type="button"
                  className="ml-1 p-1 rounded text-muted hover:text-foreground hover:bg-surface-2"
                  aria-label={`Open decision for ${group.seriesTitle} season ${row.seasonNumber}`}
                  onClick={() => d.id && onOpenDecision(d.id)}>
                  <ArrowUpRight className="w-3.5 h-3.5" />
                </button>
              </li>
            );
          })}
        </ul>
      )}
    </div>
  );
}