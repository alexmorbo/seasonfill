import { Outlet } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { SystemTabs } from '@/components/settings/SystemTabs';

// SystemLayout is the chrome under /settings/system/*. It mounts the
// path-aware <SystemTabs> bar above the active child route. Children
// (general/security/integrations) render into <Outlet />.
//
// SystemTabGuard wraps this layout from the route table — by the time
// SystemLayout mounts, role=admin is already verified.
//
// Story 486 (N-7b).
export function SystemLayout() {
  const { t } = useTranslation();
  return (
    <div className="flex flex-col gap-5">
      <header>
        <p className="text-[13px] text-muted">{t('settings.subtitle')}</p>
      </header>
      <div className="max-w-[760px]">
        <SystemTabs />
        <div className="mt-4">
          <Outlet />
        </div>
      </div>
    </div>
  );
}
