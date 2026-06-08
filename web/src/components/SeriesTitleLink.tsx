import { ExternalLink } from 'lucide-react';
import { titleHasEmbeddedYear } from '@/lib/title';

export interface SeriesTitleLinkProps {
  readonly title: string;
  readonly titleSlug?: string | undefined;
  readonly year?: number | undefined;
  readonly instanceUiUrl?: string | undefined;
}

// Strip exactly one trailing slash from the instance URL before joining
// with `/series/<slug>` so we never emit `//series/...`. `ui_url` is
// validated/normalized server-side (041a), but the SPA must not assume
// canonical form for instances created before 041a shipped.
function buildHref(uiUrl: string, slug: string): string {
  const base = uiUrl.replace(/\/+$/, '');
  return `${base}/series/${slug}`;
}

export function SeriesTitleLink({
  title,
  titleSlug,
  year,
  instanceUiUrl,
}: SeriesTitleLinkProps) {
  const canLink = Boolean(titleSlug) && Boolean(instanceUiUrl);
  // Skip the muted "(YYYY)" suffix when Sonarr already embedded one in
  // the title (PRD F-P1-4 / Story 075) — otherwise we'd render
  // "Time (2021) (2021)".
  const yearSuffix = year && !titleHasEmbeddedYear(title) ? (
    <span className="text-faint font-normal ml-1">({year})</span>
  ) : null;

  if (!canLink) {
    return (
      <span className="font-medium">
        {title}
        {yearSuffix}
      </span>
    );
  }

  return (
    <a
      href={buildHref(instanceUiUrl!, titleSlug!)}
      target="_blank"
      rel="noopener noreferrer"
      className="font-medium inline-flex items-center gap-1 group hover:underline"
    >
      <span>
        {title}
        {yearSuffix}
      </span>
      <ExternalLink
        className="w-3 h-3 text-faint opacity-0 group-hover:opacity-100 transition-opacity"
        aria-hidden="true"
      />
    </a>
  );
}
