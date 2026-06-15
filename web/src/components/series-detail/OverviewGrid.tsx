import type { ReactNode } from 'react';
import { cn } from '@/lib/utils';

export interface OverviewGridProps {
  readonly left: ReactNode;
  readonly right: ReactNode;
  readonly className?: string | undefined;
}

/**
 * V2 Overview layout — main content + right rail. 340px fixed rail on
 * desktop, single column on mobile. The right rail is responsible for
 * its own sticky positioning (RailCard sets `top:64px`).
 */
export function OverviewGrid({ left, right, className }: OverviewGridProps) {
  return (
    <div
      data-testid="overview-grid"
      className={cn(
        'grid items-start gap-[26px]',
        'grid-cols-1 lg:grid-cols-[minmax(0,1fr)_340px]',
        className,
      )}
    >
      <div className="flex flex-col gap-[22px] min-w-0">{left}</div>
      <aside className="flex flex-col gap-3 min-w-0">{right}</aside>
    </div>
  );
}
