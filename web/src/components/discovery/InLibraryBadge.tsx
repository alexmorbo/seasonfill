import { Check } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip';
import { cn } from '@/lib/utils';

export interface InLibraryBadgeProps {
  readonly instances: readonly string[];
  readonly className?: string;
}

// Story 514 / N-3b: surfaces "this show is already in one of the Sonarr
// libraries". Empty arrays render nothing so DiscoverySeriesCard can
// place it unconditionally.
export function InLibraryBadge({ instances, className }: InLibraryBadgeProps) {
  const { t } = useTranslation();
  if (!instances || instances.length === 0) return null;
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span
          data-testid="discovery-in-library-badge"
          className={cn(
            'inline-flex items-center gap-1 rounded-full',
            'bg-green-500/90 px-2 py-0.5 text-[10.5px] font-semibold',
            'text-white shadow-sm backdrop-blur-sm',
            className,
          )}
        >
          <Check className="h-3 w-3" aria-hidden="true" />
          <span>{t('discovery.in_library')}</span>
        </span>
      </TooltipTrigger>
      <TooltipContent data-testid="discovery-in-library-tooltip">
        {instances.join(', ')}
      </TooltipContent>
    </Tooltip>
  );
}
