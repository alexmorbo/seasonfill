import type { ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { SlidersHorizontal, ShieldCheck, Plug } from 'lucide-react';
import { Tabs, TabsList, TabsTrigger, TabsContent } from '@/components/ui/tabs';
import { cn } from '@/lib/utils';

export type SettingsTabKey = 'general' | 'security' | 'integrations';

interface SettingsTabsProps {
  readonly value: SettingsTabKey;
  readonly onValueChange: (v: SettingsTabKey) => void;
  readonly general: ReactNode;
  readonly security: ReactNode;
  readonly integrations: ReactNode;
}

const TRIGGER_BASE =
  'inline-flex items-center gap-2 px-[15px] py-[10px] text-[13.5px] font-medium ' +
  'text-tx-muted hover:text-tx-primary border-b-2 border-transparent -mb-px ' +
  'rounded-none bg-transparent';
const TRIGGER_ACTIVE =
  'data-[state=active]:text-tx-primary data-[state=active]:font-semibold ' +
  'data-[state=active]:border-accent data-[state=active]:bg-transparent ' +
  'data-[state=active]:shadow-none [&[data-state=active]>svg]:text-accent';

export function SettingsTabs(props: SettingsTabsProps) {
  const { t } = useTranslation();
  return (
    <Tabs
      value={props.value}
      onValueChange={(v) => props.onValueChange(v as SettingsTabKey)}
      className="w-full"
    >
      <TabsList
        className={cn(
          'flex gap-0.5 border-b border-border-faint bg-transparent p-0 h-auto',
          'rounded-none mb-6',
        )}
        data-testid="settings-tabs"
      >
        <TabsTrigger
          value="general"
          className={cn(TRIGGER_BASE, TRIGGER_ACTIVE)}
          data-tab="general"
        >
          <SlidersHorizontal className="w-4 h-4" />
          {t('settings.tabs.general')}
        </TabsTrigger>
        <TabsTrigger
          value="security"
          className={cn(TRIGGER_BASE, TRIGGER_ACTIVE)}
          data-tab="security"
        >
          <ShieldCheck className="w-4 h-4" />
          {t('settings.tabs.security')}
        </TabsTrigger>
        <TabsTrigger
          value="integrations"
          className={cn(TRIGGER_BASE, TRIGGER_ACTIVE)}
          data-tab="integrations"
        >
          <Plug className="w-4 h-4" />
          {t('settings.tabs.integrations')}
        </TabsTrigger>
      </TabsList>

      <TabsContent value="general" className="mt-0">{props.general}</TabsContent>
      <TabsContent value="security" className="mt-0">{props.security}</TabsContent>
      <TabsContent value="integrations" className="mt-0">{props.integrations}</TabsContent>
    </Tabs>
  );
}
