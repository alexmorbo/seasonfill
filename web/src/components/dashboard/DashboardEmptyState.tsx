import { useTranslation } from 'react-i18next';
import { Moon, Play, List } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { relativeTime } from '@/lib/format';
import type { SeriesCacheItem } from '@/lib/api/seriesCache';

export interface DashboardEmptyStateProps {
  readonly missingCount: number | null;
  readonly lastImport: SeriesCacheItem | null;
  readonly onScanNow: () => void;
  readonly onOpenQueue: () => void;
  readonly scanPending: boolean;
}

export function DashboardEmptyState({
  missingCount, lastImport, onScanNow, onOpenQueue, scanPending,
}: DashboardEmptyStateProps) {
  const { t } = useTranslation();
  return (
    <div
      data-testid="dashboard-empty-state"
      className="flex flex-col items-center justify-center text-center gap-3 px-6 py-15 min-h-[380px] rounded-lg border border-dashed border-border-subtle bg-bg-surface"
    >
      <div className="w-[54px] h-[54px] rounded-[15px] flex items-center justify-center bg-bg-surface-2 border border-border-faint text-accent mb-0.5">
        <Moon className="w-6 h-6" aria-hidden="true" />
      </div>
      <h2 className="text-[19px] font-semibold tracking-tight text-tx-primary">
        {t('dashboard.empty.title')}
      </h2>
      <p className="text-[13.5px] leading-relaxed text-tx-muted max-w-[430px]">
        {t('dashboard.empty.body')}
      </p>
      <div className="flex flex-wrap gap-2.5 mt-1.5 justify-center">
        <Button variant="default" onClick={onScanNow} disabled={scanPending} data-testid="empty-cta-scan">
          <Play className="w-4 h-4" aria-hidden="true" />
          {t('dashboard.empty.cta.scan')}
        </Button>
        {missingCount !== null && (
          <Button variant="outline" onClick={onOpenQueue} data-testid="empty-cta-queue">
            <List className="w-4 h-4" aria-hidden="true" />
            {t('dashboard.empty.cta.queue', { count: missingCount })}
          </Button>
        )}
      </div>
      {lastImport && (
        <div className="mt-3.5 flex items-center gap-2.5 rounded-md border border-border-faint bg-bg-base px-3 py-2 text-[12.5px] text-tx-secondary">
          <span className="w-6 h-9 rounded border border-border-subtle bg-[radial-gradient(120%_80%_at_30%_0%,oklch(0.34_0.07_205),oklch(0.19_0.04_235))]" />
          <span>
            {t('dashboard.empty.lastImport', {
              title: lastImport.title,
              season: lastImport.last_imported_episode ?? '—',
            })}
          </span>
          <span className="text-tx-faint font-mono text-[11.5px]">
            {relativeTime(lastImport.last_grab_at ?? lastImport.updated_at)}
          </span>
        </div>
      )}
    </div>
  );
}
