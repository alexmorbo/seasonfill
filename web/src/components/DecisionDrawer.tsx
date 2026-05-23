import { useMemo } from 'react';
import { Sheet, SheetContent, SheetHeader, SheetTitle } from '@/components/ui/sheet';
import { StatusBadge } from '@/components/StatusBadge';
import { EmptyState } from '@/components/EmptyState';
import { DecisionDetail } from '@/components/DecisionDetail';
import { Button } from '@/components/ui/button';
import { Loader2, Zap, AlertCircle, Copy, RotateCcw, ArrowUpRight } from 'lucide-react';
import { toast } from 'sonner';
import { useGrabDecision } from '@/lib/grab-mutation';
import { useRescanDecision } from '@/lib/rescan-mutation';
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
          {d?.superseded_by_id && <SupersededByLine successorId={d.superseded_by_id} />}
        </SheetHeader>
        <div className="px-5 py-4 flex flex-col gap-4">
          {d ? (
            <>
              <DecisionDetail d={d} />
              <ErrorDetailSection d={d} />
              <RescanSection d={d} />
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

// SupersededByLine renders in the drawer header when this decision
// has been rescanned (017 §3.5). Clicking the link swaps the drawer
// to the successor by mutating the URL `?drawer=<successor_id>`.
// We touch window.history directly because DecisionDrawer is
// router-agnostic (used in both /decisions and /scans/:id pages).
function SupersededByLine({ successorId }: { successorId: string }) {
  const onOpenSuccessor = () => {
    const url = new URL(window.location.href);
    url.searchParams.set('drawer', successorId);
    window.history.replaceState({}, '', url.toString());
    // useSearchParams in the consumer pages listens to popstate, not
    // replaceState. Dispatch a synthetic popstate so the parent
    // re-reads search params and re-renders with the successor.
    window.dispatchEvent(new PopStateEvent('popstate'));
  };
  return (
    <div className="text-[11.5px] text-status-warning font-mono flex items-center gap-1.5">
      <ArrowUpRight className="w-3 h-3" aria-hidden="true" />
      <span>Superseded by</span>
      <button
        type="button"
        onClick={onOpenSuccessor}
        className="underline underline-offset-2 hover:text-accent"
        data-testid="superseded-by-link"
      >
        {successorId.slice(0, 8)}
      </button>
    </div>
  );
}

// RescanSection renders the "Rescan" button when the decision is
// eligible (017 §3.2): not already superseded. The backend further
// gates on "no grab_records row exists for the 4-tuple" — that gate
// fires server-side as a 409; the UI shows a toast on 409 and the
// button remains visible so the operator can retry after fixing
// upstream state (e.g. clearing the grab record manually).
//
// The button is intentionally distinct visually from "Grab now":
// neutral border (not warning yellow) because rescan is read-mostly
// (re-evaluates against Sonarr but writes only one decision row +
// one supersede pointer; no upstream grab POST).
function RescanSection({ d }: { d: Decision }) {
  const rescan = useRescanDecision();
  // Server is authoritative for the "is it already executed?" gate;
  // here we only hide the section when this row is itself already
  // superseded. The drawer rendering a section for a row the user
  // *just rescanned* would be confusing — and the supersede check
  // is a single field already in hand.
  if (d.superseded_by_id) return null;

  const onClick = () => {
    if (!d.id) return;
    rescan.mutate(
      { decisionId: d.id },
      {
        // Per-call onSuccess fires AFTER the hook-level onSuccess
        // (TanStack Query ordering contract), so by this point:
        //   1. The new decision has been seeded into every
        //      `['decisions', ...]` cache (rescan-mutation.ts §5.1).
        //   2. invalidateQueries has been queued.
        // Both ensure the drawer's flattenDecisions() finds the new
        // id immediately when we swap the URL param — no "Decision
        // not found" flash.
        onSuccess: (fresh) => {
          if (!fresh.id) return;
          openDrawerForDecision(fresh.id);
        },
      },
    );
  };

  return (
    <section
      aria-labelledby="rescan-heading"
      className="border border-border-faint rounded-md p-4 bg-surface flex flex-col gap-2.5"
    >
      <div className="flex items-center gap-2">
        <RotateCcw className="w-3.5 h-3.5 text-muted" aria-hidden="true" />
        <h4
          id="rescan-heading"
          className="text-[12px] font-semibold uppercase tracking-[0.06em] text-muted"
        >
          Rescan
        </h4>
      </div>
      <p className="text-[12.5px] text-muted">
        Re-evaluate this season against Sonarr (e.g. after re-enabling
        an indexer). Bypasses cooldowns. The current decision is marked
        superseded; the new outcome lands in the same scan record. The
        drawer will switch to the new outcome automatically.
      </p>
      <div className="flex items-center gap-2">
        <Button
          variant="outline"
          size="sm"
          className="h-8"
          onClick={onClick}
          disabled={rescan.isPending || !d.id}
          aria-label="Rescan decision"
          data-testid="rescan-button"
        >
          {rescan.isPending ? (
            <Loader2 className="w-3.5 h-3.5 mr-1.5 animate-spin" aria-hidden="true" />
          ) : (
            <RotateCcw className="w-3.5 h-3.5 mr-1.5" aria-hidden="true" />
          )}
          {rescan.isPending ? 'Rescanning…' : 'Rescan'}
        </Button>
        {rescan.isSuccess && rescan.data?.id && (
          <span className="text-[11.5px] font-mono text-status-success">
            new: {rescan.data.id.slice(0, 8)}
          </span>
        )}
      </div>
    </section>
  );
}

// Swap the consumer page's `?drawer=<id>` to the new decision. We
// reuse the exact mechanism `SupersededByLine` already uses (lines
// ~195–219): mutate URL via history.replaceState + dispatch a
// synthetic popstate so useSearchParams in the parent picks up the
// new value and re-renders.
//
// This stays out of react-router intentionally — DecisionDrawer is
// rendered in two pages with subtly different router state, and
// 017b chose the popstate route for the same boundary reason.
// M-019-1 tracks the future refactor to a shared search-param hook.
//
// Defensive notes (019 fix):
//   - `window.location.href` may be undefined when callers stub
//     `window.location` in tests; fall back to `pathname + search`.
//   - `replaceState` is called with a relative path (just
//     `pathname?query`) instead of a full absolute URL — JSDOM's
//     same-origin guard rejects absolute URLs whose host differs
//     from the document's actual origin (the JSDOM default URL is
//     `about:blank` even when `window.location` has been redefined).
//     Relative paths sidestep the check entirely and behave the
//     same way in real browsers.
function openDrawerForDecision(successorId: string) {
  const loc = window.location;
  const base =
    typeof loc.href === 'string' && loc.href
      ? loc.href
      : `http://localhost${loc.pathname ?? ''}${loc.search ?? ''}`;
  const url = new URL(base);
  url.searchParams.set('drawer', successorId);
  const relative = `${url.pathname}${url.search}${url.hash}`;
  window.history.replaceState({}, '', relative);
  window.dispatchEvent(new PopStateEvent('popstate'));
}
