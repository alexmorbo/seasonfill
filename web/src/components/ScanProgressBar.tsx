import { cn } from '@/lib/utils';
import { durationMs } from '@/lib/format';

// Q-011d-1: dto.Scan has no series_total today, so the default render is
// indeterminate. The seriesTotal? prop is reserved for future M-011d-1.
export function ScanProgressBar({
  status, seriesScanned, seriesTotal, startedAt, finishedAt,
}: {
  status: string | undefined;
  seriesScanned: number;
  seriesTotal?: number | undefined;
  startedAt?: string | undefined;
  finishedAt?: string | undefined;
}) {
  const determinate = typeof seriesTotal === 'number' && seriesTotal > 0;
  const elapsed = startedAt ? durationMs(startedAt, finishedAt ?? new Date().toISOString()) : '—';
  const fillClass =
    status === 'completed' ? 'bg-status-success'
    : status === 'failed' ? 'bg-status-danger'
    : status === 'aborted' || status === 'cancelled' ? 'bg-status-warning'
    : 'bg-status-info';
  const label = determinate
    ? `${seriesScanned}/${seriesTotal} series scanned`
    : status === 'running' ? `${seriesScanned} series scanned`
    : `${seriesScanned} series scanned in ${elapsed}`;
  // Indeterminate fills to a hint percentage; animate-pulse provides motion.
  const pct = determinate
    ? Math.min(100, Math.max(0, Math.round((seriesScanned / (seriesTotal as number)) * 100)))
    : status === 'running' ? 50 : 100;

  return (
    <div
      className="flex flex-col gap-1.5"
      role="progressbar"
      aria-valuemin={0}
      aria-valuemax={determinate ? (seriesTotal as number) : undefined}
      aria-valuenow={determinate ? seriesScanned : undefined}
      aria-label={`Scan progress: ${label}`}
      data-status={status ?? 'unknown'}
      data-determinate={determinate ? 'true' : 'false'}
    >
      <div className="flex items-center justify-between text-[11.5px] font-mono">
        <span className={status === 'running' ? 'text-status-info' : 'text-muted'}>{label}</span>
        {status === 'running' && <span className="text-faint">· elapsed {elapsed}</span>}
      </div>
      <div className="h-1.5 w-full rounded bg-surface-2 overflow-hidden">
        <div
          className={cn(
            'h-full rounded transition-[width] duration-500',
            fillClass,
            status === 'running' && !determinate && 'animate-pulse',
          )}
          style={{ width: `${pct}%` }}
        />
      </div>
    </div>
  );
}
