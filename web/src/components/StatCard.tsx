import { type ReactNode } from 'react';
import { cn } from '@/lib/utils';

type Variant = 'default' | 'success' | 'warning' | 'danger';

const ACCENT: Record<Variant, string> = {
  default: 'border-border',
  success: 'border-status-success/40',
  warning: 'border-status-warning/40',
  danger: 'border-status-danger/40',
};

export function StatCard({
  label,
  value,
  suffix,
  foot,
  variant = 'default',
}: {
  label: string;
  value: ReactNode;
  suffix?: ReactNode | undefined;
  foot?: ReactNode | undefined;
  variant?: Variant;
}) {
  return (
    <div className={cn('rounded-lg bg-surface border p-4 flex flex-col gap-1.5', ACCENT[variant])}>
      <div className="text-[11px] uppercase tracking-[0.06em] text-faint">{label}</div>
      <div className="text-[28px] font-semibold font-mono leading-none">
        {value}
        {suffix !== undefined && (
          <span className="ml-1.5 text-[13px] text-faint font-normal">{suffix}</span>
        )}
      </div>
      {foot !== undefined && <div className="text-[12px] text-muted">{foot}</div>}
    </div>
  );
}
