import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router-dom';
import { Check, TriangleAlert } from 'lucide-react';
import { cn } from '@/lib/utils';
import { relativeTime } from '@/lib/format';
import { formatSeriesTitle } from '@/lib/title';
import type { SeriesCacheItem } from '@/lib/api/seriesCache';
import { MediaImage } from '@/components/MediaImage';
import { SonarrLink } from '@/components/SonarrLink';
import { useInstancePublicURL } from '@/lib/useInstancePublicURL';

export type SeriesCardVariant = 'dashboard' | 'library';

export interface SeriesCardTileProps {
  readonly item: SeriesCacheItem;
  readonly variant: SeriesCardVariant;
}

type StatusVariant = 'imported' | 'failed';

function classifyStatus(item: SeriesCacheItem): StatusVariant {
  const s = (item.status ?? '').toLowerCase();
  if (s.startsWith('import_failed') || s === 'failed') return 'failed';
  return 'imported';
}

function parseEpisode(
  raw: string | undefined,
): { season?: number; first?: number; last?: number | undefined } {
  if (!raw) return {};
  const sMatch = /^S(\d+)/i.exec(raw);
  if (!sMatch?.[1]) return {};
  const season = Number(sMatch[1]);
  const rangeMatch = /E(\d+)(?:[–-](\d+))?/i.exec(raw);
  if (!rangeMatch) return { season };
  const first = Number(rangeMatch[1]);
  const last = rangeMatch[2] ? Number(rangeMatch[2]) : undefined;
  return { season, first, last };
}

// Canonical-first navigation target. B-42a / Story 495: always prefer the
// seasonfill series PK (→ /series/:id). Fall back to the legacy 3-segment
// instance-scoped shape ONLY when series_id is absent (pre-cutover broken
// rows). Story 961 (C-card) removes the dashboard tile's always-legacy bug
// where it navigated to /series/:instance/:sonarr_id even when a canonical id
// was present — LegacySeriesRedirect then resolved to the WRONG series.
function seriesTarget(item: SeriesCacheItem): string {
  return typeof item.series_id === 'number'
    ? `/series/${item.series_id}`
    : `/series/${encodeURIComponent(item.instance_name)}/${item.sonarr_series_id}`;
}

export function SeriesCardTile({ item, variant }: SeriesCardTileProps) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const sonarrPublicURL = useInstancePublicURL(item.instance_name);
  const when = relativeTime(item.last_grab_at ?? item.updated_at);
  const showYearFooter = item.year !== undefined;

  const isDashboard = variant === 'dashboard';
  const status = classifyStatus(item);
  const { season, first, last } = isDashboard
    ? parseEpisode(item.last_imported_episode)
    : {};

  const epsLabel =
    season === undefined
      ? null
      : first === undefined
        ? t('dashboard.poster.episodes.season', { season })
        : last === undefined || last === first
          ? t('dashboard.poster.episodes.single', { season, ep: first })
          : t('dashboard.poster.episodes.range', { season, first, last });
  const newcount =
    last !== undefined && first !== undefined ? last - first + 1 : undefined;

  // Visible title is the raw, API-localized item.title (no munging).
  // formatSeriesTitle is used ONLY for the aria-label.
  const ariaLabel = t(
    isDashboard ? 'dashboard.poster.posterAria' : 'series.tile.posterAria',
    { label: formatSeriesTitle(item.title, item.year) },
  );

  const handleOpen = () => navigate(seriesTarget(item));
  const onKey = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault();
      handleOpen();
    }
  };

  const variantDataAttrs: Record<string, string> = isDashboard
    ? { 'data-variant': status }
    : {
        'data-monitored': item.monitored ? 'true' : 'false',
        'data-missing': item.missing_count > 0 ? 'true' : 'false',
      };

  return (
    <article
      role="button"
      tabIndex={0}
      onClick={handleOpen}
      onKeyDown={onKey}
      aria-label={ariaLabel}
      className={cn(
        'relative isolate overflow-hidden rounded-lg border border-border-faint aspect-[2/3] cursor-pointer outline-hidden',
        'transition-[transform,box-shadow,border-color] duration-150',
        'hover:-translate-y-[3px] hover:border-border-strong hover:shadow-[0_10px_26px_oklch(0_0_0_/_0.4)]',
        'focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-bg-base',
      )}
      data-testid={isDashboard ? 'poster-tile' : 'series-poster-tile'}
      {...variantDataAttrs}
    >
      <MediaImage
        hash={item.poster_hash}
        kind="series_poster"
        title={item.title}
        hueKey={
          item.poster_hash && item.poster_hash.length > 0
            ? item.poster_hash
            : item.title
        }
        fallback="monogram"
        aspectRatio="aspect-auto"
        className="absolute inset-0 z-0"
      />

      {/* library: monitored dot (top-left) */}
      {!isDashboard && (
        <span
          aria-label={
            item.monitored
              ? t('series.tile.monitoredOn')
              : t('series.tile.monitoredOff')
          }
          className={cn(
            'absolute z-30 top-2.5 left-2.5 inline-flex items-center justify-center w-2.5 h-2.5 rounded-full',
            item.monitored ? 'bg-ok' : 'bg-neutral',
          )}
        />
      )}

      <SonarrLink
        instance={item.instance_name}
        publicUrl={sonarrPublicURL}
        seriesId={item.sonarr_series_id}
        title={item.title}
        titleSlug={item.title_slug}
        variant="icon"
        size="sm"
        className={cn(
          'absolute z-30',
          isDashboard ? 'bottom-2.5 right-2.5' : 'bottom-2 right-2',
        )}
      />

      {/* dashboard: import status chip (top-right) */}
      {isDashboard && (
        <span
          className={cn(
            'absolute z-30 top-2.5 right-2.5 inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-[10.5px] font-semibold backdrop-blur-sm',
            status === 'imported' && 'bg-ok/85 text-bg-base',
            status === 'failed' && 'bg-warn/90 text-bg-base',
          )}
        >
          {status === 'imported' ? (
            <Check className="w-2.5 h-2.5" aria-hidden="true" />
          ) : (
            <TriangleAlert className="w-2.5 h-2.5" aria-hidden="true" />
          )}
          {status === 'imported'
            ? t('dashboard.poster.importedChip')
            : t('dashboard.poster.failChip')}
        </span>
      )}

      {/* library: missing chip (top-right) */}
      {!isDashboard && item.missing_count > 0 && (
        <span
          className="absolute z-30 top-2 right-2 inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-[10.5px] font-semibold backdrop-blur-sm bg-warn/90 text-bg-base"
          data-testid="series-tile-missing-chip"
        >
          {t('series.tile.missing', { count: item.missing_count })}
        </span>
      )}

      <div className="absolute z-20 inset-x-0 bottom-0 flex flex-col gap-1.5 px-3 pt-10 pb-3 bg-[linear-gradient(transparent,oklch(0.11_0.01_270/_0.55)_30%,oklch(0.10_0.01_270/_0.94)_72%)]">
        <div className="font-semibold text-[15.5px] leading-tight tracking-tight text-tx-primary drop-shadow-[0_1px_8px_oklch(0_0_0_/_0.55)]">
          {item.title}
        </div>
        {(showYearFooter || item.network) && (
          <div className="text-[11px] text-[oklch(1_0_0_/_0.62)]">
            {showYearFooter ? item.year : ''}
            {showYearFooter && item.network ? ' · ' : ''}
            {item.network ?? ''}
          </div>
        )}
        {isDashboard && epsLabel && (
          <div className="flex flex-wrap items-center gap-1.5">
            <span
              className={cn(
                'self-start whitespace-nowrap font-mono text-[10.5px] font-semibold rounded-md px-2 py-0.5 text-tx-primary bg-[oklch(0.20_0.01_270/_0.85)] border border-[oklch(1_0_0_/_0.14)]',
                status === 'failed' && 'bg-warn/85 border-warn text-bg-base',
              )}
            >
              {epsLabel}
            </span>
            {newcount !== undefined && newcount > 0 && (
              <span className="whitespace-nowrap font-mono text-[10.5px] font-bold rounded-md px-1.5 py-0.5 text-accent bg-accent-dim border border-accent/35">
                {t('dashboard.poster.newcount', { count: newcount })}
              </span>
            )}
          </div>
        )}
        <span className="text-[11px] text-[oklch(1_0_0_/_0.68)]">{when}</span>
      </div>
    </article>
  );
}
