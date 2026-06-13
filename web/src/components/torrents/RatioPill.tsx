import { cn } from '@/lib/utils';

export interface RatioPillProps {
  readonly value?: number | undefined;
  // For non-live rows we still show the last persisted ratio, but
  // muted; muted=true switches text to tx-muted.
  readonly muted?: boolean | undefined;
  readonly className?: string | undefined;
}

function classify(v: number): 'low' | 'mid' | 'high' {
  if (v < 1) return 'low';
  if (v < 2) return 'mid';
  return 'high';
}

const TONE: Record<'low' | 'mid' | 'high', string> = {
  low:  'text-warn',
  mid:  'text-tx-secondary',
  high: 'text-ok',
};

export function RatioPill({ value, muted, className }: RatioPillProps) {
  if (value == null || Number.isNaN(value)) {
    return <span data-testid="ratio-pill" className={cn('text-[11.5px] tabular-nums text-tx-faint', className)}>—</span>;
  }
  const tier = classify(value);
  const tone = TONE[tier];
  return (
    <span
      data-testid="ratio-pill"
      data-tier={tier}
      className={cn(
        'text-[11.5px] font-medium tabular-nums',
        muted ? 'text-tx-muted' : tone,
        className,
      )}
    >
      {value.toFixed(2)}
    </span>
  );
}
