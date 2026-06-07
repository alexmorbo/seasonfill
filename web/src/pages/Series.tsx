import { useCallback, useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { Library, Server } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { useSetPageTitle } from '@/components/shell/page-title-context';
import { useInstances } from '@/lib/instances';
import { useInstanceFilter } from '@/lib/instance-filter-context-internal';
import {
  useSeriesCacheInfinite,
  flattenSeriesCachePages,
} from '@/lib/api/seriesCache';
import { SeriesHeader } from '@/components/series/SeriesHeader';
import { SeriesGrid } from '@/components/series/SeriesGrid';
import { useNavigate } from 'react-router-dom';

export function Series() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  useSetPageTitle(t('series.title'));

  const inst = useInstances();
  const { filter } = useInstanceFilter();
  const instances = inst.data?.instances ?? [];
  const current = filter ?? instances[0]?.name ?? null;

  // 059a defaults: state=all, sort=updated_desc, limit=24.
  // 059b extends this with filter-bar driven state + client-side
  // search/monitored/network filters.
  const list = useSeriesCacheInfinite(
    current,
    { state: 'all', sort: 'updated_desc', limit: 24 },
  );

  const items = useMemo(
    () => flattenSeriesCachePages(list.data?.pages),
    [list.data?.pages],
  );
  const total = list.data?.pages?.[0]?.total ?? 0;

  const onLoadMore = useCallback(() => {
    if (list.hasNextPage && !list.isFetchingNextPage) {
      void list.fetchNextPage();
    }
  }, [list]);

  const onRefresh = useCallback(() => {
    void list.refetch();
  }, [list]);

  // First-run state — minimal inline stand-in (059b replaces with the
  // full <SeriesFirstRunState /> component).
  if (!inst.isPending && instances.length === 0) {
    return (
      <div className="max-w-[1440px] mx-auto p-1 flex flex-col items-center justify-center gap-4 py-16">
        <Server className="w-10 h-10 text-tx-faint" aria-hidden="true" />
        <h1 className="text-[18px] font-semibold text-tx-primary">
          {t('series.firstRun.title')}
        </h1>
        <p className="text-[13px] text-tx-secondary text-center max-w-[420px]">
          {t('series.firstRun.body')}
        </p>
        <Button type="button" onClick={() => navigate('/instances')}>
          {t('series.firstRun.cta')}
        </Button>
      </div>
    );
  }

  // Server returned 0 items (no cached series yet on this instance).
  const showEmptyServer = list.isSuccess && items.length === 0;

  return (
    <div className="max-w-[1440px] mx-auto p-1 flex flex-col gap-4">
      <SeriesHeader
        shownCount={items.length}
        totalCount={total}
        isLoading={list.isFetching && !list.isFetchingNextPage}
        isError={list.isError}
        onRefresh={onRefresh}
      />

      <div data-testid="filters-slot" />

      {showEmptyServer ? (
        <div
          className="flex flex-col items-center justify-center gap-3 py-16"
          data-testid="series-empty-server"
        >
          <Library className="w-10 h-10 text-tx-faint" aria-hidden="true" />
          <p className="text-[14px] text-tx-secondary text-center max-w-[420px]">
            {t('series.empty.server.body')}
          </p>
          <Button type="button" onClick={() => navigate('/scans?new=1')}>
            {t('series.empty.server.cta')}
          </Button>
        </div>
      ) : (
        <SeriesGrid
          items={items}
          isLoading={list.isPending}
          isFetchingNextPage={list.isFetchingNextPage}
          hasNextPage={list.hasNextPage ?? false}
          onLoadMore={onLoadMore}
        />
      )}
    </div>
  );
}
