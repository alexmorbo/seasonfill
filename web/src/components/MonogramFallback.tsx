import { cn } from '@/lib/utils';

export type MonogramKind = 'poster' | 'backdrop' | 'avatar';

export interface MonogramFallbackProps {
  /** Tag for accessibility — read by screen readers as
   *  "No image — <title>". The decorative "sf" glyph is hidden. */
  readonly title: string;
  /** Per-context glyph size. Defaults to poster.
   *  - poster   → 108px glyph (matches handoff Direction B poster row)
   *  - backdrop → 230px glyph (full-bleed hero)
   *  - avatar   → 86px glyph + round clip */
  readonly kind?: MonogramKind;
  /** Reserved for callers still passing it (e.g. MediaImage).
   *  Brand color is fixed; the value is ignored. Kept so the
   *  rewrite is API-additive. */
  readonly hueKey?: string;
  readonly className?: string;
  readonly 'data-testid'?: string;
}

const GLYPH_SIZE: Record<MonogramKind, string> = {
  poster: '108px',
  backdrop: '230px',
  avatar: '86px',
};

export function MonogramFallback({
  title,
  kind = 'poster',
  className,
  ...rest
}: MonogramFallbackProps) {
  return (
    <div
      data-testid={rest['data-testid'] ?? 'monogram-fallback'}
      data-kind={kind}
      role="img"
      aria-label={`No image — ${title}`}
      className={cn(
        'absolute inset-0 z-0 ph ph-b',
        kind === 'avatar' && 'ph--avatar',
        className,
      )}
    >
      <span
        className="glyph"
        aria-hidden="true"
        style={{ ['--gs' as string]: GLYPH_SIZE[kind] }}
      >
        s<b>f</b>
      </span>
    </div>
  );
}
