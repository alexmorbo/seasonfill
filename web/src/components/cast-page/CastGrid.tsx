import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import { cn } from '@/lib/utils';
import { Badge } from '@/components/ui/badge';
import { mediaUrl } from '@/api/series';
import { MonogramFallback } from '@/components/MonogramFallback';
import type { CastPageMember } from '@/api/seriesCast';

// V1 keeps Main/Recurring/Guest badges OFF per scope ("feature flag OK").
// Flip to true after the brief reconciles with the handoff (Q3).
const CAST_BADGES_ENABLED = false;

export interface CastGridProps {
  readonly cast: readonly CastPageMember[];
  readonly totalEpisodeCount: number;
  readonly className?: string | undefined;
}

type RoleBadge = { variant: 'accent' | 'info' | 'neutral'; key: 'main' | 'recurring' | 'guest' };

function deriveRoleBadge(episodes: number, total: number): RoleBadge | null {
  if (total <= 0 || episodes <= 0) return null;
  const ratio = episodes / total;
  if (ratio > 0.5) return { variant: 'accent', key: 'main' };
  if (ratio > 0.1) return { variant: 'info', key: 'recurring' };
  return { variant: 'neutral', key: 'guest' };
}

export function CastGrid({ cast, totalEpisodeCount, className }: CastGridProps) {
  const { t } = useTranslation();

  if (cast.length === 0) {
    return (
      <p
        data-testid="cast-grid-empty"
        className="text-[12.5px] text-tx-muted py-6 text-center"
      >
        {t('seriesDetail.castPage.empty.cast')}
      </p>
    );
  }

  return (
    <div
      data-testid="cast-grid"
      className={cn(
        'grid gap-3 grid-cols-1 sm:grid-cols-2 md:grid-cols-3 lg:grid-cols-5',
        className,
      )}
    >
      {cast.map((m, i) => {
        const src = mediaUrl(m.profile_asset);
        const name = m.name ?? '';
        const character = m.character_name ?? '';
        const episodes = m.episode_count ?? 0;
        const tmdb = m.tmdb_id;
        const badge = CAST_BADGES_ENABLED ? deriveRoleBadge(episodes, totalEpisodeCount) : null;
        const key = `${m.person_id ?? m.tmdb_id ?? name}-${character}-${i}`;

        const inner = (
          <div className="flex flex-col items-center gap-1.5 p-3 rounded-lg border border-border-subtle bg-bg-surface hover:border-border-strong transition-colors">
            <div className="relative w-[88px] h-[88px] rounded-full overflow-hidden border border-border-subtle bg-bg-surface-2 shrink-0">
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
                <MonogramFallback title={name} kind="avatar" />
              )}
            </div>
            <div className="text-[12.5px] font-semibold text-tx-primary text-center w-full truncate">
              {name}
            </div>
            {character && (
              <div className="text-[11.5px] text-tx-muted text-center w-full truncate">
                {t('seriesDetail.cast.asCharacter', { character })}
              </div>
            )}
            {episodes > 0 && (
              <div className="text-[11px] text-tx-faint tabular-nums">
                {t('seriesDetail.cast.episodeCount', { count: episodes })}
              </div>
            )}
            {badge && (
              <Badge
                data-testid={`cast-role-badge-${badge.key}`}
                variant={badge.variant}
                className="text-[10.5px] uppercase tracking-wide"
              >
                {t(`seriesDetail.castPage.badges.${badge.key}`)}
              </Badge>
            )}
          </div>
        );

        return tmdb ? (
          <Link
            key={key}
            data-testid="cast-grid-card"
            data-tmdb-id={tmdb}
            to={`/person/${tmdb}`}
            className="block focus-visible:outline-hidden focus-visible:ring-2 focus-visible:ring-accent rounded-lg"
          >
            {inner}
          </Link>
        ) : (
          <div key={key} data-testid="cast-grid-card" data-tmdb-id="">
            {inner}
          </div>
        );
      })}
    </div>
  );
}
