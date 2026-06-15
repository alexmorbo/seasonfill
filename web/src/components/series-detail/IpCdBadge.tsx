import type { ReactNode } from 'react';
import { cn } from '@/lib/utils';

export interface IpCdBadgeProps {
  /** Counter mode — renders `digit` over `unit` (uppercase). */
  readonly digit?: string | number;
  readonly unit?: string;
  /** Muted mode — renders an icon (Flag / Hammer) instead of the digit. */
  readonly icon?: ReactNode;
  readonly className?: string | undefined;
  readonly testId?: string;
}

/**
 * Counter / status badge for the v2 NextEpisodeCard. 50×50 square, 6px
 * radius. Two visual modes:
 *   - counter: digit + unit (uppercase) over accent background.
 *   - muted: icon over neutral surface.
 */
export function IpCdBadge({ digit, unit, icon, className, testId = 'ip-cd-badge' }: IpCdBadgeProps) {
  const muted = icon !== undefined;
  return (
    <div
      data-testid={testId}
      data-variant={muted ? 'muted' : 'counter'}
      className={cn(
        'shrink-0 w-[50px] h-[50px] rounded-md flex flex-col items-center justify-center leading-none',
        muted
          ? 'bg-bg-surface-2 text-tx-muted border border-border-subtle'
          : 'bg-accent text-accent-text',
        className,
      )}
    >
      {muted ? (
        icon
      ) : (
        <>
          <span className="font-mono text-[21px] font-bold tabular-nums">
            {digit}
          </span>
          {unit && (
            <span className="text-[9px] font-semibold uppercase tracking-[0.04em] mt-[2px]">
              {unit}
            </span>
          )}
        </>
      )}
    </div>
  );
}
