import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import { ShieldOff, Settings, DownloadCloud } from 'lucide-react';
import { Button } from '@/components/ui/button';

interface StepProps {
  n: number;
  title: string;
  body: string;
}

function Step({ n, title, body }: StepProps) {
  return (
    <div className="flex items-start gap-3">
      <span className="flex h-7 w-7 flex-none items-center justify-center rounded-full bg-bg-surface-2 font-mono text-[12px] font-semibold text-tx-secondary">
        {n}
      </span>
      <div className="flex flex-col">
        <b className="text-[14px] font-semibold text-tx-primary">{title}</b>
        <span className="text-[12.5px] text-tx-muted">{body}</span>
      </div>
    </div>
  );
}

export function WatchdogNotConfiguredEmpty() {
  const { t } = useTranslation();
  return (
    <section
      data-testid="watchdog-not-configured"
      className="mx-auto mt-6 flex w-full max-w-[560px] flex-col items-center gap-4 rounded-md border border-border-faint bg-bg-surface px-6 py-10 text-center"
    >
      <div className="flex h-12 w-12 items-center justify-center rounded-full bg-bg-surface-2 text-tx-muted">
        <ShieldOff className="h-6 w-6" aria-hidden />
      </div>
      <h2 className="text-[18px] font-semibold tracking-tight">
        {t('watchdog.notConfigured.title')}
      </h2>
      <p className="max-w-prose text-[13.5px] leading-relaxed text-tx-secondary">
        {t('watchdog.notConfigured.body')}
      </p>
      <div className="mt-2 flex w-full max-w-[420px] flex-col gap-3 text-left">
        <Step
          n={1}
          title={t('watchdog.notConfigured.step1')}
          body={t('watchdog.notConfigured.step1Desc')}
        />
        <Step
          n={2}
          title={t('watchdog.notConfigured.step2')}
          body={t('watchdog.notConfigured.step2Desc')}
        />
        <Step
          n={3}
          title={t('watchdog.notConfigured.step3')}
          body={t('watchdog.notConfigured.step3Desc')}
        />
      </div>
      <div className="mt-3 flex flex-wrap items-center justify-center gap-2">
        <Button asChild variant="default" size="sm">
          <Link to="/instances?openCreate=1">
            <DownloadCloud className="mr-1 h-3.5 w-3.5" />
            {t('watchdog.notConfigured.cta.openInstanceForm')}
          </Link>
        </Button>
        <Button asChild variant="outline" size="sm">
          <Link to="/instances">
            <Settings className="mr-1 h-3.5 w-3.5" />
            {t('watchdog.notConfigured.cta.openInstances')}
          </Link>
        </Button>
      </div>
    </section>
  );
}
