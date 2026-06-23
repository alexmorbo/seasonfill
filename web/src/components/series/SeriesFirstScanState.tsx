import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router-dom';
import { Play, Loader2 } from 'lucide-react';
import { toast } from 'sonner';
import { Button } from '@/components/ui/button';
import { useTriggerScan, firstScanRunId, NoScanStartedError } from '@/lib/scan-mutations';
import { ApiError } from '@/lib/api';

export interface SeriesFirstScanStateProps {
  readonly instance: string;
}

/**
 * Story 495 / N-1e (B-15 branch 3): rendered on `/series` when an
 * instance exists but no scan has ever run for it (`latestScan === null`)
 * and the cache is empty. The CTA POSTs `/api/v1/scan` via
 * `useTriggerScan` and navigates to `/scans/{id}` on success. Mirrors
 * the 409 / NoScanStarted / generic-error handling that DecisionDrawer
 * already uses so toast UX stays consistent.
 */
export function SeriesFirstScanState({ instance }: SeriesFirstScanStateProps) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const trigger = useTriggerScan();

  const onClick = async () => {
    try {
      const items = await trigger.mutateAsync({ instance });
      const id = firstScanRunId(items);
      toast.success(t('series.empty.firstScan.toastStarted'));
      navigate(`/scans/${id}`);
    } catch (err) {
      if (err instanceof ApiError && err.status === 409) {
        toast.error(t('series.empty.firstScan.toastAlreadyRunning'));
        return;
      }
      if (err instanceof NoScanStartedError) {
        toast.error(t('series.empty.firstScan.toastEmpty'));
        return;
      }
      toast.error(
        t('series.empty.firstScan.toastError', {
          error: err instanceof Error ? err.message : String(err),
        }),
      );
    }
  };

  return (
    <div
      data-testid="series-empty-first-scan"
      className="flex flex-col items-center justify-center gap-4 py-16"
    >
      <Play className="w-10 h-10 text-tx-faint" aria-hidden="true" />
      <h2 className="text-[15px] font-semibold text-tx-primary">
        {t('series.empty.firstScan.title')}
      </h2>
      <p className="text-[13px] text-tx-secondary text-center max-w-[420px]">
        {t('series.empty.firstScan.body')}
      </p>
      <Button
        type="button"
        onClick={onClick}
        disabled={trigger.isPending}
        data-testid="series-empty-first-scan-cta"
      >
        {trigger.isPending ? (
          <>
            <Loader2 className="w-4 h-4 mr-2 animate-spin" aria-hidden="true" />
            {t('series.empty.firstScan.ctaRunning')}
          </>
        ) : (
          t('series.empty.firstScan.cta')
        )}
      </Button>
    </div>
  );
}
