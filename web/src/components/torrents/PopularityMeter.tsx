import { cn } from '@/lib/utils';

export interface PopularityMeterProps {
  readonly value?: number | undefined;
  readonly className?: string | undefined;
}

export function PopularityMeter({ value, className }: PopularityMeterProps) {
  if (value == null || value === 0 || Number.isNaN(value)) {
    return <span data-testid="popularity-meter" className={cn('text-[11.5px] tabular-nums text-tx-faint', className)}>—</span>;
  }
  return (
    <span
      data-testid="popularity-meter"
      className={cn('text-[11.5px] tabular-nums text-tx-secondary', className)}
    >
      {value.toFixed(2)}
    </span>
  );
}
