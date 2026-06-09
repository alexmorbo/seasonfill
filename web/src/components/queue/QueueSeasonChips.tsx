import { useTranslation } from 'react-i18next';
import { cn } from '@/lib/utils';
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip';
import type { components } from '@/api/schema';

type SeasonEpisodePresence = components['schemas']['dto.SeasonEpisodePresence'];

export interface QueueSeasonChipsProps {
  readonly seasonNumber: number;
  readonly episodes: readonly SeasonEpisodePresence[];
}

// QueueSeasonChips renders a passive grid of per-episode chips for one
// season. Used inline in the queue row when the backend embedded the
// `episodes` list (small seasons only — see seasonEpisodesEmbedCap in
// the Missing handler). For large seasons (>100 aired) the row falls
// back to the aggregate S01·N pill rendered by QueueRow itself.
//
// Tooltip surfaces the episode title; the click handler is deliberately
// absent — drilling into a season is the job of the season-pill button
// above this grid.
export function QueueSeasonChips({ seasonNumber, episodes }: QueueSeasonChipsProps) {
  const { t } = useTranslation();
  return (
    <div
      className="flex flex-wrap gap-1.5"
      data-testid="queue-season-chips"
      data-season-number={seasonNumber}
      role="list"
    >
      {episodes.map((e) => {
        const num = e.number ?? 0;
        const title = (e.title ?? '').trim();
        const present = e.present === true;
        const tooltip = title.length > 0
          ? t('instanceQueue.drill.episodeTooltipTitled', { num, title })
          : t('instanceQueue.drill.episodeTooltipPlain', { num });
        return (
          <Tooltip key={num}>
            <TooltipTrigger asChild>
              <span
                role="listitem"
                className={cn(
                  'font-mono text-[10.5px] px-1.5 py-px rounded-sm border cursor-default',
                  present
                    ? 'text-ok border-ok/30 bg-ok-dim'
                    : 'text-warn border-warn/30 bg-warn-dim',
                )}
                data-present={present ? 'true' : 'false'}
                data-episode-title={title}
                aria-label={t('instanceQueue.drill.episodeAria', {
                  num,
                  state: t(`instanceQueue.drill.${present ? 'have' : 'miss'}`),
                })}
              >
                E{num}
              </span>
            </TooltipTrigger>
            <TooltipContent>{tooltip}</TooltipContent>
          </Tooltip>
        );
      })}
    </div>
  );
}
