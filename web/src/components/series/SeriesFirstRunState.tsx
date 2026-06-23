import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import { Server } from 'lucide-react';
import { Button } from '@/components/ui/button';

export function SeriesFirstRunState() {
  const { t } = useTranslation();
  return (
    <div
      data-testid="series-first-run"
      className="flex flex-col items-center justify-center gap-4 py-16"
    >
      <Server className="w-10 h-10 text-tx-faint" aria-hidden="true" />
      <h1 className="text-[18px] font-semibold text-tx-primary">
        {t('series.firstRun.title')}
      </h1>
      <p className="text-[13px] text-tx-secondary text-center max-w-[420px]">
        {t('series.firstRun.body')}
      </p>
      <ol className="flex flex-col gap-2 text-[12.5px] text-tx-secondary max-w-[420px]">
        <li>
          <span className="font-semibold text-tx-primary mr-1.5">1.</span>
          {t('series.firstRun.step1')}
        </li>
        <li>
          <span className="font-semibold text-tx-primary mr-1.5">2.</span>
          {t('series.firstRun.step2')}
        </li>
        <li>
          <span className="font-semibold text-tx-primary mr-1.5">3.</span>
          {t('series.firstRun.step3')}
        </li>
      </ol>
      {/* Story 494 (B-13): bookmarkable deep-link opens InstanceFormDialog. */}
      <Button asChild>
        <Link to="/instances?add=1">{t('series.firstRun.cta')}</Link>
      </Button>
    </div>
  );
}
