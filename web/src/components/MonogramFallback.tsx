import { cn } from '@/lib/utils';

function hueFor(key: string): number {
  let h = 0;
  for (let i = 0; i < key.length; i += 1) {
    h = (h * 31 + key.charCodeAt(i)) % 360;
  }
  return h;
}

export interface MonogramFallbackProps {
  readonly hueKey: string;
  readonly title: string;
  readonly className?: string;
  readonly 'data-testid'?: string;
}

export function MonogramFallback({
  hueKey,
  title,
  className,
  ...rest
}: MonogramFallbackProps) {
  const hue = hueFor(hueKey.length > 0 ? hueKey : title);
  const mono = (title.charAt(0) || '?').toUpperCase();
  return (
    <div
      data-testid={rest['data-testid'] ?? 'monogram-fallback'}
      aria-hidden="true"
      className={cn('absolute inset-0 z-0', className)}
      style={{
        background:
          `radial-gradient(120% 80% at 30% 0%, oklch(0.30 0.07 ${hue} / 0.9), transparent 60%),` +
          `linear-gradient(165deg, oklch(0.34 0.08 ${hue}), oklch(0.19 0.04 ${(hue + 30) % 360}) 75%)`,
      }}
    >
      <span className="absolute -right-1.5 -top-2.5 font-mono font-bold text-[120px] leading-[0.8] tracking-tighter text-[oklch(1_0_0_/_0.07)]">
        {mono}
      </span>
    </div>
  );
}
