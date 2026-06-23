import { useEffect } from 'react';
import { useNavigate, useParams } from 'react-router-dom';

export interface LegacySeriesRedirectProps {
  readonly kind?: 'detail' | 'cast';
}

/**
 * Story 495 / N-1e: soft-redirects pre-N-1e operator bookmarks of
 * `/series/:instance/:id(/cast)?` to the new global URL shape
 * `/series/:id(/cast)?`. Mount-effect navigates with replace so the
 * back-button skips the legacy URL. The component renders nothing
 * during the 1-frame redirect window.
 *
 * REMOVE 2026-09: drop the route entries in router.tsx and delete this
 * file once the operator's bookmark inventory has rotated.
 */
export function LegacySeriesRedirect({ kind = 'detail' }: LegacySeriesRedirectProps) {
  const { id } = useParams<{ instance: string; id: string }>();
  const navigate = useNavigate();
  useEffect(() => {
    if (!id) {
      navigate('/series', { replace: true });
      return;
    }
    const suffix = kind === 'cast' ? '/cast' : '';
    navigate(`/series/${encodeURIComponent(id)}${suffix}`, { replace: true });
  }, [id, kind, navigate]);
  return null;
}
