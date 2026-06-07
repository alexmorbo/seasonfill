import { useTranslation } from 'react-i18next';
import { Filter } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { EmptyState } from '@/components/EmptyState';

export function ScansEmptyState({ onReset }: { onReset: () => void }) {
  const { t } = useTranslation();
  return (
    <div data-testid="scans-empty-state">
      <EmptyState
        title={t('scans.empty.matchTitle')}
        body={t('scans.empty.matchBody')}
        action={
          <Button variant="outline" size="sm" onClick={onReset}>
            <Filter className="w-3.5 h-3.5 mr-1" aria-hidden="true" />
            {t('scans.resetFilters')}
          </Button>
        }
      />
    </div>
  );
}
