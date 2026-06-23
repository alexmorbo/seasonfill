import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import type { ComponentType, ReactNode } from 'react';
import {
  Server, Plus, BookOpen, Key, ImageIcon, Play,
  Check, Loader2, AlertTriangle, Circle,
} from 'lucide-react';
import { cn } from '@/lib/utils';
import { Button } from '@/components/ui/button';
import { useStepperState, type StepState, type StepStatus } from './useStepperState';

const STEP_ICONS: Record<string, ComponentType<{ className?: string; 'aria-hidden'?: 'true' }>> = {
  sonarr:  Server,
  webhook: Server,
  tmdb:    Key,
  omdb:    ImageIcon,
  scan:    Play,
};

const STATUS_STYLES: Record<StepStatus, { ringClass: string; iconBg: string }> = {
  done:        { ringClass: 'border-status-ok/40',      iconBg: 'bg-status-ok/15 text-status-ok' },
  in_progress: { ringClass: 'border-accent/40',         iconBg: 'bg-accent-dim text-accent' },
  todo:        { ringClass: 'border-border-subtle',     iconBg: 'bg-bg-surface-2 text-tx-secondary' },
  error:       { ringClass: 'border-status-warning/40', iconBg: 'bg-status-warning/15 text-status-warning' },
};

/**
 * Story 494 / N-1d. 5-step onboarding stepper replacing the old 3-step
 * first-run state. Steps:
 *   1. Connect Sonarr — opens InstanceFormDialog via `<Link to="/instances?add=1">`.
 *   2. Install webhook — auto (no CTA); status driven by `/webhooks/status`.
 *   3. Add TMDB key — links to /settings/external-services.
 *   4. Add OMDb key (optional, dimmed when skipped).
 *   5. Run first scan — links to /scans (operator triggers from there or InstanceHero).
 *
 * Status semantics rendered:
 *   - done        → green check, full opacity.
 *   - in_progress → blue spinner.
 *   - todo        → gray circle.
 *   - error       → orange alert.
 *
 * Optional steps (omdb) render at `opacity-60` when status='todo' and
 * snap back to full opacity once configured.
 */
export function DashboardFirstRunState() {
  const { t } = useTranslation();
  const { steps } = useStepperState();

  return (
    <div
      data-testid="dashboard-first-run"
      className="flex flex-col items-center justify-center text-center gap-3 px-6 py-15 max-w-[620px] mx-auto mt-6 min-h-[380px] rounded-lg border border-dashed border-border-subtle bg-bg-surface"
    >
      <div className="w-[54px] h-[54px] rounded-[15px] flex items-center justify-center bg-bg-surface-2 border border-border-faint text-accent mb-0.5">
        <Server className="w-6 h-6" aria-hidden="true" />
      </div>
      <h2 className="text-[19px] font-semibold tracking-tight text-tx-primary">
        {t('dashboard.firstRun.title')}
      </h2>
      <p className="text-[13.5px] leading-relaxed text-tx-muted max-w-[460px]">
        {t('dashboard.firstRun.body')}
      </p>
      <ol className="flex flex-col gap-2.5 text-left w-full max-w-[440px] mt-2">
        {steps.map((step, idx) => (
          <StepRow key={step.id} step={step} index={idx + 1} />
        ))}
      </ol>
      <div className="flex flex-wrap gap-2.5 mt-1.5 justify-center">
        <Button asChild variant="default" data-testid="first-run-cta-add">
          <Link to="/instances?add=1">
            <Plus className="w-4 h-4" aria-hidden="true" />
            {t('dashboard.firstRun.cta.addInstance')}
          </Link>
        </Button>
        <Button asChild variant="outline" data-testid="first-run-cta-help">
          <Link to="/settings">
            <BookOpen className="w-4 h-4" aria-hidden="true" />
            {t('dashboard.firstRun.cta.help')}
          </Link>
        </Button>
      </div>
    </div>
  );
}

interface StepRowProps {
  readonly step: StepState;
  readonly index: number;
}

function StepRow({ step, index }: StepRowProps): ReactNode {
  const { t } = useTranslation();
  const Icon = STEP_ICONS[step.id] ?? Circle;
  const styles = STATUS_STYLES[step.status];
  const dim = step.optional && step.status === 'todo';

  const StatusIcon =
    step.status === 'done' ? Check
    : step.status === 'in_progress' ? Loader2
    : step.status === 'error' ? AlertTriangle
    : Circle;

  return (
    <li
      data-testid={`stepper-step-${step.id}`}
      data-status={step.status}
      className={cn(
        'flex gap-3 items-start rounded-md border bg-bg-base px-3 py-2.5',
        styles.ringClass,
        dim && 'opacity-60',
      )}
    >
      <span
        className={cn(
          'w-[28px] h-[28px] rounded-full flex items-center justify-center font-mono text-[11px] font-semibold',
          styles.iconBg,
        )}
        aria-hidden="true"
      >
        <Icon className="w-3.5 h-3.5" />
      </span>
      <span className="flex flex-col flex-1 min-w-0">
        <b className="text-[13.5px] font-semibold text-tx-primary flex items-baseline gap-2">
          <span className="text-tx-muted font-mono text-[11px]">{index}.</span>
          {t(`dashboard.onboarding.step_${step.id}.title`)}
          {step.optional && (
            <span
              className="text-[10.5px] font-medium text-tx-faint rounded-sm bg-bg-surface-2 px-1.5 py-0.5"
              data-testid={`stepper-step-${step.id}-optional`}
            >
              {t('dashboard.onboarding.optional')}
            </span>
          )}
        </b>
        <span className="text-[12.5px] text-tx-muted">
          {t(`dashboard.onboarding.step_${step.id}.body`)}
        </span>
        <span
          className="text-[11.5px] text-tx-faint mt-0.5"
          data-testid={`stepper-step-${step.id}-status`}
        >
          {t(`dashboard.onboarding.status.${step.status}`)}
        </span>
      </span>
      <StatusIcon
        className={cn(
          'w-4 h-4 mt-1 shrink-0',
          step.status === 'done' && 'text-status-ok',
          step.status === 'in_progress' && 'text-accent animate-spin',
          step.status === 'error' && 'text-status-warning',
          step.status === 'todo' && 'text-tx-faint',
        )}
        aria-hidden="true"
      />
    </li>
  );
}
