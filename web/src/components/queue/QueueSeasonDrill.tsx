import { useTranslation } from 'react-i18next';
import { Play, Loader2, AlertTriangle } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { QueueAvailBar } from './QueueAvailBar';
import { QueueEpisodeChips } from './QueueEpisodeChips';
import { useSeasonEpisodes } from '@/lib/api/queueSeasonEpisodes';

export interface QueueSeasonDrillProps {
  readonly seriesId: number;
  readonly seasonNumber: number;
  readonly isScanInFlight: boolean;
  readonly onScanSeason: () => void;
}

export function QueueSeasonDrill({
  seriesId, seasonNumber,
  isScanInFlight, onScanSeason,
}: QueueSeasonDrillProps) {
  const { t } = useTranslation();
  const episodes = useSeasonEpisodes(seriesId, seasonNumber);

  if (episodes.isPending) {
    return (
      <div
        className="flex items-center gap-2 text-[12px] text-muted"
        data-testid="queue-drill-loading"
      >
        <Loader2 className="w-3.5 h-3.5 animate-spin" aria-hidden="true" />
        {t('instanceQueue.drill.loading')}
      </div>
    );
  }

  if (episodes.isError || !episodes.data) {
    return (
      <Alert variant="destructive" data-testid="queue-drill-error">
        <AlertTriangle className="w-4 h-4" />
        <AlertDescription>
          {t('instanceQueue.drill.error')}
        </AlertDescription>
      </Alert>
    );
  }

  const { items, total, miss } = episodes.data;

  return (
    <div className="flex flex-col gap-3" data-testid="queue-drill">
      <div className="flex items-center gap-2.5">
        <span className="text-[13px] font-semibold flex-1">
          {t('instanceQueue.drill.title', {
            num: seasonNumber,
            count: miss,
            total,
          })}
        </span>
        <Button
          size="sm"
          onClick={onScanSeason}
          disabled={isScanInFlight}
          title={t('instanceQueue.drill.scanSeasonNote')}
          data-testid="queue-drill-scan-season"
        >
          {isScanInFlight ? (
            <Loader2 className="w-3.5 h-3.5 mr-1 animate-spin" aria-hidden="true" />
          ) : (
            <Play className="w-3.5 h-3.5 mr-1" aria-hidden="true" />
          )}
          {t('instanceQueue.drill.scanSeason', { num: seasonNumber })}
        </Button>
      </div>
      <QueueAvailBar have={episodes.data.have} miss={episodes.data.miss} />
      <QueueEpisodeChips items={items} />
    </div>
  );
}
