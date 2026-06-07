import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import { Play } from 'lucide-react';
import { Button } from '@/components/ui/button';

export interface QueueSweepCTAProps {
  readonly backlogCount: number;
}

export function QueueSweepCTA({ backlogCount }: QueueSweepCTAProps) {
  const { t } = useTranslation();
  if (backlogCount === 0) return null;
  return (
    <div
      className="flex items-center gap-3 mt-4 p-4 border border-dashed border-border-subtle rounded-lg"
      data-testid="queue-sweep-cta"
    >
      <div className="flex-1">
        <b className="text-[13.5px] font-semibold block">
          {t('instanceQueue.sweep.heading')}
        </b>
        <span className="text-[12px] text-muted">
          {t('instanceQueue.sweep.body', { count: backlogCount })}
        </span>
      </div>
      <Button asChild>
        <Link to="/scans?new=1">
          <Play className="w-3.5 h-3.5 mr-1.5" aria-hidden="true" />
          {t('instanceQueue.sweep.button')}
        </Link>
      </Button>
    </div>
  );
}
