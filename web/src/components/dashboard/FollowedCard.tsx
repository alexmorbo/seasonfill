import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Skeleton } from '@/components/ui/skeleton';
import { MediaImage } from '@/components/MediaImage';
import { useFollowed } from '@/api/follow';

// FollowedCard — the dashboard rail «Слежу» / Watchlist card. Lists the first
// few followed series as poster thumbnails linking to their canon detail page.
// Mirrors the other rail cards (Card + CardHeader/CardContent) and reuses
// MediaImage for the content-addressed poster_asset hash.
export function FollowedCard() {
  const { t, i18n } = useTranslation();
  const { data, isLoading, isError } = useFollowed(i18n.language);
  const items = data?.items ?? [];

  return (
    <Card data-testid="followed-card">
      <CardHeader className="p-4 pb-2">
        <CardTitle className="text-sm font-semibold">
          {t('follow.railTitle')}
        </CardTitle>
      </CardHeader>
      <CardContent className="p-4 pt-0">
        {isLoading && (
          <div className="grid grid-cols-3 gap-2" data-testid="followed-card-loading">
            {Array.from({ length: 3 }).map((_, i) => (
              <Skeleton key={i} className="aspect-[2/3] w-full rounded" />
            ))}
          </div>
        )}

        {!isLoading && isError && (
          <p className="text-xs text-danger" data-testid="followed-card-error">
            {t('common.error')}
          </p>
        )}

        {!isLoading && !isError && items.length === 0 && (
          <p
            className="text-xs text-tx-muted"
            data-testid="followed-card-empty"
          >
            {t('follow.empty')}
          </p>
        )}

        {!isLoading && !isError && items.length > 0 && (
          <ul className="grid grid-cols-3 gap-2" data-testid="followed-card-list">
            {items.slice(0, 6).map((it) => (
              <li key={it.series_id}>
                <Link
                  to={`/series/${it.series_id}`}
                  title={it.title}
                  aria-label={it.title}
                  className="block overflow-hidden rounded outline-hidden focus-visible:ring-2 focus-visible:ring-accent"
                >
                  <MediaImage
                    hash={it.poster_asset}
                    kind="series_poster"
                    title={it.title ?? ''}
                    fallback="monogram"
                    className="w-full rounded"
                  />
                </Link>
              </li>
            ))}
          </ul>
        )}
      </CardContent>
    </Card>
  );
}
