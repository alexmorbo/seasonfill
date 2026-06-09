import { useTranslation } from 'react-i18next';
import { Play, MoreHorizontal, Loader2 } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { SeriesTitleLink } from '@/components/SeriesTitleLink';
import { SeriesPoster } from '@/components/SeriesPoster';
import { cn } from '@/lib/utils';
import type { MissingSeries } from '@/lib/missing';

export interface QueueRowProps {
  readonly row: MissingSeries;
  readonly instanceName: string;
  readonly instanceUiUrl: string | undefined;
  readonly openSeason: number | null;
  readonly isInFlight: boolean;
  readonly onSeasonToggle: (seasonNumber: number) => void;
  readonly onScan: () => void;
  readonly drillSlot?: React.ReactNode;
}

export function QueueRow({
  row, instanceName, instanceUiUrl, openSeason, isInFlight,
  onSeasonToggle, onScan, drillSlot,
}: QueueRowProps) {
  const { t } = useTranslation();
  const seasons = row.seasons ?? [];
  const isOpen = openSeason !== null;
  const hueKey = (row.title_slug && row.title_slug.length > 0
    ? row.title_slug
    : row.title) ?? '';

  return (
    <article
      className={cn(
        'rounded-lg border border-border-faint bg-surface p-[13px_15px] flex flex-col gap-0',
        isOpen && 'border-border-subtle',
      )}
      data-testid="queue-row"
      data-series-id={row.series_id}
    >
      <div className="flex gap-[13px] items-start">
        <SeriesPoster
          instance={instanceName}
          seriesId={row.series_id ?? 0}
          title={row.title ?? ''}
          hueKey={hueKey}
          size="small"
          aspectRatio="aspect-auto"
          className="w-[46px] h-[69px] flex-none rounded-[6px] border border-border-subtle"
        />
        <div className="flex-1 min-w-0 flex flex-col gap-2.5">
          <div className="flex items-center gap-2.5 flex-wrap">
            <SeriesTitleLink
              title={row.title ?? '—'}
              titleSlug={row.title_slug}
              year={row.year}
              instanceUiUrl={instanceUiUrl}
            />
            {/* Operator R2: render the supplied year unconditionally as
                a muted subtitle. The title itself is rendered verbatim
                by SeriesTitleLink; no embedded-year suppression. */}
            {row.year !== undefined && (
              <span className="text-[11.5px] text-faint">
                {row.year}
              </span>
            )}
            <span
              className="font-mono text-[11px] font-semibold text-warn bg-warn-dim border border-warn/30 px-2 py-px rounded-full whitespace-nowrap"
              data-testid="queue-row-missing-pill"
            >
              {t('instanceQueue.row.missing', { count: row.total_missing_aired ?? 0 })}
            </span>
            <span className="flex-1" />
            <div className="flex gap-2 flex-none">
              <Button
                size="sm"
                onClick={onScan}
                disabled={isInFlight || row.series_id === undefined}
                aria-label={t('instanceQueue.row.scanAria', {
                  title: row.title ?? `#${row.series_id ?? '?'}`,
                })}
              >
                {isInFlight ? (
                  <Loader2 className="w-3.5 h-3.5 mr-1 animate-spin" aria-hidden="true" />
                ) : (
                  <Play className="w-3.5 h-3.5 mr-1" aria-hidden="true" />
                )}
                {t('instanceQueue.row.scan')}
              </Button>
              <Button
                size="sm"
                variant="ghost"
                className="h-9 w-9 p-0"
                aria-label={t('instanceQueue.row.moreAria')}
              >
                <MoreHorizontal className="w-3.5 h-3.5" aria-hidden="true" />
              </Button>
            </div>
          </div>

          <div
            className="flex flex-wrap gap-1.5"
            data-testid="queue-row-seasons"
            role="list"
          >
            {seasons.map((sea) => {
              const num = sea.season_number ?? 0;
              const count = sea.missing_aired_count ?? 0;
              const active = openSeason === num;
              return (
                <button
                  key={num}
                  type="button"
                  role="listitem"
                  onClick={() => onSeasonToggle(num)}
                  aria-pressed={active}
                  aria-label={t('instanceQueue.row.seasonAria', { num, count })}
                  className={cn(
                    'inline-flex items-center gap-1.5 rounded-md border px-2 py-0.5',
                    'font-mono text-[11px] font-semibold cursor-pointer',
                    active
                      ? 'bg-accent-dim border-accent/40 text-accent'
                      : 'bg-surface-2 border-border-subtle text-tx-secondary hover:border-border-strong hover:text-foreground',
                  )}
                >
                  S{String(num).padStart(2, '0')}
                  <span className={cn('font-normal', active ? 'text-accent' : 'text-warn')}>
                    ·{count}
                  </span>
                </button>
              );
            })}
          </div>

          {isOpen && (
            <section
              data-testid="queue-drill-slot"
              data-series-id={row.series_id}
              data-season-number={openSeason}
              className="mt-3 p-3.5 bg-bg-base border border-border-faint rounded-md"
            >
              {drillSlot}
            </section>
          )}
        </div>
      </div>
    </article>
  );
}
