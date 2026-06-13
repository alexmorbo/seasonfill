import { ArrowDown, ArrowUp } from 'lucide-react';
import { cn } from '@/lib/utils';

export interface SpeedCellProps {
  readonly down?: number | undefined;
  readonly up?: number | undefined;
  // muted true → the row is non-live; show "—" instead of zeroes.
  readonly muted?: boolean | undefined;
  readonly className?: string | undefined;
}

function humanBps(n: number | undefined): string {
  if (!n || n <= 0) return '—';
  const units = ['B/s', 'KB/s', 'MB/s', 'GB/s'];
  let v = n;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) { v /= 1024; i += 1; }
  // 1 decimal until GB/s, then keep one for sanity.
  const prec = v >= 100 || i === 0 ? 0 : 1;
  return `${v.toFixed(prec)} ${units[i]}`;
}

export function SpeedCell({ down, up, muted, className }: SpeedCellProps) {
  const dn = muted ? '—' : humanBps(down);
  const upS = muted ? '—' : humanBps(up);
  return (
    <span data-testid="speed-cell" className={cn('inline-flex flex-col gap-0.5 text-[11.5px] tabular-nums', className)}>
      <span className="inline-flex items-center gap-1 text-tx-secondary">
        <ArrowDown className="w-3 h-3 text-tx-faint" aria-hidden="true" />
        <span data-testid="speed-cell-down">{dn}</span>
      </span>
      <span className="inline-flex items-center gap-1 text-tx-secondary">
        <ArrowUp className="w-3 h-3 text-tx-faint" aria-hidden="true" />
        <span data-testid="speed-cell-up">{upS}</span>
      </span>
    </span>
  );
}
