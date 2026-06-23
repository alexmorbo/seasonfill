import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import { ArrowRight } from 'lucide-react';
import { cn } from '@/lib/utils';
import { mediaUrl } from '@/api/series';
import { MonogramFallback } from '@/components/MonogramFallback';
import type { components } from '@/api/schema';

type CastMember = components['schemas']['dto.CastMember'];

export interface CastStripProps {
  readonly instance: string;
  readonly seriesId: number;
  readonly cast?: readonly CastMember[] | undefined;
  readonly limit?: number;
  readonly className?: string | undefined;
}

export function CastStrip({
  instance, seriesId, cast, limit = 8, className,
}: CastStripProps) {
  const { t } = useTranslation();
  const items = (cast ?? []).slice(0, limit);
  if (items.length === 0) return null;

  return (
    <section
      data-testid="cast-strip"
      aria-labelledby="cast-strip-heading"
      className={cn('flex flex-col gap-3', className)}
    >
      <div
        data-testid="cast-strip-header"
        className="flex items-center justify-between gap-2.5 mb-3.5 min-w-0"
      >
        <h2
          id="cast-strip-heading"
          className="text-[10px] font-semibold uppercase tracking-[0.1em] text-tx-faint truncate"
        >
          {t('seriesDetail.cast.label')}
        </h2>
        <Link
          to={`/series/${encodeURIComponent(instance)}/${seriesId}/cast`}
          data-testid="cast-strip-view-all"
          className="shrink-0 inline-flex items-center gap-1 text-[12.5px] text-tx-muted hover:text-tx-primary transition-colors"
        >
          {t('seriesDetail.cast.viewAll')}
          <ArrowRight className="w-[13px] h-[13px]" aria-hidden="true" />
        </Link>
      </div>

      <div
        data-testid="cast-strip-grid"
        className="grid gap-2.5"
        style={{ gridTemplateColumns: 'repeat(auto-fill, minmax(120px, 1fr))' }}
      >
        {items.map((m) => {
          const src = mediaUrl(m.profile_asset);
          const name = m.name ?? '';
          const character = m.character_name ?? '';
          return (
            <Link
              key={m.person_id ?? `${name}-${character}`}
              to={`/people/${m.person_id ?? ''}`}
              data-testid="cast-strip-card"
              className={cn(
                'flex items-center gap-2.5 rounded-md min-w-0 p-[7px_9px]',
                'border border-transparent hover:border-border-faint hover:bg-bg-surface transition-colors',
              )}
            >
              <span
                className="shrink-0 w-[42px] h-[42px] rounded-full overflow-hidden border border-border-subtle bg-bg-surface-2"
                data-testid="cast-strip-avatar"
              >
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
              </span>
              <span className="flex flex-col min-w-0">
                <span
                  className="text-[12.5px] font-medium text-tx-primary truncate"
                  data-testid="cast-strip-name"
                  title={name}
                >
                  {name}
                </span>
                {character && (
                  <span
                    className="text-[11px] text-tx-muted truncate"
                    data-testid="cast-strip-character"
                    title={character}
                  >
                    {character}
                  </span>
                )}
              </span>
            </Link>
          );
        })}
      </div>
    </section>
  );
}
