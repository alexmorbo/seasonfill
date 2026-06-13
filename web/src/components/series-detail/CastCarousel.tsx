import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import { ChevronRight } from 'lucide-react';
import { cn } from '@/lib/utils';
import { mediaUrl } from '@/api/seriesDetail';
import type { components } from '@/api/schema';

type CastMember = components['schemas']['dto.CastMember'];

export interface CastCarouselProps {
  readonly instance: string;
  readonly seriesId: number;
  readonly cast: readonly CastMember[] | undefined;
  readonly className?: string | undefined;
}

function initials(name: string | undefined): string {
  if (!name) return '?';
  const parts = name.trim().split(/\s+/).slice(0, 2);
  return parts.map((p) => p.charAt(0).toUpperCase()).join('') || '?';
}

export function CastCarousel({ instance, seriesId, cast, className }: CastCarouselProps) {
  const { t } = useTranslation();
  const items = (cast ?? []).slice(0, 10);
  if (items.length === 0) return null;

  return (
    <section
      data-testid="cast-carousel"
      aria-labelledby="cast-carousel-heading"
      className={cn('flex flex-col gap-3', className)}
    >
      <div className="flex items-center justify-between gap-3">
        <h2
          id="cast-carousel-heading"
          className="text-[10.5px] font-bold uppercase tracking-wide text-tx-faint"
        >
          {t('seriesDetail.cast.label')}
        </h2>
        <Link
          to={`/series/${encodeURIComponent(instance)}/${seriesId}/cast`}
          data-testid="cast-view-all"
          className="inline-flex items-center gap-1 text-[11.5px] text-tx-muted hover:text-tx-primary transition-colors"
        >
          {t('seriesDetail.cast.viewAll')}
          <ChevronRight className="w-3 h-3" aria-hidden="true" />
        </Link>
      </div>

      <div
        className={cn(
          'flex flex-row gap-3 overflow-x-auto snap-x snap-mandatory pb-2',
          'md:grid md:grid-cols-5 md:gap-4 md:overflow-visible md:snap-none md:pb-0',
        )}
      >
        {items.map((m) => {
          const src = mediaUrl(m.profile_asset);
          const name = m.name ?? '';
          const character = m.character_name ?? '';
          const episodes = m.episode_count ?? 0;
          const key = m.person_id ?? m.tmdb_person_id ?? `${name}-${character}`;
          return (
            <div
              key={key}
              data-testid="cast-member"
              className={cn(
                'flex flex-col items-center gap-1.5 snap-start min-w-[120px]',
                'md:min-w-0',
              )}
            >
              <div className="w-16 h-16 md:w-[72px] md:h-[72px] rounded-full overflow-hidden border border-border-subtle bg-bg-surface-2 shrink-0">
                {src ? (
                  <img
                    src={src}
                    alt=""
                    aria-hidden="true"
                    loading="lazy"
                    decoding="async"
                    className="w-full h-full object-cover"
                  />
                ) : (
                  <span className="flex items-center justify-center w-full h-full text-[18px] font-bold text-tx-faint">
                    {initials(name)}
                  </span>
                )}
              </div>
              <div className="text-[12px] font-semibold text-tx-primary text-center max-w-[120px] truncate w-full">
                {name}
              </div>
              {character && (
                <div className="text-[11px] text-tx-muted text-center max-w-[120px] truncate w-full">
                  {t('seriesDetail.cast.asCharacter', { character })}
                </div>
              )}
              {episodes > 0 && (
                <div className="text-[10.5px] text-tx-faint tabular-nums">
                  {t('seriesDetail.cast.episodeCount', { count: episodes })}
                </div>
              )}
            </div>
          );
        })}
      </div>
    </section>
  );
}
