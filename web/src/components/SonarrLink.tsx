import { useTranslation } from 'react-i18next';
import { ExternalLink } from 'lucide-react';
import { cn } from '@/lib/utils';
import { buildSonarrSeriesHref, slugifyTitle } from '@/lib/sonarrUrl';
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip';

export type SonarrLinkVariant = 'icon' | 'chip';
export type SonarrLinkSize = 'sm' | 'md';

export interface SonarrLinkProps {
  // Instance name. Kept on the API for data-attribute correlation;
  // the actual link target is built from `publicUrl`.
  readonly instance: string | null | undefined;
  // Pre-resolved public URL — callers hold the instance row (via
  // `useInstancePublicURL` or `useInstances`) and pass it in. When
  // undefined/empty we render nothing so operators are never linked
  // to an internal-only Sonarr URL.
  readonly publicUrl: string | null | undefined;
  // Sonarr series id is unused in the URL (Sonarr deep-links by slug),
  // but it's accepted for symmetry with the rest of the link helpers
  // and future analytics.
  readonly seriesId?: number | null | undefined;
  // Title for client-side slug fallback. Required so we always have a
  // deterministic path even when titleSlug is absent.
  readonly title: string;
  readonly titleSlug?: string | null | undefined;
  readonly variant?: SonarrLinkVariant;
  readonly size?: SonarrLinkSize;
  // Extra classes (positioning / overlay). The component owns visual
  // styling for the chip/icon itself — `className` is appended.
  readonly className?: string;
  // Optional override for the accessible label / tooltip. Defaults to
  // i18n `common.openInSonarr`.
  readonly label?: string;
}

// Stops the click from bubbling up to parent buttons / row handlers
// (PosterTile, GrabRow, accordion triggers) which would otherwise
// navigate away when the operator clicks the deep-link.
function stopPropagation(e: React.MouseEvent | React.KeyboardEvent) {
  e.stopPropagation();
}

export function SonarrLink({
  instance: _instance,
  publicUrl,
  title,
  titleSlug,
  variant = 'icon',
  size = 'sm',
  className,
  label,
}: SonarrLinkProps) {
  const { t } = useTranslation();
  if (!publicUrl) return null;

  const slug =
    titleSlug && titleSlug.length > 0 ? titleSlug : slugifyTitle(title);
  if (!slug) return null;

  const href = buildSonarrSeriesHref(publicUrl, slug);
  const aria = label ?? t('common.openInSonarr');

  if (variant === 'chip') {
    const padX = size === 'md' ? 'px-2.5 py-0.5' : 'px-2 py-px';
    const text = size === 'md' ? 'text-[12px]' : 'text-[11px]';
    return (
      <a
        href={href}
        target="_blank"
        rel="noopener noreferrer"
        onClick={stopPropagation}
        onKeyDown={stopPropagation}
        aria-label={aria}
        data-testid="sonarr-link"
        data-variant="chip"
        className={cn(
          'inline-flex items-center gap-1 rounded-full font-semibold',
          'text-tx-secondary bg-bg-surface-2 border border-border-subtle',
          'hover:text-tx-primary hover:border-border-strong no-underline',
          'whitespace-nowrap transition-colors',
          padX,
          text,
          className,
        )}
      >
        <ExternalLink className="size-3" aria-hidden="true" />
        Sonarr
      </a>
    );
  }

  // icon variant — square button-ish target, used as a poster overlay
  const boxClass =
    size === 'md' ? 'w-7 h-7' : 'w-6 h-6';
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <a
          href={href}
          target="_blank"
          rel="noopener noreferrer"
          onClick={stopPropagation}
          onKeyDown={stopPropagation}
          aria-label={aria}
          data-testid="sonarr-link"
          data-variant="icon"
          className={cn(
            'inline-flex items-center justify-center rounded-md',
            'bg-bg-surface/85 text-tx-primary border border-border-subtle',
            'backdrop-blur-sm hover:bg-bg-surface-2 hover:border-border-strong',
            'no-underline transition-colors',
            boxClass,
            className,
          )}
        >
          <ExternalLink className="size-3.5" aria-hidden="true" />
        </a>
      </TooltipTrigger>
      <TooltipContent>{aria}</TooltipContent>
    </Tooltip>
  );
}
