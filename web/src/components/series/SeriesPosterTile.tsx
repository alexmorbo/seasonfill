import { useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router-dom';
import { cn } from '@/lib/utils';
import { relativeTime } from '@/lib/format';
import type { SeriesCacheItem } from '@/lib/api/seriesCache';

export interface SeriesPosterTileProps {
  readonly item: SeriesCacheItem;
}

function hueFor(item: SeriesCacheItem): number {
  const src = item.poster_path && item.poster_path.length > 0 ? item.poster_path : item.title;
  let h = 0;
  for (let i = 0; i < src.length; i += 1) {
    h = (h * 31 + src.charCodeAt(i)) % 360;
  }
  return h;
}

export function SeriesPosterTile({ item }: SeriesPosterTileProps) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const hue = useMemo(() => hueFor(item), [item]);
  const mono = (item.title.charAt(0) || '?').toUpperCase();
  const when = relativeTime(item.last_grab_at ?? item.updated_at);

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
      aria-label={t('series.tile.posterAria', { title: item.title, year: item.year ?? '' })}
      style={{
        background:
          `radial-gradient(120% 80% at 30% 0%, oklch(0.30 0.07 ${hue} / 0.9), transparent 60%),` +
          `linear-gradient(165deg, oklch(0.34 0.08 ${hue}), oklch(0.19 0.04 ${(hue + 30) % 360}) 75%)`,
      }}
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
      <span
        aria-hidden="true"
        className="absolute z-0 -right-1.5 -top-2.5 font-mono font-bold text-[120px] leading-[0.8] tracking-tighter text-[oklch(1_0_0_/_0.07)]"
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
        {(item.year !== undefined || item.network) && (
          <div className="text-[11px] text-[oklch(1_0_0_/_0.62)]">
            {item.year !== undefined ? item.year : ''}
            {item.year !== undefined && item.network ? ' · ' : ''}
            {item.network ?? ''}
          </div>
        )}
        <span className="text-[11px] text-[oklch(1_0_0_/_0.68)]">{when}</span>
      </div>
    </article>
  );
}
