import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router-dom';
import { GitBranch, Play } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { EmptyState } from '@/components/EmptyState';

export function DecisionsFirstRunState() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  return (
    <div className="mx-auto max-w-[480px] mt-6" data-testid="decisions-first-run-state">
      <EmptyState
        icon={<GitBranch className="w-7 h-7" />}
        title={t('decisions.firstRun.title')}
        body={t('decisions.firstRun.body')}
        action={
          <Button
            variant="default" size="sm"
            onClick={() => navigate('/scans')}
            className="gap-1.5"
          >
            <Play className="size-3.5" />
            {t('decisions.firstRun.startScan')}
          </Button>
        }
      />
    </div>
  );
}
