import { useMemo } from 'react';
import { Link, useParams } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { AlertTriangle, Layers, SearchX } from 'lucide-react';
import { useSetPageTitle } from '@/components/shell/page-title-context';
import { useLanguage } from '@/hooks/useLanguage';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Skeleton } from '@/components/ui/skeleton';
import { EmptyState } from '@/components/EmptyState';
import { Badge } from '@/components/ui/badge';
import { MediaImage } from '@/components/MediaImage';
import { useMovieCollection, type MovieCollectionPartDTO } from '@/api/movieCollections';
import { useInstances } from '@/lib/instances';

function parseTmdbId(raw: string | undefined): number | null {
  if (!raw) return null;
  const n = Number.parseInt(raw, 10);
  return Number.isFinite(n) && n > 0 && String(n) === raw ? n : null;
}

function PartCard({ part }: { part: MovieCollectionPartDTO }) {
  const { t } = useTranslation();
  const tmdbId = part.tmdb_id ?? 0;
  const inLib = part.in_library === true;
  return (
    <Link
      to={`/movies/${tmdbId}`}
      data-testid={`collection-detail-part-${tmdbId}`}
      className="flex flex-col gap-1 rounded-md border border-border-subtle bg-bg-surface p-2 transition-colors hover:border-accent"
    >
      <div className="overflow-hidden rounded-md border border-border-subtle">
        <MediaImage
          hash={part.poster ?? null}
          kind="poster"
          title={part.title ?? ''}
          fallback="monogram"
          data-testid={`collection-detail-part-poster-${tmdbId}`}
        />
      </div>
      <span className="truncate text-[13px] font-medium text-tx-primary" title={part.title}>
        {part.title}
        {typeof part.year === 'number' && part.year > 0 && (
          <span className="ml-1 text-tx-muted tabular-nums">({part.year})</span>
        )}
      </span>
      <Badge variant={inLib ? 'ok' : 'neutral'} data-testid={`collection-detail-part-badge-${tmdbId}`}>
        {inLib ? t('movieCollection.part.inLibrary') : t('movieCollection.part.missing')}
      </Badge>
    </Link>
  );
}

// CollectionDetail — S3.2. Read-oriented TMDB-collection page reached from a
// collection search hit (/collections/:tmdbId). Reuses useMovieCollection with
// the default Radarr instance (the endpoint 400s without one, resolved from
// useInstances exactly like AddToRadarrSplitButton / useCollectionCardState —
// never hardcoded). No Radarr monitor/add-all toolbar (that stays on the movie
// detail); just the collection header + a parts grid linking to /movies/:tmdbId.
export function CollectionDetail() {
  const { t } = useTranslation();
  const { tmdbId: tmdbParam } = useParams();
  const lang = useLanguage().current;
  const tmdbId = useMemo(() => parseTmdbId(tmdbParam), [tmdbParam]);

  const instancesQ = useInstances();
  const radarrInstance = useMemo(
    () =>
      (instancesQ.data?.instances ?? []).find(
        (i) => (i.type ?? 'sonarr') === 'radarr' && Boolean(i.name),
      )?.name,
    [instancesQ.data],
  );

  // Gate the collection query on a resolved Radarr instance: undefined id →
  // useMovieCollection's `enabled` guard keeps it idle so we never fire the
  // 400-on-missing-instance request.
  const query = useMovieCollection(
    radarrInstance && tmdbId ? tmdbId : undefined,
    radarrInstance,
    lang,
  );
  const collection = query.data;

  useSetPageTitle(collection?.name ?? t('collectionDetail.title'));

  if (tmdbId === null) {
    return (
      <Alert variant="destructive" data-testid="collection-detail-invalid">
        <AlertTriangle className="h-4 w-4" />
        <AlertTitle>{t('collectionDetail.invalidTitle')}</AlertTitle>
      </Alert>
    );
  }

  if (instancesQ.isLoading) {
    return (
      <div className="flex flex-col gap-4" data-testid="collection-detail-loading">
        <Skeleton className="h-6 w-48" />
        <Skeleton className="h-24 w-full" />
      </div>
    );
  }

  if (!radarrInstance) {
    return (
      <div data-testid="collection-detail-no-instance">
        <EmptyState icon={<Layers className="h-7 w-7" />} title={t('collectionDetail.noInstance')} />
      </div>
    );
  }

  if (query.isPending) {
    return (
      <div className="flex flex-col gap-4" data-testid="collection-detail-loading">
        <Skeleton className="h-6 w-48" />
        <Skeleton className="h-24 w-full" />
      </div>
    );
  }

  if (query.isError || !collection) {
    return (
      <Alert variant="destructive" data-testid="collection-detail-error">
        <AlertTriangle className="h-4 w-4" />
        <AlertTitle>{t('collectionDetail.errorTitle')}</AlertTitle>
        <AlertDescription>
          {query.error instanceof Error ? query.error.message : t('common.error')}
        </AlertDescription>
      </Alert>
    );
  }

  const parts = collection.parts ?? [];

  return (
    <div className="flex flex-col gap-5" data-testid="collection-detail-page">
      <div className="flex flex-wrap items-center gap-4 rounded-lg border border-border-subtle bg-bg-surface p-4">
        <div className="w-[72px] shrink-0">
          <MediaImage
            hash={collection.poster ?? null}
            kind="poster"
            title={collection.name ?? ''}
            fallback="monogram"
            className="rounded-md border border-border-subtle"
            data-testid="collection-detail-poster"
          />
        </div>
        <div className="flex flex-1 flex-col gap-1">
          <h1
            data-testid="collection-detail-name"
            className="text-lg font-semibold text-tx-primary"
          >
            {collection.name}
          </h1>
          {collection.overview && (
            <p className="text-[13px] text-tx-secondary line-clamp-3" title={collection.overview}>
              {collection.overview}
            </p>
          )}
        </div>
      </div>

      {parts.length > 0 ? (
        <div
          data-testid="collection-detail-parts"
          className="grid gap-3 grid-cols-[repeat(auto-fill,minmax(150px,1fr))]"
        >
          {parts.map((part) => (
            <PartCard key={part.tmdb_id ?? part.title} part={part} />
          ))}
        </div>
      ) : (
        <div data-testid="collection-detail-empty">
          <EmptyState icon={<SearchX className="h-7 w-7" />} title={t('collectionDetail.empty')} />
        </div>
      )}
    </div>
  );
}
