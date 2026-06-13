import { useTranslation } from 'react-i18next';
import { cn } from '@/lib/utils';

export interface ETAChipProps {
  readonly seconds?: number | undefined;
  // muted → row not live; show "—".
  readonly muted?: boolean | undefined;
  readonly className?: string | undefined;
}

// qBit's sentinel for "no ETA / infinite". A value of 8640000 (100
// days in seconds) is what their API returns for stalled.
const QBIT_INFINITY = 8640000;

export function ETAChip({ seconds, muted, className }: ETAChipProps) {
  const { t } = useTranslation();
  let display: string;
  if (muted || seconds == null) {
    display = '—';
  } else if (seconds >= QBIT_INFINITY || seconds < 0) {
    display = '∞';
  } else if (seconds === 0) {
    display = '—';
  } else if (seconds < 60) {
    display = t('seriesDetail.torrents.eta.now');
  } else if (seconds < 3600) {
    display = `${Math.floor(seconds / 60)}m`;
  } else if (seconds < 86400) {
    const h = Math.floor(seconds / 3600);
    const m = Math.floor((seconds % 3600) / 60);
    display = m === 0 ? `${h}h` : `${h}h ${m}m`;
  } else {
    const d = Math.floor(seconds / 86400);
    const h = Math.floor((seconds % 86400) / 3600);
    display = h === 0 ? `${d}d` : `${d}d ${h}h`;
  }
  return (
    <span
      data-testid="eta-chip"
      className={cn('text-[11.5px] tabular-nums text-tx-secondary', className)}
    >
      {display}
    </span>
  );
}
