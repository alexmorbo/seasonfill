import { Inbox } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import type { TFunction } from 'i18next';
import { cn } from '@/lib/utils';
import type { MovieDetailLibrary } from '@/api/movies';

// Radarr's raw `minimumAvailability` enum (case varies) mapped onto our
// `movieDetail.library.availability.*` i18n keys. `predb`/`tba` both map to
// `preDb` — Radarr's older builds spell the pre-database state `tba`.
const AVAILABILITY_KEYS: Record<string, string> = {
  released: 'released',
  announced: 'announced',
  incinemas: 'inCinemas',
  predb: 'preDb',
  tba: 'preDb',
  deleted: 'deleted',
};

function availabilityLabel(t: TFunction, raw: string): string {
  const key = AVAILABILITY_KEYS[raw.toLowerCase()];
  return key
    ? t(`movieDetail.library.availability.${key}`, { defaultValue: raw })
    : raw;
}

export interface MovieHeroLibraryStripProps {
  readonly library: readonly MovieDetailLibrary[];
  readonly className?: string | undefined;
}

// Verbatim copy of `HeroLibraryStrip.tsx`'s byte formatter — kept local so
// this component has no runtime dependency on the series-only strip.
function fmtBytes(n: number | undefined): string {
  if (!n || n <= 0) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  let v = n;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) { v /= 1024; i += 1; }
  return `${v.toFixed(v >= 10 || i === 0 ? 0 : 1)} ${units[i]}`;
}

// "dark" chip palette — light-on-dark for use over the bleed-hero scrim,
// mirrors `HeroLibraryStrip.tsx`'s `tone="dark"` branch. The movie hero has
// no `sonarrOnly`-style light-surface fallback, so this is unconditional.
const chipBase = 'inline-flex items-center gap-1.5 px-2.5 py-1 rounded-md text-[12px] border bg-white/[0.08] border-white/[0.14] text-white/90';
const capColor = 'text-white/55';

function MovieLibraryRow({ row }: { row: MovieDetailLibrary }) {
  const { t } = useTranslation();
  const size = row.size_on_disk_bytes ?? 0;

  return (
    <div
      data-testid={`movie-library-row-${row.instance_name ?? 'unknown'}`}
      className="flex flex-wrap items-center gap-2"
    >
      <span className="text-[12px] font-medium text-white/90">
        {row.instance_name}
      </span>
      {row.monitored && (
        <span data-testid="movie-library-monitored" className={cn(chipBase, 'font-medium')}>
          {t('movieDetail.library.monitored')}
        </span>
      )}
      {row.has_file && (
        <span data-testid="movie-library-hasfile" className={cn(chipBase, 'font-medium')}>
          {t('movieDetail.library.hasFile')}
        </span>
      )}
      {row.availability && (
        <span className="text-[12px] text-white/70">{availabilityLabel(t, row.availability)}</span>
      )}
      {row.has_file && row.quality && (
        <span data-testid="movie-library-quality" className={cn(chipBase, 'font-mono tabular-nums')}>
          {row.quality}
        </span>
      )}
      {row.has_file && row.quality && (row.video_codec || row.audio_codec) && (
        <span data-testid="movie-library-codec" className={cn(chipBase, 'font-mono')}>
          {[row.video_codec, row.audio_codec].filter(Boolean).join(' · ')}
        </span>
      )}
      {size > 0 && (
        <span data-testid="movie-library-size" className={cn(chipBase, 'font-mono tabular-nums')}>
          {fmtBytes(size)}
        </span>
      )}
    </div>
  );
}

// MovieHeroLibraryStrip — bottom-of-hero "on disk" strip for the movie card,
// visually matching series' `HeroLibraryStrip` (same caption + divider
// treatment + dark-scrim chip palette). Movies are single-file, so unlike
// the series strip there's no episode %/counts progress — just one compact
// row per Radarr instance the movie is known to.
export function MovieHeroLibraryStrip({ library, className }: MovieHeroLibraryStripProps) {
  const { t } = useTranslation();
  const hasAnything = library.length > 0;

  return (
    <div
      data-testid="movie-hero-library-strip"
      className={cn(
        'flex flex-wrap items-center gap-2 pt-3 mt-3 border-t border-white/[0.12]',
        className,
      )}
    >
      <span className={cn('text-[10px] font-semibold uppercase tracking-[0.1em]', capColor)}>
        {t('seriesDetail.library.cap')}
      </span>

      {!hasAnything ? (
        <span className={chipBase} data-testid="movie-detail-library-empty">
          <Inbox className="w-3 h-3" aria-hidden="true" />
          {t('movieDetail.library.empty')}
        </span>
      ) : (
        <div className="flex flex-col gap-2">
          {library.map((row) => (
            <MovieLibraryRow key={row.instance_name ?? row.radarr_movie_id} row={row} />
          ))}
        </div>
      )}
    </div>
  );
}
