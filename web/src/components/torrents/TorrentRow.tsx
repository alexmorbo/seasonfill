import { Check } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { cn } from '@/lib/utils';
import { relativeTime } from '@/lib/format';
import { useFormatDate } from '@/lib/timezone';
import { TorrentStateChip } from './TorrentStateChip';
import { SpeedCell } from './SpeedCell';
import { ETAChip } from './ETAChip';
import { RatioPill } from './RatioPill';
import { PopularityMeter } from './PopularityMeter';
import type { TorrentRow as TorrentRowDTO } from '@/api/seriesTorrents';

export interface TorrentRowProps {
  readonly row: TorrentRowDTO;
  readonly className?: string | undefined;
}

function fmtBytes(n: number | undefined): string {
  if (!n || n <= 0) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  let v = n;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) { v /= 1024; i += 1; }
  const prec = v >= 100 || i === 0 ? 0 : 1;
  return `${v.toFixed(prec)} ${units[i]}`;
}

function ageDays(iso: string): number {
  const ts = new Date(iso).getTime();
  if (Number.isNaN(ts)) return NaN;
  return (Date.now() - ts) / 86_400_000;
}

function seasonLabel(n: number | null | undefined): string | undefined {
  if (n == null || n <= 0) return undefined;
  return `S${String(n).padStart(2, '0')}`;
}

type FmtFn = ReturnType<typeof useFormatDate>;

function fmtAdded(iso: string | undefined, fmt: FmtFn): string {
  if (!iso) return '—';
  const age = ageDays(iso);
  if (Number.isNaN(age)) return '—';
  if (age < 7) return relativeTime(iso);
  return fmt(iso, 'mediumDate');
}

export function TorrentRow({ row, className }: TorrentRowProps) {
  const { t } = useTranslation();
  const fmt = useFormatDate();
  const deleted = row.present === false;
  // Mute live cells when the row is either DB-only (live=false) or
  // explicitly deleted. The backend already zeroes live cells for
  // both cases (handler `mapTorrentRow`), but the boolean keeps the
  // chip/UI semantics tidy.
  const liveMuted = row.live === false || deleted;
  const tracker = row.tracker_host ?? undefined;
  const season = seasonLabel(row.season_number);

  const pct = Math.min(100, Math.max(0, Math.round((row.progress ?? 0) * 100)));

  return (
    <div
      data-testid="torrent-row"
      data-deleted={deleted ? 'true' : 'false'}
      data-live={row.live ? 'true' : 'false'}
      className={cn(
        'grid items-center gap-3 px-3 py-2 rounded-md border border-border-faint/40',
        'grid-cols-[minmax(0,1fr)_auto_auto_140px_auto_auto_auto_auto_auto]',
        '@max-[1280px]:grid-cols-[minmax(0,1fr)_auto_auto_140px_auto_auto_auto]',
        '@max-[1024px]:grid-cols-[minmax(0,1fr)_auto_auto_120px_auto]',
        deleted && 'opacity-50',
        className,
      )}
    >
      {/* Name + secondary line (tracker · season) */}
      <div className="min-w-0">
        <div className="text-[13px] text-tx-primary truncate" title={row.name ?? ''}>
          {row.name ?? '—'}
        </div>
        {(tracker || season) && (
          <div className="text-[11px] text-tx-muted truncate">
            {[season, tracker].filter(Boolean).join(' · ')}
          </div>
        )}
      </div>

      {/* Added On */}
      <div className="text-[11.5px] tabular-nums text-tx-muted whitespace-nowrap">
        {fmtAdded(row.added_on, fmt)}
      </div>

      {/* Size */}
      <div className="text-[11.5px] tabular-nums text-tx-secondary whitespace-nowrap">
        {fmtBytes(row.size_bytes)}
      </div>

      {/* Progress bar (drops at 100%) */}
      <div className="flex items-center gap-2">
        {pct >= 100 ? (
          <Check className="w-3.5 h-3.5 text-ok" aria-hidden="true" />
        ) : (
          <div
            role="progressbar"
            aria-valuenow={pct}
            aria-valuemin={0}
            aria-valuemax={100}
            className="relative flex-1 h-1 rounded-full bg-bg-surface-2 overflow-hidden"
          >
            <div className="absolute inset-y-0 left-0 bg-accent rounded-full" style={{ width: `${pct}%` }} />
          </div>
        )}
        <span className="text-[11.5px] tabular-nums text-tx-secondary w-9 text-right">{pct}%</span>
      </div>

      {/* Status chip */}
      <div>
        {deleted ? (
          <TorrentStateChip group="unknown" deleted deletedAt={row.last_activity ?? undefined} />
        ) : (
          <TorrentStateChip group={row.state_group} rawState={row.state_raw ?? undefined} />
        )}
      </div>

      {/* Seeds / peers (drops on narrow) */}
      <div className="text-[11.5px] tabular-nums text-tx-secondary whitespace-nowrap @max-[1024px]:hidden">
        <span className="text-tx-faint text-[10px] uppercase mr-1">S</span>
        <span data-testid="row-seeds">{liveMuted ? '—' : (row.num_seeds ?? 0)}</span>
        <span className="text-tx-faint mx-2">/</span>
        <span className="text-tx-faint text-[10px] uppercase mr-1">P</span>
        <span data-testid="row-peers">{liveMuted ? '—' : (row.num_leechs ?? 0)}</span>
      </div>

      {/* Speeds (drops on narrow) */}
      <div className="@max-[1024px]:hidden">
        <SpeedCell down={row.dl_speed_bps} up={row.up_speed_bps} muted={liveMuted} />
      </div>

      {/* ETA (drops on narrow) */}
      <div className="@max-[1024px]:hidden">
        <ETAChip seconds={row.eta_seconds} muted={liveMuted} />
      </div>

      {/* Ratio + popularity (popularity first to drop) */}
      <div className="flex items-center gap-3 @max-[1280px]:hidden">
        <RatioPill value={row.ratio} muted={liveMuted} />
        <PopularityMeter value={row.popularity} />
      </div>

      <span className="sr-only" data-testid="row-name">{row.name ?? ''}</span>
      <span className="sr-only" data-testid="row-meta">{t('seriesDetail.torrents.row.meta', { added: row.added_on ?? '', size: row.size_bytes ?? 0 })}</span>
    </div>
  );
}
