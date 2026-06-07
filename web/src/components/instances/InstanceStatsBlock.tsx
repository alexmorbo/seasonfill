import { useTranslation } from 'react-i18next';
import { cn } from '@/lib/utils';

export interface InstanceStatsBlockProps {
  readonly grabs: number;
  readonly imports: number;
  readonly fails: number;
  readonly windowLabelKey: 'instances.hero.stats.24h.label' | 'instances.hero.stats.7d.label';
  readonly separator?: boolean;
}

/**
 * Tri-numeric counter block (граб / имп / фейл). Used in the hero card
 * twice (24h + 7d). Mono numerals, uppercase label, fails colored red
 * when > 0 and green when == 0 (matches the design pack).
 */
export function InstanceStatsBlock({
  grabs, imports, fails, windowLabelKey, separator,
}: InstanceStatsBlockProps) {
  const { t } = useTranslation();
  return (
    <div
      data-testid="instance-stats-block"
      className={cn(
        'flex flex-col gap-1',
        separator && 'pl-[30px] border-l border-border-faint',
      )}
    >
      <span className="font-mono tabular-nums slashed-zero text-[23px] font-bold tracking-tight leading-none">
        <span data-testid="stats-grabs">{grabs}</span>
        <small className="text-[15px] text-tx-muted font-semibold"> / {imports} / </small>
        <span
          data-testid="stats-fails"
          className={cn(
            'text-[15px] font-semibold',
            fails > 0 ? 'text-status-danger' : 'text-status-ok',
          )}
        >
          {fails}
        </span>
      </span>
      <span className="text-[10px] font-semibold tracking-[0.08em] uppercase text-tx-faint">
        {t(windowLabelKey)}
      </span>
    </div>
  );
}
