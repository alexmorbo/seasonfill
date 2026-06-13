import { Trophy } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { cn } from '@/lib/utils';
import { StaleBadge } from './StaleBadge';

export interface AwardsBlockProps {
  readonly awards: string | undefined;
  readonly omdbDegraded?: boolean | undefined;
  readonly syncedAt?: string | undefined;
  readonly className?: string | undefined;
}

// Returns true when the OMDb `awards` field is effectively absent —
// either nil, empty string, or the literal "N/A" OMDb writes when it
// has no awards data for the title. We strip + uppercase so "n/a",
// " N/A ", etc. all collapse to the same nil semantics.
function isAwardsEmpty(awards: string | undefined): boolean {
  if (!awards) return true;
  const trimmed = awards.trim();
  if (trimmed.length === 0) return true;
  return trimmed.toUpperCase() === 'N/A';
}

export function AwardsBlock({
  awards,
  omdbDegraded,
  syncedAt,
  className,
}: AwardsBlockProps) {
  const { t } = useTranslation();

  // §2.9 brief — hide when empty/N/A. §2.10 brief — hide when degraded:
  // we already render the StaleBadge on the IMDb rating row, and the
  // 223 invariant is "don't leak stale awards text" — so awards section
  // itself is suppressed under degraded OMDb. Matches the 223 wire-up.
  if (isAwardsEmpty(awards) || omdbDegraded) return null;

  return (
    <div
      data-testid="awards-block"
      className={cn(
        'flex flex-col gap-2 rounded-lg border border-border-faint bg-bg-surface/60 px-4 py-3',
        className,
      )}
    >
      <div className="flex items-center gap-2 text-[10.5px] font-bold uppercase tracking-wide text-tx-faint">
        <Trophy className="w-3.5 h-3.5 text-warn" aria-hidden="true" />
        {t('seriesDetail.awards.label')}
        {omdbDegraded && syncedAt && (
          <StaleBadge asOf={syncedAt} source="omdb" />
        )}
      </div>
      <p
        data-testid="awards-text"
        className="text-[12.5px] leading-relaxed text-tx-secondary"
      >
        {awards}
      </p>
    </div>
  );
}
