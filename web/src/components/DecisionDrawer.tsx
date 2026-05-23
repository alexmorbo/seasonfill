import { useMemo } from 'react';
import { Sheet, SheetContent, SheetHeader, SheetTitle } from '@/components/ui/sheet';
import { StatusBadge } from '@/components/StatusBadge';
import { EmptyState } from '@/components/EmptyState';
import { DecisionDetail } from '@/components/DecisionDetail';
import { Button } from '@/components/ui/button';
import { Loader2, Zap, AlertCircle, Copy } from 'lucide-react';
import { toast } from 'sonner';
import { useGrabDecision } from '@/lib/grab-mutation';
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
        <div className="px-5 py-4 flex flex-col gap-4">
          {d ? (
            <>
              <DecisionDetail d={d} />
              <ErrorDetailSection d={d} />
              <GrabNowSection d={d} />
            </>
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

function GrabNowSection({ d }: { d: Decision }) {
  const grab = useGrabDecision();
  const eligible =
    d.decision === 'grab' &&
    Boolean(d.selected_guid) &&
    d.dry_run_would_grab === true;

  if (!eligible) return null;

  const onClick = () => {
    if (!d.id) return;
    grab.mutate({ decisionId: d.id });
  };

  return (
    <section
      aria-labelledby="grab-now-heading"
      className="border border-status-warning/30 rounded-md p-4 bg-status-warning/5 flex flex-col gap-2.5"
    >
      <div className="flex items-center gap-2">
        <Zap className="w-3.5 h-3.5 text-status-warning" aria-hidden="true" />
        <h4
          id="grab-now-heading"
          className="text-[12px] font-semibold uppercase tracking-[0.06em] text-status-warning"
        >
          Force grab
        </h4>
      </div>
      <p className="text-[12.5px] text-muted">
        This will force-grab the selected release in Sonarr, bypassing the
        global <span className="font-mono">dry_run</span> flag. Idempotent on
        (instance, series, season, release_guid) — safe to retry, but only one
        record will be created.
      </p>
      <div className="flex items-center gap-2">
        <Button
          variant="default"
          size="sm"
          className="h-8"
          onClick={onClick}
          disabled={grab.isPending || !d.id}
          aria-label="Grab now"
        >
          {grab.isPending ? (
            <Loader2 className="w-3.5 h-3.5 mr-1.5 animate-spin" aria-hidden="true" />
          ) : (
            <Zap className="w-3.5 h-3.5 mr-1.5" aria-hidden="true" />
          )}
          {grab.isPending ? 'Grabbing…' : 'Grab now'}
        </Button>
        {grab.isSuccess && (
          <span className="text-[11.5px] font-mono text-status-success">
            grabbed: {grab.data.id?.slice(0, 8) ?? '—'}
          </span>
        )}
      </div>
    </section>
  );
}

// ErrorDetailSection renders the raw upstream error string (truncated
// server-side to ≤256 runes) for error-category decisions. Gated on
// (category === 'error' && error_detail) — non-error decisions never
// render even if the field is populated by some future code path
// (Q-014-3).
function ErrorDetailSection({ d }: { d: Decision }) {
  if (d.category !== 'error' || !d.error_detail) return null;

  const onCopy = async () => {
    // Defensive: navigator.clipboard is undefined in JSDOM by default.
    // The test environment stubs it; in prod browsers it's always present
    // under https / localhost. Fall back to a toast instead of throwing.
    if (!navigator.clipboard?.writeText) {
      toast.error('Clipboard not available');
      return;
    }
    try {
      await navigator.clipboard.writeText(d.error_detail ?? '');
      toast.success('Copied to clipboard');
    } catch {
      toast.error('Copy failed');
    }
  };

  return (
    <section
      aria-labelledby="error-detail-heading"
      className="border border-status-danger/30 rounded-md p-4 bg-status-danger/5 flex flex-col gap-2.5"
    >
      <div className="flex items-center justify-between gap-2">
        <div className="flex items-center gap-2">
          <AlertCircle className="w-3.5 h-3.5 text-status-danger" aria-hidden="true" />
          <h4
            id="error-detail-heading"
            className="text-[12px] font-semibold uppercase tracking-[0.06em] text-status-danger"
          >
            Error detail
          </h4>
        </div>
        <button
          type="button"
          onClick={onCopy}
          className="inline-flex items-center gap-1 px-1.5 h-6 rounded border border-border-faint text-[11px] text-muted hover:text-foreground hover:bg-surface-2"
          aria-label="Copy error detail to clipboard"
          data-testid="error-detail-copy"
        >
          <Copy className="w-3 h-3" aria-hidden="true" />
          Copy
        </button>
      </div>
      <div
        className="font-mono text-[12px] bg-surface-2 rounded px-2.5 py-2 break-all whitespace-pre-wrap select-text text-foreground-2"
        data-testid="error-detail-text"
      >
        {d.error_detail}
      </div>
    </section>
  );
}
