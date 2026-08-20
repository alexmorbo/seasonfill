import { useTranslation } from 'react-i18next';
import { cn } from '@/lib/utils';
import type { MediaFact, MediaKeyword } from './view-model';

export interface MediaRailCardProps {
  readonly facts: readonly MediaFact[];
  readonly keywords?: readonly MediaKeyword[] | undefined;
  readonly className?: string | undefined;
}

interface RailRowProps {
  readonly label: string;
  readonly value: React.ReactNode;
  readonly accent?: boolean;
  readonly testId?: string;
}

function RailRow({ label, value, accent, testId }: RailRowProps) {
  return (
    <div
      data-testid={testId}
      className="flex items-center justify-between gap-3.5 py-[9px] text-[12.5px] border-b border-border-faint last:border-b-0"
    >
      <span className="text-tx-muted whitespace-nowrap">{label}</span>
      <span className={cn(
        'font-medium text-right min-w-0 inline-flex items-center gap-1.5',
        accent ? 'text-accent' : 'text-tx-secondary',
      )}>
        {value}
      </span>
    </div>
  );
}

export function MediaRailCard({ facts, keywords, className }: MediaRailCardProps) {
  const { t } = useTranslation();

  const showKeywords = (keywords?.length ?? 0) > 0;

  return (
    <div
      data-testid="rail-card"
      className={cn(
        'flex flex-col overflow-hidden rounded-lg border border-white/10 bg-bg-surface/40 backdrop-blur-md',
        'lg:sticky lg:top-[64px]',
        className,
      )}
    >
      <div className="px-4 pt-1 pb-1">
        {facts.map((f) => (
          <RailRow
            key={f.id}
            label={f.label}
            value={f.value}
            {...(f.accent ? { accent: true } : {})}
            {...(f.testId ? { testId: f.testId } : {})}
          />
        ))}
      </div>

      {showKeywords && (
        <div
          data-testid="rail-keywords"
          className="border-t border-border-faint px-4 py-3.5"
        >
          <div className="text-[10px] font-semibold uppercase tracking-[0.1em] text-tx-faint mb-2.5">
            {t('seriesDetail.overview.keywords')}
          </div>
          <div className="flex flex-wrap gap-1.5">
            {keywords!.slice(0, 12).map((k) => (
              <span
                key={k.id ?? k.name}
                className="rounded-md bg-bg-surface-2/70 border border-border-subtle px-1.5 py-0.5 text-[11px] text-tx-secondary"
              >
                {k.name}
              </span>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}
