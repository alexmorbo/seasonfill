import { useTranslation } from 'react-i18next';
import { cn } from '@/lib/utils';
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip';
import {
  episodeState,
  type SeasonEpisodeItem,
  type EpisodeState,
} from '@/lib/api/queueSeasonEpisodes';

export interface QueueEpisodeChipsProps {
  readonly items: readonly SeasonEpisodeItem[];
}

const stateClass: Record<EpisodeState, string> = {
  have: 'text-faint border-border-faint',
  miss: 'text-warn border-warn/30 bg-warn-dim',
  upcoming: 'text-muted border-border-faint bg-surface-2',
};

export function QueueEpisodeChips({ items }: QueueEpisodeChipsProps) {
  const { t } = useTranslation();
  return (
    <div
      className="flex flex-wrap gap-1.5"
      data-testid="queue-episode-chips"
      role="list"
    >
      {items.map((e) => {
        const state = episodeState(e);
        const title = (e.title ?? '').trim();
        const tooltip = title.length > 0
          ? t('instanceQueue.drill.episodeTooltipTitled', { num: e.number, title })
          : t('instanceQueue.drill.episodeTooltipPlain', { num: e.number });
        return (
          <Tooltip key={e.number}>
            <TooltipTrigger asChild>
              <span
                role="listitem"
                className={cn(
                  'font-mono text-[10.5px] px-1.5 py-px rounded-sm border cursor-default',
                  stateClass[state],
                )}
                data-state={state}
                data-episode-title={title}
                aria-label={t('instanceQueue.drill.episodeAria', {
                  num: e.number,
                  state: t(`instanceQueue.drill.${state}`),
                })}
              >
                E{e.number}
              </span>
            </TooltipTrigger>
            <TooltipContent>{tooltip}</TooltipContent>
          </Tooltip>
        );
      })}
    </div>
  );
}
