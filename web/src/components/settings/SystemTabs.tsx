import { useTranslation } from 'react-i18next';
import { useLocation, useNavigate } from 'react-router-dom';
import { SlidersHorizontal, ShieldCheck, Plug } from 'lucide-react';
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { cn } from '@/lib/utils';

const TAB_KEYS = ['general', 'security', 'integrations'] as const;
type TabKey = (typeof TAB_KEYS)[number];

const TRIGGER_BASE =
  'inline-flex items-center gap-2 px-[15px] py-[10px] text-[13.5px] font-medium ' +
  'text-tx-muted hover:text-tx-primary border-b-2 border-transparent -mb-px ' +
  'rounded-none bg-transparent';
const TRIGGER_ACTIVE =
  'data-[state=active]:text-tx-primary data-[state=active]:font-semibold ' +
  'data-[state=active]:border-accent data-[state=active]:bg-transparent ' +
  'data-[state=active]:shadow-none [&[data-state=active]>svg]:text-accent';

// activeFromPath maps /settings/system/<key> → key, falling back to
// 'general' for the bare /settings/system entry. The pattern mirrors
// the legacy SettingsTabs which read its value from state; here the
// URL is the source of truth so back/forward and direct links always
// pick the correct active tab.
function activeFromPath(pathname: string): TabKey {
  for (const k of TAB_KEYS) {
    if (pathname.startsWith(`/settings/system/${k}`)) return k;
  }
  return 'general';
}

// SystemTabs is the URL-bound replacement for the legacy value-prop
// SettingsTabs. Clicking a trigger navigates; the path drives which
// trigger renders data-state=active. Story 486 (N-7b).
//
// Browser back/forward + bookmark + reload all work because the URL
// is the model. No local state.
export function SystemTabs() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { pathname } = useLocation();
  const active: TabKey = activeFromPath(pathname);

  return (
    <Tabs
      value={active}
      onValueChange={(v) => navigate(`/settings/system/${v}`)}
      className="w-full"
    >
      <TabsList
        className={cn(
          'flex gap-0.5 border-b border-border-faint bg-transparent p-0 h-auto',
          'rounded-none mb-6',
        )}
        data-testid="system-tabs"
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
    </Tabs>
  );
}
