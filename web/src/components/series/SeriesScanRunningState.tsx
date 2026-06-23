import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import { Loader2 } from 'lucide-react';
import { Button } from '@/components/ui/button';

export interface SeriesScanRunningStateProps {
  readonly scanRunId: string;
}

/**
 * Story 495 / N-1e (B-15 branch 2): rendered on `/series` when an
 * instance exists AND the latest scan run for it is in `running`
 * state. The CTA links to `/scans/{id}` so the operator can watch
 * progress. The parent (`Series.tsx`) keeps polling
 * `useInstanceLatestScan` at 6 s while running, so the page will
 * leave this state automatically once the scan completes.
 */
export function SeriesScanRunningState({ scanRunId }: SeriesScanRunningStateProps) {
  const { t } = useTranslation();
  return (
    <div
      data-testid="series-empty-scan-running"
      className="flex flex-col items-center justify-center gap-4 py-16"
    >
      <Loader2 className="w-10 h-10 text-tx-faint animate-spin" aria-hidden="true" />
      <h2 className="text-[15px] font-semibold text-tx-primary">
        {t('series.empty.scanRunning.title')}
      </h2>
      <p className="text-[13px] text-tx-secondary text-center max-w-[420px]">
        {t('series.empty.scanRunning.body')}
      </p>
      <Button asChild variant="outline">
        <Link to={`/scans/${scanRunId}`} data-testid="series-empty-scan-link">
          {t('series.empty.scanRunning.cta')}
        </Link>
      </Button>
    </div>
  );
}
