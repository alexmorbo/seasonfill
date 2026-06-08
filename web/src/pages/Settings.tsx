import { useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router-dom';
import { GeneralTab } from '@/components/settings/GeneralTab';
import { SecurityTab } from '@/components/settings/SecurityTab';
import { IntegrationsTab } from '@/components/settings/IntegrationsTab';
import { SettingsTabs, type SettingsTabKey } from '@/components/settings/SettingsTabs';
import { useSetPageTitle } from '@/components/shell/page-title-context';

type RouteHash = SettingsTabKey | 'instances';

function parseHash(h: string): RouteHash {
  const v = h.replace(/^#/, '');
  if (v === 'security') return 'security';
  if (v === 'integrations') return 'integrations';
  if (v === 'instances') return 'instances';
  return 'general';
}

export function Settings() {
  const { t } = useTranslation();
  useSetPageTitle(t('settings.title'));
  const navigate = useNavigate();

  const initial = useMemo<RouteHash>(() => {
    if (typeof window === 'undefined') return 'general';
    return parseHash(window.location.hash);
  }, []);

  // Legacy /settings#instances → /instances. Fired before paint so the
  // General tab doesn't flash for one frame.
  useEffect(() => {
    if (initial === 'instances') {
      navigate('/instances', { replace: true });
    }
  }, [initial, navigate]);

  const [tab, setTab] = useState<SettingsTabKey>(
    initial === 'instances' ? 'general' : initial,
  );

  useEffect(() => {
    if (typeof window === 'undefined') return;
    const handler = () => {
      const next = parseHash(window.location.hash);
      if (next === 'instances') {
        navigate('/instances', { replace: true });
        return;
      }
      setTab(next);
    };
    window.addEventListener('hashchange', handler);
    return () => window.removeEventListener('hashchange', handler);
  }, [navigate]);

  useEffect(() => {
    if (typeof window === 'undefined') return;
    const target = `#${tab}`;
    if (window.location.hash === target) return;
    window.location.hash = tab;
  }, [tab]);

  return (
    <div className="flex flex-col gap-5">
      <header>
        <p className="text-[13px] text-muted">{t('settings.subtitle')}</p>
      </header>

      <div className="max-w-[760px]">
        <SettingsTabs
          value={tab}
          onValueChange={setTab}
          general={<GeneralTab />}
          security={<SecurityTab />}
          integrations={<IntegrationsTab />}
        />
      </div>
    </div>
  );
}
