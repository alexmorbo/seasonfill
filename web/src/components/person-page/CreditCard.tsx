import type { ReactNode } from 'react';
import { Link } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { cn } from '@/lib/utils';
import { mediaUrl } from '@/api/series';

export type CreditBadge = 'inLibrary' | 'tmdbOnly';

export type CreditLinkTarget =
  | { readonly kind: 'internal'; readonly to: string }
  | { readonly kind: 'tmdb'; readonly href: string }
  | { readonly kind: 'none' };

export interface CreditCardProps {
  readonly title: string;
  readonly year?: number | undefined;
  readonly role?: string | undefined;
  readonly posterAsset?: string | null | undefined;
  readonly badge?: CreditBadge | undefined;
  readonly link: CreditLinkTarget;
  /** Optional footer slot — instance label for library, dept pill for crew TMDB. */
  readonly footer?: ReactNode | undefined;
  /** Optional overlay slot rendered on top of the poster (votes chip, TMDB hover chevron). */
  readonly overlay?: ReactNode | undefined;
  /** Optional subtitle slot below role (e.g. original_title italic). */
  readonly subtitle?: ReactNode | undefined;
  readonly testId: string;
  readonly dataAttrs?: Record<string, string | number | undefined | null> | undefined;
}

/**
 * CreditCard is the shared visual primitive consumed by both
 * LibraryCreditsGrid and OtherCreditsGrid. Story 537 (B-42e).
 *
 * It centralises the poster + title-year + role-chip + badge
 * layout so the two grids stay visually consistent after the
 * library/TMDB split, and so the link-kind branching
 * (internal vs external vs none) is implemented in ONE place.
 */
export function CreditCard({
  title,
  year,
  role,
  posterAsset,
  badge,
  link,
  footer,
  overlay,
  subtitle,
  testId,
  dataAttrs,
}: CreditCardProps) {
  const { t } = useTranslation();
  const src = mediaUrl(posterAsset ?? undefined);
  const titleYear = year ? `${title} · ${year}` : title;

  const inner = (
    <div className="flex flex-col gap-1.5 p-2 rounded-lg border border-border-subtle bg-bg-surface hover:border-accent/40 transition-colors h-full relative">
      <div className="aspect-[2/3] w-full rounded overflow-hidden bg-bg-surface-2 border border-border-subtle relative">
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
        {badge && (
          <span
            data-testid={`${testId}-badge`}
            data-badge={badge}
            className={cn(
              'absolute top-2 left-2 inline-flex items-center text-[10px] font-semibold px-1.5 py-0.5 rounded border backdrop-blur-sm uppercase tracking-wide',
              badge === 'inLibrary'
                ? 'bg-accent/15 text-accent border-accent/40'
                : 'bg-bg-surface/85 text-tx-muted border-border-subtle',
            )}
          >
            {t(`person.badges.${badge}`)}
          </span>
        )}
        {overlay}
      </div>
      <div
        className="text-[12.5px] font-semibold text-tx-primary truncate"
        title={title}
      >
        {titleYear}
      </div>
      {role && (
        <div className="text-[11.5px] text-tx-muted truncate" title={role}>
          {role}
        </div>
      )}
      {subtitle}
      {footer}
    </div>
  );

  const dataProps: Record<string, string> = {};
  if (dataAttrs) {
    for (const [k, v] of Object.entries(dataAttrs)) {
      if (v !== undefined && v !== null && v !== '') {
        dataProps[`data-${k}`] = String(v);
      }
    }
  }

  if (link.kind === 'internal') {
    return (
      <Link
        to={link.to}
        data-testid={testId}
        className="block focus-visible:outline-hidden focus-visible:ring-2 focus-visible:ring-accent rounded-lg"
        {...dataProps}
      >
        {inner}
      </Link>
    );
  }
  if (link.kind === 'tmdb') {
    return (
      <a
        href={link.href}
        target="_blank"
        rel="noreferrer noopener"
        data-testid={testId}
        className="block focus-visible:outline-hidden focus-visible:ring-2 focus-visible:ring-accent rounded-lg group"
        {...dataProps}
      >
        {inner}
      </a>
    );
  }
  return (
    <div data-testid={testId} {...dataProps}>
      {inner}
    </div>
  );
}
