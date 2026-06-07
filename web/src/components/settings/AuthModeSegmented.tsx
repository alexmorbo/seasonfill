import { useTranslation } from 'react-i18next';
import { Lock, AlertTriangle } from 'lucide-react';
import { cn } from '@/lib/utils';

export type AuthMode = 'forms' | 'basic' | 'none' | 'oidc';
export const AUTH_MODES: readonly AuthMode[] = ['forms', 'basic', 'none', 'oidc'] as const;

interface Props {
  readonly current: AuthMode;
  readonly onAttempt: (target: AuthMode) => void;
}

const MODE_LABEL: Record<AuthMode, string> = {
  forms: 'Forms',
  basic: 'Basic',
  none: 'None',
  oidc: 'OIDC',
};

export function AuthModeSegmented({ current, onAttempt }: Props) {
  const { t } = useTranslation();
  return (
    <section
      data-testid="auth-mode-segmented"
      className="flex flex-col gap-3.5 rounded-[var(--r-lg)] border border-border-subtle bg-bg-surface p-[18px]"
    >
      <header className="flex items-center gap-2.5">
        <h2 className="text-[15px] font-[650] tracking-[-0.01em] m-0">
          {t('settings.security.authCard.title', {
            defaultValue: t('settings.security.auth.section'),
          })}
        </h2>
        <span
          data-testid="auth-mode-pill"
          className={cn(
            'ml-auto inline-flex items-center gap-1.5 font-mono text-[11.5px] font-semibold',
            'bg-accent-dim text-accent border border-accent/35 px-2.5 py-[3px] rounded-full',
          )}
        >
          <Lock className="w-3 h-3" aria-hidden="true" />
          {t('settings.security.modePill.current', { mode: MODE_LABEL[current] })}
        </span>
      </header>

      <div
        role="radiogroup"
        aria-label={t('settings.security.auth.modeLabel')}
        className="inline-flex gap-0.5 self-start rounded-[var(--r-md)] border border-border-subtle bg-bg-base p-[3px]"
      >
        {AUTH_MODES.map((m) => {
          const on = m === current;
          return (
            <button
              key={m}
              type="button"
              role="radio"
              aria-checked={on}
              data-mode={m}
              onClick={() => { if (!on) onAttempt(m); }}
              className={cn(
                'px-[18px] py-2 text-[13px] font-[550] rounded-[var(--r-sm)]',
                'border-0 cursor-pointer',
                on
                  ? 'bg-accent text-accent-foreground'
                  : 'bg-transparent text-tx-muted hover:text-tx-primary',
              )}
            >
              {MODE_LABEL[m]}
            </button>
          );
        })}
      </div>

      <div
        className={cn(
          'flex gap-2.5 items-start rounded-[var(--r-md)] p-3',
          'bg-status-danger-dim border border-status-danger/30',
        )}
      >
        <AlertTriangle className="w-4 h-4 mt-px text-status-danger shrink-0" aria-hidden="true" />
        <p className="text-[12.5px] text-tx-secondary leading-relaxed m-0">
          <span className="text-status-danger font-semibold">
            {t('settings.security.modePill.dangerNoteHead')}
          </span>{' '}
          {t('settings.security.modePill.dangerNoteBody')}{' '}
          <code className="font-mono">seasonfill auth-mode --set forms</code>
          {t('settings.security.modePill.dangerNoteTail')}
        </p>
      </div>
    </section>
  );
}
