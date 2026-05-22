import { useMemo } from 'react';
import type { ReactNode } from 'react';
import { Sheet, SheetContent, SheetHeader, SheetTitle } from '@/components/ui/sheet';
import { StatusBadge } from '@/components/StatusBadge';
import { EmptyState } from '@/components/EmptyState';
import { useGrabs, flattenGrabs, type Grab } from '@/lib/grabs';
import { relativeTime } from '@/lib/format';

function KV({ k, v, mono = false }: { k: string; v: ReactNode; mono?: boolean }) {
  return (
    <div className="grid grid-cols-[120px_1fr] gap-x-3 py-1.5 border-b border-border-faint last:border-b-0">
      <span className="text-[12px] text-faint">{k}</span>
      <span className={`text-[12.5px] ${mono ? 'font-mono break-all' : ''}`}>{v}</span>
    </div>
  );
}

export function GrabDrawer({
  id,
  open,
  onOpenChange,
  rows,
}: {
  id: string | null;
  open: boolean;
  onOpenChange: (o: boolean) => void;
  rows?: readonly Grab[] | undefined;
}) {
  const q = useGrabs();
  const all = useMemo(() => rows ?? flattenGrabs(q.data?.pages), [rows, q.data]);
  const g = id ? all.find((x) => x.id === id) : null;

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent side="right" className="w-full sm:max-w-md overflow-y-auto p-0">
        <SheetHeader className="px-5 pt-5 pb-3 border-b border-border-faint">
          <SheetTitle className="flex items-center gap-3 text-[15px] font-semibold tracking-tight">
            <span>{g?.series_title ?? 'Grab'}</span>
            {g?.status && <StatusBadge value={g.status} />}
          </SheetTitle>
          {g?.created_at && (
            <div className="text-[12px] text-faint font-mono">
              {g.instance} · {relativeTime(g.updated_at ?? g.created_at)}
            </div>
          )}
        </SheetHeader>
        <div className="px-5 py-4">
          {!g ? (
            <EmptyState title="Grab not found" body="Rotated past the loaded page." />
          ) : (
            <>
              <KV k="Release" v={g.release_title ?? '—'} mono />
              <KV k="Quality" v={g.quality ?? '—'} mono />
              <KV k="Indexer" v={g.indexer_name ?? '—'} mono />
              <KV k="CF score" v={g.custom_format_score ?? 0} mono />
              <KV k="Coverage" v={g.coverage_count ?? 0} mono />
              <KV k="Attempts" v={g.attempts ?? 0} mono />
              <KV
                k="Season"
                v={g.season_number !== undefined ? `S${String(g.season_number).padStart(2, '0')}` : '—'}
                mono
              />
              {g.release_guid && <KV k="Release GUID" v={g.release_guid} mono />}
              {g.scan_run_id && <KV k="Scan run" v={g.scan_run_id.slice(0, 8)} mono />}
              {g.error_message && (
                <div className="mt-3">
                  <h4 className="text-[11px] uppercase tracking-[0.06em] text-status-danger mb-1.5">
                    Error
                  </h4>
                  <div className="font-mono text-[12px] bg-status-danger/10 border border-status-danger/30 rounded-md p-2.5 break-all">
                    {g.error_message}
                  </div>
                </div>
              )}
            </>
          )}
        </div>
      </SheetContent>
    </Sheet>
  );
}
