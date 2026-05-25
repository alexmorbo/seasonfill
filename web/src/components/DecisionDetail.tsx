import type { ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { cn } from '@/lib/utils';
import { CategoryChip } from '@/components/CategoryChip';
import type { Decision } from '@/lib/decisions';

function Row({
  k,
  v,
  mono = false,
  accent,
}: {
  k: string;
  v: ReactNode;
  mono?: boolean;
  accent?: 'pos' | 'neg' | 'muted';
}) {
  return (
    <div className="grid grid-cols-[160px_1fr] gap-x-3 py-1.5 border-b border-border-faint last:border-b-0">
      <span className="text-[12px] text-faint">{k}</span>
      <span
        className={cn(
          'text-[12.5px]',
          mono && 'font-mono',
          accent === 'pos' && 'text-status-success',
          accent === 'neg' && 'text-status-danger',
          accent === 'muted' && 'text-muted',
        )}
      >
        {v}
      </span>
    </div>
  );
}

export function DecisionDetail({ d }: { d: Decision }) {
  const { t } = useTranslation();
  const missing = d.missing_count ?? 0;
  const reasonText = d.reason
    ? t(`reasons.${d.reason}`, { defaultValue: d.reason })
    : '—';
  return (
    <div className="px-1 py-2">
      <div className="flex items-center justify-between mb-1.5 gap-2">
        <h4 className="text-[11px] uppercase tracking-[0.06em] text-foreground-2">
          {t('decisions.detail.tree')}
        </h4>
        <CategoryChip value={d.category} variant="compact" />
      </div>
      <Row k={t('decisions.detail.reason')} v={reasonText} accent="muted" />
      <Row k={t('decisions.detail.candidates')} v={d.candidates_count ?? 0} mono />
      <Row k={t('decisions.detail.releasesFound')} v={d.releases_found ?? 0} mono />
      <Row k={t('decisions.detail.existingFiles')} v={d.existing_count ?? 0} mono />
      <Row
        k={t('decisions.detail.missing')}
        v={missing}
        mono
        {...(missing > 0 ? { accent: 'neg' as const } : {})}
      />
      {d.selected_guid && (
        <Row k={t('decisions.detail.selectedGuid')} v={<span className="break-all">{d.selected_guid}</span>} mono />
      )}
      {d.dry_run_would_grab !== undefined && (
        <Row
          k={t('decisions.detail.dryRunWouldGrab')}
          v={d.dry_run_would_grab ? t('common.yes').toLowerCase() : t('common.no').toLowerCase()}
          mono
          accent={d.dry_run_would_grab ? 'pos' : 'muted'}
        />
      )}
      {d.created_at && <Row k={t('decisions.detail.created')} v={d.created_at} mono accent="muted" />}
    </div>
  );
}
