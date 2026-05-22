import type { ReactNode } from 'react';
import { cn } from '@/lib/utils';
import type { Decision } from '@/lib/decisions';

function Row({
  k,
  v,
  mono = false,
  accent,
}: {
  k: string;
  v: ReactNode;
  mono?: boolean;
  accent?: 'pos' | 'neg' | 'muted';
}) {
  return (
    <div className="grid grid-cols-[160px_1fr] gap-x-3 py-1.5 border-b border-border-faint last:border-b-0">
      <span className="text-[12px] text-faint">{k}</span>
      <span
        className={cn(
          'text-[12.5px]',
          mono && 'font-mono',
          accent === 'pos' && 'text-status-success',
          accent === 'neg' && 'text-status-danger',
          accent === 'muted' && 'text-muted',
        )}
      >
        {v}
      </span>
    </div>
  );
}

export function DecisionDetail({ d }: { d: Decision }) {
  const missing = d.missing_count ?? 0;
  return (
    <div className="px-1 py-2">
      <h4 className="text-[11px] uppercase tracking-[0.06em] text-foreground-2 mb-1.5">
        Decision tree
      </h4>
      <Row k="Reason" v={d.reason ?? '—'} mono accent="muted" />
      <Row k="Candidates evaluated" v={d.candidates_count ?? 0} mono />
      <Row k="Releases found" v={d.releases_found ?? 0} mono />
      <Row k="Existing files" v={d.existing_count ?? 0} mono />
      <Row
        k="Missing episodes"
        v={missing}
        mono
        {...(missing > 0 ? { accent: 'neg' as const } : {})}
      />
      {d.selected_guid && (
        <Row k="Selected guid" v={<span className="break-all">{d.selected_guid}</span>} mono />
      )}
      {d.dry_run_would_grab !== undefined && (
        <Row
          k="Dry-run would grab"
          v={d.dry_run_would_grab ? 'yes' : 'no'}
          mono
          accent={d.dry_run_would_grab ? 'pos' : 'muted'}
        />
      )}
      {d.created_at && <Row k="Created" v={d.created_at} mono accent="muted" />}
    </div>
  );
}
