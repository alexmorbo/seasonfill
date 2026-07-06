import { useCallback, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { api } from '@/lib/api';
import type { components } from '@/api/schema';

type SeriesResolveResponse = components['schemas']['dto.SeriesResolveResponse'];

export interface ResolveNavTarget {
  readonly seriesId?: number | undefined;
  readonly tmdbId?: number | undefined;
}

// Card click → internal /series/:id navigation, resolving a TMDB id to the
// canonical series id on demand. Shared by every surface that migrates onto
// the unified SeriesCard: pass a canonical `seriesId` for a direct jump, or a
// `tmdbId` when only the TMDB id is known (discovery / TMDB-only recs). The
// resolve call hits GET /series/resolve?tmdb_id=… which mints a canon stub for
// unknown ids, so navigation always lands on a real /series/:id route.
export function useResolveSeriesNav() {
  const navigate = useNavigate();
  const { t } = useTranslation();
  const [pending, setPending] = useState(false);

  const resolveAndNavigate = useCallback(
    async ({ seriesId, tmdbId }: ResolveNavTarget): Promise<void> => {
      if (typeof seriesId === 'number' && seriesId > 0) {
        navigate(`/series/${seriesId}`);
        return;
      }
      if (typeof tmdbId !== 'number' || tmdbId <= 0) return;

      setPending(true);
      try {
        const res = await api<SeriesResolveResponse>(
          `/series/resolve?tmdb_id=${encodeURIComponent(tmdbId)}`,
        );
        if (typeof res.series_id === 'number' && res.series_id > 0) {
          navigate(`/series/${res.series_id}`);
        }
      } catch {
        // Resolve failed — leave the user where they are rather than crashing
        // the card, but surface a toast so the dead click is visible. A later
        // click retries.
        toast.error(t('discovery.error.resolve_failed'));
      } finally {
        setPending(false);
      }
    },
    [navigate, t],
  );

  return { resolveAndNavigate, pending };
}
