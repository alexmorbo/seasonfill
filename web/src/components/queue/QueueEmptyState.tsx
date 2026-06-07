import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import { CheckCheck, Radar, ArrowLeft } from 'lucide-react';
import { Button } from '@/components/ui/button';

export function QueueEmptyState() {
  const { t } = useTranslation();
  return (
    <div
      className="max-w-[480px] mx-auto mt-6 text-center"
      data-testid="queue-empty"
    >
      <div className="mx-auto w-12 h-12 rounded-full bg-ok-dim text-ok flex items-center justify-center mb-3">
        <CheckCheck className="w-6 h-6" aria-hidden="true" />
      </div>
      <h2 className="text-[18px] font-semibold mb-2">
        {t('instanceQueue.empty.title')}
      </h2>
      <p className="text-[13px] text-muted mb-4">
        {t('instanceQueue.empty.body')}
      </p>
      <div className="flex items-center justify-center gap-2">
        <Button variant="outline" size="sm" asChild>
          <Link to="/scans">
            <Radar className="w-3.5 h-3.5 mr-1.5" aria-hidden="true" />
            {t('instanceQueue.empty.scanHistory')}
          </Link>
        </Button>
        <Button variant="outline" size="sm" asChild>
          <Link to="/instances">
            <ArrowLeft className="w-3.5 h-3.5 mr-1.5" aria-hidden="true" />
            {t('instanceQueue.empty.backToInstances')}
          </Link>
        </Button>
      </div>
    </div>
  );
}
