import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router-dom';
import { Check, X, Ban, Loader2, ChevronRight } from 'lucide-react';
import { cn } from '@/lib/utils';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { relativeTime, durationMs } from '@/lib/format';
import type { Scan } from '@/lib/scans';

type StPillKind = 'ok' | 'fail' | 'abort' | 'running';

function statusKind(status: string | undefined): StPillKind {
  if (status === 'failed') return 'fail';
  if (status === 'aborted' || status === 'cancelled') return 'abort';
  if (status === 'running') return 'running';
  return 'ok';
}

const ST_PILL_CLASS: Record<StPillKind, string> = {
  ok:      'text-status-success bg-status-success-dim',
  fail:    'text-status-danger  bg-status-danger-dim',
  abort:   'text-muted          bg-surface-2',
  running: 'text-status-info    bg-status-info-dim',
};

function StPill({ status }: { status: string | undefined }) {
  const kind = statusKind(status);
  const Icon = kind === 'fail' ? X : kind === 'abort' ? Ban : kind === 'running' ? Loader2 : Check;
  return (
    <span
      data-status-kind={kind}
      className={cn(
        'inline-flex items-center gap-1.5 px-2.5 h-[18px] rounded-full font-mono text-[11.5px] font-semibold',
        ST_PILL_CLASS[kind],
      )}
    >
      <Icon className={cn('w-3 h-3', kind === 'running' && 'animate-spin')} aria-hidden="true" />
      {status ?? '—'}
    </span>
  );
}

export function ScansTable({ rows }: { rows: readonly Scan[] }) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const onKey = (e: React.KeyboardEvent, id: string | undefined) => {
    if ((e.key === 'Enter' || e.key === ' ') && id) {
      e.preventDefault();
      navigate(`/scans/${id}`);
    }
  };
  return (
    <Table data-testid="scans-table">
      <TableHeader>
        <TableRow>
          <TableHead className="w-[120px]">{t('scans.columns.status')}</TableHead>
          <TableHead className="w-[80px]">{t('scans.columns.trigger')}</TableHead>
          <TableHead className="w-[110px]">{t('scans.columns.instance')}</TableHead>
          <TableHead className="w-[120px]">{t('scans.columns.started')}</TableHead>
          <TableHead className="w-[70px]">{t('scans.columns.duration')}</TableHead>
          <TableHead className="w-[70px]">{t('scans.columns.series')}</TableHead>
          <TableHead className="w-[70px]">{t('scans.columns.candidates')}</TableHead>
          <TableHead className="w-[70px]">{t('scans.columns.grabs')}</TableHead>
          <TableHead className="w-[32px]"></TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {rows.map((s) => (
          <TableRow
            key={s.id}
            data-testid="scans-row"
            onClick={() => s.id && navigate(`/scans/${s.id}`)}
            onKeyDown={(e) => onKey(e, s.id)}
            tabIndex={0}
            role="button"
            aria-label={t('scans.openScanAria', { id: (s.id ?? '').slice(0, 8) })}
            className="cursor-pointer focus:outline-hidden focus-visible:ring-2 focus-visible:ring-ring group"
          >
            <TableCell><StPill status={s.status} /></TableCell>
            <TableCell className="font-mono text-[12.5px] text-foreground-2">{s.trigger ?? '—'}</TableCell>
            <TableCell className="font-mono text-[12.5px] text-foreground-2">{s.instance ?? '—'}</TableCell>
            <TableCell className="font-mono text-[12.5px] text-foreground-2">{relativeTime(s.started_at)}</TableCell>
            <TableCell className="font-mono text-[12.5px] text-foreground-2">{durationMs(s.started_at, s.finished_at)}</TableCell>
            <TableCell className="font-mono text-[12.5px] text-foreground-2">{s.series_scanned ?? 0}</TableCell>
            <TableCell className="font-mono text-[12.5px] text-foreground-2">{s.candidates_found ?? 0}</TableCell>
            <TableCell className="font-mono text-[12.5px] text-foreground font-semibold">{s.grabs_performed ?? 0}</TableCell>
            <TableCell className="text-right text-faint group-hover:text-accent">
              <ChevronRight className="w-4 h-4 inline-block" aria-hidden="true" />
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}
