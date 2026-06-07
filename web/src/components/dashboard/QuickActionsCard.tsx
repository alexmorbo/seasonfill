import { Play, CircleAlert, List, ArrowRight } from 'lucide-react';
import { useNavigate } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { useTriggerScan } from '@/lib/scan-mutations';
import { useInstances } from '@/lib/instances';
import { useInstanceFilter } from '@/lib/instance-filter-context-internal';
import { cn } from '@/lib/utils';

export function QuickActionsCard() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const scan = useTriggerScan();
  const { data: instData } = useInstances();
  const { filter } = useInstanceFilter();
  const currentInstance = filter ?? instData?.instances?.[0]?.name ?? '';

  const onScanAll = () => scan.mutate({}, {
    onSuccess: (items) => { toast.success(t('dashboard.rail.quickActions.scanStarted', { count: items.length })); navigate('/scans'); },
    onError: (err) => { toast.error(t('dashboard.rail.quickActions.scanFailed', { error: err.message })); },
  });
  const onLastFail = () => navigate('/grabs?status=import_failed');
  const onQueue = () => currentInstance
    ? navigate(`/instances/${encodeURIComponent(currentInstance)}/queue`)
    : navigate('/instances');

  return (
    <Card data-testid="quick-actions-card">
      <CardHeader className="p-4 pb-2">
        <CardTitle className="text-sm font-semibold">{t('dashboard.rail.quickActions.title')}</CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-2 p-3 pt-0">
        <QaButton testId="qa-scan-all" icon={<Play className="h-3.5 w-3.5" />} label={t('dashboard.rail.quickActions.scanAll')} onClick={onScanAll} disabled={scan.isPending} />
        <QaButton testId="qa-last-fail" icon={<CircleAlert className="h-3.5 w-3.5" />} label={t('dashboard.rail.quickActions.lastFail')} onClick={onLastFail} />
        <QaButton testId="qa-queue" icon={<List className="h-3.5 w-3.5" />} label={t('dashboard.rail.quickActions.queue')} onClick={onQueue} />
      </CardContent>
    </Card>
  );
}

interface QaButtonProps { icon: React.ReactNode; label: string; onClick: () => void; disabled?: boolean; testId: string }
function QaButton({ icon, label, onClick, disabled, testId }: QaButtonProps) {
  return (
    <button type="button" onClick={onClick} disabled={disabled} data-testid={testId}
            className={cn('flex w-full items-center gap-2.5 rounded-md border border-border-faint bg-transparent px-3 py-2 text-left text-sm text-tx-secondary',
              'transition-colors hover:bg-bg-surface-2 hover:border-border-subtle hover:text-tx-primary disabled:cursor-not-allowed disabled:opacity-60')}>
      <span className="text-tx-muted">{icon}</span><span>{label}</span>
      <ArrowRight className="ml-auto h-3.5 w-3.5 text-tx-faint" />
    </button>
  );
}
