import { useTranslation } from 'react-i18next';
import { CheckCircle2 } from 'lucide-react';

/**
 * Story 495 / N-1e (B-15 branch 4): rendered on `/series` when an
 * instance exists AND the latest scan run completed with zero results
 * — that is, Seasonfill found no series with missing seasons. Positive
 * framing replaces the prior `SeriesEmptyState variant="server"` CTA
 * which incorrectly pushed the operator to "run a scan" in this case.
 */
export function SeriesAllHealthyState() {
  const { t } = useTranslation();
  return (
    <div
      data-testid="series-empty-all-healthy"
      className="flex flex-col items-center justify-center gap-3 py-16"
    >
      <CheckCircle2 className="w-10 h-10 text-ok" aria-hidden="true" />
      <h2 className="text-[15px] font-semibold text-tx-primary">
        {t('series.empty.allHealthy.title')}
      </h2>
      <p className="text-[13px] text-tx-secondary text-center max-w-[420px]">
        {t('series.empty.allHealthy.body')}
      </p>
    </div>
  );
}
