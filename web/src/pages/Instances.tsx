import { useInstances } from '@/lib/instances';
import { Card, CardContent, CardHeader } from '@/components/ui/card';
import { Skeleton } from '@/components/ui/skeleton';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Info, AlertTriangle } from 'lucide-react';
import { StatusBadge } from '@/components/StatusBadge';
import { EmptyState } from '@/components/EmptyState';
import { relativeTime } from '@/lib/format';
import { cn } from '@/lib/utils';
import { KIND_DOT, statusKind } from '@/lib/badge-variants';

export function Instances() {
  const q = useInstances();
  const instances = q.data?.instances ?? [];

  return (
    <div className="max-w-[1440px] mx-auto p-6 flex flex-col gap-5">
      <header className="flex items-center gap-4">
        <h1 className="text-[22px] font-semibold tracking-tight">Sonarr instances</h1>
        <span className="font-mono text-[12px] text-faint">
          configured via helm/values.yaml — read-only in v1
        </span>
      </header>

      {q.isError && (
        <Alert variant="destructive">
          <AlertTriangle className="w-4 h-4" />
          <AlertTitle>Failed to load instances</AlertTitle>
          <AlertDescription>{q.error.message}</AlertDescription>
        </Alert>
      )}

      {!q.isError && q.isPending && (
        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-3">
          {[0, 1, 2].map((i) => (
            <Card key={i}>
              <CardContent className="p-5 flex flex-col gap-2.5">
                <Skeleton className="h-4 w-32" />
                <Skeleton className="h-3 w-48" />
                <Skeleton className="h-3 w-40" />
                <Skeleton className="h-3 w-36" />
              </CardContent>
            </Card>
          ))}
        </div>
      )}
      {!q.isError && !q.isPending && instances.length === 0 && (
        <Card>
          <CardContent className="p-0">
            <EmptyState
              title="No instances configured"
              body="Add at least one Sonarr instance under helm values to start scanning."
            />
          </CardContent>
        </Card>
      )}
      {instances.length > 0 && (
        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-3">
          {instances.map((inst) => (
            <Card key={inst.name} className="relative">
              <span
                className={cn(
                  'absolute top-4 right-4 w-2 h-2 rounded-full',
                  KIND_DOT[statusKind(inst.health)],
                )}
                aria-hidden="true"
              />
              <CardHeader className="pb-2">
                <h3 className="text-[15px] font-semibold tracking-tight flex items-center gap-2">
                  <span className="font-mono">{inst.name}</span>
                  {inst.health && inst.health !== 'available' && (
                    <StatusBadge value={inst.health} />
                  )}
                </h3>
              </CardHeader>
              <CardContent className="text-[13px]">
                <dl className="grid grid-cols-[110px_1fr] gap-y-1.5 gap-x-3 text-[12.5px]">
                  <dt className="text-faint">Health</dt>
                  <dd className="font-mono">{inst.health ?? 'unknown'}</dd>
                  <dt className="text-faint">Last check</dt>
                  <dd className="text-muted">{relativeTime(inst.last_check_at)}</dd>
                  <dt className="text-faint">Transitions</dt>
                  <dd
                    className={cn(
                      'font-mono',
                      (inst.transitions_count ?? 0) > 0 && 'text-status-warning',
                    )}
                  >
                    {inst.transitions_count ?? 0}
                  </dd>
                  {inst.last_error && (
                    <>
                      <dt className="text-faint">Last error</dt>
                      <dd className="text-muted font-mono text-[11.5px] break-all">
                        {inst.last_error}
                      </dd>
                    </>
                  )}
                </dl>
              </CardContent>
            </Card>
          ))}
        </div>
      )}

      <Card>
        <CardContent className="p-4 flex items-start gap-3 text-[13px] text-foreground-2">
          <Info className="w-4 h-4 text-muted shrink-0 mt-0.5" />
          <div>
            <div className="font-semibold text-foreground mb-1">
              Instance CRUD is read-only in v1
            </div>
            Adding, removing, or editing instances at runtime requires moving config from{' '}
            <span className="font-mono">values.yaml</span> into the database. Tracked as Phase
            5.2+.
          </div>
        </CardContent>
      </Card>
    </div>
  );
}
