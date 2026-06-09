import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router-dom';
import { Check, TriangleAlert, RotateCw } from 'lucide-react';
import { cn } from '@/lib/utils';
import { relativeTime } from '@/lib/format';
import { formatSeriesTitle } from '@/lib/title';
import type { SeriesCacheItem } from '@/lib/api/seriesCache';
import { SeriesPoster } from '@/components/SeriesPoster';

export interface PosterTileProps {
  readonly item: SeriesCacheItem;
}

type Variant = 'imported' | 'failed' | 'regrab';

function classifyVariant(item: SeriesCacheItem): Variant {
  const s = (item.status ?? '').toLowerCase();
  if (s.startsWith('import_failed') || s === 'failed') return 'failed';
  return 'imported';
}

function parseEpisode(raw: string | undefined): { season?: number; first?: number; last?: number | undefined } {
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

export function PosterTile({ item }: PosterTileProps) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const variant = classifyVariant(item);
  const mono = (item.title.charAt(0) || '?').toUpperCase();
  const { season, first, last } = parseEpisode(item.last_imported_episode);
  // Operator R2: always render the subtitle when year is available —
  // no embedded-year suppression.
  const showYearFooter = item.year !== undefined;
  const ariaLabel = t('dashboard.poster.posterAria', {
    label: formatSeriesTitle(item.title, item.year),
  });

  const epsLabel = season === undefined
    ? null
    : first === undefined
      ? t('dashboard.poster.episodes.season', { season })
      : last === undefined || last === first
        ? t('dashboard.poster.episodes.single', { season, ep: first })
        : t('dashboard.poster.episodes.range', { season, first, last });
  const newcount = last !== undefined && first !== undefined ? last - first + 1 : undefined;
  const when = relativeTime(item.last_grab_at ?? item.updated_at);

  const handleOpen = () => navigate(`/series?q=${encodeURIComponent(item.title)}`);
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
      data-testid="poster-tile"
      data-variant={variant}
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
        className={cn(
          'absolute z-30 top-2.5 right-2.5 inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-[10.5px] font-semibold backdrop-blur-sm',
          variant === 'imported' && 'bg-ok/85 text-bg-base',
          variant === 'failed' && 'bg-warn/90 text-bg-base',
          variant === 'regrab' && 'bg-bg-surface-2/70 text-tx-primary border border-border-subtle',
        )}
      >
        {variant === 'imported' && <Check className="w-2.5 h-2.5" aria-hidden="true" />}
        {variant === 'failed' && <TriangleAlert className="w-2.5 h-2.5" aria-hidden="true" />}
        {variant === 'regrab' && <RotateCw className="w-2.5 h-2.5" aria-hidden="true" />}
        {variant === 'imported' && 'imported'}
        {variant === 'failed' && t('dashboard.poster.failChip')}
        {variant === 'regrab' && t('dashboard.poster.regrabChip', { n: 1 })}
      </span>
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
        {epsLabel && (
          <div className="flex flex-wrap items-center gap-1.5">
            <span
              className={cn(
                'self-start whitespace-nowrap font-mono text-[10.5px] font-semibold rounded-md px-2 py-0.5 text-tx-primary bg-[oklch(0.20_0.01_270/_0.85)] border border-[oklch(1_0_0_/_0.14)]',
                variant === 'failed' && 'bg-warn/85 border-warn text-bg-base',
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
