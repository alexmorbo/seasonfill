import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group';
import { cn } from '@/lib/utils';

export interface SegmentedFieldOption {
  readonly value: string;
  readonly label: string;
}

export interface SegmentedFieldProps {
  readonly id?: string;
  readonly value: string;
  readonly onChange: (next: string) => void;
  readonly options: readonly SegmentedFieldOption[];
  readonly ariaLabel?: string;
  readonly className?: string;
  readonly maxWidth?: number; // px; matches `.sseg` 280px cap for cooldown
}

export function SegmentedField({
  id, value, onChange, options, ariaLabel, className, maxWidth,
}: SegmentedFieldProps) {
  return (
    <ToggleGroup
      id={id}
      type="single"
      value={value}
      // Radix can emit '' on deselect — never let that land downstream.
      onValueChange={(v) => { if (v) onChange(v); }}
      aria-label={ariaLabel}
      data-testid="segmented-field"
      style={maxWidth ? { maxWidth } : undefined}
      className={cn(
        'inline-flex w-full bg-base border border-border-subtle rounded-md p-[3px] gap-[2px]',
        className,
      )}
    >
      {options.map((opt) => (
        <ToggleGroupItem
          key={opt.value}
          value={opt.value}
          aria-label={opt.label}
          data-value={opt.value}
          className={cn(
            'flex-1 px-2.5 py-1.5 rounded-[var(--r-sm)] text-[12.5px] font-[550]',
            'text-tx-muted bg-transparent',
            'data-[state=on]:bg-accent data-[state=on]:text-accent-text',
            'whitespace-nowrap',
          )}
        >
          {opt.label}
        </ToggleGroupItem>
      ))}
    </ToggleGroup>
  );
}
