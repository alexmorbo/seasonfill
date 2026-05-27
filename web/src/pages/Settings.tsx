import { useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router-dom';
import {
  Tabs, TabsContent, TabsList, TabsTrigger,
} from '@/components/ui/tabs';
import { GeneralTab } from '@/components/settings/GeneralTab';
import { SecurityTab } from '@/components/settings/SecurityTab';

type TabKey = 'general' | 'security';

function parseHash(h: string): TabKey | 'instances' {
  const v = h.replace(/^#/, '');
  if (v === 'security') return 'security';
  if (v === 'instances') return 'instances';
  return 'general';
}

export function Settings() {
  const { t } = useTranslation();
  const navigate = useNavigate();

  const initial = useMemo<TabKey | 'instances'>(() => {
    if (typeof window === 'undefined') return 'general';
    return parseHash(window.location.hash);
  }, []);

  // Legacy deep link: /settings#instances → /instances.
  // Fire from a layout effect so the redirect happens before paint and
  // the General tab doesn't flash for one frame.
  useEffect(() => {
    if (initial === 'instances') {
      navigate('/instances', { replace: true });
    }
  }, [initial, navigate]);

  const [tab, setTab] = useState<TabKey>(
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
    <div className="max-w-[1024px] mx-auto p-6 flex flex-col gap-5">
      <header>
        <h1 className="text-[22px] font-semibold tracking-tight">{t('settings.title')}</h1>
        <p className="text-[13px] text-muted mt-1">
          {t('settings.subtitle')}
        </p>
      </header>

      <Tabs value={tab} onValueChange={(v) => setTab(v as TabKey)}>
        <TabsList>
          <TabsTrigger value="general">{t('settings.tabs.general')}</TabsTrigger>
          <TabsTrigger value="security">{t('settings.tabs.security')}</TabsTrigger>
        </TabsList>

        <TabsContent value="general" className="mt-4">
          <GeneralTab />
        </TabsContent>
        <TabsContent value="security" className="mt-4">
          <SecurityTab />
        </TabsContent>
      </Tabs>
    </div>
  );
}
