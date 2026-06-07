import { Plus } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { cn } from '@/lib/utils';

export interface AddInstanceGhostRowProps {
  readonly onClick: () => void;
}

/**
 * Dashed-border ghost row at the end of the instance list. Click opens
 * the instance create dialog.
 */
export function AddInstanceGhostRow({ onClick }: AddInstanceGhostRowProps) {
  const { t } = useTranslation();
  return (
    <button
      type="button"
      onClick={onClick}
      data-testid="instance-add-ghost"
      className={cn(
        'flex items-center justify-center gap-2 w-full',
        'py-[15px] rounded-lg bg-transparent',
        'border border-dashed border-border-subtle text-tx-muted text-[13.5px]',
        'hover:border-accent hover:text-accent hover:bg-accent-dim transition-colors',
      )}
    >
      <Plus className="w-4 h-4" />
      {t('instances.add.ghost')}
    </button>
  );
}
