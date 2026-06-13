import { Clock } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { cn } from '@/lib/utils';
import { relativeTime } from '@/lib/format';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';

export interface StaleBadgeProps {
  readonly asOf: string;
  readonly source: 'tmdb' | 'omdb' | 'sonarr_queue' | 'torrents';
  readonly className?: string | undefined;
}

export function StaleBadge({ asOf, source, className }: StaleBadgeProps) {
  const { t } = useTranslation();
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span
          data-testid="stale-badge"
          data-source={source}
          className={cn(
            'inline-flex items-center gap-1 rounded-full border border-dashed border-warn/45',
            'bg-warn-dim text-warn px-1.5 py-0.5 text-[10.5px] font-medium',
            className,
          )}
        >
          <Clock className="w-3 h-3" aria-hidden="true" />
          <span>{t('seriesDetail.stale.asOf', { time: relativeTime(asOf) })}</span>
        </span>
      </TooltipTrigger>
      <TooltipContent>{t(`seriesDetail.stale.source.${source}`)}</TooltipContent>
    </Tooltip>
  );
}
