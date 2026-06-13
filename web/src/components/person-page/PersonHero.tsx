import { useTranslation } from 'react-i18next';
import { ExternalLink } from 'lucide-react';
import { cn } from '@/lib/utils';
import { mediaUrl } from '@/api/seriesDetail';
import { useFormatDate } from '@/lib/timezone';
import type { PersonInfo } from '@/api/person';

export interface PersonHeroProps {
  readonly person: PersonInfo | undefined;
  readonly className?: string | undefined;
}

function initials(name: string | undefined): string {
  if (!name) return '?';
  const parts = name.trim().split(/\s+/).slice(0, 2);
  return parts.map((p) => p.charAt(0).toUpperCase()).join('') || '?';
}

function computeAge(birthday: string | undefined, deathday: string | undefined): number | null {
  if (!birthday) return null;
  const b = new Date(birthday);
  if (Number.isNaN(b.getTime())) return null;
  const end = deathday ? new Date(deathday) : new Date();
  if (Number.isNaN(end.getTime())) return null;
  let age = end.getFullYear() - b.getFullYear();
  const m = end.getMonth() - b.getMonth();
  if (m < 0 || (m === 0 && end.getDate() < b.getDate())) age--;
  return age >= 0 ? age : null;
}

interface ExternalLinkSpec {
  readonly key: 'imdb' | 'tmdb' | 'homepage' | 'wikipedia';
  readonly href: string;
}

function buildLinks(person: PersonInfo | undefined): readonly ExternalLinkSpec[] {
  const out: ExternalLinkSpec[] = [];
  // IMDb / Homepage / Wikipedia are not in PersonInfo today; the
  // component renders them when H-2 adds the fields in a follow-up.
  if (person?.tmdb_id) {
    out.push({ key: 'tmdb', href: `https://www.themoviedb.org/person/${person.tmdb_id}` });
  }
  return out;
}

export function PersonHero({ person, className }: PersonHeroProps) {
  const { t } = useTranslation();
  const fmt = useFormatDate();
  const fmtBirth = (iso: string | undefined): string =>
    iso ? fmt(iso, 'mediumDate', { fallback: iso }) : '';
  const name = person?.name ?? '';
  const photo = mediaUrl(person?.profile_asset);
  const known = person?.known_for_department;
  const birthday = person?.birthday;
  const deathday = person?.deathday;
  const place = person?.place_of_birth;
  const age = computeAge(birthday, deathday);
  const links = buildLinks(person);

  const bornLine = birthday
    ? (place
        ? t('person.bornIn', { date: fmtBirth(birthday), place })
        : t('person.bornOn', { date: fmtBirth(birthday) }))
    : '';

  return (
    <header
      data-testid="person-hero"
      className={cn(
        'flex flex-col md:flex-row gap-4 md:gap-6 items-start',
        className,
      )}
    >
      <div
        className="shrink-0 w-full max-w-[240px] aspect-[2/3] rounded-lg overflow-hidden border border-border-subtle bg-bg-surface-2"
        data-testid="person-hero-photo"
      >
        {photo ? (
          <img
            src={photo}
            alt={name}
            loading="lazy"
            decoding="async"
            className="w-full h-full object-cover"
          />
        ) : (
          <span className="flex items-center justify-center w-full h-full text-[44px] font-bold text-tx-faint">
            {initials(name)}
          </span>
        )}
      </div>

      <div className="flex flex-col gap-2 min-w-0 flex-1">
        <h1
          data-testid="person-hero-name"
          className="text-[22px] md:text-[26px] font-semibold text-tx-primary leading-tight"
        >
          {name}
        </h1>

        {known && (
          <div className="flex items-center gap-2">
            <span
              data-testid="person-known-for"
              className="inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium bg-accent/10 text-accent border border-accent/30"
            >
              {t('person.knownFor', { department: known })}
            </span>
          </div>
        )}

        {(bornLine || deathday || age != null) && (
          <div className="text-[12.5px] text-tx-muted flex flex-wrap items-center gap-x-2 gap-y-1 tabular-nums">
            {bornLine && <span data-testid="person-born">{bornLine}</span>}
            {age != null && (
              <>
                <span aria-hidden="true">·</span>
                <span data-testid="person-age">{t('person.age', { count: age })}</span>
              </>
            )}
            {deathday && (
              <>
                <span aria-hidden="true">·</span>
                <span data-testid="person-died" className="text-rose-500">
                  {t('person.diedOn', { date: fmtBirth(deathday) })}
                </span>
              </>
            )}
          </div>
        )}

        {links.length > 0 && (
          <div
            data-testid="person-external-links"
            className="flex flex-wrap items-center gap-2 pt-1"
          >
            {links.map((l) => (
              <a
                key={l.key}
                href={l.href}
                target="_blank"
                rel="noreferrer noopener"
                className="inline-flex items-center gap-1 text-[12px] text-tx-muted hover:text-accent transition-colors"
                data-testid={`person-link-${l.key}`}
              >
                {t(`person.links.${l.key}`)}
                <ExternalLink className="w-3 h-3" aria-hidden="true" />
              </a>
            ))}
          </div>
        )}
      </div>
    </header>
  );
}
