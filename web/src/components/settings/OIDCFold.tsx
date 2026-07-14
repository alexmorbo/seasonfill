import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { ChevronDown } from 'lucide-react';
import { cn } from '@/lib/utils';
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible';
import {
  OIDCConfigBlock,
  type OIDCFormShape,
  type OIDCTestResult,
} from './OIDCConfigBlock';

interface Props {
  // defaultOpen seeds the initial expanded state — the parent passes true when
  // OIDC is already configured so operators land on the populated section.
  readonly defaultOpen?: boolean;
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
  defaultOpen, forceOpen, value, onChange, onTest, errors,
}: Props) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(Boolean(defaultOpen));

  const effectivelyOpen = open || Boolean(forceOpen);

  return (
    <Collapsible
      open={effectivelyOpen}
      onOpenChange={(next) => { if (!forceOpen) setOpen(next); }}
      asChild
    >
      <section
        data-testid="oidc-fold"
        data-open={effectivelyOpen}
        className="border border-border-faint rounded-[var(--r-lg)] overflow-hidden"
      >
        <CollapsibleTrigger asChild>
          <button
            type="button"
            data-testid="oidc-fold-head"
            className={cn(
              'flex items-center gap-2.5 w-full px-3.5 py-3 cursor-pointer text-left',
              'bg-bg-surface border-0',
            )}
          >
            <h3 className="text-[13.5px] font-semibold m-0">
              {t('settings.security.oidcFold.title')}
            </h3>
            <span className="text-[12px] text-tx-muted">
              {effectivelyOpen
                ? t('settings.security.oidcFold.collapseHint')
                : t('settings.security.oidcFold.expandHint')}
            </span>
            <ChevronDown
              className={cn(
                'w-4 h-4 ml-auto text-tx-muted transition-transform',
                effectivelyOpen && 'rotate-180',
              )}
              aria-hidden="true"
            />
          </button>
        </CollapsibleTrigger>
        <CollapsibleContent
          data-testid="oidc-fold-content"
          className="p-3.5 border-t border-border-faint flex flex-col gap-3.5"
        >
          <OIDCConfigBlock
            value={value}
            onChange={onChange}
            onTest={onTest}
            {...(errors && { errors })}
          />
        </CollapsibleContent>
      </section>
    </Collapsible>
  );
}
