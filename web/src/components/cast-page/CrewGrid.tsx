import { useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import { cn } from '@/lib/utils';
import { mediaUrl } from '@/api/seriesDetail';
import { MonogramFallback } from '@/components/MonogramFallback';
import type { CrewPageMember } from '@/api/seriesCast';

export interface CrewGridProps {
  readonly crew: readonly CrewPageMember[];
  readonly className?: string | undefined;
}

interface CrewCard {
  readonly key: string;
  readonly tmdbId: number | undefined;
  readonly personId: number | undefined;
  readonly name: string;
  readonly profileAsset: string | undefined;
  readonly jobs: readonly string[];
  readonly episodes: number;
}

interface DepartmentBucket {
  readonly department: string;
  readonly cards: readonly CrewCard[];
}

// Group the flat crew slice (composer-sorted department→name) by department,
// folding multiple (person, job) rows from the same person inside the same
// department into a single card carrying every job.
function groupByDepartment(crew: readonly CrewPageMember[]): readonly DepartmentBucket[] {
  const order: string[] = [];
  const byDept = new Map<string, Map<string, CrewCard & { jobs: string[] }>>();

  for (const row of crew) {
    const dept = row.department ?? 'Other';
    const name = row.name ?? '';
    const personKey = row.person_id != null ? `p:${row.person_id}` : `n:${name}`;
    if (!byDept.has(dept)) {
      byDept.set(dept, new Map());
      order.push(dept);
    }
    const bucket = byDept.get(dept) as Map<string, CrewCard & { jobs: string[] }>;
    const existing = bucket.get(personKey);
    if (existing) {
      if (row.job && !existing.jobs.includes(row.job)) existing.jobs.push(row.job);
      if ((row.episode_count ?? 0) > existing.episodes) {
        (existing as { episodes: number }).episodes = row.episode_count ?? 0;
      }
      continue;
    }
    bucket.set(personKey, {
      key: `${dept}-${personKey}`,
      tmdbId: row.tmdb_id,
      personId: row.person_id,
      name,
      profileAsset: row.profile_asset,
      jobs: row.job ? [row.job] : [],
      episodes: row.episode_count ?? 0,
    });
  }

  return order.map((dept) => ({
    department: dept,
    cards: Array.from((byDept.get(dept) as Map<string, CrewCard>).values()),
  }));
}

export function CrewGrid({ crew, className }: CrewGridProps) {
  const { t } = useTranslation();
  const groups = useMemo(() => groupByDepartment(crew), [crew]);

  if (groups.length === 0) {
    return (
      <p
        data-testid="crew-grid-empty"
        className="text-[12.5px] text-tx-muted py-6 text-center"
      >
        {t('seriesDetail.castPage.empty.crew')}
      </p>
    );
  }

  return (
    <div data-testid="crew-grid" className={cn('flex flex-col gap-6', className)}>
      {groups.map((g) => (
        <section
          key={g.department}
          data-testid="crew-department"
          data-department={g.department}
          className="flex flex-col gap-3"
        >
          <h3 className="text-[11px] font-bold uppercase tracking-wide text-tx-faint">
            {g.department}
          </h3>
          <div className="grid gap-3 grid-cols-1 sm:grid-cols-2 md:grid-cols-3 lg:grid-cols-5">
            {g.cards.map((c) => {
              const src = mediaUrl(c.profileAsset);
              const topTwo = c.jobs.slice(0, 2).join(' · ');
              const allJobs = c.jobs.join(' · ');
              const titleAttr = c.jobs.length > 2 ? allJobs : undefined;

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
                      <MonogramFallback title={c.name} kind="avatar" />
                    )}
                  </div>
                  <div className="text-[12.5px] font-semibold text-tx-primary text-center w-full truncate">
                    {c.name}
                  </div>
                  {topTwo && (
                    <div
                      className="text-[11.5px] text-tx-muted text-center w-full truncate"
                      title={titleAttr}
                    >
                      {topTwo}
                    </div>
                  )}
                  {c.episodes > 0 && (
                    <div className="text-[11px] text-tx-faint tabular-nums">
                      {t('seriesDetail.cast.episodeCount', { count: c.episodes })}
                    </div>
                  )}
                </div>
              );

              return c.tmdbId ? (
                <Link
                  key={c.key}
                  data-testid="crew-grid-card"
                  data-tmdb-id={c.tmdbId}
                  to={`/person/${c.tmdbId}`}
                  className="block focus-visible:outline-hidden focus-visible:ring-2 focus-visible:ring-accent rounded-lg"
                >
                  {inner}
                </Link>
              ) : (
                <div key={c.key} data-testid="crew-grid-card" data-tmdb-id="">
                  {inner}
                </div>
              );
            })}
          </div>
        </section>
      ))}
    </div>
  );
}
