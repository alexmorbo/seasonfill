import { useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  Tabs, TabsContent, TabsList, TabsTrigger,
} from '@/components/ui/tabs';
import { InstancesTab } from '@/components/settings/InstancesTab';
import { GeneralTab } from '@/components/settings/GeneralTab';
import { SecurityTab } from '@/components/settings/SecurityTab';

type TabKey = 'instances' | 'general' | 'security';

function parseHash(h: string): TabKey {
  const v = h.replace(/^#/, '');
  if (v === 'general' || v === 'security') return v;
  return 'instances';
}

export function Settings() {
  const { t } = useTranslation();
  const initial = useMemo<TabKey>(() => {
    if (typeof window === 'undefined') return 'instances';
    return parseHash(window.location.hash);
  }, []);
  const [tab, setTab] = useState<TabKey>(initial);

  useEffect(() => {
    if (typeof window === 'undefined') return;
    const handler = () => setTab(parseHash(window.location.hash));
    window.addEventListener('hashchange', handler);
    return () => window.removeEventListener('hashchange', handler);
  }, []);

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
          <TabsTrigger value="instances">{t('settings.tabs.instances')}</TabsTrigger>
          <TabsTrigger value="general">{t('settings.tabs.general')}</TabsTrigger>
          <TabsTrigger value="security">{t('settings.tabs.security')}</TabsTrigger>
        </TabsList>

        <TabsContent value="instances" className="mt-4">
          <InstancesTab />
        </TabsContent>
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
