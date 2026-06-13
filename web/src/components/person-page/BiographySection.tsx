import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import type { TFunction } from 'i18next';
import { cn } from '@/lib/utils';
import type { PersonSyncInfo } from '@/api/person';

export interface BiographySectionProps {
  readonly biography: string | undefined;
  readonly bioLanguage: string | undefined;
  readonly uiLanguage: string | undefined;
  readonly sync: PersonSyncInfo | undefined;
  readonly className?: string | undefined;
}

function formatRelative(t: TFunction, iso: string | undefined): string {
  if (!iso) return '';
  const ts = new Date(iso).getTime();
  if (Number.isNaN(ts)) return '';
  const delta = Math.max(0, Date.now() - ts);
  const mins = Math.round(delta / 60_000);
  if (mins < 1) return t('person.relTime.justNow');
  if (mins < 60) return t('person.relTime.minutesAgo', { count: mins });
  const hours = Math.round(mins / 60);
  if (hours < 24) return t('person.relTime.hoursAgo', { count: hours });
  const days = Math.round(hours / 24);
  return t('person.relTime.daysAgo', { count: days });
}

function shouldShowEnFallback(bioLang: string | undefined, uiLang: string | undefined): boolean {
  if (!bioLang) return false;
  const ui = (uiLang ?? '').toLowerCase().slice(0, 2);
  const bio = bioLang.toLowerCase().slice(0, 2);
  return ui !== '' && bio !== '' && ui !== bio;
}

export function BiographySection({
  biography,
  bioLanguage,
  uiLanguage,
  sync,
  className,
}: BiographySectionProps) {
  const { t } = useTranslation();
  const [expanded, setExpanded] = useState(false);

  if (!biography || biography.trim().length === 0) return null;

  const showEn = shouldShowEnFallback(bioLanguage, uiLanguage);
  const rel = formatRelative(t, sync?.synced_at);

  return (
    <section
      data-testid="person-biography"
      className={cn('flex flex-col gap-2', className)}
    >
      <div className="flex items-center gap-2">
        <h2 className="text-[15px] font-semibold text-tx-primary">
          {t('person.bio.heading')}
        </h2>
        {showEn && (
          <span
            data-testid="person-bio-en-chip"
            className="inline-flex items-center px-1.5 py-0.5 rounded text-[10px] font-bold uppercase tracking-wide bg-bg-surface-2 text-tx-muted border border-border-subtle"
          >
            {t('person.bio.langFallback')}
          </span>
        )}
      </div>

      <p
        data-testid="person-bio-text"
        className={cn(
          'text-[13px] leading-relaxed text-tx-secondary whitespace-pre-wrap',
          !expanded && 'line-clamp-4',
        )}
      >
        {biography}
      </p>

      <button
        type="button"
        onClick={() => setExpanded((v) => !v)}
        className="self-start text-[12px] text-accent hover:underline focus-visible:outline-hidden focus-visible:ring-2 focus-visible:ring-accent rounded"
        data-testid="person-bio-toggle"
      >
        {expanded ? t('person.bio.readLess') : t('person.bio.readMore')}
      </button>

      {rel && (
        <div
          data-testid="person-bio-source"
          className="text-[11px] text-tx-faint font-mono tracking-tight"
        >
          {t('person.bio.source', { rel })}
        </div>
      )}
    </section>
  );
}
