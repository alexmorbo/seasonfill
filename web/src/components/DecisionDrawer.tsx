import { useMemo } from 'react';
import { Sheet, SheetContent, SheetHeader, SheetTitle } from '@/components/ui/sheet';
import { StatusBadge } from '@/components/StatusBadge';
import { EmptyState } from '@/components/EmptyState';
import { DecisionDetail } from '@/components/DecisionDetail';
import { useDecisions, flattenDecisions, type Decision } from '@/lib/decisions';
import { relativeTime } from '@/lib/format';

export function DecisionDrawer({
  id,
  open,
  onOpenChange,
  rows,
}: {
  id: string | null;
  open: boolean;
  onOpenChange: (o: boolean) => void;
  rows?: readonly Decision[] | undefined;
}) {
  const q = useDecisions();
  const all = useMemo(() => rows ?? flattenDecisions(q.data?.pages), [rows, q.data]);
  const d = id ? all.find((x) => x.id === id) : null;

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent side="right" className="w-full sm:max-w-md overflow-y-auto p-0">
        <SheetHeader className="px-5 pt-5 pb-3 border-b border-border-faint">
          <SheetTitle className="flex items-center gap-3 text-[15px] font-semibold tracking-tight">
            <span>{d?.series_title ?? 'Decision'}</span>
            {d?.season_number !== undefined && (
              <span className="font-mono text-[12px] text-muted">
                S{String(d.season_number).padStart(2, '0')}
              </span>
            )}
            {d?.decision && <StatusBadge value={d.decision} mode="outcome" />}
          </SheetTitle>
          {d?.created_at && (
            <div className="text-[12px] text-faint font-mono">
              {d.instance} · {relativeTime(d.created_at)} · scan{' '}
              <span className="text-accent">{(d.scan_run_id ?? '').slice(0, 8)}</span>
            </div>
          )}
        </SheetHeader>
        <div className="px-5 py-4">
          {d ? (
            <DecisionDetail d={d} />
          ) : (
            <EmptyState
              title="Decision not found"
              body="Rotated past the loaded page. Reload to re-fetch."
            />
          )}
        </div>
      </SheetContent>
    </Sheet>
  );
}
