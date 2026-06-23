import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { ExternalLink } from 'lucide-react';
import { cn } from '@/lib/utils';
import { Switch } from '@/components/ui/switch';
import { Button } from '@/components/ui/button';
import type { OtherCreditEntry, OtherSort } from '@/api/person';
import { mediaUrl } from '@/api/series';
import { OtherSortControl } from './OtherSortControl';

const INITIAL_LIMIT = 10;

export interface OtherCreditsGridProps {
  readonly credits: readonly OtherCreditEntry[];
  readonly className?: string | undefined;
}

function tmdbHref(c: OtherCreditEntry): string {
  const kind = c.media_type === 'movie' ? 'movie' : 'tv';
  return `https://www.themoviedb.org/${kind}/${c.tmdb_media_id ?? ''}`;
}

function sortOtherCredits(
  rows: readonly OtherCreditEntry[],
  sort: OtherSort,
): readonly OtherCreditEntry[] {
  if (sort === 'recent') return rows;
  const copy = rows.slice();
  if (sort === 'votes_desc') {
    copy.sort((a, b) => {
      const av = a.vote_count ?? -1;
      const bv = b.vote_count ?? -1;
      if (bv !== av) return bv - av;
      // Tie-break on year DESC to keep deterministic order.
      const ay = a.year ?? 0;
      const by = b.year ?? 0;
      return by - ay;
    });
    return copy;
  }
  // title_asc
  copy.sort((a, b) => (a.title ?? '').localeCompare(b.title ?? ''));
  return copy;
}

function formatVotes(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}k`;
  return String(n);
}

export function OtherCreditsGrid({ credits, className }: OtherCreditsGridProps) {
  const { t } = useTranslation();
  const [includeMovies, setIncludeMovies] = useState(false);
  const [expanded, setExpanded] = useState(false);
  const [sort, setSort] = useState<OtherSort>('recent');

  const filtered = useMemo(
    () => credits.filter((c) => includeMovies || c.media_type === 'tv'),
    [credits, includeMovies],
  );

  const sorted = useMemo(() => sortOtherCredits(filtered, sort), [filtered, sort]);

  if (filtered.length === 0) return null;

  const visible = expanded ? sorted : sorted.slice(0, INITIAL_LIMIT);
  const hasMore = sorted.length > INITIAL_LIMIT && !expanded;

  return (
    <section
      data-testid="person-other-section"
      className={cn('flex flex-col gap-3', className)}
    >
      <div className="flex items-center justify-between gap-3 flex-wrap">
        <h2 className="text-[15px] font-semibold text-tx-primary">
          {t('person.other.heading', { count: Math.min(sorted.length, INITIAL_LIMIT) })}
        </h2>
        <div className="flex items-center gap-3 flex-wrap">
          <OtherSortControl value={sort} onChange={setSort} />
          <label className="flex items-center gap-2 text-[12.5px] text-tx-muted">
            <Switch
              checked={includeMovies}
              onCheckedChange={setIncludeMovies}
              data-testid="person-include-movies"
            />
            <span>{t('person.other.includeMovies')}</span>
          </label>
        </div>
      </div>

      <div
        data-testid="person-other-grid"
        className="grid gap-3 grid-cols-1 sm:grid-cols-2 md:grid-cols-3 lg:grid-cols-5"
      >
        {visible.map((c, idx) => {
          const src = mediaUrl(c.poster_asset);
          const role = c.role_label ?? c.character_name ?? '';
          const titleYear = c.year ? `${c.title ?? ''} · ${c.year}` : (c.title ?? '');
          const key = `${c.tmdb_media_id ?? 'x'}-${c.media_type ?? 'tv'}-${idx}`;
          const originalTitleDiffers =
            c.original_title != null
            && c.original_title.trim().toLowerCase()
              !== (c.title ?? '').trim().toLowerCase();
          const showVotesChip = sort === 'votes_desc' && c.vote_count != null;
          const showDeptPill = c.kind === 'crew' && c.department != null && c.department !== '';

          return (
            <a
              key={key}
              href={tmdbHref(c)}
              target="_blank"
              rel="noreferrer noopener"
              data-testid="person-other-card"
              data-media-type={c.media_type ?? 'tv'}
              data-vote-count={c.vote_count ?? ''}
              className="group block focus-visible:outline-hidden focus-visible:ring-2 focus-visible:ring-accent rounded-lg"
            >
              <div className="flex flex-col gap-1.5 p-2 rounded-lg border border-border-subtle bg-bg-surface hover:border-accent/40 transition-colors h-full relative">
                <div className="aspect-[2/3] w-full rounded overflow-hidden bg-bg-surface-2 border border-border-subtle">
                  {src && (
                    <img
                      src={src}
                      alt=""
                      aria-hidden="true"
                      loading="lazy"
                      decoding="async"
                      className="w-full h-full object-cover"
                    />
                  )}
                </div>
                {showVotesChip && (
                  <span
                    data-testid="person-other-votes-chip"
                    className="absolute top-2 left-2 inline-flex items-center text-[10.5px] font-semibold bg-bg-surface/90 text-tx-primary px-1.5 py-0.5 rounded border border-border-subtle"
                  >
                    {formatVotes(c.vote_count as number)}
                  </span>
                )}
                <div className="text-[12.5px] font-semibold text-tx-primary truncate" title={c.title}>
                  {titleYear}
                </div>
                {role && (
                  <div className="flex items-center gap-1.5 text-[11.5px] text-tx-muted">
                    <span className="truncate" title={role}>{role}</span>
                    {showDeptPill && (
                      <span
                        data-testid="person-other-dept-pill"
                        data-department={c.department}
                        className="shrink-0 text-[10px] uppercase tracking-wide bg-bg-surface-2 border border-border-subtle rounded px-1.5 py-0.5"
                      >
                        {c.department}
                      </span>
                    )}
                  </div>
                )}
                {originalTitleDiffers && (
                  <div
                    data-testid="person-other-original-title"
                    className="text-[11px] italic text-tx-faint truncate"
                    title={c.original_title}
                  >
                    {c.original_title}
                  </div>
                )}
                <span className="opacity-0 group-hover:opacity-100 transition-opacity absolute top-2 right-2 inline-flex items-center gap-1 text-[10.5px] text-accent bg-bg-surface/90 px-1.5 py-0.5 rounded border border-accent/30">
                  {t('person.other.openInTmdb')}
                  <ExternalLink className="w-3 h-3" aria-hidden="true" />
                </span>
              </div>
            </a>
          );
        })}
      </div>

      {hasMore && (
        <div className="flex justify-center pt-2">
          <Button
            variant="outline"
            size="sm"
            onClick={() => setExpanded(true)}
            data-testid="person-other-show-more"
          >
            {t('person.other.showMore')}
          </Button>
        </div>
      )}
    </section>
  );
}
