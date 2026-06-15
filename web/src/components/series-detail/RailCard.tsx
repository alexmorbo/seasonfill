import { useTranslation } from 'react-i18next';
import { Trophy } from 'lucide-react';
import { cn } from '@/lib/utils';
import {
  mediaUrl,
  type SeriesHero,
  type StatusToken,
} from '@/api/seriesDetail';
import type { components } from '@/api/schema';
import { CountryName } from './CountryName';

type TaxonomyChip = components['schemas']['dto.TaxonomyChip'];

export interface RailCardProps {
  readonly status: StatusToken;
  readonly hero: SeriesHero | undefined;
  readonly awards?: string | undefined;
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
  status, hero, awards, omdbDegraded, keywords, className,
}: RailCardProps) {
  const { t } = useTranslation();

  const network = hero?.networks?.[0];
  const networkLogo = mediaUrl(network?.logo_asset);
  const showStudio = Boolean(hero?.studio);
  const showCountry = Boolean(hero?.country);
  const showAwards = Boolean(awards) && !omdbDegraded;
  const showNetwork = Boolean(network?.name);
  const showKeywords = (keywords?.length ?? 0) > 0;

  return (
    <div
      data-testid="rail-card"
      className={cn(
        'flex flex-col overflow-hidden rounded-lg border border-border-faint bg-bg-surface',
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
              <>
                {networkLogo && (
                  <img
                    src={networkLogo}
                    alt={network?.name ?? ''}
                    title={network?.name ?? ''}
                    className="h-4 w-auto object-contain opacity-90"
                    loading="lazy"
                  />
                )}
                <span className="font-mono text-[10.5px] tracking-[0.08em] uppercase">
                  {network?.name}
                </span>
              </>
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
        {showCountry && (
          <RailRow
            label={t('seriesDetail.rail.country')}
            value={<CountryName code={hero?.country ?? undefined} />}
            testId="rail-row-country"
          />
        )}
        {showAwards && (
          <RailRow
            label={t('seriesDetail.rail.awards')}
            testId="rail-row-awards"
            value={
              <>
                <Trophy className="w-3.5 h-3.5 text-warn" aria-hidden="true" />
                <span>{awards}</span>
              </>
            }
          />
        )}
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
