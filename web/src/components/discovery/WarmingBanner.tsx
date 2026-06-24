import { useTranslation } from 'react-i18next';
import { Loader2 } from 'lucide-react';

// Story 517 / N-3e: amber tinted banner shown while a discovery slice
// is degraded (cold cache or TMDB throttle). `aria-live="polite"` so
// AT users hear the warming announcement once the network kicks back in.
export interface WarmingBannerProps {
  readonly kind: 'cold_start' | 'tmdb_throttled';
  readonly estimateSeconds?: number;
  readonly retryAfterSeconds?: number;
}

export function WarmingBanner({
  kind, estimateSeconds, retryAfterSeconds,
}: WarmingBannerProps) {
  const { t } = useTranslation();
  const message = kind === 'cold_start'
    ? t('discovery.warming.cold_start', { seconds: estimateSeconds ?? 30 })
    : t('discovery.warming.tmdb');
  return (
    <div
      role="status"
      aria-live="polite"
      data-testid="discovery-warming-banner"
      data-kind={kind}
      data-retry-after={retryAfterSeconds ?? ''}
      className={
        'flex items-center gap-2 rounded-lg border px-3 py-2 text-sm '
        + 'border-amber-500/30 bg-amber-500/10 text-amber-100'
      }
    >
      <Loader2 className="h-4 w-4 shrink-0 animate-spin" aria-hidden />
      <span>{message}</span>
    </div>
  );
}
