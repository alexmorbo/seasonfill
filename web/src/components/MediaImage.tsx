import { useState } from 'react';
import { cn } from '@/lib/utils';
import { mediaUrl } from '@/api/seriesDetail';
import { MonogramFallback } from './MonogramFallback';

export type MediaImageKind =
  | 'series_poster'
  | 'poster'
  | 'backdrop'
  | 'still'
  | 'profile'
  | 'logo';

export type MediaImageFallback = 'monogram' | 'svg';

export interface MediaImageProps {
  /** Content-addressed sha256 hex. Pass undefined when the DTO field
   *  is absent; the fallback is rendered instead of <img>. */
  readonly hash: string | null | undefined;
  /** Asset kind. Reserved for future per-kind URL routing and
   *  fallback selection; currently informational only. */
  readonly kind?: MediaImageKind;
  /** Tag for accessibility + monogram hueKey fallback. */
  readonly title: string;
  /** Drives the monogram gradient hue. Stable across re-renders. */
  readonly hueKey?: string;
  readonly fallback: MediaImageFallback;
  readonly className?: string;
  readonly aspectRatio?: string;
  readonly 'data-testid'?: string;
}

function SvgFallback({
  className,
  testId,
}: {
  readonly className?: string;
  readonly testId?: string;
}) {
  return (
    <div
      data-testid={testId ?? 'media-image-svg-fallback'}
      aria-hidden="true"
      className={cn(
        'absolute inset-0 z-0 flex items-center justify-center bg-bg-surface-1',
        className,
      )}
    >
      <svg
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.5"
        strokeLinecap="round"
        strokeLinejoin="round"
        className="w-1/3 h-1/3 text-tx-faint opacity-50"
      >
        <rect x="3" y="3" width="18" height="18" rx="2" />
        <circle cx="9" cy="9" r="2" />
        <path d="m21 15-5-5L5 21" />
      </svg>
    </div>
  );
}

export function MediaImage({
  hash,
  title,
  hueKey,
  fallback,
  className,
  aspectRatio,
  ...rest
}: MediaImageProps) {
  const [errored, setErrored] = useState(false);
  const src = mediaUrl(hash);
  const showImg = Boolean(src) && !errored;
  const effectiveHueKey = hueKey ?? hash ?? title;

  return (
    <div
      data-testid={rest['data-testid'] ?? 'media-image'}
      className={cn(
        'relative isolate overflow-hidden',
        aspectRatio ?? 'aspect-[2/3]',
        className,
      )}
    >
      {!showImg && fallback === 'monogram' && (
        <MonogramFallback hueKey={effectiveHueKey} title={title} />
      )}
      {!showImg && fallback === 'svg' && <SvgFallback />}
      {showImg && (
        <img
          src={src}
          alt=""
          aria-hidden="true"
          loading="lazy"
          decoding="async"
          onError={() => setErrored(true)}
          className="absolute inset-0 z-[1] h-full w-full object-cover"
          data-testid="media-image-img"
        />
      )}
    </div>
  );
}
