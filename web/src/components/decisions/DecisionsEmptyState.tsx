import { useTranslation } from 'react-i18next';
import { FilterX, RotateCcw } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { EmptyState } from '@/components/EmptyState';

export interface DecisionsEmptyStateProps {
  readonly onReset: () => void;
}

export function DecisionsEmptyState({ onReset }: DecisionsEmptyStateProps) {
  const { t } = useTranslation();
  return (
    <div className="mx-auto max-w-[460px] mt-6" data-testid="decisions-empty-state">
      <EmptyState
        icon={<FilterX className="w-7 h-7" />}
        title={t('decisions.empty.filterTitle')}
        body={t('decisions.empty.filterBody')}
        action={
          <Button variant="outline" size="sm" onClick={onReset} className="gap-1.5">
            <RotateCcw className="size-3.5" />
            {t('decisions.empty.resetFilters')}
          </Button>
        }
      />
    </div>
  );
}
