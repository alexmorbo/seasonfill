import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { AlertTriangle } from 'lucide-react';
import { Link } from 'react-router-dom';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { listExternalServices, type ExternalServiceDTO } from '@/api/externalServices';

const POLL_MS = 30_000;
const STALE_MS = 15_000;

/**
 * TMDBStatusBanner — Dashboard banner that surfaces TMDB 401 state.
 *
 * Story 489 (B-17). Polls /external-services every 30s. Renders null
 * when the query is loading, when the TMDB row is missing, or when
 * `last_validation_status !== 'invalid_key'`. Otherwise paints a
 * warning-styled alert with a deep-link to /settings/external-services.
 *
 * Alert variant: `default` with inline status-warning colour classes —
 * the shared `<Alert>` only ships `default` and `destructive`, and the
 * UX call (story Decision §9) is "warning, not error" so the operator
 * notices without panic.
 */
export function TMDBStatusBanner() {
  const { t } = useTranslation();
  const q = useQuery({
    queryKey: ['external-services'],
    queryFn: listExternalServices,
    refetchInterval: POLL_MS,
    staleTime: STALE_MS,
  });
  if (!q.isSuccess) return null;
  const tmdb = q.data.find((s: ExternalServiceDTO) => s.service === 'tmdb');
  if (!tmdb || tmdb.last_validation_status !== 'invalid_key') return null;
  return (
    <Alert
      data-testid="tmdb-status-banner"
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
