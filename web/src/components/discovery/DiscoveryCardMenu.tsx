import type { SyntheticEvent } from 'react';
import { useTranslation } from 'react-i18next';
import { MoreVertical, EyeOff } from 'lucide-react';
import {
  DropdownMenu, DropdownMenuTrigger, DropdownMenuContent, DropdownMenuItem,
} from '@/components/ui/dropdown-menu';
import { cn } from '@/lib/utils';

// DiscoveryCardMenu is a poster-overlay kebab. It is rendered as a SIBLING of
// the SeriesCard (never nested inside its <a>/role=button root) and the wrapper
// stops click/keydown propagation — so opening the menu can never trigger card
// navigation. Correct-by-construction, mirroring the FollowButton sibling
// overlay pattern in SeriesCard.tsx:236-247.
export function DiscoveryCardMenu({ onHide }: { readonly onHide: () => void }) {
  const { t } = useTranslation();
  const stop = (e: SyntheticEvent) => e.stopPropagation();

  return (
    <div
      className="absolute right-1.5 top-1.5 z-30"
      onClick={stop}
      onKeyDown={stop}
      role="presentation"
    >
      <DropdownMenu>
        <DropdownMenuTrigger
          data-testid="discovery-card-menu-trigger"
          aria-label={t('discovery.card.menu')}
          className={cn(
            'inline-flex h-7 w-7 items-center justify-center rounded-md',
            'bg-black/50 text-white backdrop-blur-sm hover:bg-black/70',
            // Hidden until hover on pointer devices, always visible on touch;
            // focus reveals it for keyboard users. `group` lives on the
            // DiscoveryCard wrapper (this component's parent).
            'opacity-100 md:opacity-0 group-hover:opacity-100 focus-visible:opacity-100',
            'transition-opacity focus-visible:outline-hidden focus-visible:ring-2 focus-visible:ring-accent',
          )}
        >
          <MoreVertical className="h-4 w-4" />
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" data-testid="discovery-card-menu">
          <DropdownMenuItem
            data-testid="discovery-card-hide"
            onSelect={() => onHide()}
          >
            <EyeOff className="h-4 w-4" />
            {t('discovery.card.hide')}
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    </div>
  );
}
