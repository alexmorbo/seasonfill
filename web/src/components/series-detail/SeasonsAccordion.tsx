import { useState, useMemo } from 'react';
import type { ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { cn } from '@/lib/utils';
import {
  Accordion,
  AccordionContent,
  AccordionItem,
  AccordionTrigger,
} from '@/components/ui/accordion';
import { mediaUrl } from '@/api/seriesDetail';
import { useSeriesSeason } from '@/api/seriesSeason';
import type { components } from '@/api/schema';
import { EpisodeRow } from './EpisodeRow';

type Season = components['schemas']['dto.Season'];

export interface SeasonsAccordionProps {
  readonly instance: string;
  readonly seriesId: number;
  readonly seasons: readonly Season[] | undefined;
  readonly lang?: string | undefined;
  readonly className?: string | undefined;
  // Optional badge rendered inline with the section heading
  // (used for per-section StaleBadge wire-up from SeriesDetail).
  readonly staleBadge?: ReactNode;
}

function sortSeasons(seasons: readonly Season[]): readonly Season[] {
  // Regular seasons DESC, Specials (season 0) always pinned to the end.
  return [...seasons].sort((a, b) => {
    const aS = a.season_number ?? 0;
    const bS = b.season_number ?? 0;
    if (aS === 0 && bS !== 0) return 1;
    if (bS === 0 && aS !== 0) return -1;
    return bS - aS;
  });
}

function seasonYear(airDate: string | undefined): string {
  if (!airDate) return '';
  const m = airDate.match(/^(\d{4})/);
  return m ? m[1]! : '';
}

interface SeasonAccordionItemProps {
  readonly instance: string;
  readonly seriesId: number;
  readonly season: Season;
  readonly lang?: string | undefined;
  readonly expanded: boolean;
}

function SeasonAccordionItem({
  instance, seriesId, season, lang, expanded,
}: SeasonAccordionItemProps) {
  const { t } = useTranslation();
  const seasonNumber = season.season_number ?? 0;
  const lazy = useSeriesSeason({
    instance,
    seriesId,
    seasonNumber,
    ...(lang ? { lang } : {}),
    enabled: expanded,
  });
  // Lazy data overrides composite payload only when present.
  // Episodes always render DESC (latest first).
  const lazyEpisodes = lazy.data?.season?.episodes;
  const compositeEpisodes = season.episodes;
  const episodes = useMemo(() => {
    const raw = lazyEpisodes ?? compositeEpisodes ?? [];
    return [...raw].reverse();
  }, [lazyEpisodes, compositeEpisodes]);
  const isSpecial = seasonNumber === 0;
  const onDisk = season.on_disk_count ?? 0;
  const total = season.episode_count ?? episodes.length;
  const year = seasonYear(season.air_date);
  const posterSrc = mediaUrl(season.poster_asset);
  const seasonLabel = isSpecial
    ? t('seriesDetail.seasons.specials')
    : t('seriesDetail.seasons.season', { n: seasonNumber });

  return (
    <AccordionItem
      value={`s${seasonNumber}`}
      data-testid="season-accordion-item"
      data-season={seasonNumber}
      data-special={isSpecial ? 'true' : 'false'}
      className={cn('border-b border-border-faint last:border-b-0', isSpecial && 'opacity-80')}
    >
      <AccordionTrigger className="px-3 py-2.5 hover:no-underline hover:bg-bg-surface/40 rounded-md">
        <div className="flex flex-1 items-center gap-3 min-w-0">
          <div className="w-10 h-[60px] rounded overflow-hidden border border-border-subtle bg-bg-surface-2 shrink-0">
            {posterSrc ? (
              <img src={posterSrc} alt="" aria-hidden="true" loading="lazy" className="w-full h-full object-cover" />
            ) : null}
          </div>
          <div className="flex flex-col gap-0.5 min-w-0 flex-1 text-left">
            <div className="text-[13px] font-semibold text-tx-primary">{seasonLabel}</div>
            <div className="text-[11.5px] text-tx-muted truncate">
              {t('seriesDetail.seasons.episodesCount', { count: total })}
              {year && <> · <span className="tabular-nums">{year}</span></>}
            </div>
          </div>
          <div className="text-[11.5px] text-tx-secondary tabular-nums whitespace-nowrap pr-2">
            {t('seriesDetail.seasons.onDisk', { on: onDisk, total })}
          </div>
        </div>
      </AccordionTrigger>
      <AccordionContent className="px-3 pt-1 pb-3">
        {episodes.length === 0 ? (
          <div className="text-[12px] text-tx-faint py-4 text-center">
            {t('seriesDetail.seasons.empty')}
          </div>
        ) : (
          <div className="flex flex-col gap-1">
            {episodes.map((ep, idx) => (
              <EpisodeRow
                key={ep.sonarr_episode_id ?? ep.episode_number ?? `idx-${idx}`}
                episode={ep}
                seasonNumber={seasonNumber}
              />
            ))}
          </div>
        )}
      </AccordionContent>
    </AccordionItem>
  );
}

export function SeasonsAccordion({
  instance, seriesId, seasons, lang, className, staleBadge,
}: SeasonsAccordionProps) {
  const { t } = useTranslation();
  const sorted = useMemo(() => sortSeasons(seasons ?? []), [seasons]);
  const [expanded, setExpanded] = useState<readonly string[]>([]);

  return (
    <section
      data-testid="seasons-accordion"
      aria-labelledby="seasons-accordion-heading"
      className={cn('flex flex-col gap-3 rounded-lg border border-border-faint bg-bg-surface/40 px-3 py-3', className)}
    >
      <h2
        id="seasons-accordion-heading"
        className="flex items-center gap-2 text-[10.5px] font-bold uppercase tracking-wide text-tx-faint px-1"
      >
        {t('seriesDetail.seasons.label')}
        {staleBadge}
      </h2>
      {sorted.length === 0 ? (
        <div className="text-[12px] text-tx-faint px-1 py-2">{t('seriesDetail.seasons.none')}</div>
      ) : (
        <Accordion
          type="multiple"
          value={expanded as string[]}
          onValueChange={(v: string[]) => setExpanded(v)}
          className="w-full"
        >
          {sorted.map((season) => {
            const sn = season.season_number ?? 0;
            return (
              <SeasonAccordionItem
                key={sn}
                instance={instance}
                seriesId={seriesId}
                season={season}
                {...(lang ? { lang } : {})}
                expanded={expanded.includes(`s${sn}`)}
              />
            );
          })}
        </Accordion>
      )}
    </section>
  );
}
