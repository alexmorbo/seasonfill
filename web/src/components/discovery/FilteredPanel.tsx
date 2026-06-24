import { useTranslation } from 'react-i18next';
import {
  useDiscoveryGenresList, useDiscoveryNetworksList,
  type DiscoveryFilter,
} from '@/api/discovery';
import {
  Select, SelectTrigger, SelectValue, SelectContent, SelectItem,
} from '@/components/ui/select';
import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import { cn } from '@/lib/utils';
import type { UseDiscoverFilterResult } from '@/hooks/useDiscoverFilter';

// Story 516 / N-3d: ad-hoc Discover filter UI. Pushes patches into
// the URL-synced hook on every change so deep-linking just works.

const YEAR_MIN = 1950;
const YEAR_MAX = new Date().getFullYear();
const RATING_MIN = 0;
const RATING_MAX = 10;

// Static enum lists kept short — i18n labels come from the panel.
const COUNTRIES = ['US', 'GB', 'JP', 'KR', 'FR', 'ES', 'DE', 'CN', 'IN'] as const;
const STATUSES = [
  'returning', 'ended', 'canceled', 'in_production', 'planned', 'pilot',
] as const;
const TYPES = [
  'scripted', 'documentary', 'miniseries', 'reality', 'news', 'talk', 'video',
] as const;
const SORTS = [
  { v: 'popularity.desc',     k: 'popularity_desc' },
  { v: 'popularity.asc',      k: 'popularity_asc' },
  { v: 'vote_average.desc',   k: 'vote_desc' },
  { v: 'first_air_date.desc', k: 'first_air_desc' },
  { v: 'first_air_date.asc',  k: 'first_air_asc' },
] as const;
const ANY_COUNTRY = '__any__';

const CHIP_BASE = cn(
  'inline-flex items-center rounded-full border px-3 py-1 text-[12.5px] font-medium',
  'transition-colors focus-visible:outline-hidden focus-visible:ring-2 focus-visible:ring-accent',
);
const CHIP_ACTIVE = 'border-accent bg-accent/15 text-accent';
const CHIP_IDLE   = 'border-border-faint text-tx-muted hover:text-tx-primary';

// Year/rating bounds derived from current filter — fall back to defaults.
function yearRange(f: DiscoveryFilter): [number, number] {
  const lo = f.first_air_date_gte ? Number(f.first_air_date_gte.slice(0, 4)) : YEAR_MIN;
  const hi = f.first_air_date_lte ? Number(f.first_air_date_lte.slice(0, 4)) : YEAR_MAX;
  return [
    Number.isFinite(lo) ? Math.max(YEAR_MIN, lo) : YEAR_MIN,
    Number.isFinite(hi) ? Math.min(YEAR_MAX, hi) : YEAR_MAX,
  ];
}
function ratingRange(f: DiscoveryFilter): [number, number] {
  return [
    typeof f.vote_average_gte === 'number' ? f.vote_average_gte : RATING_MIN,
    typeof f.vote_average_lte === 'number' ? f.vote_average_lte : RATING_MAX,
  ];
}

function toggleIn<T>(arr: readonly T[] | undefined, value: T): T[] {
  const has = arr?.includes(value) ?? false;
  if (has) return (arr ?? []).filter((x) => x !== value);
  return [...(arr ?? []), value];
}

export interface FilteredPanelProps {
  readonly state: UseDiscoverFilterResult;
}

export function FilteredPanel({ state }: FilteredPanelProps) {
  const { t } = useTranslation();
  const { filter, setFilter, clearFilter, hasActiveFilter } = state;
  const genresQ = useDiscoveryGenresList();
  const networksQ = useDiscoveryNetworksList();
  const [yLo, yHi] = yearRange(filter);
  const [rLo, rHi] = ratingRange(filter);

  const patchYears = (lo: number, hi: number) => {
    const cleanLo = lo === YEAR_MIN ? undefined : `${lo}-01-01`;
    const cleanHi = hi === YEAR_MAX ? undefined : `${hi}-12-31`;
    setFilter({ first_air_date_gte: cleanLo, first_air_date_lte: cleanHi });
  };
  const patchRating = (lo: number, hi: number) => {
    const cleanLo = lo === RATING_MIN ? undefined : lo;
    const cleanHi = hi === RATING_MAX ? undefined : hi;
    setFilter({ vote_average_gte: cleanLo, vote_average_lte: cleanHi });
  };

  return (
    <section
      data-testid="discovery-filtered-panel"
      className="rounded-md border border-border-faint bg-bg-surface-1 p-4 space-y-5"
    >
      <div className="grid gap-5 lg:grid-cols-2">
        <div>
          <div className="mb-2 text-[12px] font-semibold uppercase tracking-wide text-tx-muted">
            {t('discovery.filter.genres')}
          </div>
          {genresQ.isPending ? (
            <div className="flex flex-wrap gap-2" data-testid="discovery-filter-genres-skeleton">
              {Array.from({ length: 6 }).map((_, i) =>
                <Skeleton key={i} className="h-7 w-20 rounded-full" />)}
            </div>
          ) : (
            <div className="flex flex-wrap gap-2" data-testid="discovery-filter-genres">
              {(genresQ.data?.items ?? []).map((g) => {
                const active = filter.with_genres?.includes(g.id) ?? false;
                return (
                  <button
                    key={g.id} type="button" aria-pressed={active}
                    data-testid="discovery-filter-genre-chip"
                    data-genre-id={g.id}
                    onClick={() => setFilter({
                      with_genres: toggleIn(filter.with_genres, g.id),
                    })}
                    className={cn(CHIP_BASE, active ? CHIP_ACTIVE : CHIP_IDLE)}
                  >{g.name}</button>
                );
              })}
            </div>
          )}
        </div>

        <div>
          <div className="mb-2 text-[12px] font-semibold uppercase tracking-wide text-tx-muted">
            {t('discovery.filter.networks')}
          </div>
          {networksQ.isPending ? (
            <div className="flex flex-wrap gap-2" data-testid="discovery-filter-networks-skeleton">
              {Array.from({ length: 6 }).map((_, i) =>
                <Skeleton key={i} className="h-7 w-24 rounded-full" />)}
            </div>
          ) : (
            <div className="flex flex-wrap gap-2" data-testid="discovery-filter-networks">
              {(networksQ.data?.items ?? []).map((n) => {
                const active = filter.with_networks?.includes(n.id) ?? false;
                return (
                  <button
                    key={n.id} type="button" aria-pressed={active}
                    data-testid="discovery-filter-network-chip"
                    data-network-id={n.id}
                    onClick={() => setFilter({
                      with_networks: toggleIn(filter.with_networks, n.id),
                    })}
                    className={cn(CHIP_BASE, active ? CHIP_ACTIVE : CHIP_IDLE)}
                  >{n.name}</button>
                );
              })}
            </div>
          )}
        </div>

        <div>
          <div className="mb-2 text-[12px] font-semibold uppercase tracking-wide text-tx-muted">
            {t('discovery.filter.year_range')} ({yLo} – {yHi})
          </div>
          <div className="flex items-center gap-3">
            <input
              type="range" min={YEAR_MIN} max={YEAR_MAX} value={yLo}
              aria-label="year-min" data-testid="discovery-filter-year-min"
              onChange={(e) => {
                const lo = Math.min(Number(e.target.value), yHi);
                patchYears(lo, yHi);
              }}
              className="flex-1 accent-accent"
            />
            <input
              type="range" min={YEAR_MIN} max={YEAR_MAX} value={yHi}
              aria-label="year-max" data-testid="discovery-filter-year-max"
              onChange={(e) => {
                const hi = Math.max(Number(e.target.value), yLo);
                patchYears(yLo, hi);
              }}
              className="flex-1 accent-accent"
            />
          </div>
        </div>

        <div>
          <div className="mb-2 text-[12px] font-semibold uppercase tracking-wide text-tx-muted">
            {t('discovery.filter.rating_range')} ({rLo.toFixed(1)} – {rHi.toFixed(1)})
          </div>
          <div className="flex items-center gap-3">
            <input
              type="range" min={RATING_MIN} max={RATING_MAX} step={0.5} value={rLo}
              aria-label="rating-min" data-testid="discovery-filter-rating-min"
              onChange={(e) => {
                const lo = Math.min(Number(e.target.value), rHi);
                patchRating(lo, rHi);
              }}
              className="flex-1 accent-accent"
            />
            <input
              type="range" min={RATING_MIN} max={RATING_MAX} step={0.5} value={rHi}
              aria-label="rating-max" data-testid="discovery-filter-rating-max"
              onChange={(e) => {
                const hi = Math.max(Number(e.target.value), rLo);
                patchRating(rLo, hi);
              }}
              className="flex-1 accent-accent"
            />
          </div>
        </div>

        <div>
          <div className="mb-2 text-[12px] font-semibold uppercase tracking-wide text-tx-muted">
            {t('discovery.filter.origin_country')}
          </div>
          <Select
            value={filter.with_origin_country?.[0] ?? ANY_COUNTRY}
            onValueChange={(v) => setFilter({
              with_origin_country: v === ANY_COUNTRY ? undefined : [v],
            })}
          >
            <SelectTrigger
              data-testid="discovery-filter-country"
              aria-label={t('discovery.filter.origin_country')}
            >
              <SelectValue placeholder={t('discovery.filter.any_country')} />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value={ANY_COUNTRY}>
                {t('discovery.filter.any_country')}
              </SelectItem>
              {COUNTRIES.map((c) => (
                <SelectItem key={c} value={c}>{c}</SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>

        <div>
          <div className="mb-2 text-[12px] font-semibold uppercase tracking-wide text-tx-muted">
            {t('discovery.filter.sort_by')}
          </div>
          <Select
            value={filter.sort_by ?? 'popularity.desc'}
            onValueChange={(v) => setFilter({
              sort_by: v === 'popularity.desc' ? undefined : v,
            })}
          >
            <SelectTrigger
              data-testid="discovery-filter-sort"
              aria-label={t('discovery.filter.sort_by')}
            >
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {SORTS.map(({ v, k }) => (
                <SelectItem key={v} value={v}>{t(`discovery.sort.${k}`)}</SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>

        <div>
          <div className="mb-2 text-[12px] font-semibold uppercase tracking-wide text-tx-muted">
            {t('discovery.filter.status')}
          </div>
          <div className="flex flex-wrap gap-2" data-testid="discovery-filter-statuses">
            {STATUSES.map((s) => {
              const active = filter.with_status?.includes(s) ?? false;
              return (
                <button
                  key={s} type="button" aria-pressed={active}
                  data-testid="discovery-filter-status-chip" data-status={s}
                  onClick={() => setFilter({
                    with_status: toggleIn(filter.with_status, s),
                  })}
                  className={cn(CHIP_BASE, active ? CHIP_ACTIVE : CHIP_IDLE)}
                >{t(`discovery.status.${s}`)}</button>
              );
            })}
          </div>
        </div>

        <div>
          <div className="mb-2 text-[12px] font-semibold uppercase tracking-wide text-tx-muted">
            {t('discovery.filter.type')}
          </div>
          <div className="flex flex-wrap gap-2" data-testid="discovery-filter-types">
            {TYPES.map((s) => {
              const active = filter.with_type?.includes(s) ?? false;
              return (
                <button
                  key={s} type="button" aria-pressed={active}
                  data-testid="discovery-filter-type-chip" data-type={s}
                  onClick={() => setFilter({
                    with_type: toggleIn(filter.with_type, s),
                  })}
                  className={cn(CHIP_BASE, active ? CHIP_ACTIVE : CHIP_IDLE)}
                >{t(`discovery.type.${s}`)}</button>
              );
            })}
          </div>
        </div>
      </div>

      <div className="flex justify-end gap-2 pt-2 border-t border-border-faint">
        <Button
          type="button" variant="ghost" size="sm" onClick={clearFilter}
          data-testid="discovery-filter-clear" disabled={!hasActiveFilter}
        >{t('discovery.filter.clear')}</Button>
      </div>
    </section>
  );
}
