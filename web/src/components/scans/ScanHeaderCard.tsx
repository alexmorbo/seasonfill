import { useTranslation } from 'react-i18next';
import { Copy } from 'lucide-react';
import { toast } from 'sonner';
import { Card } from '@/components/ui/card';
import { StatusBadge } from '@/components/StatusBadge';
import { ScanProgressBar } from '@/components/ScanProgressBar';
import { CancelScanDialog } from '@/components/CancelScanDialog';
import { relativeTime, durationMs } from '@/lib/format';
import type { Scan } from '@/lib/scans';

function Chip({ children, accent = false }: { children: React.ReactNode; accent?: boolean }) {
  return (
    <span
      className={
        'inline-flex items-center gap-1.5 px-2.5 h-[22px] rounded-full font-mono text-[11.5px] ' +
        (accent
          ? 'bg-accent-dim text-accent font-semibold'
          : 'bg-surface-2 text-foreground-2')
      }
      data-testid={accent ? 'scan-chip-accent' : 'scan-chip'}
    >
      {children}
    </span>
  );
}

export function ScanHeaderCard({ scan }: { scan: Scan }) {
  const { t } = useTranslation();
  const idShort = (scan.id ?? '').slice(0, 8);
  const grabsOk = (scan.grabs_performed ?? 0) - (scan.grabs_failed ?? 0);
  const grabsFailed = scan.grabs_failed ?? 0;
  const copy = () => {
    if (scan.id) {
      navigator.clipboard?.writeText(scan.id);
      toast.success(t('scanDetail.copyScanIdToast'));
    }
  };
  return (
    <Card className="p-4 flex flex-col gap-4" data-testid="scan-header-card">
      <div className="flex items-center gap-3 flex-wrap">
        <h3 className="text-[15px] font-semibold font-mono m-0">{idShort}…</h3>
        <button
          onClick={copy}
          aria-label={t('scanDetail.copyScanId')}
          className="text-muted hover:text-foreground p-1 rounded -ml-1"
          data-testid="scan-header-copy"
        >
          <Copy className="w-3.5 h-3.5" />
        </button>
        <StatusBadge value={scan.status} />
        {scan.status === 'running' && scan.id && (
          <>
            <span className="font-mono text-[11px] text-faint" data-testid="poll-indicator">
              {t('scanDetail.pollIndicator')}
            </span>
            <CancelScanDialog scanId={scan.id} />
          </>
        )}
        <div className="flex-1" />
        <span className="font-mono text-[12px] text-faint">
          {scan.trigger ?? '—'} · {scan.instance ?? '—'} · {relativeTime(scan.started_at)}
          {scan.finished_at && ` → ${relativeTime(scan.finished_at)}`}
        </span>
      </div>

      {scan.status === 'running' && (
        <ScanProgressBar
          status={scan.status}
          seriesScanned={scan.series_scanned ?? 0}
          {...(scan.started_at && { startedAt: scan.started_at })}
          {...(scan.finished_at && { finishedAt: scan.finished_at })}
        />
      )}

      <div className="flex gap-2 flex-wrap">
        <Chip>{t('scanDetail.chip.dryRun', { state: scan.dry_run ? t('common.on') : t('common.off') })}</Chip>
        <Chip>{t('scanDetail.chip.seriesScanned', { count: scan.series_scanned ?? 0 })}</Chip>
        <Chip>{t('scanDetail.chip.candidates', { count: scan.candidates_found ?? 0 })}</Chip>
        <Chip accent>
          {t('scanDetail.chip.grabs', { count: grabsOk })}
          {grabsFailed > 0 && ` · ${t('scanDetail.chip.grabsFailed', { count: grabsFailed })}`}
        </Chip>
        <Chip>{t('scanDetail.chip.errors', { count: scan.errors_count ?? 0 })}</Chip>
        <Chip>{durationMs(scan.started_at, scan.finished_at)}</Chip>
      </div>
    </Card>
  );
}
