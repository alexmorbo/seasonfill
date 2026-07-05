import type { ReactNode } from 'react';
import { Link } from 'react-router-dom';
import { mediaUrl } from '@/api/series';

export type CreditLinkTarget =
  | { readonly kind: 'internal'; readonly to: string }
  | { readonly kind: 'tmdb'; readonly href: string }
  | { readonly kind: 'none' };

export interface CreditCardProps {
  readonly title: string;
  readonly year?: number | undefined;
  readonly role?: string | undefined;
  readonly posterAsset?: string | null | undefined;
  readonly link: CreditLinkTarget;
  /** Optional footer slot below the role/subtitle. */
  readonly footer?: ReactNode | undefined;
  /** Optional overlay slot rendered on top of the poster (TMDB hover chevron). */
  readonly overlay?: ReactNode | undefined;
  /** Optional subtitle slot below role (e.g. original_title italic). */
  readonly subtitle?: ReactNode | undefined;
  readonly testId: string;
  readonly dataAttrs?: Record<string, string | number | undefined | null> | undefined;
}

/**
 * CreditCard is the thin external-link card used on the person page for MOVIE
 * credits only. Movies are not series, so they cannot ride the internal
 * SeriesCard routing — they link out to themoviedb.org. TV/library credits are
 * rendered by the unified SeriesCard instead; the shared badge/role primitive
 * this component once provided has been retired.
 */
export function CreditCard({
  title,
  year,
  role,
  posterAsset,
  link,
  footer,
  overlay,
  subtitle,
  testId,
  dataAttrs,
}: CreditCardProps) {
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
