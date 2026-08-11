// MovieCollectionBlock (Ф6-R-6b Wave B) — the collection panel on MovieDetail.
// Renders the collection header (poster + name + Radarr native-monitor toggle),
// a parts grid with per-part library-membership badges, and an "Add all
// missing" dialog that fans the missing parts out to Radarr.
//
// The Radarr monitor endpoint (PUT /collections/:id/monitor) is ENABLE-ONLY
// (EnableNativeMonitor); there is no un-monitor path server-side, so the Switch
// is disabled once already monitored — it can only be flipped on.

import { useMemo, useState, type FormEvent } from 'react';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import { toast } from 'sonner';
import { MediaImage } from '@/components/MediaImage';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import { Switch } from '@/components/ui/switch';
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from '@/components/ui/dialog';
import { RadarrTargetFields } from './RadarrTargetFields';
import {
  useMovieCollection, useAddAllMissing, useSetCollectionMonitor,
  type MovieCollectionPartDTO,
} from '@/api/movieCollections';
import { useInstances } from '@/lib/instances';
import type { MinimumAvailability } from '@/api/addToRadarr';

function PartCard({ part }: { part: MovieCollectionPartDTO }) {
  const { t } = useTranslation();
  const tmdbId = part.tmdb_id ?? 0;
  const inLib = part.in_library === true;
  return (
    <Link
      to={`/movies/${tmdbId}`}
      data-testid={`movie-collection-part-${tmdbId}`}
      className="flex flex-col gap-1 rounded-md border border-border-subtle bg-bg-surface p-2 transition-colors hover:border-accent"
    >
      <span className="truncate text-[13px] font-medium text-tx-primary" title={part.title}>
        {part.title}
        {part.year !== undefined && (
          <span className="ml-1 text-tx-muted tabular-nums">({part.year})</span>
        )}
      </span>
      <Badge
        variant={inLib ? 'ok' : 'neutral'}
        data-testid={`movie-collection-part-badge-${tmdbId}`}
      >
        {inLib
          ? t('movieCollection.part.inLibrary')
          : t('movieCollection.part.missing')}
      </Badge>
    </Link>
  );
}

export interface MovieCollectionBlockProps {
  readonly tmdbCollectionId: number;
  /** Instance to query the collection with + the preferred monitor target
   *  (the movie's first library instance). */
  readonly instance?: string | undefined;
}

export function MovieCollectionBlock({
  tmdbCollectionId,
  instance,
}: MovieCollectionBlockProps) {
  const { t } = useTranslation();
  const query = useMovieCollection(tmdbCollectionId, instance);

  const instancesQ = useInstances();
  const radarrInstances = useMemo(
    () => (instancesQ.data?.instances ?? []).filter(
      (i) => Boolean(i.name) && (i.type ?? 'sonarr') === 'radarr',
    ),
    [instancesQ.data],
  );

  // Monitor target: the movie's library instance, else the first radarr one.
  const monitorInstance = instance || radarrInstances[0]?.name || '';

  const monitorMut = useSetCollectionMonitor();
  const addAllMut = useAddAllMissing();

  // Add-all dialog field state.
  const [dialogOpen, setDialogOpen] = useState(false);
  const [explicitInstance, setExplicitInstance] = useState('');
  const effectiveInstance = explicitInstance || (radarrInstances[0]?.name ?? '');
  const [qualityProfileId, setQualityProfileId] = useState('');
  const [rootFolderPath, setRootFolderPath] = useState('');
  const [minimumAvailability, setMinimumAvailability] =
    useState<MinimumAvailability>('released');
  const [searchOnAdd, setSearchOnAdd] = useState(true);

  function handleInstanceChange(next: string) {
    if (!next) return;
    setExplicitInstance(next);
    setQualityProfileId('');
    setRootFolderPath('');
  }

  if (query.isPending) {
    return (
      <section data-testid="movie-collection-loading">
        <Skeleton className="h-6 w-40" />
        <Skeleton className="mt-3 h-24 w-full" />
      </section>
    );
  }
  // A broken collection panel must not break the page.
  if (query.isError || !query.data) return null;

  const collection = query.data;
  const parts = collection.parts ?? [];
  const monitored = collection.radarr_monitored === true;

  function handleMonitorToggle(checked: boolean) {
    // Enable-only endpoint: ignore the off transition (unreachable while the
    // Switch is disabled-once-on, but guard anyway).
    if (!checked || monitored || !monitorInstance) return;
    monitorMut.mutate(
      { collectionId: tmdbCollectionId, body: { instance_name: monitorInstance } },
      {
        onError: () => toast.error(t('movies.add.errors.unknown')),
      },
    );
  }

  const canSubmitAddAll = Boolean(
    effectiveInstance && qualityProfileId && rootFolderPath && !addAllMut.isPending,
  );

  function handleAddAll(e: FormEvent) {
    e.preventDefault();
    if (!canSubmitAddAll) return;
    addAllMut.mutate(
      {
        collectionId: tmdbCollectionId,
        body: {
          instance_name: effectiveInstance,
          quality_profile_id: Number(qualityProfileId),
          root_folder_path: rootFolderPath,
          minimum_availability: minimumAvailability,
          monitored: true,
          search_on_add: searchOnAdd,
        },
      },
      {
        onSuccess: (res) => {
          toast.success(t('movieCollection.addAllResult', {
            added: res.added ?? 0,
            already: res.already_present ?? 0,
            failed: res.failed ?? 0,
          }));
          setDialogOpen(false);
        },
        onError: () => toast.error(t('movies.add.errors.unknown')),
      },
    );
  }

  return (
    <section data-testid="movie-collection-block" className="flex flex-col gap-3">
      <h2 className="text-[13px] font-semibold uppercase tracking-wide text-tx-faint">
        {t('movieCollection.title')}
      </h2>

      <div className="flex flex-wrap items-center gap-3 rounded-lg border border-border-subtle bg-bg-surface p-3">
        <div className="w-[64px] shrink-0">
          <MediaImage
            hash={collection.poster ?? null}
            kind="poster"
            title={collection.name ?? ''}
            fallback="monogram"
            className="rounded-md border border-border-subtle"
            data-testid="movie-collection-poster"
          />
        </div>
        <div className="flex flex-1 flex-col gap-1">
          <span
            data-testid="movie-collection-name"
            className="text-[15px] font-semibold text-tx-primary"
          >
            {collection.name}
          </span>
          <label className="flex items-center gap-2 text-[13px] text-tx-secondary">
            <Switch
              checked={monitored}
              disabled={monitored || !monitorInstance || monitorMut.isPending}
              onCheckedChange={handleMonitorToggle}
              data-testid="movie-collection-monitor-toggle"
            />
            <span>{t('movieCollection.monitorToggle')}</span>
          </label>
          {!monitorInstance && (
            <span
              data-testid="movie-collection-monitor-no-instance"
              className="text-[12px] text-tx-muted"
            >
              {t('movieCollection.monitorNoInstance')}
            </span>
          )}
        </div>

        <Dialog open={dialogOpen} onOpenChange={setDialogOpen}>
          <DialogTrigger asChild>
            <Button
              variant="outline"
              size="sm"
              data-testid="movie-collection-add-all-open"
              disabled={radarrInstances.length === 0}
            >
              {t('movieCollection.addAll')}
            </Button>
          </DialogTrigger>
          <DialogContent data-testid="movie-collection-add-all-dialog" className="max-w-md">
            <DialogHeader>
              <DialogTitle>{t('movieCollection.addAll')}</DialogTitle>
            </DialogHeader>
            <form onSubmit={handleAddAll} className="space-y-4" data-testid="movie-collection-add-all-form">
              <RadarrTargetFields
                idPrefix="mcb"
                radarrInstances={radarrInstances}
                effectiveInstance={effectiveInstance}
                onInstanceChange={handleInstanceChange}
                qualityProfileId={qualityProfileId}
                onQualityProfileChange={setQualityProfileId}
                rootFolderPath={rootFolderPath}
                onRootFolderChange={setRootFolderPath}
                minimumAvailability={minimumAvailability}
                onMinimumAvailabilityChange={setMinimumAvailability}
                searchOnAdd={searchOnAdd}
                onSearchOnAddChange={setSearchOnAdd}
              />
              <DialogFooter>
                <Button
                  type="button"
                  variant="outline"
                  onClick={() => setDialogOpen(false)}
                  data-testid="movie-collection-add-all-cancel"
                >
                  {t('movies.add.cancel')}
                </Button>
                <Button
                  type="submit"
                  disabled={!canSubmitAddAll}
                  data-testid="movie-collection-add-all-submit"
                >
                  {addAllMut.isPending
                    ? t('movies.add.submitting')
                    : t('movieCollection.addAll')}
                </Button>
              </DialogFooter>
            </form>
          </DialogContent>
        </Dialog>
      </div>

      {parts.length > 0 && (
        <div
          data-testid="movie-collection-parts"
          className="grid gap-2 grid-cols-[repeat(auto-fill,minmax(160px,1fr))]"
        >
          {parts.map((part) => (
            <PartCard key={part.tmdb_id ?? part.title} part={part} />
          ))}
        </div>
      )}
    </section>
  );
}
