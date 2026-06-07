import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router-dom';
import { Library, Filter } from 'lucide-react';
import { Button } from '@/components/ui/button';

export type SeriesEmptyVariant = 'server' | 'filtered';

export interface SeriesEmptyStateProps {
  readonly variant: SeriesEmptyVariant;
  readonly onClearFilters?: () => void;
}

export function SeriesEmptyState({ variant, onClearFilters }: SeriesEmptyStateProps) {
  const { t } = useTranslation();
  const navigate = useNavigate();

  if (variant === 'filtered') {
    return (
      <div
        data-testid="series-empty-filtered"
        className="flex flex-col items-center justify-center gap-3 py-16"
      >
        <Filter className="w-10 h-10 text-tx-faint" aria-hidden="true" />
        <h2 className="text-[15px] font-semibold text-tx-primary">
          {t('series.empty.filtered.title')}
        </h2>
        <p className="text-[13px] text-tx-secondary text-center max-w-[420px]">
          {t('series.empty.filtered.body')}
        </p>
        <Button type="button" variant="ghost" onClick={onClearFilters}>
          {t('series.empty.filtered.cta')}
        </Button>
      </div>
    );
  }

  return (
    <div
      data-testid="series-empty-server"
      className="flex flex-col items-center justify-center gap-3 py-16"
    >
      <Library className="w-10 h-10 text-tx-faint" aria-hidden="true" />
      <h2 className="text-[15px] font-semibold text-tx-primary">
        {t('series.empty.server.title')}
      </h2>
      <p className="text-[13px] text-tx-secondary text-center max-w-[420px]">
        {t('series.empty.server.body')}
      </p>
      <Button type="button" onClick={() => navigate('/scans?new=1')}>
        {t('series.empty.server.cta')}
      </Button>
    </div>
  );
}
