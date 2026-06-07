import { cn } from '@/lib/utils';

export interface QueueAvailBarProps {
  readonly have: number;
  readonly miss: number;
}

// Render a two-color proportional bar. `have` + `miss` may be less
// than total (upcoming episodes are neither), so we normalize over
// (have + miss) to fill the bar visually. When both are 0 we render
// an empty bar (no proportions to draw).
export function QueueAvailBar({ have, miss }: QueueAvailBarProps) {
  const sum = have + miss;
  const havePct = sum === 0 ? 0 : Math.round((have / sum) * 100);
  const missPct = sum === 0 ? 0 : 100 - havePct;
  return (
    <div
      className="flex h-2.5 rounded-md overflow-hidden bg-surface-2"
      role="progressbar"
      aria-valuenow={havePct}
      aria-valuemin={0}
      aria-valuemax={100}
      aria-label={`have ${have}, missing ${miss}`}
      data-testid="queue-avail-bar"
    >
      {havePct > 0 && (
        <span
          className={cn('h-full bg-accent')}
          style={{ width: `${havePct}%` }}
          data-testid="queue-avail-have"
        />
      )}
      {missPct > 0 && (
        <span
          className={cn('h-full bg-warn/50')}
          style={{ width: `${missPct}%` }}
          data-testid="queue-avail-miss"
        />
      )}
    </div>
  );
}
