import { useTranslation } from 'react-i18next';
import { cn } from '@/lib/utils';
import { KIND_DOT, outcomeKind } from '@/lib/badge-variants';
import { OUTCOMES, type Outcome } from '@/lib/outcomes';

export function OutcomeChips({
  selected,
  onToggle,
}: {
  selected: ReadonlySet<string>;
  onToggle: (o: Outcome) => void;
}) {
  const { t } = useTranslation();
  return (
    <div className="flex flex-wrap items-center gap-1.5">
      <span className="text-[11px] uppercase tracking-[0.06em] text-faint mr-1 self-center">
        {t('decisions.columns.outcome')}
      </span>
      {OUTCOMES.map((o) => {
        const on = selected.has(o);
        return (
          <button
            key={o}
            type="button"
            aria-pressed={on}
            onClick={() => onToggle(o)}
            className={cn(
              'inline-flex items-center gap-1.5 px-2.5 h-7 rounded-full border font-mono text-[11px] transition',
              on
                ? 'border-accent/40 bg-accent/10 text-foreground'
                : 'border-border-faint text-foreground-2 hover:bg-surface-2',
            )}
          >
            <span
              className={cn('inline-block w-1.5 h-1.5 rounded-full', KIND_DOT[outcomeKind(o)])}
            />
            {t(`outcomes.${o}`, { defaultValue: o })}
          </button>
        );
      })}
    </div>
  );
}
