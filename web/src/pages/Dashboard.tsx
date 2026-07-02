import { useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router-dom';
import { useQuery, type UseQueryResult, keepPreviousData } from '@tanstack/react-query';
import { ArrowRight, TriangleAlert } from 'lucide-react';
import { toast } from 'sonner';
import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import { useSetPageTitle } from '@/components/shell/page-title-context';
import { useInstances } from '@/lib/instances';
import { useInstanceFilter } from '@/lib/instance-filter-context-internal';
import { useTriggerScan } from '@/lib/scan-mutations';
import { ApiError, api } from '@/lib/api';
import { useSeriesCache, type SeriesCacheItem } from '@/lib/api/seriesCache';
import { useLanguage } from '@/hooks/useLanguage';
import { HeroGreeting } from '@/components/dashboard/HeroGreeting';
import { PosterGrid } from '@/components/dashboard/PosterGrid';
import { DashboardEmptyState } from '@/components/dashboard/DashboardEmptyState';
import { DashboardFirstRunState } from '@/components/dashboard/DashboardFirstRunState';
import { DashboardRail } from '@/components/dashboard/DashboardRail';
import { TMDBStatusBanner } from '@/components/dashboard/TMDBStatusBanner';
import { useStepperState } from '@/components/dashboard/useStepperState';
import { relativeTime } from '@/lib/format';

// Local mirror of 048 CountersAggregateDTO — swap to codegen once 048
// is merged.
interface CountersAggregate {
  readonly items: ReadonlyArray<{
    readonly instance_name: string;
    readonly window: string;
    readonly totals: { readonly grabs: number; readonly imports: number; readonly fails: number };
    readonly avg_grabs_7d: number;
  }>;
}

function useCountersAggregate(window: '24h' | '7d'): UseQueryResult<CountersAggregate, ApiError> {
  return useQuery<CountersAggregate, ApiError>({
    queryKey: ['counters', window] as const,
    queryFn: () => api<CountersAggregate>(`/counters?window=${window}`),
    staleTime: 60_000,
    refetchInterval: 60_000,
    refetchOnWindowFocus: false,
    placeholderData: keepPreviousData,
  });
}

export function Dashboard() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  useSetPageTitle(t('dashboard.title'));

  const inst = useInstances();
  const { filter } = useInstanceFilter();
  const instances = inst.data?.instances ?? [];
  const current = filter ?? instances[0]?.name ?? null;
  const lang = useLanguage().current;

  const imported = useSeriesCache(current, { status: 'imported', limit: 12, sort: 'updated_desc', lang });
  const counters = useCountersAggregate('24h');
  const stepper = useStepperState();

  const importedItems = imported.data?.items ?? [];
  const totals = useMemo(() => {
    const items = counters.data?.items ?? [];
    return items.reduce(
      (acc, it) => ({
        grabs: acc.grabs + it.totals.grabs,
        imports: acc.imports + it.totals.imports,
        fails: acc.fails + it.totals.fails,
        avg: acc.avg + it.avg_grabs_7d,
      }),
      { grabs: 0, imports: 0, fails: 0, avg: 0 },
    );
  }, [counters.data]);

  const empty24h = !imported.isPending && importedItems.length === 0 && totals.grabs === 0;

  const lastImport = useSeriesCache(
    current, { status: 'all', limit: 1, sort: 'updated_desc', lang }, { enabled: empty24h },
  );
  const missing = useSeriesCache(
    current, { status: 'missing', limit: 100, lang }, { enabled: empty24h },
  );

  const triggerScan = useTriggerScan();
  const onScanNow = () => {
    triggerScan.mutate({}, {
      onSuccess: () => { toast.success(t('dashboard.empty.scanStarted')); navigate('/scans'); },
      onError: (err) => toast.error(t('dashboard.empty.scanFailed', { error: err.message })),
    });
  };

  // Onboarding shell.
  // Story 489 (B-17): TMDBStatusBanner mounts above both first-run and
  // main paths so the operator sees an invalid-key warning even before
  // configuring their first Sonarr instance.
  // Story 494 (B-16a): full first-run stepper renders when instances are
  // absent OR any required onboarding step is not done (TMDB / webhook /
  // scan). Once all required steps are green → normal Dashboard layout.
  if (!inst.isPending && (instances.length === 0 || !stepper.allRequiredDone)) {
    return (
      <div className="flex flex-col gap-5" data-testid="dashboard-onboarding-shell">
        <TMDBStatusBanner />
        <DashboardFirstRunState />
      </div>
    );
  }

  const heroGrabs = counters.isSuccess ? totals.grabs : null;
  const heroImports = counters.isSuccess ? totals.imports : null;
  const heroFails = counters.isSuccess ? totals.fails : null;
  const heroAvg = counters.isSuccess ? totals.avg : null;
  const quietWhen = empty24h
    ? lastImport.data?.items[0]
      ? relativeTime(lastImport.data.items[0].last_grab_at ?? lastImport.data.items[0].updated_at)
      : null
    : undefined;

  const missingCount = missing.isSuccess
    ? missing.data.has_more ? 100 : missing.data.items.length
    : null;

  return (
    <div className="flex flex-col gap-5">
      <TMDBStatusBanner />
      <HeroGreeting
        grabs={heroGrabs}
        imports={heroImports}
        fails={heroFails}
        avg7d={heroAvg}
        quietLastImport={empty24h ? quietWhen : undefined}
      />
      <div className="grid gap-[22px] items-start grid-cols-1 lg:grid-cols-[1fr_332px]">
        <section data-testid="dashboard-left">
          <div className="flex items-baseline gap-2.5 mb-3">
            <h2 className="text-[14px] font-semibold tracking-tight m-0">
              {t('dashboard.recentImports.title')}
            </h2>
            <span className="text-[12px] text-tx-faint" data-testid="recent-meta">
              {imported.isPending ? (
                <Skeleton className="inline-block h-3 w-32 align-middle" />
              ) : imported.isError ? (
                <span className="inline-flex items-center gap-1 text-warn">
                  <TriangleAlert className="w-3 h-3" aria-hidden="true" />
                  {t('dashboard.recentImports.loadFailed')}
                </span>
              ) : (
                t('dashboard.recentImports.subtitle', { count: importedItems.length })
              )}
            </span>
            <span className="flex-1" />
            <Button
              variant="ghost"
              size="sm"
              onClick={() => navigate('/grabs?status=imported')}
              data-testid="recent-all-link"
            >
              {t('dashboard.recentImports.allLink')}
              <ArrowRight className="w-3.5 h-3.5" aria-hidden="true" />
            </Button>
          </div>
          {empty24h ? (
            <DashboardEmptyState
              missingCount={missingCount}
              lastImport={(lastImport.data?.items[0] as SeriesCacheItem | undefined) ?? null}
              onScanNow={onScanNow}
              onOpenQueue={() => current && navigate(`/instances/${current}/queue`)}
              scanPending={triggerScan.isPending}
            />
          ) : (
            <PosterGrid items={importedItems} isLoading={imported.isPending} />
          )}
        </section>
        <DashboardRail />
      </div>
    </div>
  );
}
