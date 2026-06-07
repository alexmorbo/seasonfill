import { useTranslation } from 'react-i18next';
import { RefreshCw, TriangleAlert } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';

export interface SeriesHeaderProps {
  readonly shownCount: number;
  readonly totalCount: number;
  readonly isLoading: boolean;
  readonly isError: boolean;
  readonly onRefresh: () => void;
}

export function SeriesHeader({
  shownCount, totalCount, isLoading, isError, onRefresh,
}: SeriesHeaderProps) {
  const { t } = useTranslation();
  return (
    <div className="flex items-end justify-between gap-3 pb-3 border-b border-border-faint">
      <div className="flex flex-col gap-1 min-w-0">
        <h1 className="text-[20px] font-semibold tracking-tight text-tx-primary">
          {t('series.title')}
        </h1>
        <div className={cn(
          'text-[12.5px] font-mono tabular-nums flex items-center gap-1.5',
          isError ? 'text-warn' : 'text-tx-faint',
        )}>
          {isError && <TriangleAlert className="w-3.5 h-3.5" aria-hidden="true" />}
          {isError
            ? t('series.loadFailed')
            : t('series.header.count', { shown: shownCount, total: totalCount })}
        </div>
      </div>
      <Button
        type="button"
        variant="ghost"
        size="sm"
        onClick={onRefresh}
        disabled={isLoading}
        aria-label={t('series.refreshAria')}
        data-testid="series-header-refresh"
      >
        <RefreshCw className={cn('w-3.5 h-3.5 mr-1.5', isLoading && 'animate-spin')} />
        {t('series.refresh')}
      </Button>
    </div>
  );
}
