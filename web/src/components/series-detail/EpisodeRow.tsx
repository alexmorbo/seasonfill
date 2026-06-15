import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Award } from 'lucide-react';
import { cn } from '@/lib/utils';
import { mediaUrl } from '@/api/seriesDetail';
import { relativeTime } from '@/lib/format';
import { useFormatDate } from '@/lib/timezone';
import { formatEpisodeMeta } from '@/lib/episodeMeta';
import type { components } from '@/api/schema';

type Episode = components['schemas']['dto.Episode'];

export interface EpisodeRowProps {
  readonly episode: Episode;
  readonly seasonNumber?: number | undefined;
  readonly className?: string | undefined;
}

function pad(n: number): string { return n.toString().padStart(2, '0'); }

type FmtFn = ReturnType<typeof useFormatDate>;

function airLabel(iso: string | undefined, fmt: FmtFn): string {
  if (!iso) return '';
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return '';
  const ageMs = Date.now() - d.getTime();
  const THIRTY_DAYS = 30 * 86_400_000;
  if (Math.abs(ageMs) < THIRTY_DAYS) return relativeTime(iso);
  return fmt(iso, 'mediumDate');
}

function diskDot(ep: Episode): { color: string; testId: string } {
  if (ep.has_file) return { color: 'bg-ok', testId: 'episode-dot-have' };
  // Missing + monitored + aired = red. Otherwise muted.
  const aired = ep.air_date ? new Date(ep.air_date).getTime() <= Date.now() : false;
  if (ep.monitored && aired) return { color: 'bg-danger', testId: 'episode-dot-missing' };
  return { color: 'bg-tx-faint', testId: 'episode-dot-unmonitored' };
}

export function EpisodeRow({ episode, seasonNumber, className }: EpisodeRowProps) {
  const { t } = useTranslation();
  const fmt = useFormatDate();
  const [loaded, setLoaded] = useState(false);
  const [expanded, setExpanded] = useState(false);

  const stillSrc = mediaUrl(episode.still_asset);
  const sn = seasonNumber ?? (episode as { season_number?: number }).season_number;
  const code = `S${pad(sn ?? 0)}E${pad(episode.episode_number ?? 0)}`;
  const dot = diskDot(episode);
  const finaleLabel = episode.finale_type
    ? t(`seriesDetail.seasons.finale.${episode.finale_type}` as 'seriesDetail.seasons.finale.season', { defaultValue: '' })
    : '';

  return (
    <div
      data-testid="episode-row"
      data-has-file={episode.has_file ? 'true' : 'false'}
      className={cn(
        'flex gap-3 rounded-md px-2 py-2 hover:bg-bg-surface/40 transition-colors',
        className,
      )}
    >
      {/* Still thumb (16:9 — 96×54 mobile, 128×72 desktop) */}
      <div
        className="relative w-24 h-[54px] md:w-32 md:h-[72px] rounded overflow-hidden shrink-0 border border-border-subtle bg-bg-surface-2"
        data-testid="episode-still"
      >
        {!loaded && (
          <div
            aria-hidden="true"
            className="absolute inset-0 bg-bg-surface-2 animate-pulse"
            data-testid="episode-still-shimmer"
          />
        )}
        {stillSrc && (
          <img
            src={stillSrc}
            alt=""
            aria-hidden="true"
            loading="lazy"
            decoding="async"
            onLoad={() => setLoaded(true)}
            className={cn(
              'relative z-[1] w-full h-full object-cover transition-opacity',
              loaded ? 'opacity-100' : 'opacity-0',
            )}
          />
        )}
      </div>

      <div className="flex flex-col gap-1 min-w-0 flex-1">
        <div className="flex flex-wrap items-baseline gap-x-2 gap-y-0.5 text-[12.5px]">
          <span className="font-mono font-semibold text-tx-primary tabular-nums">{code}</span>
          {episode.title && (
            <span className="text-tx-secondary truncate">{episode.title}</span>
          )}
          {episode.air_date && (
            <span className="text-tx-muted">· {airLabel(episode.air_date, fmt)}</span>
          )}
          {episode.runtime_minutes && episode.runtime_minutes > 0 && (
            <span className="text-tx-faint tabular-nums">· {t('seriesDetail.seasons.runtime', { mins: episode.runtime_minutes })}</span>
          )}
          {finaleLabel && (
            <span
              data-testid="episode-finale"
              className="inline-flex items-center gap-1 rounded-full bg-warn-dim text-warn px-1.5 py-0.5 text-[10px] font-semibold"
            >
              <Award className="w-2.5 h-2.5" aria-hidden="true" />
              {finaleLabel}
            </span>
          )}
        </div>

        <div className="flex items-center gap-2 text-[11px] text-tx-muted">
          <span aria-hidden="true" className={cn('w-1.5 h-1.5 rounded-full', dot.color)} data-testid={dot.testId} />
          {!episode.has_file && (
            <span className="text-[10.5px] text-tx-faint">
              {episode.monitored
                ? t('seriesDetail.seasons.missing')
                : t('seriesDetail.seasons.unmonitored')}
            </span>
          )}
          {episode.has_file && (() => {
            const meta = formatEpisodeMeta(episode);
            if (!meta) return null;
            return (
              <span
                data-testid="episode-row-eq"
                className="ml-auto font-mono text-[11px] text-tx-muted truncate"
              >
                {meta}
              </span>
            );
          })()}
        </div>

        {episode.overview && (
          <button
            type="button"
            onClick={() => setExpanded((v) => !v)}
            data-testid="episode-overview"
            className={cn(
              'text-left text-[11.5px] text-tx-secondary leading-relaxed',
              'hover:text-tx-primary transition-colors cursor-pointer',
              !expanded && 'line-clamp-2',
            )}
            aria-expanded={expanded}
          >
            {episode.overview}
          </button>
        )}
      </div>
    </div>
  );
}
