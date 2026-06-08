import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router-dom';
import { cn } from '@/lib/utils';
import { relativeTime } from '@/lib/format';
import { formatSeriesTitle, titleHasEmbeddedYear } from '@/lib/title';
import type { SeriesCacheItem } from '@/lib/api/seriesCache';
import { SeriesPoster } from '@/components/SeriesPoster';

export interface SeriesPosterTileProps {
  readonly item: SeriesCacheItem;
}

export function SeriesPosterTile({ item }: SeriesPosterTileProps) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const mono = (item.title.charAt(0) || '?').toUpperCase();
  const when = relativeTime(item.last_grab_at ?? item.updated_at);
  // Hide standalone year in footer when Sonarr already disambiguated
  // the title (Story 075 / PRD F-P1-4).
  const showYearFooter = item.year !== undefined && !titleHasEmbeddedYear(item.title);
  const ariaLabel = t('series.tile.posterAria', {
    label: formatSeriesTitle(item.title, item.year),
  });

  const handleOpen = () => navigate(`/grabs?series=${item.sonarr_series_id}`);
  const onKey = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault();
      handleOpen();
    }
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
      data-testid="series-poster-tile"
      data-monitored={item.monitored ? 'true' : 'false'}
      data-missing={item.missing_count > 0 ? 'true' : 'false'}
    >
      <SeriesPoster
        instance={item.instance_name}
        seriesId={item.sonarr_series_id}
        title={item.title}
        hueKey={item.poster_path && item.poster_path.length > 0 ? item.poster_path : item.title}
        size="full"
        aspectRatio="aspect-auto"
        className="absolute inset-0 z-0"
      />

      <span
        aria-hidden="true"
        className="absolute z-10 -right-1.5 -top-2.5 font-mono font-bold text-[120px] leading-[0.8] tracking-tighter text-[oklch(1_0_0_/_0.07)]"
      >
        {mono}
      </span>

      <span
        aria-label={item.monitored ? t('series.tile.monitoredOn') : t('series.tile.monitoredOff')}
        className={cn(
          'absolute z-30 top-2.5 left-2.5 inline-flex items-center justify-center w-2.5 h-2.5 rounded-full',
          item.monitored ? 'bg-ok' : 'bg-neutral',
        )}
      />

      {item.missing_count > 0 && (
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
        <span className="text-[11px] text-[oklch(1_0_0_/_0.68)]">{when}</span>
      </div>
    </article>
  );
}
