import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router-dom';
import { Server, Plus, BookOpen } from 'lucide-react';
import { Button } from '@/components/ui/button';

const STEP_KEYS = ['step1', 'step2', 'step3'] as const;

export function DashboardFirstRunState() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  return (
    <div
      data-testid="dashboard-first-run"
      className="flex flex-col items-center justify-center text-center gap-3 px-6 py-15 max-w-[580px] mx-auto mt-6 min-h-[380px] rounded-lg border border-dashed border-border-subtle bg-bg-surface"
    >
      <div className="w-[54px] h-[54px] rounded-[15px] flex items-center justify-center bg-bg-surface-2 border border-border-faint text-accent mb-0.5">
        <Server className="w-6 h-6" aria-hidden="true" />
      </div>
      <h2 className="text-[19px] font-semibold tracking-tight text-tx-primary">
        {t('dashboard.firstRun.title')}
      </h2>
      <p className="text-[13.5px] leading-relaxed text-tx-muted max-w-[430px]">
        {t('dashboard.firstRun.body')}
      </p>
      <ol className="flex flex-col gap-2.5 text-left w-full max-w-[400px] mt-2">
        {STEP_KEYS.map((k, idx) => (
          <li key={k} className="flex gap-3 items-start">
            <span className="w-[23px] h-[23px] rounded-full flex items-center justify-center bg-bg-surface-2 border border-border-subtle text-tx-secondary font-mono text-[11px] font-semibold">
              {idx + 1}
            </span>
            <span className="flex flex-col">
              <b className="text-[13.5px] font-semibold text-tx-primary">
                {t(`dashboard.firstRun.${k}.title`)}
              </b>
              <span className="text-[12.5px] text-tx-muted">
                {t(`dashboard.firstRun.${k}.body`)}
              </span>
            </span>
          </li>
        ))}
      </ol>
      <div className="flex flex-wrap gap-2.5 mt-1.5 justify-center">
        <Button variant="default" onClick={() => navigate('/instances')} data-testid="first-run-cta-add">
          <Plus className="w-4 h-4" aria-hidden="true" />
          {t('dashboard.firstRun.cta.addInstance')}
        </Button>
        <Button variant="outline" onClick={() => navigate('/settings')} data-testid="first-run-cta-help">
          <BookOpen className="w-4 h-4" aria-hidden="true" />
          {t('dashboard.firstRun.cta.help')}
        </Button>
      </div>
    </div>
  );
}
