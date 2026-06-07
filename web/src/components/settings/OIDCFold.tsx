import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { ChevronDown } from 'lucide-react';
import { cn } from '@/lib/utils';
import {
  OIDCConfigBlock,
  type OIDCFormShape,
  type OIDCTestResult,
} from './OIDCConfigBlock';
import type { AuthMode } from './AuthModeSegmented';

interface Props {
  readonly mode: AuthMode;
  readonly forceOpen?: boolean;
  readonly value: OIDCFormShape & {
    client_secret_configured: boolean;
    client_secret_env_override: boolean;
  };
  readonly onChange: (next: OIDCFormShape) => void;
  readonly onTest: () => Promise<OIDCTestResult>;
  readonly errors?: {
    issuer?: string; client_id?: string; redirect_url?: string; scopes?: string;
  };
}

export function OIDCFold({
  mode, forceOpen, value, onChange, onTest, errors,
}: Props) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(mode === 'oidc');

  // Auto-open/close on mode change. Forced-open (errors) wins.
  useEffect(() => {
    setOpen(mode === 'oidc');
  }, [mode]);

  const effectivelyOpen = open || Boolean(forceOpen) || mode === 'oidc';

  return (
    <section
      data-testid="oidc-fold"
      data-open={effectivelyOpen}
      data-mode={mode}
      className="border border-border-faint rounded-[var(--r-lg)] overflow-hidden"
    >
      <button
        type="button"
        data-testid="oidc-fold-head"
        onClick={() => { if (!forceOpen) setOpen((v) => !v); }}
        className={cn(
          'flex items-center gap-2.5 w-full px-3.5 py-3 cursor-pointer text-left',
          'bg-bg-surface border-0',
        )}
      >
        <h3 className="text-[13.5px] font-semibold m-0">
          {t('settings.security.oidcFold.title')}
        </h3>
        {mode !== 'oidc' && !effectivelyOpen && (
          <span className="font-mono text-[10.5px] text-tx-faint bg-bg-surface-2 border border-border-faint px-[7px] py-px rounded-[5px]">
            {t('settings.security.oidcFold.hidden')}
          </span>
        )}
        {mode !== 'oidc' && (
          <span className="text-[12px] text-tx-muted">
            {effectivelyOpen
              ? t('settings.security.oidcFold.collapseHint')
              : t('settings.security.oidcFold.expandHint')}
          </span>
        )}
        <ChevronDown
          className={cn(
            'w-4 h-4 ml-auto text-tx-muted transition-transform',
            effectivelyOpen && 'rotate-180',
          )}
          aria-hidden="true"
        />
      </button>
      {effectivelyOpen && (
        <div className="p-3.5 border-t border-border-faint flex flex-col gap-3.5">
          <OIDCConfigBlock
            value={value}
            onChange={onChange}
            onTest={onTest}
            {...(errors && { errors })}
          />
        </div>
      )}
    </section>
  );
}
