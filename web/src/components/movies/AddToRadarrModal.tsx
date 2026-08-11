// Add-to-Radarr modal — movie analogue of AddToSonarrModal, simpler (movies
// have no seasons). Rendered by AddToRadarrProvider at app-shell level, never
// inside a card. Holds only its own transient field state; the QP/RF seeding
// lives in the shared <RadarrTargetFields>.
//
// Wiring contract:
//   - useInstances() filtered to radarr targets ((type ?? 'sonarr') ===
//     'radarr'); empty → movies.add.noRadarr message + disabled submit.
//   - QP/RF selects gated on a chosen instance (BE 404s if asked before).
//   - minimum_availability defaults to 'released' (ADR-0018 grill decision).
//   - Submit toasts on success/error. Errors map from the F-2c envelope's
//     `error` slug; unknown slugs fall back to the generic message. The movie
//     discovery + collection caches are invalidated inside useAddToRadarr().

import { useMemo, useState, type FormEvent } from 'react';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import {
  useAddToRadarr, type MinimumAvailability,
} from '@/api/addToRadarr';
import type { AddToRadarrTarget } from './add-to-radarr-context';
import { RadarrTargetFields } from './RadarrTargetFields';
import { useInstances } from '@/lib/instances';
import { ApiError } from '@/lib/api';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';

const ERROR_SLUGS = new Set([
  'instance_not_found', 'radarr_unreachable',
]);

export interface AddToRadarrModalProps {
  readonly target: AddToRadarrTarget;
  readonly onClose: () => void;
}

export function AddToRadarrModal({ target, onClose }: AddToRadarrModalProps) {
  const { t } = useTranslation();
  const instancesQ = useInstances();
  const radarrInstances = useMemo(
    () => (instancesQ.data?.instances ?? []).filter(
      (i) => Boolean(i.name) && (i.type ?? 'sonarr') === 'radarr',
    ),
    [instancesQ.data],
  );

  const [explicitInstance, setExplicitInstance] = useState(target.instanceName ?? '');
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

  const addMut = useAddToRadarr();
  const tmdbID = target.tmdbId;

  const canSubmit = Boolean(
    effectiveInstance && qualityProfileId && rootFolderPath
    && typeof tmdbID === 'number' && tmdbID > 0
    && !addMut.isPending,
  );

  function handleSubmit(e: FormEvent) {
    e.preventDefault();
    if (!canSubmit || typeof tmdbID !== 'number') return;
    addMut.mutate(
      {
        instance_name: effectiveInstance,
        tmdb_id: tmdbID,
        quality_profile_id: Number(qualityProfileId),
        root_folder_path: rootFolderPath,
        minimum_availability: minimumAvailability,
        search_on_add: searchOnAdd,
      },
      {
        onSuccess: () => {
          toast.success(t('movies.add.success', { tag: target.title }));
          onClose();
        },
        onError: (err) => {
          let key = 'movies.add.errors.unknown';
          if (err instanceof ApiError) {
            const body = err.body as { error?: string } | undefined;
            const slug = body?.error;
            if (slug && ERROR_SLUGS.has(slug)) {
              key = `movies.add.errors.${slug}`;
            }
          }
          toast.error(t(key));
        },
      },
    );
  }

  return (
    <Dialog open onOpenChange={(o) => { if (!o) onClose(); }}>
      <DialogContent data-testid="add-to-radarr-modal" className="max-w-md">
        <DialogHeader>
          <DialogTitle>
            {t('movies.add.modalTitle', { title: target.title })}
          </DialogTitle>
        </DialogHeader>

        <form
          onSubmit={handleSubmit}
          className="space-y-4"
          data-testid="add-to-radarr-form"
        >
          <RadarrTargetFields
            idPrefix="atr"
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
              onClick={() => onClose()}
              data-testid="add-to-radarr-cancel"
            >
              {t('movies.add.cancel')}
            </Button>
            <Button
              type="submit"
              disabled={!canSubmit}
              data-testid="add-to-radarr-submit"
            >
              {addMut.isPending
                ? t('movies.add.submitting')
                : t('movies.add.submit')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
