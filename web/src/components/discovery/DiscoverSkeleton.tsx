// Story 517 / N-3e: cold-start + TMDB-throttle skeleton. 20 placeholder
// cards in the same 5-col grid as DiscoverySeriesCard. `animate-pulse`
// rather than the shared <Skeleton/> so the warming state visually reads
// distinct from the plain isPending skeleton.
export interface DiscoverSkeletonProps {
  readonly testId?: string;
  readonly count?: number;
}

const GRID_CLASS =
  'grid gap-4 grid-cols-2 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-5';

export function DiscoverSkeleton({
  testId = 'discovery-warming-skeleton',
  count = 20,
}: DiscoverSkeletonProps) {
  return (
    <div className={GRID_CLASS} data-testid={testId}>
      {Array.from({ length: count }).map((_, i) => (
        <div
          key={i}
          className="overflow-hidden rounded-lg bg-bg-surface-1 animate-pulse"
          data-testid="discovery-warming-skeleton-card"
        >
          <div className="aspect-[2/3] w-full bg-bg-surface-2" />
          <div className="space-y-1.5 p-2.5">
            <div className="h-3 w-3/4 rounded bg-bg-surface-2" />
            <div className="h-2.5 w-1/3 rounded bg-bg-surface-2" />
          </div>
        </div>
      ))}
    </div>
  );
}
