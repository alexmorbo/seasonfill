import { useCallback, useMemo } from 'react';
import { useSearchParams } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import {
  Flame, TrendingUp, Tag, SlidersHorizontal,
  type LucideIcon,
} from 'lucide-react';
import { useSetPageTitle } from '@/components/shell/page-title-context';
import { Tabs, TabsList, TabsTrigger, TabsContent } from '@/components/ui/tabs';
import { EmptyState } from '@/components/EmptyState';
import { TrendingGrid } from '@/components/discovery/TrendingGrid';
import { cn } from '@/lib/utils';

// Tab key constants — drive url <-> tab sync. Stories 515 (popular),
// 516 (genres), 517 (filter) swap the placeholder TabsContent bodies;
// the shell stays put.
const TAB_KEYS = ['trending', 'popular', 'genres', 'filtered'] as const;
export type DiscoveryTabKey = (typeof TAB_KEYS)[number];

const TAB_META: ReadonlyArray<{ key: DiscoveryTabKey; icon: LucideIcon }> = [
  { key: 'trending', icon: Flame },
  { key: 'popular',  icon: TrendingUp },
  { key: 'genres',   icon: Tag },
  { key: 'filtered', icon: SlidersHorizontal },
];

const isTabKey = (v: string | null): v is DiscoveryTabKey =>
  v !== null && (TAB_KEYS as readonly string[]).includes(v);

const TRIGGER = cn(
  'inline-flex items-center gap-2 px-[15px] py-[10px] text-[13.5px] font-medium',
  'text-tx-muted hover:text-tx-primary border-b-2 border-transparent -mb-px',
  'rounded-none bg-transparent',
  'data-[state=active]:text-tx-primary data-[state=active]:font-semibold',
  'data-[state=active]:border-accent data-[state=active]:bg-transparent',
  'data-[state=active]:shadow-none [&[data-state=active]>svg]:text-accent',
);

export function DiscoveryPage() {
  const { t } = useTranslation();
  useSetPageTitle(t('discovery.title'));

  const [params, setParams] = useSearchParams();
  const active = useMemo<DiscoveryTabKey>(() => {
    const raw = params.get('tab');
    return isTabKey(raw) ? raw : 'trending';
  }, [params]);

  const onChange = useCallback((next: string) => {
    if (!isTabKey(next)) return;
    const np = new URLSearchParams(params);
    np.set('tab', next);
    setParams(np, { replace: true });
  }, [params, setParams]);

  return (
    <div className="space-y-6">
      <Tabs value={active} onValueChange={onChange} className="w-full">
        <TabsList
          className={cn(
            'flex gap-0.5 border-b border-border-faint bg-transparent p-0 h-auto',
            'rounded-none',
          )}
          data-testid="discovery-tabs"
        >
          {TAB_META.map(({ key, icon: Icon }) => (
            <TabsTrigger key={key} value={key} className={TRIGGER} data-tab={key}>
              <Icon className="h-4 w-4" />
              {t(`discovery.tabs.${key}`)}
            </TabsTrigger>
          ))}
        </TabsList>

        <TabsContent value="trending" className="mt-6">
          <TrendingGrid />
        </TabsContent>
        {(['popular', 'genres', 'filtered'] as const).map((k) => (
          <TabsContent key={k} value={k} className="mt-6">
            <EmptyState title={t(`discovery.tabs.${k}`)} />
          </TabsContent>
        ))}
      </Tabs>
    </div>
  );
}
