import { useTranslation } from 'react-i18next';
import { ChevronRight } from 'lucide-react';
import { CategoryChip } from '@/components/CategoryChip';
import { cn } from '@/lib/utils';
import type { Decision } from '@/lib/api/decisions';

export interface DecisionsSeasonRowProps {
  readonly decision: Decision;
  readonly decisionCount: number;
  readonly onOpen: (d: Decision) => void;
}

export function DecisionsSeasonRow({
  decision, decisionCount, onOpen,
}: DecisionsSeasonRowProps) {
  const { t } = useTranslation();
  const season = decision.season_number ?? -1;
  const isGrab = decision.decision === 'grab';
  return (
    <button
      type="button"
      onClick={() => onOpen(decision)}
      className={cn(
        'grid grid-cols-[46px_170px_88px_1fr_auto] gap-3 items-center w-full',
        'px-3 py-2 text-left rounded-md hover:bg-surface-2',
        'focus:outline-hidden focus-visible:ring-2 focus-visible:ring-ring',
      )}
      data-testid="decisions-season-row"
      aria-label={t('decisions.season.openAria', {
        season: `S${String(season).padStart(2, '0')}`,
      })}
    >
      <span className="font-mono text-[12.5px] text-tx-secondary">
        S{String(season).padStart(2, '0')}
      </span>
      <span><CategoryChip value={decision.category} variant="compact" /></span>
      <span>
        <span
          className={cn(
            'inline-flex items-center px-1.5 h-[18px] rounded-full border font-mono text-[10.5px]',
            isGrab
              ? 'border-accent text-accent'
              : 'border-border-faint text-tx-muted',
          )}
        >
          {t(isGrab ? 'decisions.row.chip.grab' : 'decisions.row.chip.skip')}
        </span>
      </span>
      <span className="text-[12.5px] text-tx-muted truncate">
        {decision.reason
          ? t(`reasons.${decision.reason}`, { defaultValue: decision.reason })
          : '—'}
      </span>
      <span className="font-mono text-[11px] text-tx-faint inline-flex items-center gap-1.5">
        <span className="text-tx-muted">
          {t('decisions.row.decisionCount', { count: decisionCount })}
        </span>
        <ChevronRight className="size-3.5" />
      </span>
    </button>
  );
}
