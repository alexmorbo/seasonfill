import { cn } from '@/lib/utils';
import { CHIP_CLASS, type Chip } from '@/lib/grabs/chipBuilder';

export interface ChipsRowProps {
  readonly chips: readonly Chip[];
  readonly className?: string | undefined;
}

export function ChipsRow({ chips, className }: ChipsRowProps) {
  return (
    <div className={cn('flex flex-wrap items-center gap-1.5', className)}>
      {chips.map((c) => (
        <span
          key={c.id}
          title={c.title}
          className={cn(
            'inline-flex items-center gap-1 rounded-[5px] border',
            'px-1.5 py-px font-mono text-[10.5px] font-semibold whitespace-nowrap',
            CHIP_CLASS[c.variant],
          )}
        >
          {c.label}
        </span>
      ))}
    </div>
  );
}
