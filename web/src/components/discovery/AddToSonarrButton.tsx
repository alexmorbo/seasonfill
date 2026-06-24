// Story 522 / N-4e: tiny "Add to Sonarr" button rendered inside
// DiscoverySeriesCard. Visible only when the series isn't already in
// any Sonarr instance. Click stops the parent <Link> navigation and
// opens the modal.

import { useState, type MouseEvent } from 'react';
import { useTranslation } from 'react-i18next';
import { Plus } from 'lucide-react';
import type { DiscoverySeriesItem } from '@/api/discovery';
import { cn } from '@/lib/utils';
import { AddToSonarrModal } from './AddToSonarrModal';

export interface AddToSonarrButtonProps {
  readonly item: DiscoverySeriesItem;
  readonly className?: string;
}

export function AddToSonarrButton({ item, className }: AddToSonarrButtonProps) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);

  const inLibrary = item.in_library_instances ?? [];
  if (inLibrary.length > 0) return null;

  function handleClick(e: MouseEvent<HTMLButtonElement>) {
    // Card is wrapped in a <Link>; without this the navigation fires
    // before the modal mounts.
    e.preventDefault();
    e.stopPropagation();
    setOpen(true);
  }

  return (
    <>
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
      {open && (
        <AddToSonarrModal
          open={open}
          onOpenChange={setOpen}
          item={item}
        />
      )}
    </>
  );
}
