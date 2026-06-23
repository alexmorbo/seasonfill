import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { AlertTriangle, ImageIcon, X } from 'lucide-react';
import { Link } from 'react-router-dom';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { listExternalServices, type ExternalServiceDTO } from '@/api/externalServices';

const POLL_MS = 30_000;
const STALE_MS = 15_000;
const DISMISS_KEY = 'tmdb_disabled_banner_dismissed';

/**
 * TMDBStatusBanner — Dashboard banner that surfaces TMDB configuration
 * state. Two variants:
 *
 *   1. (Story 494 / B-16b) "missing" — TMDB row absent OR
 *      (`enabled=false` AND `api_key_configured=false`). Info banner.
 *      Dismissible via localStorage (key `tmdb_disabled_banner_dismissed`).
 *      Renders nothing if dismissed.
 *
 *   2. (Story 489 / B-17) "invalid_key" — TMDB row exists with
 *      last_validation_status='invalid_key'. Warn banner. NOT dismissible —
 *      always surfaces because it indicates a live operational problem.
 *
 * Dispatch order: invalid_key (warn) preempts missing (info). When the
 * operator configures a valid key, both variants disappear.
 */
export function TMDBStatusBanner() {
  const { t } = useTranslation();
  const [dismissed, setDismissed] = useState<boolean>(() => readDismissed());

  const q = useQuery({
    queryKey: ['external-services'],
    queryFn: listExternalServices,
    refetchInterval: POLL_MS,
    staleTime: STALE_MS,
  });

  if (!q.isSuccess) return null;
  const tmdb = q.data.find((s: ExternalServiceDTO) => s.service === 'tmdb');

  // Dispatch: invalid_key (warn) wins, regardless of dismiss state.
  if (tmdb?.last_validation_status === 'invalid_key') {
    return (
      <Alert
        data-testid="tmdb-status-banner"
        data-variant="invalid_key"
        className="border-status-warning/40 bg-status-warning/10 text-status-warning"
      >
        <AlertTriangle className="w-4 h-4 text-status-warning" aria-hidden="true" />
        <AlertTitle>{t('settings.externalServices.invalidKey.banner.title')}</AlertTitle>
        <AlertDescription>
          {t('settings.externalServices.invalidKey.banner.body')}{' '}
          <Link
            to="/settings/external-services"
            className="underline text-status-warning"
            data-testid="tmdb-status-banner-link"
          >
            {t('settings.externalServices.invalidKey.banner.cta')}
          </Link>
        </AlertDescription>
      </Alert>
    );
  }

  // B-16b: missing — TMDB not configured at all.
  const missing = !tmdb || (!tmdb.enabled && !tmdb.api_key_configured);
  if (!missing || dismissed) return null;

  return (
    <Alert
      data-testid="tmdb-status-banner"
      data-variant="missing"
      className="border-accent/30 bg-accent-dim/40 text-tx-primary"
    >
      <ImageIcon className="w-4 h-4 text-accent" aria-hidden="true" />
      <AlertTitle>{t('dashboard.tmdb_disabled_banner.title')}</AlertTitle>
      <AlertDescription className="flex items-center gap-2">
        <span className="flex-1">
          {t('dashboard.tmdb_disabled_banner.body')}{' '}
          <Link
            to="/settings/external-services"
            className="underline text-accent"
            data-testid="tmdb-status-banner-link"
          >
            {t('dashboard.tmdb_disabled_banner.cta')}
          </Link>
        </span>
        <Button
          variant="ghost"
          size="sm"
          onClick={() => { writeDismissed(); setDismissed(true); }}
          data-testid="tmdb-status-banner-dismiss"
          aria-label={t('dashboard.tmdb_disabled_banner.dismiss')}
        >
          <X className="w-3.5 h-3.5" aria-hidden="true" />
        </Button>
      </AlertDescription>
    </Alert>
  );
}

function readDismissed(): boolean {
  if (typeof window === 'undefined') return false;
  try {
    return window.localStorage.getItem(DISMISS_KEY) === '1';
  } catch {
    return false;
  }
}

function writeDismissed(): void {
  if (typeof window === 'undefined') return;
  try {
    window.localStorage.setItem(DISMISS_KEY, '1');
  } catch {
    /* localStorage quota / privacy mode — silent no-op. */
  }
}
