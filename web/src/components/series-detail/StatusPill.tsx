import { useTranslation } from 'react-i18next';
import { cn } from '@/lib/utils';
import { type StatusToken } from '@/api/seriesDetail';

export interface StatusPillProps {
  readonly status: StatusToken;
  readonly className?: string | undefined;
}

const STYLES: Record<StatusToken, { bg: string; dot: string; text: string }> = {
  continuing:    { bg: 'bg-ok-dim',      dot: 'bg-ok',      text: 'text-ok' },
  ended:         { bg: 'bg-neutral/10',  dot: 'bg-neutral', text: 'text-tx-secondary' },
  canceled:      { bg: 'bg-danger-dim',  dot: 'bg-danger',  text: 'text-danger' },
  in_production: { bg: 'bg-info-dim',    dot: 'bg-info',    text: 'text-info' },
  upcoming:      { bg: 'bg-warn-dim',    dot: 'bg-warn',    text: 'text-warn' },
  unknown:       { bg: 'bg-bg-surface-2', dot: 'bg-tx-faint', text: 'text-tx-muted' },
};

export function StatusPill({ status, className }: StatusPillProps) {
  const { t } = useTranslation();
  const s = STYLES[status];
  return (
    <span
      data-testid="status-pill"
      data-status={status}
      className={cn(
        'inline-flex items-center gap-1.5 rounded-full px-2.5 py-0.5 text-[11.5px] font-semibold',
        s.bg, s.text, className,
      )}
    >
      <span aria-hidden="true" className={cn('inline-block w-1.5 h-1.5 rounded-full', s.dot)} />
      {t(`seriesDetail.status.${status}`)}
    </span>
  );
}
