import { useState, useMemo } from 'react';
import type { ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import type { TFunction } from 'i18next';
import { ArrowDown } from 'lucide-react';
import { cn } from '@/lib/utils';
import {
  Accordion,
  AccordionContent,
  AccordionItem,
  AccordionTrigger,
} from '@/components/ui/accordion';
import { Skeleton } from '@/components/ui/skeleton';
import { mediaUrl } from '@/api/series';
import { useSeriesSeason } from '@/api/seriesSeason';
import type { components } from '@/api/schema';
import { EpisodeRow } from './EpisodeRow';

type Season = components['schemas']['dto.Season'];

// --- Season label resolution (bug 973) -------------------------------------
//
// The BE /seasons + /season/:n endpoints fall back to a season's *canon* name
// when the requested language has no season_texts row. That canon name is
// whatever language enriched it first — frequently Russian ("Сезон 4") even
// under lang=en-US — so rendering it verbatim leaks the wrong locale and is
// inconsistent across series. We normalise any plain numbered/specials name to
// the i18n label built from season_number (driven by the UI locale, not the
// leaked DTO string) and render only a GENUINE custom title verbatim.
//
// Covered locales include the two the deployment serves (en, ru) plus common
// European + CJK TMDB languages, so a canon row enriched by any of them still
// normalises. A name matching none is treated as custom.
const NUMBERED_SEASON_RE =
  /^(?:season|сезон|staffel|saison|temporada|stagione|série|series|시즌|シーズン)\s*0*\d+$/iu;

const SPECIALS_NAMES: ReadonlySet<string> = new Set([
  'specials', 'special',
  'спецвыпуски', 'спецматериалы',
  'spezials',
  'spéciaux', 'épisodes spéciaux',
  'especiales', 'speciali', 'especiais',
]);

function isPlainNumberedName(name: string): boolean {
  return NUMBERED_SEASON_RE.test(name) || SPECIALS_NAMES.has(name.toLowerCase());
}

/**
 * resolveSeasonLabel — the human label for a season accordion row (bug 973).
 *
 * - season 0 → localized "Specials" label (season 0 IS specials).
 * - empty name OR a plain numbered/specials name (any covered locale, incl. a
 *   RU-leaked "Сезон 4") → localized "Season {n}" from season_number.
 * - genuine custom title → rendered verbatim (e.g. "Book One: Water").
 */
export function resolveSeasonLabel(
  season: Pick<Season, 'season_number' | 'name'>,
  t: TFunction,
): string {
  const seasonNumber = season.season_number ?? 0;
  if (seasonNumber === 0) return t('seriesDetail.seasons.specials');
  const name = (season.name ?? '').trim();
  if (name === '' || isPlainNumberedName(name)) {
    return t('seriesDetail.seasons.season', { n: seasonNumber });
  }
  return name;
}

// Per-season library counts sourced from GET /series/:id/library (NOT the
// canonical /seasons summary). Keyed by season_number. Absent entry ⇒ the
// season's library state is unknown → render totals only (no misleading 0/8).
export interface LibrarySeasonCounts {
  readonly onDisk: number;
  readonly downloading: number;
}

export interface SeasonsAccordionProps {
  readonly seriesId: number;
  readonly seasons: readonly Season[] | undefined;
  readonly lang?: string | undefined;
  readonly className?: string | undefined;
  // Optional badge rendered inline with the section heading
  // (used for per-section StaleBadge wire-up from SeriesDetail).
  readonly staleBadge?: ReactNode;
  // Story 495 / N-1e (B-20): when true AND `seasons` is empty, render
  // 5 skeleton rows + loading label instead of the "Сезоны пока
  // недоступны" fallback. Distinguishes "TMDB enrichment in flight"
  // from "TMDB returned no data".
  readonly tmdbSeasonLoading?: boolean | undefined;
  // Story 970 / C3c-2 — per-season on-disk / downloading counts from the
  // /library endpoint (per-instance). Keyed by season_number. When a season has
  // no entry (map miss), the row renders only the canonical episode_count and
  // omits the "X/total on disk" line — never "0/total".
  readonly librarySeasons?: ReadonlyMap<number, LibrarySeasonCounts> | undefined;
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
  readonly seriesId: number;
  readonly season: Season;
  readonly lang?: string | undefined;
  readonly expanded: boolean;
  readonly libEntry?: LibrarySeasonCounts | undefined;
}

function SeasonAccordionItem({
  seriesId, season, lang, expanded, libEntry,
}: SeasonAccordionItemProps) {
  const { t } = useTranslation();
  const seasonNumber = season.season_number ?? 0;
  const lazy = useSeriesSeason({
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
  // Story 970: per-season library counts come from the /library endpoint,
  // threaded via `libEntry`. `undefined` ⇒ library state unknown for this
  // season → show totals only, never "0/total".
  const total = season.episode_count ?? episodes.length;
  const onDisk = libEntry?.onDisk;
  const downloading = libEntry?.downloading ?? 0;
  const year = seasonYear(season.air_date);
  const posterSrc = mediaUrl(season.poster_asset);
  const seasonLabel = resolveSeasonLabel(season, t);

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
          <div className="flex items-center gap-2 pr-2 whitespace-nowrap">
            {downloading > 0 && (
              <span
                data-testid="season-downloading-chip"
                data-season={seasonNumber}
                className="inline-flex items-center gap-1 px-1.5 py-0.5 rounded text-[11px] font-medium border border-border-faint bg-bg-surface-2 text-tx-secondary tabular-nums"
              >
                <ArrowDown className="w-3 h-3" aria-hidden="true" />
                {t('seriesDetail.seasons.downloading', { count: downloading })}
              </span>
            )}
            {onDisk !== undefined && (
              <div
                data-testid="season-on-disk"
                data-season={seasonNumber}
                className="text-[11.5px] text-tx-secondary tabular-nums"
              >
                {t('seriesDetail.seasons.onDisk', { on: onDisk, total })}
              </div>
            )}
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
  seriesId, seasons, lang, className, staleBadge, tmdbSeasonLoading, librarySeasons,
}: SeasonsAccordionProps) {
  const { t } = useTranslation();
  const sorted = useMemo(() => sortSeasons(seasons ?? []), [seasons]);
  const [expanded, setExpanded] = useState<readonly string[]>([]);
  const showLoading = sorted.length === 0 && Boolean(tmdbSeasonLoading);

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
        {showLoading && (
          <span
            data-testid="seasons-loading-label"
            className="ml-2 text-[10px] font-normal normal-case tracking-normal text-tx-muted"
          >
            {t('seriesDetail.degraded.seasons.loading')}
          </span>
        )}
      </h2>
      {showLoading ? (
        <div className="flex flex-col gap-2 px-1">
          {Array.from({ length: 5 }).map((_, i) => (
            <div
              key={i}
              data-testid="seasons-skeleton-row"
              className="flex items-center gap-3 rounded-md px-3 py-2.5"
            >
              <Skeleton className="w-10 h-[60px] rounded shrink-0" />
              <div className="flex flex-col gap-1.5 flex-1 min-w-0">
                <Skeleton className="h-3.5 w-[40%]" />
                <Skeleton className="h-3 w-[60%]" />
              </div>
              <Skeleton className="h-3 w-16" />
            </div>
          ))}
        </div>
      ) : sorted.length === 0 ? (
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
                seriesId={seriesId}
                season={season}
                {...(lang ? { lang } : {})}
                expanded={expanded.includes(`s${sn}`)}
                {...(librarySeasons?.get(sn) ? { libEntry: librarySeasons.get(sn) } : {})}
              />
            );
          })}
        </Accordion>
      )}
    </section>
  );
}
