import { useTranslation } from 'react-i18next';
import { cn } from '@/lib/utils';
import { relativeTime } from '@/lib/format';
import { TorrentStateChip } from './TorrentStateChip';
import { SpeedCell } from './SpeedCell';
import { ETAChip } from './ETAChip';
import { RatioPill } from './RatioPill';
import type { TorrentRow as TorrentRowDTO } from '@/api/seriesTorrents';

export interface TorrentCardProps {
  readonly row: TorrentRowDTO;
  readonly className?: string | undefined;
}

function fmtBytes(n: number | undefined): string {
  if (!n || n <= 0) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  let v = n;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) { v /= 1024; i += 1; }
  return `${v.toFixed(v >= 100 || i === 0 ? 0 : 1)} ${units[i]}`;
}

export function TorrentCard({ row, className }: TorrentCardProps) {
  const { t } = useTranslation();
  const deleted = row.present === false;
  const liveMuted = row.live === false || deleted;
  const pct = Math.min(100, Math.max(0, Math.round((row.progress ?? 0) * 100)));
  return (
    <div
      data-testid="torrent-card"
      data-deleted={deleted ? 'true' : 'false'}
      className={cn(
        'flex flex-col gap-2 rounded-lg border border-border-faint bg-bg-surface/60 px-3 py-3',
        deleted && 'opacity-50',
        className,
      )}
    >
      <div className="flex items-start justify-between gap-2 min-w-0">
        <div className="min-w-0 flex-1">
          <div className="text-[13px] text-tx-primary truncate" title={row.name ?? ''}>
            {row.name ?? '—'}
          </div>
          <div className="text-[11px] text-tx-muted truncate">
            {row.added_on ? relativeTime(row.added_on) : '—'}
            {row.tracker_host ? ` · ${row.tracker_host}` : ''}
          </div>
        </div>
        {deleted
          ? <TorrentStateChip group="unknown" deleted deletedAt={row.last_activity ?? undefined} />
          : <TorrentStateChip group={row.state_group} rawState={row.state_raw ?? undefined} />}
      </div>

      <div className="grid grid-cols-3 gap-x-3 gap-y-2 pt-1 border-t border-border-faint/60">
        <Stat label={t('seriesDetail.torrents.col.size')}     value={fmtBytes(row.size_bytes)} />
        <Stat label={t('seriesDetail.torrents.col.progress')} value={`${pct}%`} />
        <Stat label={t('seriesDetail.torrents.col.ratio')}    valueNode={<RatioPill value={row.ratio} muted={liveMuted} />} />
        <Stat label={t('seriesDetail.torrents.col.speed')}    valueNode={<SpeedCell down={row.dl_speed_bps} up={row.up_speed_bps} muted={liveMuted} />} />
        <Stat label={t('seriesDetail.torrents.col.eta')}      valueNode={<ETAChip seconds={row.eta_seconds} muted={liveMuted} />} />
        <Stat label={t('seriesDetail.torrents.col.state')}    value={row.state_group ?? '—'} />
      </div>
    </div>
  );
}

function Stat({ label, value, valueNode }: { label: string; value?: string; valueNode?: React.ReactNode }) {
  return (
    <div className="flex flex-col gap-0.5 min-w-0">
      <div className="text-[10px] font-bold uppercase tracking-wide text-tx-faint">{label}</div>
      <div className="text-[12px] text-tx-secondary tabular-nums truncate">
        {valueNode ?? value ?? '—'}
      </div>
    </div>
  );
}
