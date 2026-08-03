// S5 / ADR-0008: surface-agnostic "Add to Sonarr" trigger. Droppable on any
// surface; it just calls openAddToSonarr(target). The modal lives at
// app-shell level (AddToSonarrProvider), NOT inside this button — so nothing
// the modal does can navigate the host card.

import { type MouseEvent } from 'react';
import { useTranslation } from 'react-i18next';
import { Plus } from 'lucide-react';
import { cn } from '@/lib/utils';
import {
  useAddToSonarrLauncher,
  type AddToSonarrTarget,
} from './add-to-sonarr-context';

export interface AddToSonarrButtonProps {
  readonly target: AddToSonarrTarget;
  readonly className?: string;
}

export function AddToSonarrButton({ target, className }: AddToSonarrButtonProps) {
  const { t } = useTranslation();
  const { openAddToSonarr } = useAddToSonarrLauncher();

  function handleClick(e: MouseEvent<HTMLButtonElement>) {
    // This button sits inside a clickable card (<Link>/role="button"). The
    // click target here is the button itself — stable, never detached — so
    // preventing default + stopping propagation is deterministic. (Contrast
    // with the modal's Radix teardown clicks, which is exactly why the modal
    // is no longer a child of the card.)
    e.preventDefault();
    e.stopPropagation();
    openAddToSonarr(target);
  }

  return (
    <button
      type="button"
      onClick={handleClick}
      data-testid="add-to-sonarr-button"
      aria-label={t('discovery.add.button')}
      className={cn(
        'inline-flex items-center gap-1 rounded-full',
        'bg-blue-500/90 px-2 py-0.5 text-[10.5px] font-semibold',
        'text-white shadow-sm backdrop-blur-sm',
        'hover:bg-blue-500 focus-visible:outline-hidden',
        'focus-visible:ring-2 focus-visible:ring-accent',
        className,
      )}
    >
      <Plus className="h-3 w-3" aria-hidden="true" />
      <span>{t('discovery.add.button')}</span>
    </button>
  );
}
