import { Skeleton } from '@/components/ui/skeleton';

export function SeriesDetailSkeleton() {
  return (
    <div data-testid="series-detail-skeleton" className="flex flex-col gap-5">
      {/* Hero placeholder */}
      <div
        className="relative rounded-xl border border-border-faint bg-bg-surface overflow-hidden"
        style={{ minHeight: 'clamp(360px, 38vh, 460px)' }}
      >
        <div className="absolute inset-0 flex items-end gap-5 p-6 md:p-8">
          <Skeleton className="w-[140px] h-[210px] md:w-[168px] md:h-[252px] min-[1440px]:w-[200px] min-[1440px]:h-[300px] rounded-lg" />
          <div className="flex-1 flex flex-col gap-3">
            <Skeleton className="h-7 w-2/3 max-w-[420px]" />
            <Skeleton className="h-4 w-1/3 max-w-[260px]" />
            <Skeleton className="h-3 w-1/2 max-w-[380px]" />
            <div className="flex gap-2 pt-2">
              <Skeleton className="h-8 w-32" />
              <Skeleton className="h-8 w-24" />
              <Skeleton className="h-8 w-28" />
            </div>
          </div>
        </div>
      </div>

      {/* Overview + aside placeholder */}
      <div className="grid grid-cols-1 min-[1440px]:grid-cols-[1fr_380px] gap-4">
        <div className="flex flex-col gap-2">
          <Skeleton className="h-3 w-20" />
          <Skeleton className="h-4 w-full" />
          <Skeleton className="h-4 w-11/12" />
          <Skeleton className="h-4 w-10/12" />
          <Skeleton className="h-4 w-8/12" />
        </div>
        <div className="flex flex-col gap-3">
          <Skeleton className="h-16 w-full rounded-lg" />
          <Skeleton className="h-20 w-full rounded-lg" />
        </div>
      </div>

      {/* Library card placeholder */}
      <Skeleton className="h-28 w-full rounded-lg" />
    </div>
  );
}
