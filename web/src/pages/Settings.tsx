import { useEffect, useMemo, useState } from 'react';
import {
  Tabs, TabsContent, TabsList, TabsTrigger,
} from '@/components/ui/tabs';
import { InstancesTab } from '@/components/settings/InstancesTab';

type TabKey = 'instances' | 'general' | 'security';

function parseHash(h: string): TabKey {
  const v = h.replace(/^#/, '');
  if (v === 'general' || v === 'security') return v;
  return 'instances';
}

export function Settings() {
  const initial = useMemo<TabKey>(() => {
    if (typeof window === 'undefined') return 'instances';
    return parseHash(window.location.hash);
  }, []);
  const [tab, setTab] = useState<TabKey>(initial);

  useEffect(() => {
    if (typeof window === 'undefined') return;
    window.location.hash = tab;
  }, [tab]);

  return (
    <div className="max-w-[1024px] mx-auto p-6 flex flex-col gap-5">
      <header>
        <h1 className="text-[22px] font-semibold tracking-tight">Settings</h1>
        <p className="text-[13px] text-muted mt-1">
          Manage Sonarr instances, scheduling, scan defaults, and runtime auth.
        </p>
      </header>

      <Tabs value={tab} onValueChange={(v) => setTab(v as TabKey)}>
        <TabsList>
          <TabsTrigger value="instances">Instances</TabsTrigger>
          <TabsTrigger value="general">General</TabsTrigger>
          <TabsTrigger value="security">Security</TabsTrigger>
        </TabsList>

        <TabsContent value="instances" className="mt-4">
          <InstancesTab />
        </TabsContent>
        <TabsContent value="general" className="mt-4">
          <div className="text-muted text-[13px]">
            General tab is delivered by story 027e-2.
          </div>
        </TabsContent>
        <TabsContent value="security" className="mt-4">
          <div className="text-muted text-[13px]">
            Security tab is delivered by story 027e-3.
          </div>
        </TabsContent>
      </Tabs>
    </div>
  );
}
