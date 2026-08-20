import { useState } from 'react';
import { cn } from '@/lib/utils';
import { mediaUrl } from '@/api/series';
import { MonogramFallback, type MonogramKind } from './MonogramFallback';

export type MediaImageKind =
  | 'series_poster'
  | 'poster'
  | 'backdrop'
  | 'still'
  | 'profile'
  | 'logo';

// Map the asset-kind taxonomy onto the placeholder-kind taxonomy.
// `still` + `logo` keep poster sizing — neither has a dedicated
// placeholder slot today and 108px reads cleanly in any aspect.
function monogramKindFor(kind: MediaImageKind | undefined): MonogramKind {
  if (kind === 'backdrop') return 'backdrop';
  if (kind === 'profile') return 'avatar';
  return 'poster';
}

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
  /** Skip native lazy-loading for always-above-the-fold images (hero-area
   *  thumbnails). Chromium's `loading="lazy"` intersection heuristic can
   *  get stuck and never fire for small images inside a container whose
   *  geometry isn't finalized at insertion time (e.g. a card mounted async
   *  inside a `backdrop-filter` glass shell) — `MediaHero`'s own poster and
   *  backdrop `<img>`s are eager-by-default for the same reason. Defaults
   *  to false (unchanged lazy behavior) for every other caller. */
  readonly eager?: boolean;
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
  eager,
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
        <MonogramFallback
          title={title}
          kind={monogramKindFor(rest.kind)}
          hueKey={effectiveHueKey}
        />
      )}
      {!showImg && fallback === 'svg' && <SvgFallback />}
      {showImg && (
        <img
          src={src}
          alt=""
          aria-hidden="true"
          loading={eager ? 'eager' : 'lazy'}
          decoding="async"
          onError={() => setErrored(true)}
          className="absolute inset-0 z-[1] h-full w-full object-cover"
          data-testid="media-image-img"
        />
      )}
    </div>
  );
}
