import { useTranslation } from 'react-i18next';
import { Loader2 } from 'lucide-react';
import { cn } from '@/lib/utils';
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip';
import type { DegradedSource } from '@/api/series';

export interface DegradedChipProps {
  readonly sources: readonly DegradedSource[];
  readonly className?: string | undefined;
}

// Story 531 — single unified "X sources catching up" indicator.
// Aggregates degraded[] across parent /series, /series/:id/overview
// and /series/:id/recommendations queries (deduped by caller).
//
// Renders nothing when `sources` is empty so callers can pass an
// unconditional `<DegradedChip sources={aggregated} />` without
// having to guard.
export function DegradedChip({ sources, className }: DegradedChipProps) {
  const { t } = useTranslation();
  if (sources.length === 0) return null;

  // Per-source labels live alongside the chip's count key so we don't
  // fork a second i18n family. Switch is exhaustive on DegradedSource.
  const labelFor = (src: DegradedSource): string => {
    switch (src) {
      case 'tmdb_series':
        return t('seriesDetail.degraded.chip.source.tmdb_series');
      case 'tmdb_season':
        return t('seriesDetail.degraded.chip.source.tmdb_season');
      case 'tmdb_person':
        return t('seriesDetail.degraded.chip.source.tmdb_person');
      case 'omdb':
        return t('seriesDetail.degraded.chip.source.omdb');
      case 'sonarr_queue':
        return t('seriesDetail.degraded.chip.source.sonarr_queue');
    }
  };

  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span
          data-testid="series-degraded-chip"
          data-sources={sources.join(',')}
          className={cn(
            'inline-flex items-center gap-1 rounded-full border border-dashed border-warn/45',
            'bg-warn-dim text-warn px-1.5 py-0.5 text-[10.5px] font-medium',
            className,
          )}
        >
          <Loader2 className="w-3 h-3 animate-spin" aria-hidden="true" />
          <span>
            {t('seriesDetail.degraded.chip.count', { count: sources.length })}
          </span>
        </span>
      </TooltipTrigger>
      <TooltipContent>
        <ul
          data-testid="series-degraded-chip-tooltip"
          className="flex flex-col gap-0.5 text-[11px]"
        >
          {sources.map((src) => (
            <li key={src} data-source={src}>
              {labelFor(src)}
            </li>
          ))}
        </ul>
      </TooltipContent>
    </Tooltip>
  );
}
