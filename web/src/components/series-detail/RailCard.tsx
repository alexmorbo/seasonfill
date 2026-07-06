import { useTranslation } from 'react-i18next';
import { cn } from '@/lib/utils';
import {
  mediaUrl,
  type SeriesHero,
  type StatusToken,
} from '@/api/series';
import type { components } from '@/api/schema';
import { CountryName } from './CountryName';
import { LanguageName } from './LanguageName';
import { PremiereDate } from './PremiereDate';

type TaxonomyChip = components['schemas']['dto.TaxonomyChip'];

export interface RailCardProps {
  readonly status: StatusToken;
  readonly hero: SeriesHero | undefined;
  // B-36: `awards` moved out of the rail — now served by <RatingsSection />.
  // `omdbDegraded` is retained as a no-op pass-through for future rail
  // rows that may need degraded gating (RailCard call site still passes
  // it; deletion would force a SeriesDetail.tsx edit per drop).
  readonly omdbDegraded?: boolean | undefined;
  readonly keywords?: readonly TaxonomyChip[] | undefined;
  readonly className?: string | undefined;
}

interface RailRowProps {
  readonly label: string;
  readonly value: React.ReactNode;
  readonly accent?: boolean;
  readonly testId?: string;
}

function RailRow({ label, value, accent, testId }: RailRowProps) {
  return (
    <div
      data-testid={testId}
      className="flex items-center justify-between gap-3.5 py-[9px] text-[12.5px] border-b border-border-faint last:border-b-0"
    >
      <span className="text-tx-muted whitespace-nowrap">{label}</span>
      <span className={cn(
        'font-medium text-right min-w-0 inline-flex items-center gap-1.5',
        accent ? 'text-accent' : 'text-tx-secondary',
      )}>
        {value}
      </span>
    </div>
  );
}

export function RailCard({
  status, hero, omdbDegraded: _omdbDegraded, keywords, className,
}: RailCardProps) {
  const { t } = useTranslation();

  const network = hero?.networks?.[0];
  const networkLogo = mediaUrl(network?.logo_asset);
  const showStudio = Boolean(hero?.studio);
  const countries = hero?.countries ?? [];
  // Prefer the plural array when present; fall back to the singular field
  // for pre-365a payloads. Empty array AND empty singular → hide row.
  const countriesList: readonly string[] = countries.length > 0
    ? countries
    : (hero?.country ? [hero.country] : []);
  const showCountries = countriesList.length > 0;
  const showPremiereDate = Boolean(hero?.premiere_date);
  const showOriginalLanguage = Boolean(hero?.original_language);
  // B-36: awards row removed — rendered by <RatingsSection /> under cast.
  const showNetwork = Boolean(network?.name);
  const showKeywords = (keywords?.length ?? 0) > 0;

  return (
    <div
      data-testid="rail-card"
      className={cn(
        'flex flex-col overflow-hidden rounded-lg border border-white/10 bg-bg-surface/40 backdrop-blur-md',
        'lg:sticky lg:top-[64px]',
        className,
      )}
    >
      <div className="px-4 pt-1 pb-1">
        <RailRow
          label={t('seriesDetail.rail.status')}
          value={t(`seriesDetail.status.${status}`)}
          accent={status === 'continuing'}
          testId="rail-row-status"
        />
        {showNetwork && (
          <RailRow
            label={t('seriesDetail.rail.network')}
            testId="rail-row-network"
            value={
              networkLogo ? (
                <img
                  src={networkLogo}
                  alt={network?.name ?? ''}
                  title={network?.name ?? ''}
                  className="h-4 w-auto object-contain opacity-90"
                  loading="lazy"
                />
              ) : (
                <span className="font-mono text-[10.5px] tracking-[0.08em] uppercase">
                  {network?.name}
                </span>
              )
            }
          />
        )}
        {showStudio && (
          <RailRow
            label={t('seriesDetail.rail.studio')}
            value={<span data-testid="rail-row-studio-value">{hero?.studio}</span>}
            testId="rail-row-studio"
          />
        )}
        {showPremiereDate && (
          <RailRow
            label={t('seriesDetail.rail.premiereDate')}
            value={<PremiereDate iso={hero?.premiere_date ?? undefined} />}
            testId="rail-row-premiere-date"
          />
        )}
        {showCountries && (
          <RailRow
            label={t('seriesDetail.rail.country', { count: countriesList.length })}
            testId="rail-row-countries"
            value={
              <span data-testid="rail-row-countries-value">
                {countriesList.map((c, i) => (
                  <span key={`${c}-${i}`}>
                    {i > 0 && ', '}
                    <CountryName code={c} />
                  </span>
                ))}
              </span>
            }
          />
        )}
        {showOriginalLanguage && (
          <RailRow
            label={t('seriesDetail.rail.originalLanguage')}
            value={<LanguageName code={hero?.original_language ?? undefined} />}
            testId="rail-row-original-language"
          />
        )}
        {/* B-36: awards row relocated to <RatingsSection /> under cast. */}
      </div>

      {showKeywords && (
        <div
          data-testid="rail-keywords"
          className="border-t border-border-faint px-4 py-3.5"
        >
          <div className="text-[10px] font-semibold uppercase tracking-[0.1em] text-tx-faint mb-2.5">
            {t('seriesDetail.overview.keywords')}
          </div>
          <div className="flex flex-wrap gap-1.5">
            {keywords!.slice(0, 12).map((k) => (
              <span
                key={k.id ?? k.name}
                className="rounded-md bg-bg-surface-2/70 border border-border-subtle px-1.5 py-0.5 text-[11px] text-tx-secondary"
              >
                {k.name}
              </span>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}
