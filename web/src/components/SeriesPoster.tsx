import { useMemo, useState } from 'react';
import { cn } from '@/lib/utils';
import { buildPosterUrl, type PosterSize } from '@/lib/posters';

export interface SeriesPosterProps {
  // Stable identity for the upstream poster. When either is missing /
  // non-positive we skip the <img> and just render the gradient.
  readonly instance?: string | null | undefined;
  readonly seriesId?: number | null | undefined;

  // Visible label for accessibility. Not rendered in pixels — the
  // existing tile chrome already shows the title.
  readonly title: string;

  // Stable hue source. Falls back to title or seriesId.
  readonly hueKey?: string | undefined;

  // 'full' for >=150px tiles, 'small' for thumbs.
  readonly size?: PosterSize;

  // Tailwind class controlling outer box; defaults to aspect-[2/3].
  // Callers may override (e.g. `w-[46px] h-[69px]`) when they need a
  // fixed pixel thumb instead of an aspect ratio.
  readonly className?: string;
  readonly aspectRatio?: string;

  // Test hook.
  readonly 'data-testid'?: string;
}

function hueFor(key: string): number {
  let h = 0;
  for (let i = 0; i < key.length; i += 1) {
    h = (h * 31 + key.charCodeAt(i)) % 360;
  }
  return h;
}

export function SeriesPoster({
  instance, seriesId, title, hueKey, size = 'full',
  className, aspectRatio, ...rest
}: SeriesPosterProps) {
  const hueSrc = hueKey && hueKey.length > 0
    ? hueKey
    : title.length > 0
      ? title
      : String(seriesId ?? 0);
  const hue = useMemo(() => hueFor(hueSrc), [hueSrc]);
  const [errored, setErrored] = useState(false);

  const hasUpstream = Boolean(instance) && typeof seriesId === 'number' && seriesId > 0;
  const showImg = hasUpstream && !errored;
  const src = showImg ? buildPosterUrl(instance as string, seriesId as number, size) : undefined;

  return (
    <div
      data-testid={rest['data-testid']}
      className={cn(
        'relative isolate overflow-hidden',
        aspectRatio ?? 'aspect-[2/3]',
        className,
      )}
      style={{
        background:
          `radial-gradient(120% 80% at 30% 0%, oklch(0.30 0.07 ${hue} / 0.9), transparent 60%),` +
          `linear-gradient(165deg, oklch(0.34 0.08 ${hue}), oklch(0.19 0.04 ${(hue + 30) % 360}) 75%)`,
      }}
    >
      {showImg && src && (
        <img
          src={src}
          alt=""
          aria-hidden="true"
          loading="lazy"
          decoding="async"
          onError={() => setErrored(true)}
          className="absolute inset-0 z-[1] h-full w-full object-cover"
          data-testid="series-poster-img"
        />
      )}
    </div>
  );
}
