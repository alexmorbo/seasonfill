// useCollectionCardState — shared data/mutation/dialog-field state for the
// two movie-collection card renderings (`CollectionHeroCard` — the compact
// hero-right card — and `MovieCollectionBlock` — the fuller below-hero panel
// with the parts grid). Both components query the SAME endpoint
// (`GET /collections/:id`) and drive the SAME mutations
// (`useSetCollectionMonitor` / `useAddAllMissing`); this hook is a pure
// extraction of that shared logic — no behavior change, same DOM/testids
// in either caller.
import { useMemo, useState, type FormEvent } from 'react';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import {
  useMovieCollection, useAddAllMissing, useSetCollectionMonitor,
} from '@/api/movieCollections';
import { useInstances } from '@/lib/instances';
import type { MinimumAvailability } from '@/api/addToRadarr';

export interface UseCollectionCardStateArgs {
  readonly tmdbCollectionId: number;
  /** Instance to query the collection with + the preferred monitor target
   *  (the movie's first library instance). */
  readonly instance?: string | undefined;
  /** BCP-47 tag forwarded as `?lang=` so the BE emits localized fields. */
  readonly lang?: string | undefined;
}

export function useCollectionCardState({
  tmdbCollectionId, instance, lang,
}: UseCollectionCardStateArgs) {
  const { t } = useTranslation();
  const query = useMovieCollection(tmdbCollectionId, instance, lang);

  const instancesQ = useInstances();
  const radarrInstances = useMemo(
    () => (instancesQ.data?.instances ?? []).filter(
      (i) => Boolean(i.name) && (i.type ?? 'sonarr') === 'radarr',
    ),
    [instancesQ.data],
  );

  const collection = query.data;
  const monitored = collection?.radarr_monitored === true;

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

  return {
    query,
    collection,
    monitored,
    monitorInstance,
    monitorMut,
    handleMonitorToggle,
    radarrInstances,
    dialogOpen,
    setDialogOpen,
    effectiveInstance,
    qualityProfileId,
    setQualityProfileId,
    rootFolderPath,
    setRootFolderPath,
    minimumAvailability,
    setMinimumAvailability,
    searchOnAdd,
    setSearchOnAdd,
    handleInstanceChange,
    canSubmitAddAll,
    handleAddAll,
    addAllMut,
  };
}
