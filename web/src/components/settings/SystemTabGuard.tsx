import { type ReactNode, useEffect } from 'react';
import { useNavigate } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { useMe } from '@/hooks/useMe';

// Auto-redirect delay for the 403 message. Operator-frozen at 2s per
// brief (line 49): "auto-redirect to /settings/profile after 2s".
const REDIRECT_DELAY_MS = 2000;

interface SystemTabGuardProps {
  readonly children: ReactNode;
}

// SystemTabGuard gates /settings/system/* on role === 'admin'.
// - Loading: render the same checking-session placeholder
//   ProtectedRoute uses; avoids a flash of the 403 panel for slow
//   /me requests.
// - role === 'admin': pass through children unchanged.
// - role !== 'admin' (or /me errored): render localized 403 panel +
//   schedule a 2s timer to navigate('/settings/profile', replace).
//   Timer is cleared on unmount so a fast-clicker doesn't double-fire.
//
// Story 486 (N-7b).
export function SystemTabGuard({ children }: SystemTabGuardProps) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const me = useMe();
  const allowed = me.data?.role === 'admin';

  useEffect(() => {
    if (me.isLoading || allowed) return;
    const id = window.setTimeout(() => {
      navigate('/settings/profile', { replace: true });
    }, REDIRECT_DELAY_MS);
    return () => window.clearTimeout(id);
  }, [allowed, me.isLoading, navigate]);

  if (me.isLoading) {
    return (
      <div className="grid place-items-center h-32 text-faint mono">
        {t('common.checkingSession')}
      </div>
    );
  }

  if (!allowed) {
    return (
      <div
        data-testid="system-tab-guard-denied"
        role="alert"
        aria-live="polite"
        className="flex flex-col items-center justify-center gap-2 min-h-[160px] rounded-md border border-border-faint bg-surface/40 p-5 text-center"
      >
        <p className="text-[14px] font-medium text-tx-primary">
          {t('settings.system.access_denied')}
        </p>
        <p className="text-[12px] text-tx-muted">
          {t('settings.system.redirect_notice')}
        </p>
      </div>
    );
  }

  return <>{children}</>;
}
